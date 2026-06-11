package renamer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/parser"
)

// ── normalizeCategory ────────────────────────────────────────────────────────

func TestNormalizeCategory(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Movies", "movies"},
		{"  Filmes ", "filmes"},
		{"TV Shows", "tvshows"},
		{"Séries", "series"},
		{"Documentários", "documentarios"},
		{"Anime!", "anime"},
		{"Coleção 2024", "colecao2024"},
		// every accent branch + ç + dropped punctuation/symbols
		{"àâãäéèêëíìîïóòôõöúùûüç", "aaaaeeeeiiiiooooouuuuc"},
		{"A_B-C.D & E", "abcde"},
	}
	for _, tc := range cases {
		if got := normalizeCategory(tc.in); got != tc.want {
			t.Errorf("normalizeCategory(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// ── enrichTitle / fetchEpisodeName (nil-TMDB branches, no network) ────────────

func TestEnrichTitle_NilTMDBUsesMetaThenParser(t *testing.T) {
	meta := &ai.RenameMetadata{Title: "Some Movie", Year: 0, Kind: "movie"}
	parsed := parser.Parse("Some.Movie.1999.1080p")
	title, year, kind := enrichTitle(context.Background(), nil, meta, parsed, "movie")
	if title != "Some Movie" {
		t.Errorf("title = %q, want 'Some Movie'", title)
	}
	if year != 1999 {
		t.Errorf("year = %d, want 1999 (from parser fallback)", year)
	}
	if kind != "movie" {
		t.Errorf("kind = %q, want movie", kind)
	}
}

func TestEnrichTitle_NilTMDBKeepsMetaYear(t *testing.T) {
	meta := &ai.RenameMetadata{Title: "Inception", Year: 2010, Kind: "movie"}
	parsed := parser.Quality{}
	title, year, _ := enrichTitle(context.Background(), nil, meta, parsed, "movie")
	if title != "Inception" || year != 2010 {
		t.Errorf("got (%q,%d), want (Inception,2010)", title, year)
	}
}

func TestFetchEpisodeName_GuardClauses(t *testing.T) {
	ctx := context.Background()
	// Not tv → "".
	if got := fetchEpisodeName(ctx, nil, "movie", "X", 1, 1); got != "" {
		t.Errorf("movie kind: got %q, want empty", got)
	}
	// nil TMDB → "".
	if got := fetchEpisodeName(ctx, nil, "tv", "X", 1, 1); got != "" {
		t.Errorf("nil tmdb: got %q, want empty", got)
	}
	// episode <= 0 → "" (season pack).
	if got := fetchEpisodeName(ctx, nil, "tv", "X", 1, 0); got != "" {
		t.Errorf("episode 0: got %q, want empty", got)
	}
}

func TestReconcileSeasonEpisode_RegexOverridesAI(t *testing.T) {
	meta := &ai.RenameMetadata{Season: 9, Episode: 9, Kind: "movie"}
	parsed := parser.Parse("Show.S02E05.1080p")
	s, e, k := reconcileSeasonEpisode(meta, parsed)
	if s != 2 || e != 5 || k != "tv" {
		t.Errorf("got (%d,%d,%q), want (2,5,tv)", s, e, k)
	}
}

func TestReconcileSeasonEpisode_NoRegexKeepsAI(t *testing.T) {
	meta := &ai.RenameMetadata{Season: 3, Episode: 7, Kind: "tv"}
	parsed := parser.Quality{}
	s, e, k := reconcileSeasonEpisode(meta, parsed)
	if s != 3 || e != 7 || k != "tv" {
		t.Errorf("got (%d,%d,%q), want (3,7,tv)", s, e, k)
	}
}

// ── resolveCategoryFolder ────────────────────────────────────────────────────

func TestResolveCategoryFolder_NilOrEmpty(t *testing.T) {
	if cat, reused := resolveCategoryFolder("movie", nil); cat != "" || reused != "" {
		t.Errorf("nil ctx: got (%q,%q), want empty", cat, reused)
	}
	if cat, reused := resolveCategoryFolder("movie", &LocalContext{}); cat != "" || reused != "" {
		t.Errorf("empty folders: got (%q,%q), want empty", cat, reused)
	}
}

func TestResolveCategoryFolder_ReusesEnglishMovieFolder(t *testing.T) {
	lc := &LocalContext{DestFolders: []string{"Movies", "Series", "Music"}}
	cat, reused := resolveCategoryFolder("movie", lc)
	if cat != "Movies" || reused != "Movies" {
		t.Errorf("got (%q,%q), want (Movies,Movies)", cat, reused)
	}
}

func TestResolveCategoryFolder_ReusesCaseInsensitive(t *testing.T) {
	// existing "movies" (lowercase) must be reused for a movie — no duplicate.
	lc := &LocalContext{DestFolders: []string{"anime", "movies"}}
	cat, reused := resolveCategoryFolder("movie", lc)
	if cat != "movies" || reused != "movies" {
		t.Errorf("got (%q,%q), want (movies,movies) — must not duplicate by case", cat, reused)
	}
}

func TestResolveCategoryFolder_ReusesPortugueseForMovie(t *testing.T) {
	lc := &LocalContext{DestFolders: []string{"Filmes", "Seriados"}}
	cat, reused := resolveCategoryFolder("movie", lc)
	if cat != "Filmes" {
		t.Errorf("movie cat = %q, want Filmes", cat)
	}
	if reused != "Filmes" {
		t.Errorf("reused = %q, want Filmes", reused)
	}
}

func TestResolveCategoryFolder_ReusesTVSynonyms(t *testing.T) {
	for _, folder := range []string{"Series", "Séries", "TV Shows", "Seriados", "Anime"} {
		lc := &LocalContext{DestFolders: []string{folder}}
		cat, reused := resolveCategoryFolder("tv", lc)
		if cat != folder || reused != folder {
			t.Errorf("tv folder %q: got (%q,%q), want reused", folder, cat, reused)
		}
	}
}

func TestResolveCategoryFolder_NoMatchReturnsEmpty(t *testing.T) {
	// A library with only unrelated folders → no reuse, caller uses default.
	lc := &LocalContext{DestFolders: []string{"Music", "Photos", "Books"}}
	cat, reused := resolveCategoryFolder("movie", lc)
	if cat != "" || reused != "" {
		t.Errorf("got (%q,%q), want empty (no synonym present)", cat, reused)
	}
}

func TestResolveCategoryFolder_KindIsolation(t *testing.T) {
	// "Movies" present but kind=tv → must NOT reuse the movie folder.
	lc := &LocalContext{DestFolders: []string{"Movies"}}
	if cat, _ := resolveCategoryFolder("tv", lc); cat != "" {
		t.Errorf("tv must not reuse a movie-only folder, got %q", cat)
	}
}

// ── buildTargetPath with reused category ─────────────────────────────────────

func TestBuildTargetPath_MovieUsesReusedCategory(t *testing.T) {
	got := buildTargetPath(targetPathInput{Category: "Movies", Kind: "movie", CleanTitle: "Inception", Year: 2010, Ext: ".mkv"})
	want := filepath.Join("Movies", "Inception - 2010", "Inception - 2010.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_TVUsesReusedCategory(t *testing.T) {
	got := buildTargetPath(targetPathInput{Category: "TV Shows", Kind: "tv", CleanTitle: "Dark", Season: 1, Episode: 3, Ext: ".mkv"})
	want := filepath.Join("TV Shows", "Dark", "Season 01", "Dark - S01E03.mkv")
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

func TestBuildTargetPath_EmptyCategoryKeepsLegacyDefault(t *testing.T) {
	// Backwards compat: empty Category → "Filmes"/"Series" as before.
	movie := buildTargetPath(targetPathInput{Kind: "movie", CleanTitle: "X", Year: 2000, Ext: ".mp4"})
	if !strings.HasPrefix(movie, "Filmes"+string(filepath.Separator)) {
		t.Errorf("movie default = %q, want Filmes/...", movie)
	}
	tv := buildTargetPath(targetPathInput{Kind: "tv", CleanTitle: "Y", Season: 1, Episode: 1, Ext: ".mp4"})
	if !strings.HasPrefix(tv, "Series"+string(filepath.Separator)) {
		t.Errorf("tv default = %q, want Series/...", tv)
	}
}

// ── buildAIContextHint ───────────────────────────────────────────────────────

func TestBuildAIContextHint_NilEmpty(t *testing.T) {
	if h := buildAIContextHint(nil); h != "" {
		t.Errorf("nil ctx hint = %q, want empty", h)
	}
	if h := buildAIContextHint(&LocalContext{}); h != "" {
		t.Errorf("empty ctx hint = %q, want empty", h)
	}
}

func TestBuildAIContextHint_IncludesPathAndFolders(t *testing.T) {
	lc := &LocalContext{
		CurrentPath: "Movies/2024",
		MountName:   "media",
		DestFolders: []string{"Movies", "Series", "Anime"},
	}
	h := buildAIContextHint(lc)
	if !strings.Contains(h, "media/Movies/2024") {
		t.Errorf("hint missing current location: %q", h)
	}
	for _, f := range []string{"Movies", "Series", "Anime"} {
		if !strings.Contains(h, f) {
			t.Errorf("hint missing folder %q: %q", f, h)
		}
	}
	if !strings.Contains(h, "Movies==Filmes") {
		t.Errorf("hint missing reuse instruction: %q", h)
	}
}

func TestBuildAIContextHint_TruncatesFolders(t *testing.T) {
	folders := make([]string, maxAIContextFolders+10)
	for i := range folders {
		folders[i] = "Folder" + string(rune('A'+i%26)) + string(rune('0'+i/26))
	}
	lc := &LocalContext{DestFolders: folders}
	h := buildAIContextHint(lc)
	// The first kept folder must be present and a folder past the cap must not.
	if !strings.Contains(h, folders[0]) {
		t.Errorf("hint missing first folder %q", folders[0])
	}
	excluded := folders[maxAIContextFolders+5]
	if strings.Contains(h, excluded) {
		t.Errorf("hint includes folder %q beyond the cap of %d", excluded, maxAIContextFolders)
	}
}

// ── GeneratePreviewWithContext (end-to-end, stubbed AI) ──────────────────────

// stubAIServer returns an httptest server replying with the given rename JSON.
func stubAIServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": content}},
			},
		})
	}))
}

