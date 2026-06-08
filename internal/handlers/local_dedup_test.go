package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
)

func dedupBrowser(t *testing.T) (*local.Browser, string) {
	t.Helper()
	dir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{{Name: "Test", Path: dir}})
	return b, dir
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestFindDuplicates_GroupsByContentNotName(t *testing.T) {
	b, dir := dedupBrowser(t)
	same := []byte("the very same bytes, twice over and then some padding")
	writeFile(t, filepath.Join(dir, "a.mkv"), same)
	writeFile(t, filepath.Join(dir, "sub", "b-different-name.mkv"), same) // dup of a, different name + dir
	writeFile(t, filepath.Join(dir, "unique.mkv"), []byte("a wholly different file body here"))
	// Same SIZE as `same` but different content → must NOT be grouped (guards
	// the size pre-filter against false positives).
	writeFile(t, filepath.Join(dir, "collide.mkv"), []byte(strings.Repeat("x", len(same))))

	groups, err := findDuplicates(context.Background(), b, "Test", "")
	if err != nil {
		t.Fatalf("findDuplicates: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected exactly 1 duplicate group, got %d: %+v", len(groups), groups)
	}
	if len(groups[0].Files) != 2 {
		t.Fatalf("expected 2 files in the group, got %d", len(groups[0].Files))
	}
	paths := []string{groups[0].Files[0].Path, groups[0].Files[1].Path}
	if !dedupContains(paths, "a.mkv") || !dedupContains(paths, "sub/b-different-name.mkv") {
		t.Fatalf("unexpected group members: %v", paths)
	}
}

func TestFindDuplicates_LargeFilesSampledHeadTail(t *testing.T) {
	b, dir := dedupBrowser(t)
	// Files larger than 2*dupSampleBytes exercise the head+tail sampling path
	// (no full read). Two identical large files must group; one with a different
	// head must not.
	big := make([]byte, 3*dupSampleBytes)
	for i := range big {
		big[i] = byte(i % 7)
	}
	writeFile(t, filepath.Join(dir, "big-a.bin"), big)
	writeFile(t, filepath.Join(dir, "big-a-copy.bin"), big)

	diff := make([]byte, len(big))
	copy(diff, big)
	diff[0] ^= 0xFF // differ in the sampled head
	writeFile(t, filepath.Join(dir, "big-diff.bin"), diff)

	groups, err := findDuplicates(context.Background(), b, "Test", "")
	if err != nil {
		t.Fatalf("findDuplicates: %v", err)
	}
	if len(groups) != 1 || len(groups[0].Files) != 2 {
		t.Fatalf("expected 1 group of 2, got %+v", groups)
	}
}

func TestFingerprintFile_IdenticalSmallFilesMatch(t *testing.T) {
	dir := t.TempDir()
	small := []byte("hello chapters and duplicates")
	writeFile(t, filepath.Join(dir, "s1"), small)
	writeFile(t, filepath.Join(dir, "s2"), small)
	f1, _ := fingerprintFile(filepath.Join(dir, "s1"), int64(len(small)))
	f2, _ := fingerprintFile(filepath.Join(dir, "s2"), int64(len(small)))
	if f1 == "" || f1 != f2 {
		t.Fatalf("identical small files must share a fingerprint: %q vs %q", f1, f2)
	}
}

func TestFindDuplicates_NoDuplicates(t *testing.T) {
	b, dir := dedupBrowser(t)
	writeFile(t, filepath.Join(dir, "a.mkv"), []byte("aaaa"))
	writeFile(t, filepath.Join(dir, "b.mkv"), []byte("bbbbbb"))
	groups, err := findDuplicates(context.Background(), b, "Test", "")
	if err != nil {
		t.Fatalf("findDuplicates: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("expected no groups, got %d", len(groups))
	}
}

func TestLocalDuplicates_Endpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, dir := dedupBrowser(t)
	same := []byte("dup body dup body dup body")
	writeFile(t, filepath.Join(dir, "one.mkv"), same)
	writeFile(t, filepath.Join(dir, "two.mkv"), same)

	r := gin.New()
	r.GET("/d", LocalDuplicates(b))
	req := httptest.NewRequest("GET", "/d?mount=Test&path=", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Total  int        `json:"total"`
		Groups []dupGroup `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Total != 1 || len(resp.Groups) != 1 || len(resp.Groups[0].Files) != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestLocalDuplicates_MissingMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b, _ := dedupBrowser(t)
	r := gin.New()
	r.GET("/d", LocalDuplicates(b))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/d", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func TestDeleteDuplicates_RemovesAndGuards(t *testing.T) {
	b, dir := dedupBrowser(t)
	writeFile(t, filepath.Join(dir, "keep.mkv"), []byte("body"))
	writeFile(t, filepath.Join(dir, "drop.mkv"), []byte("body"))

	deleted, errs := deleteDuplicates(b, nil, nil, "Test", dir, []string{
		"drop.mkv",
		"../escape.mkv", // traversal → rejected
		"missing.mkv",   // not on disk → reported, not fatal
	})
	if deleted != 1 {
		t.Fatalf("deleted=%d want 1; errs=%v", deleted, errs)
	}
	if _, err := os.Stat(filepath.Join(dir, "drop.mkv")); !os.IsNotExist(err) {
		t.Fatal("drop.mkv should be gone")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.mkv")); err != nil {
		t.Fatal("keep.mkv must survive")
	}
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors (escape + missing), got %v", errs)
	}
}

func TestWithinBase(t *testing.T) {
	base := filepath.Join(string(filepath.Separator), "mnt", "user")
	if !withinBase(base, filepath.Join(base, "movies", "a.mkv")) {
		t.Error("a file under base should pass")
	}
	if withinBase(base, base) {
		t.Error("the base itself must not pass")
	}
	if withinBase(base, filepath.Join(string(filepath.Separator), "mnt", "other", "a.mkv")) {
		t.Error("a sibling dir must not pass")
	}
}

func dedupContains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
