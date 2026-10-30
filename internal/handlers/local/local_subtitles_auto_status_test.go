package local

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/subtitles"
)

// ─── fakes ──────────────────────────────────────────────────────────────────

// fakeLocalFetcher records StartLocal calls and scripts Status replies.
type fakeLocalFetcher struct {
	mu     sync.Mutex
	status subtitles.AutoStatus
	local  []string // "hashKey#fileIdx|name|path"
}

func (f *fakeLocalFetcher) StartLocal(hashKey string, fileIdx int, name, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.local = append(f.local, hashKey+"#"+strconvItoa(fileIdx)+"|"+name+"|"+path)
}

func (f *fakeLocalFetcher) Status(_ context.Context, _ string, _ int) subtitles.AutoStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Mirror the real AutoFetcher.Status: it ALWAYS reports one of the four
	// vocabulary values — never an empty status — so an unscripted fake means
	// "none".
	if f.status.Status == "" {
		return subtitles.AutoStatus{Status: subtitleStatusNone}
	}
	return f.status
}

func (f *fakeLocalFetcher) startCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.local)
}

func (f *fakeLocalFetcher) lastStart() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.local) == 0 {
		return ""
	}
	return f.local[len(f.local)-1]
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// ─── decodeLocalPseudoHash (named, tested guard) ────────────────────────────

func TestDecodeLocalPseudoHash_Valid(t *testing.T) {
	hash := localInfoHash("Filmes", "Movie 2024/a.mkv")
	mount, path, ok := decodeLocalPseudoHash(hash)
	if !ok {
		t.Fatalf("decode %q failed", hash)
	}
	if mount != "Filmes" || path != "Movie 2024/a.mkv" {
		t.Errorf("got mount=%q path=%q", mount, path)
	}
}

func TestDecodeLocalPseudoHash_RejectsTorrentHashAndCorrupt(t *testing.T) {
	cases := []struct {
		name string
		hash string
	}{
		{"torrent hex hash", "0123456789abcdef0123456789abcdef01234567"},
		{"no prefix", "YWJj"},
		{"corrupt base64", "local-%%%not-base64%%%"},
		{"empty json fields", "local-e30"},           // "{}"
		{"missing path", "local-eyJtb3VudCI6Ik0ifQ"}, // {"mount":"M"}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := decodeLocalPseudoHash(tc.hash); ok {
				t.Errorf("decodeLocalPseudoHash(%q) = ok, want false", tc.hash)
			}
		})
	}
}

// ─── harness ────────────────────────────────────────────────────────────────

// autoCfg builds the config slice for the status handler tests.
func autoCfg(autoFetch bool) *config.Config {
	return &config.Config{Subtitles: config.SubtitlesConfig{
		AutoFetch:        autoFetch,
		DefaultLanguages: "pt-BR,pt",
	}}
}

// enabledClient builds an OpenSubtitles client whose Enabled() matches enabled
// (no network involved — only the API key decides).
func enabledClient(t *testing.T) *subtitles.Client {
	t.Helper()
	return subtitles.New("test-api-key", "", "", t.TempDir())
}

// newAutoStatusLocalEnv builds a browser over a temp mount containing one
// video (rel "movies/movie.mkv") plus the status route wired to the fake
// fetcher and a real LinksStore. Returns the router, the pseudo-hash and the
// absolute path of the fixture file.
func newAutoStatusLocalEnv(t *testing.T, cfg *config.Config, client *subtitles.Client,
	fetcher *fakeLocalFetcher, claims *auth.Claims) (*gin.Engine, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(root, "movies", "movie.mkv")
	if err := os.WriteFile(abs, []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: root}})
	pool := dbtest.NewDB(t)
	links, err := subtitles.NewLinksStore(pool)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	r := gin.New()
	r.Use(withClaims(claims))
	r.GET("/api/local/subtitles/auto/:hash/:file",
		LocalSubtitlesAutoStatus(b, links, client, fetcher, cfg))
	return r, localInfoHash("M", "movies/movie.mkv"), abs
}

func getLocalAutoStatus(r *gin.Engine, hash, file string) *httptest.ResponseRecorder {
	return getLocalAutoStatusQuery(r, hash, file, "")
}