func TestGeneratePreviewWithContext_ReusesExistingMoviesFolder(t *testing.T) {
	srv := stubAIServer(t, `{"title":"Inception","year":2010,"kind":"movie","season":0,"episode":0}`)
	defer srv.Close()
	aiClient := newAIClient(t, srv.URL)

	lc := &LocalContext{
		CurrentPath: "Movies/2010",
		MountName:   "media",
		DestFolders: []string{"Movies", "Series"}, // English library
	}
	preview, err := GeneratePreviewWithContext(context.Background(), aiClient, nil, "Inception.2010.1080p.mkv", lc)
	if err != nil {
		t.Fatalf("GeneratePreviewWithContext: %v", err)
	}
	// MUST land under the existing "Movies", NOT a new "Filmes".
	if !strings.HasPrefix(preview.TargetPath, "Movies"+string(filepath.Separator)) {
		t.Errorf("TargetPath = %q, want under existing Movies/", preview.TargetPath)
	}
	if strings.Contains(preview.TargetPath, "Filmes") {
		t.Errorf("TargetPath %q recreated 'Filmes' inside a Movies library — duplicate!", preview.TargetPath)
	}
	if preview.ReusedFolder != "Movies" {
		t.Errorf("ReusedFolder = %q, want Movies", preview.ReusedFolder)
	}
}

