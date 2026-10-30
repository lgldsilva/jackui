package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
)

// ─── fakes ──────────────────────────────────────────────────────────────────

// fakeAutoFetcher records every hook call and scripts Status replies.
type fakeAutoFetcher struct {
	mu        sync.Mutex
	status    subtitles.AutoStatus
	started   []string // "hex#idx" per StartTorrent
	refined   []string // "hex#idx" per RefineOnComplete
	cancelled []string // hashKey per Cancel
	local     []string // hashKey#idx|name|path per StartLocal
}

func (f *fakeAutoFetcher) StartTorrent(_ subtitles.TorrentMeta, h metainfo.Hash, fileIdx int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, h.HexString()+"#"+strconv.Itoa(fileIdx))
}

func (f *fakeAutoFetcher) StartLocal(hashKey string, fileIdx int, name, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.local = append(f.local, hashKey+"#"+strconv.Itoa(fileIdx)+"|"+name+"|"+path)
}

func (f *fakeAutoFetcher) Status(_ context.Context, hashKey string, fileIdx int) subtitles.AutoStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Mirror the real AutoFetcher.Status: it ALWAYS reports one of the four
	// vocabulary values — never an empty status — so an unscripted fake means
	// "none".
	if f.status.Status == "" {
		return subtitles.AutoStatus{Status: statusAutoNone}
	}
	return f.status
}

func (f *fakeAutoFetcher) Cancel(hashKey string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, hashKey)
}

func (f *fakeAutoFetcher) RefineOnComplete(_ subtitles.TorrentMeta, h metainfo.Hash, fileIdx int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refined = append(f.refined, h.HexString()+"#"+strconv.Itoa(fileIdx))
}

func (f *fakeAutoFetcher) callsStarted() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func (f *fakeAutoFetcher) callsRefined() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.refined)
}

// fakeTorrentMeta is a scriptable TorrentMeta double for hook calls.
type fakeTorrentMeta struct{ complete bool }

func (f *fakeTorrentMeta) Name(_ metainfo.Hash, _ int) string   { return "name.mkv" }
func (f *fakeTorrentMeta) Complete(_ metainfo.Hash, _ int) bool { return f.complete }
func (f *fakeTorrentMeta) OSHash(_ context.Context, _ metainfo.Hash, _ int) (streamer.HashResult, error) {
	return streamer.HashResult{Hash: "deadbeefdeadbeef", Size: 1 << 16}, nil
}

// ─── harness ────────────────────────────────────────────────────────────────

const autoStatusHash = "0123456789abcdef0123456789abcdef01234567"

// newAutoStatusEnv wires the status handler onto a testing streamer with one
// active (incomplete) fixture torrent, a real LinksStore over the test
// database and the fake fetcher. Returns the router, the fixture hash and the
// links store so tests seed rows.
func newAutoStatusEnv(t *testing.T, cfg *config.Config, client *subtitles.Client,
	fetcher *fakeAutoFetcher, meta subtitles.TorrentMeta) (*gin.Engine, metainfo.Hash, *subtitles.LinksStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	h, cleanup := s.SeedActiveForTesting("auto-sub-status", time.Now())
	t.Cleanup(cleanup)
	pool := dbtest.NewDB(t)
	links, err := subtitles.NewLinksStore(pool)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	r := gin.New()
	r.GET("/api/stream/:hash/:file/subtitles/auto",
		SubtitlesAutoStatus(s, meta, links, client, fetcher, cfg))
	return r, h, links
}

func getAutoStatus(r *gin.Engine, hash string, fileIdx string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/stream/"+hash+"/"+fileIdx+"/subtitles/auto", nil)
	r.ServeHTTP(w, req)
	return w
}

func decodeAutoStatus(t *testing.T, w *httptest.ResponseRecorder) subtitleAutoStatusResponse {
	t.Helper()
	var resp subtitleAutoStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return resp
}

func autoStatusClient(t *testing.T, enabled bool) *subtitles.Client {
	t.Helper()
	key := ""
	if enabled {
		key = "test-api-key"
	}
	return subtitles.New(key, "", "", t.TempDir())
}

func autoStatusCfg(autoFetch bool) *config.Config {
	return &config.Config{Subtitles: config.SubtitlesConfig{
		AutoFetch:        autoFetch,
		DefaultLanguages: "pt-BR,pt",
	}}
}

// ─── 400 validation paths ───────────────────────────────────────────────────

