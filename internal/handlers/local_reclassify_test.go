package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/renamer"
)

// --- sanitizeOverrideTarget: the path-traversal + sanitize guard --------------

func TestSanitizeOverrideTarget_RejectsTraversal(t *testing.T) {
	cases := []string{
		"../escape.mkv",
		"a/../../escape.mkv",
		"/abs/path.mkv",
		"..",
		"",
		"   ",
		"./../x.mkv",
	}
	for _, in := range cases {
		if got, ok := sanitizeOverrideTarget(in, nil); ok {
			t.Errorf("sanitizeOverrideTarget(%q) = (%q, true), want rejected", in, got)
		}
	}
}

func TestSanitizeOverrideTarget_SanitizesUnsafeChars(t *testing.T) {
	// Colons, pipes, quotes are scrubbed per-segment exactly like the AI path.
	got, ok := sanitizeOverrideTarget(`Movies/Foo: The "Bar" | Baz.mkv`, nil)
	if !ok {
		t.Fatalf("expected ok, got rejected")
	}
	want := filepath.Join("Movies", "Foo - The 'Bar' - Baz.mkv")
	if got != want {
		t.Errorf("sanitizeOverrideTarget = %q, want %q", got, want)
	}
	// A backslash-separated segment is treated as a separator, not a literal char.
	if renamer.SanitizeFilename("a:b") == "a:b" {
		t.Error("SanitizeFilename should have rewritten ':' ")
	}
}

func TestSanitizeOverrideTarget_CollapsesEmptySegments(t *testing.T) {
	got, ok := sanitizeOverrideTarget("Movies//./Inception (2010)/Inception.mkv", nil)
	if !ok {
		t.Fatalf("expected ok")
	}
	want := filepath.Join("Movies", "Inception (2010)", "Inception.mkv")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSanitizeOverrideTarget_ReusesExistingCategoryFolder(t *testing.T) {
	// User typed lowercase "movies" but the library already has "Movies":
	// the top-level segment is rewritten to reuse the existing folder.
	lc := &renamer.LocalContext{DestFolders: []string{"Movies", "Series"}}
	got, ok := sanitizeOverrideTarget("movies/Inception (2010)/Inception.mkv", lc)
	if !ok {
		t.Fatalf("expected ok")
	}
	want := filepath.Join("Movies", "Inception (2010)", "Inception.mkv")
	if got != want {
		t.Errorf("got %q, want %q (should reuse existing 'Movies')", got, want)
	}
}

func TestSanitizeOverrideTarget_NoReuseWhenSingleSegment(t *testing.T) {
	// A single-segment override (bare filename, no category) must NOT be
	// rewritten by category reuse — there's no folder to reuse.
	lc := &renamer.LocalContext{DestFolders: []string{"Movies"}}
	got, ok := sanitizeOverrideTarget("movie.mkv", lc)
	if !ok || got != "movie.mkv" {
		t.Errorf("got (%q,%v), want (\"movie.mkv\",true)", got, ok)
	}
}

// --- scopedOverrides: maps un-scoped keys to scoped keys ----------------------

func TestScopedOverrides_NonSubpathMount(t *testing.T) {
	b := local.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	req := &localPromoteReq{
		Mount:     "M",
		Overrides: map[string]string{"a.mkv": "Movies/a.mkv", "b.mkv": "  ", "c.mkv": "Series/c.mkv"},
	}
	got := scopedOverrides(b, req, "alice")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries (blank dropped), got %d: %v", len(got), got)
	}
	if got["a.mkv"] != "Movies/a.mkv" || got["c.mkv"] != "Series/c.mkv" {
		t.Errorf("unexpected mapping: %v", got)
	}
}

func TestScopedOverrides_SubpathMountPrefixesUser(t *testing.T) {
	b := local.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: t.TempDir(), UserSubpath: true}})
	req := &localPromoteReq{
		Mount:     "Meus downloads",
		Overrides: map[string]string{"film.mkv": "Movies/film.mkv"},
	}
	got := scopedOverrides(b, req, "bob")
	if got["bob/film.mkv"] != "Movies/film.mkv" {
		t.Errorf("expected scoped key 'bob/film.mkv', got %v", got)
	}
}

func TestScopedOverrides_NilWhenEmpty(t *testing.T) {
	b := local.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	if got := scopedOverrides(b, &localPromoteReq{Mount: "M"}, ""); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	// All-blank overrides also collapse to nil.
	req := &localPromoteReq{Mount: "M", Overrides: map[string]string{"x": "", "y": "   "}}
	if got := scopedOverrides(b, req, ""); got != nil {
		t.Errorf("expected nil for all-blank, got %v", got)
	}
}

// --- end-to-end: LocalPromote honours the override and batches --------------

func mkSrcFile(t *testing.T, dir, name string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, name), []byte("data"))
}

func promoteRouter(b *local.Browser, sharedDir string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// aiClient + tmdbClient nil → the AI path is skipped, so the override (or the
	// plain fallback) decides the destination deterministically in tests.
	r.POST("/api/local/promote", LocalPromote(b, nil, nil, sharedDir, nil, nil, nil))
	return r
}

