package renamer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/tmdb"
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
	// Movie with year → "Title - Year".
	got := buildTargetPath(targetPathInput{Kind: "movie", CleanTitle: "Inception", Year: 2010, Ext: ".mkv", RawName: "Inception.2010.1080p.BluRay-GROUP.mkv"})
	want := filepath.Join("Filmes", "Inception - 2010", "Inception - 2010.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_MovieSequel(t *testing.T) {
	// Sequel (number in title) → "Title - N" (not the year).
	got := buildTargetPath(targetPathInput{Kind: "movie", CleanTitle: "Toy Story 3", Year: 2010, Ext: ".mkv"})
	want := filepath.Join("Filmes", "Toy Story - 3", "Toy Story - 3.mkv")
	if got != want {
		t.Errorf("sequel: got %q; want %q", got, want)
	}
	// A large number in the title (year/part of the name) is NOT treated as a sequel.
	got2 := buildTargetPath(targetPathInput{Kind: "movie", CleanTitle: "Blade Runner 2049", Year: 0, Ext: ".mkv"})
	want2 := filepath.Join("Filmes", "Blade Runner 2049", "Blade Runner 2049.mkv")
	if got2 != want2 {
		t.Errorf("non-sequel: got %q; want %q", got2, want2)
	}
}

func TestBuildTargetPath_MovieNoYear(t *testing.T) {
	got := buildTargetPath(targetPathInput{Kind: "movie", CleanTitle: "Inception", Ext: ".mkv", RawName: "raw.mkv"})
	want := filepath.Join("Filmes", "Inception", "Inception.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVBasic(t *testing.T) {
	// Series S01E01 without an episode name.
	got := buildTargetPath(targetPathInput{Kind: "tv", CleanTitle: "Breaking Bad", Season: 1, Episode: 1, Ext: ".mkv", RawName: "raw.mkv"})
	want := filepath.Join("Series", "Breaking Bad", "Season 01", "Breaking Bad - S01E01.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVWithEpisodeName(t *testing.T) {
	got := buildTargetPath(targetPathInput{Kind: "tv", CleanTitle: "Breaking Bad", Season: 1, Episode: 1, EpName: "Pilot", Ext: ".mkv", RawName: "raw.mkv"})
	want := filepath.Join("Series", "Breaking Bad", "Season 01", "Breaking Bad - S01E01 - Pilot.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVSeasonZeroDefaultsToOne(t *testing.T) {
	// Season 0 from the AI must fall back to Season 01.
	got := buildTargetPath(targetPathInput{Kind: "tv", CleanTitle: "Show", Episode: 5, Ext: ".mp4", RawName: "raw.mp4"})
	want := filepath.Join("Series", "Show", "Season 01", "Show - S01E05.mp4")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVNoEpisodeFallsBackToRawName(t *testing.T) {
	// No episode number → uses rawName inside the season folder.
	got := buildTargetPath(targetPathInput{Kind: "tv", CleanTitle: "Show", Season: 2, Ext: ".mkv", RawName: "original.raw.file.mkv"})
	want := filepath.Join("Series", "Show", "Season 02", "original.raw.file.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVGroupInTitle(t *testing.T) {
	// The "Group" in the name was already stripped by the AI earlier; cleanTitle
	// arrives clean. This test ensures the resulting path has no group artifacts.
	got := buildTargetPath(targetPathInput{Kind: "tv", CleanTitle: "Dark", Season: 1, Episode: 3, Ext: ".mkv", RawName: "Dark.S01E03.1080p.x265-YIFY.mkv"})
	want := filepath.Join("Series", "Dark", "Season 01", "Dark - S01E03.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVDualEpisode(t *testing.T) {
	// Dual episode (S01E01E02): the AI returns episode=1 — the second episode
	// is not modeled yet. The generated path reflects only E01.
	// NOSONAR: multi-ep support blocked until AI returns Episode2
	got := buildTargetPath(targetPathInput{Kind: "tv", CleanTitle: "The Wire", Season: 1, Episode: 1, Ext: ".mkv", RawName: "The.Wire.S01E01E02.mkv"})
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

	// Create the file at the destination to force a conflict.
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

// ── GeneratePreview ──────────────────────────────────────────────────────────

func TestGeneratePreview_AIAndTMDB(t *testing.T) {
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"role":    "assistant",
					"content": `{"title":"Inception","year":2010,"kind":"movie","season":0,"episode":0}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer aiSrv.Close()

	aiClient := newAIClient(t, aiSrv.URL)

	preview, err := GeneratePreview(context.Background(), aiClient, nil, "Inception.2010.1080p.mkv")
	if err != nil {
		t.Fatalf("GeneratePreview: %v", err)
	}
	if preview.CleanName != "Inception" {
		t.Errorf("CleanName = %q, want 'Inception'", preview.CleanName)
	}
	if preview.Kind != "movie" {
		t.Errorf("Kind = %q, want 'movie'", preview.Kind)
	}
	if preview.Year != 2010 {
		t.Errorf("Year = %d, want 2010", preview.Year)
	}
	if preview.TargetPath == "" {
		t.Error("expected non-empty TargetPath")
	}
}

func TestGeneratePreview_NilTMDB(t *testing.T) {
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"role":    "assistant",
					"content": `{"title":"Breaking Bad","year":2008,"kind":"tv","season":1,"episode":1}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer aiSrv.Close()

	aiClient := newAIClient(t, aiSrv.URL)

	preview, err := GeneratePreview(context.Background(), aiClient, nil, "Breaking.Bad.S01E01.mkv")
	if err != nil {
		t.Fatalf("GeneratePreview with nil TMDB: %v", err)
	}
	if preview.Kind != "tv" {
		t.Errorf("Kind = %q, want 'tv'", preview.Kind)
	}
	if preview.Season != 1 || preview.Episode != 1 {
		t.Errorf("Season/Episode = %d/%d, want 1/1", preview.Season, preview.Episode)
	}
}

func TestGeneratePreview_AIError_FallsBackToParser(t *testing.T) {
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer aiSrv.Close()

	aiClient := newAIClient(t, aiSrv.URL)
	// AI down → must NOT hard-error; the regex fallback derives metadata.
	preview, err := GeneratePreview(context.Background(), aiClient, nil, "Inception.2010.1080p.BluRay.mkv")
	if err != nil {
		t.Fatalf("AI failure should fall back, not error: %v", err)
	}
	if preview == nil || preview.CleanName == "" {
		t.Fatal("fallback should still produce a title")
	}
	if preview.Year != 2010 {
		t.Errorf("fallback year = %d, want 2010 (from regex)", preview.Year)
	}
}

func TestGeneratePreview_ParserOverridesSeasonEpisode(t *testing.T) {
	// AI down → fallback; the regex S/E drives kind=tv and the numbers, giving
	// coherent series organization even without the AI.
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer aiSrv.Close()
	aiClient := newAIClient(t, aiSrv.URL)

	preview, err := GeneratePreview(context.Background(), aiClient, nil, "Euphoria.S01E03.1080p.WEB-DL.mkv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preview.Kind != "tv" {
		t.Errorf("Kind = %q, want tv", preview.Kind)
	}
	if preview.Season != 1 || preview.Episode != 3 {
		t.Errorf("S/E = %d/%d, want 1/3", preview.Season, preview.Episode)
	}
}

func TestGeneratePreview_TVWithEpisodeName(t *testing.T) {
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"role":    "assistant",
					"content": `{"title":"Breaking Bad","year":2008,"kind":"tv","season":1,"episode":1}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer aiSrv.Close()

	aiClient := newAIClient(t, aiSrv.URL)

	preview, err := GeneratePreview(context.Background(), aiClient, nil, "Breaking.Bad.S01E01.1080p.mkv")
	if err != nil {
		t.Fatalf("GeneratePreview: %v", err)
	}
	if preview.EpisodeName != "" {
		t.Errorf("EpisodeName = %q, want empty (no TMDB)", preview.EpisodeName)
	}
}

func TestGeneratePreview_FallbackToAIWhenTMDBFails(t *testing.T) {
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"role":    "assistant",
					"content": `{"title":"Unknown Movie","year":2020,"kind":"movie","season":0,"episode":0}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer aiSrv.Close()

	aiClient := newAIClient(t, aiSrv.URL)

	preview, err := GeneratePreview(context.Background(), aiClient, nil, "Unknown.Movie.2020.mkv")
	if err != nil {
		t.Fatalf("GeneratePreview: %v", err)
	}
	if preview.CleanName != "Unknown Movie" {
		t.Errorf("CleanName = %q, want 'Unknown Movie'", preview.CleanName)
	}
	if preview.Year != 2020 {
		t.Errorf("Year = %d, want 2020", preview.Year)
	}
}

func TestSanitizeInGeneratePreview(t *testing.T) {
	aiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{
					"role":    "assistant",
					"content": `{"title":"Bad: File/Name *Test?","year":2022,"kind":"movie","season":0,"episode":0}`,
				}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer aiSrv.Close()

	aiClient := newAIClient(t, aiSrv.URL)
	preview, err := GeneratePreview(context.Background(), aiClient, nil, "Bad.File.Name.2022.mkv")
	if err != nil {
		t.Fatalf("GeneratePreview: %v", err)
	}
	if strings.ContainsAny(preview.CleanName, "/\\:*?\"<>|") {
		t.Errorf("CleanName contains forbidden chars: %q", preview.CleanName)
	}
}

func newAIClient(t *testing.T, baseURL string) *ai.Client {
	t.Helper()
	cfg := config.AIConfig{
		Enabled: true,
		Providers: map[string]config.AIProvider{
			"test": {BaseURL: baseURL, APIKey: "test-key"},
		},
		Chain: []config.AIChainSlot{
			{ID: "test", Provider: "test", Model: "test-model"},
		},
	}
	c := ai.New(cfg)
	if c == nil {
		t.Fatal("ai.New returned nil")
	}
	return c
}

func newTMDBClient(t *testing.T, baseURL string) *tmdb.Client {
	t.Helper()
	c, err := tmdb.New("test-key", "", dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("tmdb.New: %v", err)
	}
	return c
}