func TestSubtitlesAutoStatus_BadHashHex(t *testing.T) {
	r, _, _ := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true),
		&fakeAutoFetcher{}, &fakeTorrentMeta{})
	w := getAutoStatus(r, "nothex", "0")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestSubtitlesAutoStatus_NonNumericFile(t *testing.T) {
	r, h, _ := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true),
		&fakeAutoFetcher{}, &fakeTorrentMeta{})
	w := getAutoStatus(r, h.HexString(), "abc")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestSubtitlesAutoStatus_FileIdxOutOfRange(t *testing.T) {
	r, h, _ := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true),
		&fakeAutoFetcher{}, &fakeTorrentMeta{})
	w := getAutoStatus(r, h.HexString(), "7")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

// ─── 404 inactive torrent ───────────────────────────────────────────────────

func TestSubtitlesAutoStatus_TorrentNotActive(t *testing.T) {
	r, _, _ := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true),
		&fakeAutoFetcher{}, &fakeTorrentMeta{})
	w := getAutoStatus(r, autoStatusHash, "0") // never seeded → not active
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

// ─── kick-pending ───────────────────────────────────────────────────────────

func TestSubtitlesAutoStatus_NoRowKicksStartTorrentAndReturnsPending(t *testing.T) {
	fetcher := &fakeAutoFetcher{}
	r, h, _ := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true),
		fetcher, &fakeTorrentMeta{})
	w := getAutoStatus(r, h.HexString(), "0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	resp := decodeAutoStatus(t, w)
	if resp.Status != subtitles.StatusPending {
		t.Errorf("status = %q, want %q", resp.Status, subtitles.StatusPending)
	}
	if resp.Reason != "" {
		t.Errorf("reason = %q, want empty", resp.Reason)
	}
	if resp.Subtitle != nil {
		t.Errorf("subtitle = %+v, want nil for pending", resp.Subtitle)
	}
	if got := fetcher.callsStarted(); got != 1 {
		t.Errorf("StartTorrent calls = %d, want 1", got)
	}
}

// ─── none without kicking ───────────────────────────────────────────────────

func TestSubtitlesAutoStatus_AutoFetchOffReturnsNoneWithoutKicking(t *testing.T) {
	fetcher := &fakeAutoFetcher{}
	r, h, _ := newAutoStatusEnv(t, autoStatusCfg(false), autoStatusClient(t, true),
		fetcher, &fakeTorrentMeta{})
	w := getAutoStatus(r, h.HexString(), "0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := decodeAutoStatus(t, w)
	if resp.Status != statusAutoNone {
		t.Errorf("status = %q, want %q", resp.Status, statusAutoNone)
	}
	if got := fetcher.callsStarted(); got != 0 {
		t.Errorf("StartTorrent calls = %d, want 0 (auto-fetch off)", got)
	}
}

func TestSubtitlesAutoStatus_ClientDisabledReturnsNoneWithoutKicking(t *testing.T) {
	fetcher := &fakeAutoFetcher{}
	r, h, _ := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, false),
		fetcher, &fakeTorrentMeta{})
	w := getAutoStatus(r, h.HexString(), "0")
	resp := decodeAutoStatus(t, w)
	if resp.Status != statusAutoNone {
		t.Errorf("status = %q, want %q", resp.Status, statusAutoNone)
	}
	if got := fetcher.callsStarted(); got != 0 {
		t.Errorf("StartTorrent calls = %d, want 0 (client disabled)", got)
	}
}

// ─── ready row → subtitle block ─────────────────────────────────────────────

func TestSubtitlesAutoStatus_ReadyRowReturnsSubtitleWithVTTURL(t *testing.T) {
	fetcher := &fakeAutoFetcher{status: subtitles.AutoStatus{
		Status: subtitles.StatusReady, FileID: "69654321", Language: "pt-BR",
		HearingImpaired: true,
	}}
	r, h, _ := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true),
		fetcher, &fakeTorrentMeta{})
	w := getAutoStatus(r, h.HexString(), "0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	resp := decodeAutoStatus(t, w)
	if resp.Status != subtitles.StatusReady {
		t.Fatalf("status = %q, want ready", resp.Status)
	}
	if resp.Subtitle == nil {
		t.Fatal("subtitle block missing for ready status")
	}
	if resp.Subtitle.ID != "69654321" || resp.Subtitle.Language != "pt-BR" || !resp.Subtitle.HearingImpaired {
		t.Errorf("subtitle = %+v", resp.Subtitle)
	}
	want := "/api/subtitles/download/69654321"
	if resp.Subtitle.VTTURL != want {
		t.Errorf("vttUrl = %q, want %q", resp.Subtitle.VTTURL, want)
	}
}

// ─── refine trigger path ────────────────────────────────────────────────────