func postPromote(t *testing.T, r *gin.Engine, body localPromoteReq) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	bJSON, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/local/promote", bytes.NewReader(bJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w, resp
}

func TestLocalPromote_HonoursOverride(t *testing.T) {
	src := t.TempDir()
	shared := t.TempDir()
	mkSrcFile(t, src, "raw.release.name.mkv")
	// Pre-existing "Movies" so the override's lowercase "movies" reuses it.
	if err := os.MkdirAll(filepath.Join(shared, "Movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := local.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: src}})
	r := promoteRouter(b, shared)

	w, resp := postPromote(t, r, localPromoteReq{
		Mount:     "Meus downloads",
		Paths:     []string{"raw.release.name.mkv"},
		Overrides: map[string]string{"raw.release.name.mkv": "movies/Cool Movie (2024)/Cool Movie.mkv"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	// Landed exactly where the user edited it, reusing existing "Movies".
	want := filepath.Join(shared, "Movies", "Cool Movie (2024)", "Cool Movie.mkv")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected file at %s: %v", want, err)
	}
	if moved, _ := resp["moved"].(float64); moved != 1 {
		t.Errorf("moved=%v, want 1", resp["moved"])
	}
}

func TestLocalPromote_OverrideTraversalNeutralized(t *testing.T) {
	src := t.TempDir()
	shared := t.TempDir()
	mkSrcFile(t, src, "movie.mkv")
	b := local.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: src}})
	r := promoteRouter(b, shared)

	// A traversal override is rejected by the guard → falls back to the plain
	// targetDir/baseName under base. The file must NEVER escape `shared`.
	w, _ := postPromote(t, r, localPromoteReq{
		Mount:        "Meus downloads",
		Paths:        []string{"movie.mkv"},
		TargetSubdir: "dest",
		Overrides:    map[string]string{"movie.mkv": "../../../etc/movie.mkv"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	// Fallback landing inside base/dest, not outside.
	if _, err := os.Stat(filepath.Join(shared, "dest", "movie.mkv")); err != nil {
		t.Errorf("expected fallback inside base, got %v", err)
	}
	// Nothing escaped one level above shared.
	escaped := filepath.Join(filepath.Dir(shared), "etc", "movie.mkv")
	if _, err := os.Stat(escaped); err == nil {
		t.Errorf("file escaped base to %s", escaped)
	}
}

func TestLocalPromote_BatchPartialSuccess(t *testing.T) {
	src := t.TempDir()
	shared := t.TempDir()
	mkSrcFile(t, src, "ok.mkv") // exists → moves
	// "missing.mkv" is NOT created → that item fails.
	b := local.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: src}})
	r := promoteRouter(b, shared)

	w, resp := postPromote(t, r, localPromoteReq{
		Mount: "Meus downloads",
		Paths: []string{"ok.mkv", "missing.mkv"},
		Overrides: map[string]string{
			"ok.mkv":      "Movies/Ok.mkv",
			"missing.mkv": "Movies/Missing.mkv",
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d (want 200, partial success) body=%s", w.Code, w.Body.String())
	}
	if moved, _ := resp["moved"].(float64); moved != 1 {
		t.Errorf("moved=%v, want 1", resp["moved"])
	}
	if failed, _ := resp["failed"].(float64); failed != 1 {
		t.Errorf("failed=%v, want 1", resp["failed"])
	}
	// Per-item results: one ok, one error, both keyed by the original path.
	results, _ := resp["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results len=%d, want 2: %v", len(results), resp["results"])
	}
	byPath := map[string]map[string]any{}
	for _, r := range results {
		m := r.(map[string]any)
		byPath[m["path"].(string)] = m
	}
	if ok, _ := byPath["ok.mkv"]["ok"].(bool); !ok {
		t.Errorf("ok.mkv result not ok: %v", byPath["ok.mkv"])
	}
	if ok, _ := byPath["missing.mkv"]["ok"].(bool); ok {
		t.Errorf("missing.mkv should have failed: %v", byPath["missing.mkv"])
	}
	if byPath["missing.mkv"]["error"] == nil {
		t.Errorf("missing.mkv result should carry an error")
	}
	if _, err := os.Stat(filepath.Join(shared, "Movies", "Ok.mkv")); err != nil {
		t.Errorf("ok.mkv not moved: %v", err)
	}
}

func TestLocalPromote_BatchAllFail422(t *testing.T) {
	src := t.TempDir()
	shared := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: src}})
	r := promoteRouter(b, shared)
	// Neither file exists → moved==0 → 422 with per-item results still populated.
	w, resp := postPromote(t, r, localPromoteReq{
		Mount: "Meus downloads",
		Paths: []string{"a.mkv", "b.mkv"},
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422 body=%s", w.Code, w.Body.String())
	}
	if results, _ := resp["results"].([]any); len(results) != 2 {
		t.Errorf("expected 2 per-item results even on total failure, got %v", resp["results"])
	}
}
