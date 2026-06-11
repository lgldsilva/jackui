package handlers

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/contentid"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func dedupPC(data []byte, pieceLen int64) contentid.PieceCheck {
	var hs [][]byte
	for off := int64(0); off < int64(len(data)); off += pieceLen {
		end := off + pieceLen
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		s := sha1.Sum(data[off:end])
		hs = append(hs, append([]byte(nil), s[:]...))
	}
	return contentid.PieceCheck{PieceLen: pieceLen, FileStart: 0, FileLen: int64(len(data)), PieceHashes: hs}
}

func dedupData(n int) []byte {
	d := make([]byte, n)
	for i := range d {
		d[i] = byte((i*7 + 5) % 251)
	}
	return d
}

func dedupStore(t *testing.T) *downloads.Store {
	t.Helper()
	s, err := downloads.New(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPickMatch_CertainLocalWins(t *testing.T) {
	data := dedupData(4 * 1024)
	pc := dedupPC(data, 1024)
	dir := t.TempDir()
	local := filepath.Join(dir, "local.bin")
	if err := os.WriteFile(local, data, 0o644); err != nil {
		t.Fatal(err)
	}
	f := streamer.FileInfo{Index: 0, Path: "Movie/m.mkv", Size: int64(len(data)), IsVideo: true}
	// A probable remote candidate FIRST, then the certain local one — certain wins.
	cands := []catalogCand{
		{source: "cloud", absPath: local, size: int64(len(data)), remote: true},
		{source: "download", absPath: local, size: int64(len(data)), remote: false},
	}
	// torrentFP is irrelevant here: the certain local match short-circuits before
	// any fingerprint comparison.
	m := pickMatch(f, cands, pc, true, func() string { return "" })
	if m == nil || m.Confidence != "certain" || m.Source != "download" {
		t.Fatalf("expected a certain local match, got %+v", m)
	}
	if m.Name != "m.mkv" {
		t.Fatalf("name should be the basename, got %q", m.Name)
	}
}

func TestPickMatch_ProbableRemote(t *testing.T) {
	data := dedupData(3 * 1024)
	pc := dedupPC(data, 1024)
	dir := t.TempDir()
	cloud := filepath.Join(dir, "cloud.bin")
	if err := os.WriteFile(cloud, data, 0o644); err != nil {
		t.Fatal(err)
	}
	want, _ := contentid.Fingerprint(cloud, int64(len(data)))
	f := streamer.FileInfo{Index: 0, Path: "x.mkv", Size: int64(len(data))}
	cands := []catalogCand{{source: "cloud", mount: "GDrive", relPath: "x.mkv", absPath: cloud, size: int64(len(data)), remote: true}}

	m := pickMatch(f, cands, pc, true, func() string { return want })
	if m == nil || m.Confidence != "probable" || m.Mount != "GDrive" {
		t.Fatalf("expected a probable cloud match, got %+v", m)
	}
	// Fingerprint mismatch → no match.
	if got := pickMatch(f, cands, pc, true, func() string { return "different" }); got != nil {
		t.Fatalf("a fingerprint mismatch must not match, got %+v", got)
	}
	// Empty torrent fingerprint (read failed) → no match.
	if got := pickMatch(f, cands, pc, true, func() string { return "" }); got != nil {
		t.Fatalf("empty torrent fingerprint must not match, got %+v", got)
	}
}

func TestPickMatch_NoCertainWhenContentDiffers(t *testing.T) {
	data := dedupData(3 * 1024)
	pc := dedupPC(data, 1024)
	bad := append([]byte(nil), data...)
	bad[100] ^= 0xFF
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.bin")
	if err := os.WriteFile(p, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	f := streamer.FileInfo{Index: 0, Path: "x.mkv", Size: int64(len(data))}
	cands := []catalogCand{{source: "download", absPath: p, size: int64(len(data)), remote: false}}
	if m := pickMatch(f, cands, pc, true, func() string { return "" }); m != nil {
		t.Fatalf("different local content must not be a certain match, got %+v", m)
	}
}

func TestAddDownloadCandidates(t *testing.T) {
	s := dedupStore(t)
	if _, err := s.CreateLinked(downloads.Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m"}, "/lib/a.mkv", 5000); err != nil {
		t.Fatal(err)
	}
	idx := map[int64][]catalogCand{}
	addDownloadCandidates(idx, s, 1, map[int64]bool{5000: true})
	if len(idx[5000]) != 1 || idx[5000][0].absPath != "/lib/a.mkv" || idx[5000][0].source != "download" {
		t.Fatalf("download candidate not indexed: %+v", idx[5000])
	}
}

func TestAddMountCandidates(t *testing.T) {
	dir := t.TempDir()
	body := []byte("a media file body")
	writeFile(t, filepath.Join(dir, "movie.mkv"), body)
	writeFile(t, filepath.Join(dir, "other.mkv"), []byte("different size body here"))
	b := local.NewBrowser([]config.ExternalMount{{Name: "Lib", Path: dir}})

	idx := map[int64][]catalogCand{}
	addMountCandidates(context.Background(), idx, b, "", map[int64]bool{int64(len(body)): true})
	if len(idx[int64(len(body))]) != 1 {
		t.Fatalf("expected exactly the same-size file, got %+v", idx)
	}
	c := idx[int64(len(body))][0]
	if c.source != "library" || c.mount != "Lib" || c.relPath != "movie.mkv" {
		t.Fatalf("unexpected mount candidate: %+v", c)
	}
}

func TestAddMountCandidates_UserSubpathNoDoublePrefix(t *testing.T) {
	// Regression: a UserSubpath mount must match the user's file with a relPath
	// that round-trips through ResolvePathFor (no {user}/{user}/ doubling).
	dir := t.TempDir()
	body := []byte("user scoped media body")
	writeFile(t, filepath.Join(dir, "alice", "film.mkv"), body)
	writeFile(t, filepath.Join(dir, "bob", "secret.mkv"), body) // another user, same size — must NOT leak
	b := local.NewBrowser([]config.ExternalMount{{Name: "Mine", Path: dir, UserSubpath: true}})

	idx := map[int64][]catalogCand{}
	addMountCandidates(context.Background(), idx, b, "alice", map[int64]bool{int64(len(body)): true})
	cands := idx[int64(len(body))]
	if len(cands) != 1 {
		t.Fatalf("expected exactly alice's file (no bob leak), got %+v", cands)
	}
	c := cands[0]
	if c.relPath != "film.mkv" {
		t.Fatalf("relPath should be stripped of the user prefix, got %q", c.relPath)
	}
	if _, err := os.Stat(c.absPath); err != nil {
		t.Fatalf("resolved absPath must exist (no double prefix): %q err=%v", c.absPath, err)
	}
}

func TestFindDedupMatches_EmptyWhenNoActiveTorrent(t *testing.T) {
	// NewForTesting streamer has no active torrent → FilePieceCheck/FingerprintFile
	// error → no match surfaces even when a same-size candidate exists.
	s := streamer.NewForTesting()
	st := dedupStore(t)
	if _, err := st.CreateLinked(downloads.Download{UserID: 1, InfoHash: "h", FileIndex: 0, Magnet: "m"}, "/lib/a.mkv", 1234); err != nil {
		t.Fatal(err)
	}
	info := &streamer.TorrentInfo{InfoHash: "00", Files: []streamer.FileInfo{{Index: 0, Path: "a.mkv", Size: 1234}}}
	got := findDedupMatches(context.Background(), s, st, nil, [20]byte{}, info, "", 1)
	if len(got) != 0 {
		t.Fatalf("no active torrent → no matches, got %+v", got)
	}
	// No files at all → empty, no panic.
	if got := findDedupMatches(context.Background(), s, st, nil, [20]byte{}, &streamer.TorrentInfo{}, "", 1); len(got) != 0 {
		t.Fatalf("no files → empty, got %+v", got)
	}
}

func dedupCtx(t *testing.T, userID int, username, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("auth.claims", &auth.Claims{UserID: userID, Username: username, Role: auth.RoleUser})
	return c, w
}

func TestDedupLink_Endpoint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "movie.mkv"), []byte("body bytes"))
	b := local.NewBrowser([]config.ExternalMount{{Name: "Lib", Path: dir}})
	st := dedupStore(t)

	body := `{"infoHash":"abc","magnet":"magnet:abc","name":"Movie","items":[{"fileIndex":0,"mount":"Lib","relPath":"movie.mkv"}]}`
	c, w := dedupCtx(t, 1, "alice", body)
	DedupLink(st, b)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Linked int      `json:"linked"`
		Errors []string `json:"errors"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Linked != 1 {
		t.Fatalf("linked=%d errors=%v", resp.Linked, resp.Errors)
	}
	got, err := st.GetByKey(1, "abc", 0)
	if err != nil || got == nil || !got.Linked || got.Status != downloads.StatusCompleted {
		t.Fatalf("linked row not created correctly: %+v err=%v", got, err)
	}
}

func TestDedupLink_BadRequestsAndGuards(t *testing.T) {
	dir := t.TempDir()
	b := local.NewBrowser([]config.ExternalMount{{Name: "Lib", Path: dir}})
	st := dedupStore(t)

	// Missing infoHash → 400.
	c, w := dedupCtx(t, 1, "alice", `{"items":[{"fileIndex":0,"mount":"Lib","relPath":"x"}]}`)
	DedupLink(st, b)(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing infoHash: status=%d", w.Code)
	}

	// Inaccessible mount + missing file → reported as errors, linked=0.
	c, w = dedupCtx(t, 1, "alice", `{"infoHash":"h","items":[{"fileIndex":0,"mount":"Nope","relPath":"x"},{"fileIndex":-1,"mount":"Lib","relPath":"y"},{"fileIndex":0,"mount":"Lib","relPath":"missing.mkv"}]}`)
	DedupLink(st, b)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	var resp struct {
		Linked int      `json:"linked"`
		Errors []string `json:"errors"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Linked != 0 || len(resp.Errors) != 3 {
		t.Fatalf("expected 0 linked + 3 errors, got linked=%d errors=%v", resp.Linked, resp.Errors)
	}
}

func TestMakeMatch_NameFallback(t *testing.T) {
	// A file with an empty/odd torrent path falls back to the raw path as the name.
	m := makeMatch(streamer.FileInfo{Index: 2, Path: "", Size: 9}, catalogCand{source: "download"}, "certain")
	if m.Name != "" || m.FileIndex != 2 {
		t.Fatalf("name fallback wrong: %+v", m)
	}
	m2 := makeMatch(streamer.FileInfo{Index: 0, Path: "Dir/Sub/clip.mp4", Size: 9}, catalogCand{source: "library"}, "certain")
	if m2.Name != "clip.mp4" {
		t.Fatalf("basename wrong: %q", m2.Name)
	}
}

func TestDedupCheck_MissingMagnet(t *testing.T) {
	c, w := dedupCtx(t, 1, "alice", `{}`)
	DedupCheck(streamer.NewForTesting(), dedupStore(t), nil)(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing magnet: status=%d", w.Code)
	}
}