func TestSubtitlesAutoStatus_ReadyQueryRowCompleteFileKicksRefine(t *testing.T) {
	fetcher := &fakeAutoFetcher{status: subtitles.AutoStatus{Status: subtitles.StatusReady, FileID: "1"}}
	meta := &fakeTorrentMeta{complete: true}
	r, h, links := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true), fetcher, meta)
	if err := links.MarkReady(context.Background(), h.HexString(), 0, "1", "pt-BR", "pt-BR,pt",
		false, false, "/cache/1.vtt", subtitles.SourceQuery); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	w := getAutoStatus(r, h.HexString(), "0")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := fetcher.callsRefined(); got != 1 {
		t.Errorf("RefineOnComplete calls = %d, want 1", got)
	}
	// The CURRENT status is returned even though a refinement was kicked.
	if resp := decodeAutoStatus(t, w); resp.Status != subtitles.StatusReady {
		t.Errorf("status = %q, want ready", resp.Status)
	}
}

func TestSubtitlesAutoStatus_ReadyHashRowDoesNotKickRefine(t *testing.T) {
	fetcher := &fakeAutoFetcher{status: subtitles.AutoStatus{Status: subtitles.StatusReady, FileID: "1"}}
	meta := &fakeTorrentMeta{complete: true}
	r, h, links := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true), fetcher, meta)
	if err := links.MarkReady(context.Background(), h.HexString(), 0, "1", "pt-BR", "pt-BR,pt",
		false, false, "/cache/1.vtt", subtitles.SourceHash); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	getAutoStatus(r, h.HexString(), "0")
	if got := fetcher.callsRefined(); got != 0 {
		t.Errorf("RefineOnComplete calls = %d, want 0 (already frame-exact)", got)
	}
}

func TestSubtitlesAutoStatus_ReadyQueryRowIncompleteFileDoesNotKickRefine(t *testing.T) {
	fetcher := &fakeAutoFetcher{status: subtitles.AutoStatus{Status: subtitles.StatusReady, FileID: "1"}}
	meta := &fakeTorrentMeta{complete: false}
	r, h, links := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true), fetcher, meta)
	if err := links.MarkReady(context.Background(), h.HexString(), 0, "1", "pt-BR", "pt-BR,pt",
		false, false, "/cache/1.vtt", subtitles.SourceQuery); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	getAutoStatus(r, h.HexString(), "0")
	if got := fetcher.callsRefined(); got != 0 {
		t.Errorf("RefineOnComplete calls = %d, want 0 (file incomplete)", got)
	}
}

// ─── pending / failed rows pass through untouched ───────────────────────────

func TestSubtitlesAutoStatus_PendingRowPassesThrough(t *testing.T) {
	fetcher := &fakeAutoFetcher{status: subtitles.AutoStatus{Status: subtitles.StatusPending}}
	r, h, links := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true), fetcher, &fakeTorrentMeta{})
	if err := links.UpsertPending(context.Background(), h.HexString(), 0, "pt-BR,pt"); err != nil {
		t.Fatalf("UpsertPending: %v", err)
	}
	w := getAutoStatus(r, h.HexString(), "0")
	resp := decodeAutoStatus(t, w)
	if resp.Status != subtitles.StatusPending || resp.Subtitle != nil {
		t.Errorf("resp = %+v, want pending without subtitle", resp)
	}
	if fetcher.callsStarted()+fetcher.callsRefined() != 0 {
		t.Errorf("unexpected kicks: started=%d refined=%d", fetcher.callsStarted(), fetcher.callsRefined())
	}
}

func TestSubtitlesAutoStatus_FailedRowPassesThroughWithoutRetry(t *testing.T) {
	fetcher := &fakeAutoFetcher{status: subtitles.AutoStatus{Status: subtitles.StatusFailed, Reason: subtitles.ReasonNoResults}}
	meta := &fakeTorrentMeta{complete: true}
	r, h, links := newAutoStatusEnv(t, autoStatusCfg(true), autoStatusClient(t, true), fetcher, meta)
	if err := links.MarkFailed(context.Background(), h.HexString(), 0, subtitles.ReasonNoResults); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	w := getAutoStatus(r, h.HexString(), "0")
	resp := decodeAutoStatus(t, w)
	if resp.Status != subtitles.StatusFailed || resp.Reason != subtitles.ReasonNoResults {
		t.Errorf("resp = %+v, want failed/no_results", resp)
	}
	if fetcher.callsStarted()+fetcher.callsRefined() != 0 {
		t.Errorf("failed rows must never be re-kicked: started=%d refined=%d",
			fetcher.callsStarted(), fetcher.callsRefined())
	}
}