func getLocalAutoStatusQuery(r *gin.Engine, hash, file, rawQuery string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	url := "/api/local/subtitles/auto/" + hash + "/" + file
	if rawQuery != "" {
		url += "?" + rawQuery
	}
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

func decodeLocalAutoStatus(t *testing.T, w *httptest.ResponseRecorder) subtitleAutoStatusResponse {
	t.Helper()
	var resp subtitleAutoStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return resp
}

// ─── 400 / 404 paths ────────────────────────────────────────────────────────

func TestLocalSubtitlesAutoStatus_BadPseudoHash(t *testing.T) {
	fetcher := &fakeLocalFetcher{}
	r, _, _ := newAutoStatusLocalEnv(t, autoCfg(true), enabledClient(t), fetcher, nil)
	w := getLocalAutoStatus(r, "0123456789abcdef0123456789abcdef01234567", "0")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestLocalSubtitlesAutoStatus_NonNumericFile(t *testing.T) {
	fetcher := &fakeLocalFetcher{}
	r, hash, _ := newAutoStatusLocalEnv(t, autoCfg(true), enabledClient(t), fetcher, nil)
	w := getLocalAutoStatus(r, hash, "xyz")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

// TestLocalSubtitlesAutoStatus_NonZeroFileRejected: local files are ALWAYS
// addressed by index 0 (the LocalPlay hook hardcodes it), so any other :file
// is rejected with 400 — a poller sending a bogus index would otherwise mint a
// second (localInfoHash, fileIdx) dedup row and kick a second, quota-charged
// fetch for the SAME file (second-model audit §5).
func TestLocalSubtitlesAutoStatus_NonZeroFileRejected(t *testing.T) {
	for _, file := range []string{"3", "-1"} {
		fetcher := &fakeLocalFetcher{}
		r, hash, _ := newAutoStatusLocalEnv(t, autoCfg(true), enabledClient(t), fetcher, nil)
		w := getLocalAutoStatus(r, hash, file)
		if w.Code != http.StatusBadRequest {
			t.Errorf("file %q: status = %d, want 400; body: %s", file, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "file index 0") {
			t.Errorf("file %q: error message %q must document the index-0 rule", file, w.Body.String())
		}
		if fetcher.startCalls() != 0 {
			t.Errorf("file %q: StartLocal must not run for a non-zero local file index", file)
		}
	}
}

func TestLocalSubtitlesAutoStatus_MissingFile(t *testing.T) {
	fetcher := &fakeLocalFetcher{}
	r, _, _ := newAutoStatusLocalEnv(t, autoCfg(true), enabledClient(t), fetcher, nil)
	hash := localInfoHash("M", "movies/gone.mkv")
	w := getLocalAutoStatus(r, hash, "0")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
	if fetcher.startCalls() != 0 {
		t.Errorf("StartLocal must not run for a missing file")
	}
}

// ─── kick-pending / none ────────────────────────────────────────────────────

func TestLocalSubtitlesAutoStatus_NoRowKicksStartLocalAndReturnsPending(t *testing.T) {
	fetcher := &fakeLocalFetcher{}
	r, hash, abs := newAutoStatusLocalEnv(t, autoCfg(true), enabledClient(t), fetcher, nil)
	w := getLocalAutoStatus(r, hash, "0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	resp := decodeLocalAutoStatus(t, w)
	if resp.Status != subtitles.StatusPending {
		t.Errorf("status = %q, want pending", resp.Status)
	}
	if resp.Subtitle != nil {
		t.Errorf("subtitle = %+v, want nil for pending", resp.Subtitle)
	}
	if fetcher.startCalls() != 1 {
		t.Fatalf("StartLocal calls = %d, want 1", fetcher.startCalls())
	}
	// The dedup key is the SAME pseudo-hash; name+abs are the resolved file.
	want := hash + "#0|movie.mkv|" + abs
	if got := fetcher.lastStart(); got != want {
		t.Errorf("StartLocal args = %q, want %q", got, want)
	}
}

func TestLocalSubtitlesAutoStatus_AutoFetchOffReturnsNoneWithoutKicking(t *testing.T) {
	fetcher := &fakeLocalFetcher{}
	r, hash, _ := newAutoStatusLocalEnv(t, autoCfg(false), enabledClient(t), fetcher, nil)
	w := getLocalAutoStatus(r, hash, "0")
	resp := decodeLocalAutoStatus(t, w)
	if resp.Status != subtitleStatusNone {
		t.Errorf("status = %q, want none", resp.Status)
	}
	if fetcher.startCalls() != 0 {
		t.Errorf("StartLocal calls = %d, want 0 (auto-fetch off)", fetcher.startCalls())
	}
}

func TestLocalSubtitlesAutoStatus_ClientDisabledReturnsNoneWithoutKicking(t *testing.T) {
	fetcher := &fakeLocalFetcher{}
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.mkv"), []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: root}})
	pool := dbtest.NewDB(t)
	links, err := subtitles.NewLinksStore(pool)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	disabled := subtitles.New("", "", "", t.TempDir())
	r2 := gin.New()
	r2.GET("/api/local/subtitles/auto/:hash/:file",
		LocalSubtitlesAutoStatus(b, links, disabled, fetcher, autoCfg(true)))
	w := getLocalAutoStatus(r2, localInfoHash("M", "movie.mkv"), "0")
	resp := decodeLocalAutoStatus(t, w)
	if resp.Status != subtitleStatusNone {
		t.Errorf("status = %q, want none", resp.Status)
	}
	if fetcher.startCalls() != 0 {
		t.Errorf("StartLocal calls = %d, want 0 (client disabled)", fetcher.startCalls())
	}
}

// ─── ready → subtitle block ─────────────────────────────────────────────────

func TestLocalSubtitlesAutoStatus_ReadyReturnsSubtitleWithVTTURL(t *testing.T) {
	fetcher := &fakeLocalFetcher{status: subtitles.AutoStatus{
		Status: subtitles.StatusReady, FileID: "42", Language: "pt-BR", Forced: true,
	}}
	r, hash, _ := newAutoStatusLocalEnv(t, autoCfg(true), enabledClient(t), fetcher, nil)
	w := getLocalAutoStatus(r, hash, "0")
	resp := decodeLocalAutoStatus(t, w)
	if resp.Status != subtitles.StatusReady || resp.Subtitle == nil {
		t.Fatalf("resp = %+v, want ready with subtitle", resp)
	}
	if resp.Subtitle.ID != "42" || resp.Subtitle.Language != "pt-BR" || !resp.Subtitle.Forced {
		t.Errorf("subtitle = %+v", resp.Subtitle)
	}
	if resp.Subtitle.VTTURL != "/api/subtitles/download/42" {
		t.Errorf("vttUrl = %q", resp.Subtitle.VTTURL)
	}
	// A ready row means there is nothing left to kick.
	if fetcher.startCalls() != 0 {
		t.Errorf("StartLocal calls = %d, want 0 for ready row", fetcher.startCalls())
	}
}

// ─── ?user= view-as interaction (W1 review note 3) ──────────────────────────

// TestLocalSubtitlesAutoStatus_AdminViewAsUserHonored proves the ?user=
// override flows through the SAME ScopePath chokepoint as the query-param
// endpoints: an admin targeting bob resolves bob's per-user file and kicks
// StartLocal with bob's absolute path.
func TestLocalSubtitlesAutoStatus_AdminViewAsUserHonored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	for _, u := range []string{"bob", "alice"} {
		if err := os.MkdirAll(filepath.Join(root, u), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, u, "movie.mkv"), []byte(u), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: root, UserSubpath: true}})
	pool := dbtest.NewDB(t)
	links, err := subtitles.NewLinksStore(pool)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	fetcher := &fakeLocalFetcher{}
	r := gin.New()
	r.Use(withClaims(&auth.Claims{UserID: 1, Username: "alice", Role: auth.RoleAdmin}))
	r.GET("/api/local/subtitles/auto/:hash/:file",
		LocalSubtitlesAutoStatus(b, links, enabledClient(t), fetcher, autoCfg(true)))

	// The hash encodes the USER-RELATIVE path the admin saw while viewing bob.
	hash := localInfoHash("Meus downloads", "movie.mkv")
	w := getLocalAutoStatusQuery(r, hash, "0", "user=bob")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if fetcher.startCalls() != 1 {
		t.Fatalf("StartLocal calls = %d, want 1", fetcher.startCalls())
	}
	wantPath := filepath.Join(root, "bob", "movie.mkv")
	if got := fetcher.lastStart(); got != hash+"#0|movie.mkv|"+wantPath {
		t.Errorf("StartLocal args = %q, want resolve under %q", got, wantPath)
	}
}

// TestLocalSubtitlesAutoStatus_NonAdminUserOverrideIgnored proves a non-admin
// cannot cross the per-user boundary with ?user= (scopeUser drops it): the
// request resolves the caller's OWN subdir, which has no such file → 404.
func TestLocalSubtitlesAutoStatus_NonAdminUserOverrideIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bob"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bob", "movie.mkv"), []byte("bob"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := lb.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: root, UserSubpath: true}})
	pool := dbtest.NewDB(t)
	links, err := subtitles.NewLinksStore(pool)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	fetcher := &fakeLocalFetcher{}
	r := gin.New()
	r.Use(withClaims(&auth.Claims{UserID: 2, Username: "alice", Role: auth.RoleUser}))
	r.GET("/api/local/subtitles/auto/:hash/:file",
		LocalSubtitlesAutoStatus(b, links, enabledClient(t), fetcher, autoCfg(true)))

	hash := localInfoHash("Meus downloads", "movie.mkv")
	w := getLocalAutoStatusQuery(r, hash, "0", "user=bob")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (override ignored, alice has no such file); body: %s",
			w.Code, w.Body.String())
	}
	if fetcher.startCalls() != 0 {
		t.Errorf("StartLocal calls = %d, want 0", fetcher.startCalls())
	}
}