func TestGeneratePreviewWithContext_NoMatchKeepsDefault(t *testing.T) {
	srv := stubAIServer(t, `{"title":"Inception","year":2010,"kind":"movie","season":0,"episode":0}`)
	defer srv.Close()
	aiClient := newAIClient(t, srv.URL)

	lc := &LocalContext{DestFolders: []string{"Music", "Photos"}}
	preview, err := GeneratePreviewWithContext(context.Background(), aiClient, nil, "Inception.2010.mkv", lc)
	if err != nil {
		t.Fatalf("GeneratePreviewWithContext: %v", err)
	}
	if !strings.HasPrefix(preview.TargetPath, "Filmes"+string(filepath.Separator)) {
		t.Errorf("TargetPath = %q, want default Filmes/ when no synonym present", preview.TargetPath)
	}
	if preview.ReusedFolder != "" {
		t.Errorf("ReusedFolder = %q, want empty (created fresh)", preview.ReusedFolder)
	}
}

func TestGeneratePreviewWithContext_NilContextEqualsLegacy(t *testing.T) {
	srv := stubAIServer(t, `{"title":"Inception","year":2010,"kind":"movie","season":0,"episode":0}`)
	defer srv.Close()
	aiClient := newAIClient(t, srv.URL)

	preview, err := GeneratePreviewWithContext(context.Background(), aiClient, nil, "Inception.2010.mkv", nil)
	if err != nil {
		t.Fatalf("GeneratePreviewWithContext: %v", err)
	}
	if !strings.HasPrefix(preview.TargetPath, "Filmes"+string(filepath.Separator)) {
		t.Errorf("nil context should behave like legacy: %q", preview.TargetPath)
	}
}

func TestGeneratePreviewWithContext_SystemPromptCarriesTaxonomy(t *testing.T) {
	// Verify the existing-folders taxonomy reaches the SYSTEM message while the
	// USER message stays the bare raw name (so title extraction is unaffected).
	var sawSystem, sawUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		for _, m := range req.Messages {
			if m.Role == "system" {
				sawSystem = m.Content
			}
			if m.Role == "user" {
				sawUser = m.Content
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": `{"title":"Inception","year":2010,"kind":"movie"}`}},
			},
		})
	}))
	defer srv.Close()
	aiClient := newAIClient(t, srv.URL)

	lc := &LocalContext{DestFolders: []string{"Movies", "Series"}, CurrentPath: "Movies"}
	if _, err := GeneratePreviewWithContext(context.Background(), aiClient, nil, "Inception.2010.mkv", lc); err != nil {
		t.Fatalf("GeneratePreviewWithContext: %v", err)
	}
	if !strings.Contains(sawSystem, "existing top-level folders") {
		t.Errorf("system prompt missing taxonomy hint: %q", sawSystem)
	}
	if !strings.Contains(sawSystem, "Movies") || !strings.Contains(sawSystem, "Series") {
		t.Errorf("system prompt missing existing folders: %q", sawSystem)
	}
	// The user message must be ONLY the raw name (no context noise).
	if strings.Contains(sawUser, "existing top-level folders") || strings.Contains(sawUser, "[context]") {
		t.Errorf("user message polluted with context: %q", sawUser)
	}
	if strings.TrimSpace(sawUser) != "Inception.2010" {
		t.Errorf("user message = %q, want bare 'Inception.2010'", sawUser)
	}
}
