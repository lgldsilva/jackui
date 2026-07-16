package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func writeZipFile(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func previewTestRouter(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()

	writeZipFile(t, filepath.Join(dir, "files.zip"), map[string][]byte{
		"readme.nfo":  []byte("release notes ä"),
		"art/img.png": []byte("pngbytes"),
		"evil.svg":    []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"tool.exe":    []byte{0x4d, 0x5a},
	})
	writeZipFile(t, filepath.Join(dir, "comic.cbz"), map[string][]byte{
		"p10.jpg":   []byte("page-ten"),
		"p2.jpg":    []byte("page-two"),
		"notes.txt": []byte("not a page"),
	})
	writeZipFile(t, filepath.Join(dir, "book.epub"), map[string][]byte{
		"META-INF/container.xml": []byte(`<?xml version="1.0"?><container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`),
		"OEBPS/content.opf": []byte(`<?xml version="1.0"?><package><metadata xmlns:dc="x"><dc:title>T</dc:title></metadata>
			<manifest><item id="c1" href="ch1.xhtml"/><item id="css" href="style.css"/></manifest>
			<spine><itemref idref="c1"/></spine></package>`),
		"OEBPS/ch1.xhtml": []byte(`<html><head></head><body><script>alert(1)</script><img src="img/p.png"/><p>oi</p></body></html>`),
		"OEBPS/style.css": []byte("p{margin:0}"),
		"OEBPS/img/p.png": []byte("pngbytes"),
	})
	if err := os.WriteFile(filepath.Join(dir, "fake.rar"), []byte("not really rar"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "video.mkv"), []byte("ebml"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := local.NewBrowser([]config.ExternalMount{{Name: "Media", Path: dir}})
	d := PreviewDeps{Local: b}

	router := gin.New()
	router.GET("/api/preview/archive", PreviewArchiveList(d))
	router.GET("/api/preview/archive/entry", PreviewArchiveEntry(d))
	router.GET("/api/preview/comic", PreviewComicManifest(d))
	router.GET("/api/preview/comic/page", PreviewComicPage(d))
	router.GET("/api/preview/epub", PreviewEpubManifest(d))
	router.GET("/api/preview/epub/chapter", PreviewEpubChapter(d))
	router.GET("/api/preview/epub/res", PreviewEpubResource(d))
	return router, dir
}

func previewGET(router *gin.Engine, url string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestPreviewArchiveListLocal(t *testing.T) {
	router, _ := previewTestRouter(t)
	w := previewGET(router, "/api/preview/archive?mount=Media&path=files.zip")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Format    string `json:"format"`
		Truncated bool   `json:"truncated"`
		Entries   []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json: %v", err)
	}
	if resp.Format != "zip" || resp.Truncated || len(resp.Entries) != 4 {
		t.Errorf("resp = %+v", resp)
	}
}

func TestPreviewArchiveEntryText(t *testing.T) {
	router, _ := previewTestRouter(t)
	w := previewGET(router, "/api/preview/archive/entry?mount=Media&path=files.zip&name=readme.nfo")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q (must NEVER be active content)", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	if !strings.Contains(w.Body.String(), "release notes") {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestPreviewArchiveEntrySVGSandboxed(t *testing.T) {
	router, _ := previewTestRouter(t)
	w := previewGET(router, "/api/preview/archive/entry?mount=Media&path=files.zip&name=evil.svg")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if w.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Error("SVG served without CSP sandbox — direct navigation could script our origin")
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}

func TestPreviewArchiveEntryRefusals(t *testing.T) {
	router, _ := previewTestRouter(t)
	cases := []struct {
		url  string
		want int
	}{
		{"/api/preview/archive/entry?mount=Media&path=files.zip&name=tool.exe", http.StatusUnsupportedMediaType},
		{"/api/preview/archive/entry?mount=Media&path=files.zip&name=../../../etc/passwd.txt", http.StatusNotFound},
		{"/api/preview/archive/entry?mount=Media&path=files.zip&name=missing.txt", http.StatusNotFound},
		{"/api/preview/archive/entry?mount=Media&path=video.mkv&name=a.txt", http.StatusUnsupportedMediaType},
		{"/api/preview/archive?mount=Media&path=../../etc", http.StatusBadRequest},
		{"/api/preview/archive?mount=Nope&path=files.zip", http.StatusForbidden},
		{"/api/preview/archive?mount=Media&path=ghost.zip", http.StatusNotFound},
		{"/api/preview/archive", http.StatusBadRequest},
		{"/api/preview/archive?mount=Media", http.StatusBadRequest},
		{"/api/preview/archive?mount=Media&path=fake.rar", http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		if w := previewGET(router, tc.url); w.Code != tc.want {
			t.Errorf("GET %s = %d, want %d (body=%s)", tc.url, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestPreviewComicManifestAndPage(t *testing.T) {
	router, _ := previewTestRouter(t)
	w := previewGET(router, "/api/preview/comic?mount=Media&path=comic.cbz")
	if w.Code != http.StatusOK {
		t.Fatalf("manifest status = %d", w.Code)
	}
	var resp struct {
		Pages []string `json:"pages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Pages) != 2 || resp.Pages[0] != "p2.jpg" || resp.Pages[1] != "p10.jpg" {
		t.Errorf("pages = %v, want natural order [p2.jpg p10.jpg]", resp.Pages)
	}

	pw := previewGET(router, "/api/preview/comic/page?mount=Media&path=comic.cbz&name=p2.jpg")
	if pw.Code != http.StatusOK || pw.Body.String() != "page-two" {
		t.Errorf("page = %d %q", pw.Code, pw.Body.String())
	}
	if ct := pw.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("page Content-Type = %q", ct)
	}
	if pw.Header().Get("Cache-Control") == "" {
		t.Error("comic page should be cacheable")
	}

	if bad := previewGET(router, "/api/preview/comic/page?mount=Media&path=comic.cbz&name=notes.txt"); bad.Code != http.StatusUnsupportedMediaType {
		t.Errorf("non-image page status = %d, want 415", bad.Code)
	}
}

func TestPreviewEpubFlow(t *testing.T) {
	router, _ := previewTestRouter(t)

	w := previewGET(router, "/api/preview/epub?mount=Media&path=book.epub")
	if w.Code != http.StatusOK {
		t.Fatalf("manifest status = %d, body=%s", w.Code, w.Body.String())
	}
	var book struct {
		Title    string   `json:"title"`
		Chapters []string `json:"chapters"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &book); err != nil {
		t.Fatal(err)
	}
	if book.Title != "T" || len(book.Chapters) != 1 || book.Chapters[0] != "OEBPS/ch1.xhtml" {
		t.Errorf("book = %+v", book)
	}

	cw := previewGET(router, "/api/preview/epub/chapter?mount=Media&path=book.epub&name=OEBPS/ch1.xhtml")
	if cw.Code != http.StatusOK {
		t.Fatalf("chapter status = %d, body=%s", cw.Code, cw.Body.String())
	}
	if cw.Header().Get("Content-Security-Policy") != "sandbox" {
		t.Error("chapter without CSP sandbox")
	}
	body := cw.Body.String()
	if strings.Contains(body, "<script") {
		t.Error("chapter script not stripped")
	}
	if !strings.Contains(body, "/api/preview/epub/res?") || !strings.Contains(body, "name=OEBPS%2Fimg%2Fp.png") {
		t.Errorf("img ref not rewritten to res endpoint:\n%s", body)
	}
	if !strings.Contains(body, "mount=Media") {
		t.Errorf("res URL lost the source params:\n%s", body)
	}

	// A doc NOT in the spine must not be servable as a chapter (e.g. the OPF).
	if nw := previewGET(router, "/api/preview/epub/chapter?mount=Media&path=book.epub&name=OEBPS/content.opf"); nw.Code != http.StatusNotFound {
		t.Errorf("off-spine chapter status = %d, want 404", nw.Code)
	}

	rw := previewGET(router, "/api/preview/epub/res?mount=Media&path=book.epub&name=OEBPS/style.css")
	if rw.Code != http.StatusOK || rw.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Errorf("css res = %d, ct=%q", rw.Code, rw.Header().Get("Content-Type"))
	}
	if bad := previewGET(router, "/api/preview/epub/res?mount=Media&path=book.epub&name=OEBPS/ch1.xhtml"); bad.Code != http.StatusUnsupportedMediaType {
		t.Errorf("xhtml as res status = %d, want 415 (chapters only via /chapter)", bad.Code)
	}
}

func TestPreviewTorrentSourceErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	d := PreviewDeps{} // no streamer wired
	router := gin.New()
	router.GET("/api/preview/archive", PreviewArchiveList(d))

	if w := previewGET(router, "/api/preview/archive?hash=00ff&idx=0"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil streamer status = %d, want 503", w.Code)
	}

	withStreamer := PreviewDeps{Streamer: streamer.NewForTesting()}
	router2 := gin.New()
	router2.GET("/api/preview/archive", PreviewArchiveList(withStreamer))
	validHash := strings.Repeat("ab", 20)
	cases := []struct {
		url  string
		want int
	}{
		{"/api/preview/archive?hash=zz&idx=0", http.StatusBadRequest},                // malformed hash
		{"/api/preview/archive?hash=" + validHash + "&idx=x", http.StatusBadRequest}, // malformed idx
		{"/api/preview/archive?hash=" + validHash + "&idx=0", http.StatusNotFound},   // torrent not active
	}
	for _, tc := range cases {
		if w := previewGET(router2, tc.url); w.Code != tc.want {
			t.Errorf("GET %s = %d, want %d (body=%s)", tc.url, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestPreviewWrongFormatErrors(t *testing.T) {
	router, _ := previewTestRouter(t)
	cases := []struct {
		url  string
		want int
	}{
		// Non-container files refused before any decoding.
		{"/api/preview/archive?mount=Media&path=video.mkv", http.StatusUnsupportedMediaType},
		{"/api/preview/comic?mount=Media&path=video.mkv", http.StatusUnsupportedMediaType},
		{"/api/preview/comic/page?mount=Media&path=video.mkv&name=p.jpg", http.StatusUnsupportedMediaType},
		{"/api/preview/comic/page?mount=Media&path=comic.cbz&name=ghost.jpg", http.StatusNotFound},
		// Corrupt rar surfaces as unprocessable everywhere.
		{"/api/preview/comic?mount=Media&path=fake.rar", http.StatusUnprocessableEntity},
		// Plain zip is not an EPUB → 404 (container.xml entry not found).
		{"/api/preview/epub?mount=Media&path=files.zip", http.StatusNotFound},
		{"/api/preview/epub/chapter?mount=Media&path=files.zip&name=x.xhtml", http.StatusNotFound},
	}
	for _, tc := range cases {
		if w := previewGET(router, tc.url); w.Code != tc.want {
			t.Errorf("GET %s = %d, want %d (body=%s)", tc.url, w.Code, tc.want, w.Body.String())
		}
	}
}

// A zip-bomb style entry (decompresses past MaxEntryBytes) must map to 413.
func TestPreviewArchiveEntryTooLarge(t *testing.T) {
	router, dir := previewTestRouter(t)
	writeZipFile(t, filepath.Join(dir, "bomb.zip"), map[string][]byte{
		"huge.txt": bytes.Repeat([]byte("0"), int(11<<20)), // 11 MiB > 10 MiB cap
	})
	w := previewGET(router, "/api/preview/archive/entry?mount=Media&path=bomb.zip&name=huge.txt")
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}

// A completed download must be previewable by hash WITHOUT an active torrent:
// the resolver finds the on-disk file via the downloads store (same path
// StreamFile uses) and serves random access from there.
func TestPreviewTorrentFromCompletedStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bundle.cbz")
	writeZipFile(t, zipPath, map[string][]byte{"p1.jpg": []byte("j"), "p2.jpg": []byte("j")})
	d, err := store.Create(downloads.Download{UserID: 0, InfoHash: hgBHexHash, FileIndex: 0, Magnet: "m", Name: "bundle.cbz", FilePath: zipPath})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetStatus(0, d.ID, downloads.StatusCompleted); err != nil {
		t.Fatalf("set status: %v", err)
	}

	router := gin.New()
	router.GET("/api/preview/comic", PreviewComicManifest(PreviewDeps{Streamer: streamer.NewForTesting(), Downloads: store}))
	w := previewGET(router, "/api/preview/comic?hash="+hgBHexHash+"&idx=0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "p1.jpg") {
		t.Errorf("body = %s", w.Body.String())
	}

	// Completed row pointing at a vanished file → falls through to the
	// streamer, which has no active torrent → 404 (not a 500).
	gone, err := store.Create(downloads.Download{UserID: 0, InfoHash: strings.Repeat("cd", 20), FileIndex: 0, Magnet: "m", Name: "gone.cbz", FilePath: filepath.Join(dir, "gone.cbz")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetStatus(0, gone.ID, downloads.StatusCompleted); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if w := previewGET(router, "/api/preview/comic?hash="+strings.Repeat("cd", 20)+"&idx=0"); w.Code != http.StatusNotFound {
		t.Errorf("vanished completed file: status = %d, want 404", w.Code)
	}
}

// Unreadable file (exists but no permission) → 500 from openLocalPreviewFile.
func TestPreviewLocalUnreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can open anything")
	}
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	p := filepath.Join(dir, "sealed.zip")
	writeZipFile(t, p, map[string][]byte{"a.txt": []byte("x")})
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	b := local.NewBrowser([]config.ExternalMount{{Name: "Media", Path: dir}})
	router := gin.New()
	router.GET("/api/preview/archive", PreviewArchiveList(PreviewDeps{Local: b}))
	w := previewGET(router, "/api/preview/archive?mount=Media&path=sealed.zip")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (body=%s)", w.Code, w.Body.String())
	}
}

// StreamFile must apply the same stored-XSS guard as /api/local/file: an SVG
// (or HTML/JS) coming out of a torrent is forced to download with nosniff —
// rendering it same-origin would expose the JWT in localStorage.
func TestStreamFileSecurityHeadersOnCompleted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newDownloadsStore(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "evil.svg")
	if err := os.WriteFile(file, []byte(`<svg onload="alert(1)"/>`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d, err := store.Create(downloads.Download{UserID: 0, InfoHash: hgBHexHash, FileIndex: 0, Magnet: "m", Name: "evil.svg", FilePath: file})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.SetStatus(0, d.ID, downloads.StatusCompleted); err != nil {
		t.Fatalf("set status: %v", err)
	}

	router := gin.New()
	router.GET("/f/:hash/:file", StreamFile(streamer.NewForTesting(), store))
	w := previewGET(router, "/f/"+hgBHexHash+"/0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff on torrent-served bytes")
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want forced octet-stream for svg", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
}

func TestEpubResContentType(t *testing.T) {
	cases := []struct {
		name string
		ct   string
		ok   bool
	}{
		{"style.css", "text/css; charset=utf-8", true},
		{"f.ttf", "font/ttf", true},
		{"f.otf", "font/otf", true},
		{"f.woff", "font/woff", true},
		{"f.woff2", "font/woff2", true},
		{"img/p.png", "image/png", true},
		{"chapter.xhtml", "", false},
		{"evil.js", "", false},
	}
	for _, tc := range cases {
		ct, ok := epubResContentType(tc.name)
		if ct != tc.ct || ok != tc.ok {
			t.Errorf("epubResContentType(%q) = (%q, %v), want (%q, %v)", tc.name, ct, ok, tc.ct, tc.ok)
		}
	}
}

func TestPathDir(t *testing.T) {
	if got := pathDir("OEBPS/text/ch1.xhtml"); got != "OEBPS/text" {
		t.Errorf("pathDir nested = %q", got)
	}
	if got := pathDir("ch1.xhtml"); got != "." {
		t.Errorf("pathDir root = %q", got)
	}
}
