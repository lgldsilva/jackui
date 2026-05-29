package renamer

import (
	"os"
	"path/filepath"
	"testing"
)

// ── sanitizeFilename ────────────────────────────────────────────────────────

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Breaking Bad: Season 1", "Breaking Bad - Season 1"},
		{"Movie/Title", "Movie-Title"},
		{"Hack*ers", "Hackers"},
		{"What?", "What"},
		{`"Quotes"`, "'Quotes'"},
		{"A<B>C", "ABC"},
		{"A|B", "A-B"},
		{"  leading and trailing  ", "leading and trailing"},
		{"Normal Title", "Normal Title"},
		// Release groups in brackets are not special chars — left intact.
		{"Show.Name [GROUP]", "Show.Name [GROUP]"},
	}
	for _, tc := range cases {
		got := sanitizeFilename(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeFilename(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// ── buildTargetPath ──────────────────────────────────────────────────────────

func TestBuildTargetPath_Movie(t *testing.T) {
	// Filme com ano → pasta e arquivo com "(year)".
	got := buildTargetPath("movie", "Inception", 2010, 0, 0, "", ".mkv", "Inception.2010.1080p.BluRay-GROUP.mkv")
	want := filepath.Join("Filmes", "Inception (2010)", "Inception (2010).mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_MovieNoYear(t *testing.T) {
	got := buildTargetPath("movie", "Inception", 0, 0, 0, "", ".mkv", "raw.mkv")
	want := filepath.Join("Filmes", "Inception", "Inception.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVBasic(t *testing.T) {
	// Série S01E01 sem nome de episódio.
	got := buildTargetPath("tv", "Breaking Bad", 0, 1, 1, "", ".mkv", "raw.mkv")
	want := filepath.Join("Series", "Breaking Bad", "Season 01", "Breaking Bad - S01E01.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVWithEpisodeName(t *testing.T) {
	got := buildTargetPath("tv", "Breaking Bad", 0, 1, 1, "Pilot", ".mkv", "raw.mkv")
	want := filepath.Join("Series", "Breaking Bad", "Season 01", "Breaking Bad - S01E01 - Pilot.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVSeasonZeroDefaultsToOne(t *testing.T) {
	// Season 0 da IA deve cair em Season 01.
	got := buildTargetPath("tv", "Show", 0, 0, 5, "", ".mp4", "raw.mp4")
	want := filepath.Join("Series", "Show", "Season 01", "Show - S01E05.mp4")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVNoEpisodeFallsBackToRawName(t *testing.T) {
	// Sem número de episódio → usa rawName na pasta da temporada.
	got := buildTargetPath("tv", "Show", 0, 2, 0, "", ".mkv", "original.raw.file.mkv")
	want := filepath.Join("Series", "Show", "Season 02", "original.raw.file.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVGroupInTitle(t *testing.T) {
	// "Group" no nome já foi removido pela IA antes; o cleanTitle chega limpo.
	// Este teste garante que o path resultante não tem artefatos do grupo.
	got := buildTargetPath("tv", "Dark", 0, 1, 3, "", ".mkv", "Dark.S01E03.1080p.x265-YIFY.mkv")
	want := filepath.Join("Series", "Dark", "Season 01", "Dark - S01E03.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVDualEpisode(t *testing.T) {
	// Episódio duplo (S01E01E02): a IA retorna episode=1 — o segundo episódio
	// não está modelado ainda. O path gerado reflete somente E01.
	// TODO: implementar suporte a multi-ep quando a IA retornar Episode2.
	got := buildTargetPath("tv", "The Wire", 0, 1, 1, "", ".mkv", "The.Wire.S01E01E02.mkv")
	want := filepath.Join("Series", "The Wire", "Season 01", "The Wire - S01E01.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// ── ResolveTargetConflict ────────────────────────────────────────────────────

func TestResolveTargetConflict_NoConflict(t *testing.T) {
	base := t.TempDir()
	rel := filepath.Join("Filmes", "Movie (2010)", "Movie (2010).mkv")
	got := ResolveTargetConflict(base, rel)
	if got != rel {
		t.Errorf("expected original path %q; got %q", rel, got)
	}
}

func TestResolveTargetConflict_OneConflict(t *testing.T) {
	base := t.TempDir()
	rel := filepath.Join("Filmes", "Movie (2010)", "Movie (2010).mkv")

	// Cria o arquivo no destino para forçar conflito.
	full := filepath.Join(base, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	got := ResolveTargetConflict(base, rel)
	want := filepath.Join("Filmes", "Movie (2010)", "Movie (2010) (2).mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestResolveTargetConflict_MultipleConflicts(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join("Filmes", "Movie (2010)")
	base1 := "Movie (2010).mkv"
	base2 := "Movie (2010) (2).mkv"

	for _, name := range []string{base1, base2} {
		full := filepath.Join(base, dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rel := filepath.Join(dir, base1)
	got := ResolveTargetConflict(base, rel)
	want := filepath.Join(dir, "Movie (2010) (3).mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}
