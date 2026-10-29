package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// ─── viewer open/close auto-fetch hooks ─────────────────────────────────────
//
// StreamViewerOpen carries no file index (its request is just :hash), so the
// StartTorrent kick lives on the stream-open path (StreamFile — lane F2
// decision); the CLOSE side carries the Cancel hook, only on the LAST viewer.

// newViewerHookRouter wires the viewer routes onto a testing streamer with one
// active fixture torrent plus the fake fetcher.
func newViewerHookRouter(t *testing.T, fetcher *fakeAutoFetcher) (*gin.Engine, metainfo.Hash) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	h, cleanup := s.SeedActiveForTesting("viewer-hook", time.Now())
	t.Cleanup(cleanup)
	r := gin.New()
	r.POST("/api/stream/:hash/viewer", StreamViewerOpen(s))
	r.DELETE("/api/stream/:hash/viewer", StreamViewerClose(s, nil, fetcher))
	return r, h
}

func postViewer(r *gin.Engine, hash string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/stream/"+hash+"/viewer", nil))
	return w
}

func deleteViewer(r *gin.Engine, hash string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/stream/"+hash+"/viewer", nil))
	return w
}

// TestStreamViewerClose_LastViewerCancelsAutoFetch: the only viewer closing
// must cancel the item's in-flight auto-fetch (best-effort, bounded).
func TestStreamViewerClose_LastViewerCancelsAutoFetch(t *testing.T) {
	fetcher := &fakeAutoFetcher{}
	r, h := newViewerHookRouter(t, fetcher)
	if w := postViewer(r, h.HexString()); w.Code != http.StatusOK {
		t.Fatalf("open = %d, want 200", w.Code)
	}
	if w := deleteViewer(r, h.HexString()); w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200", w.Code)
	}
	if len(fetcher.cancelled) != 1 || fetcher.cancelled[0] != h.HexString() {
		t.Errorf("Cancel calls = %v, want [%s]", fetcher.cancelled, h.HexString())
	}
}

// TestStreamViewerClose_NotLastViewerKeepsFetchAlive: closing ONE of two
// players must NOT cancel — the survivor's fetch keeps running.
func TestStreamViewerClose_NotLastViewerKeepsFetchAlive(t *testing.T) {
	fetcher := &fakeAutoFetcher{}
	r, h := newViewerHookRouter(t, fetcher)
	hash := h.HexString()
	postViewer(r, hash)
	postViewer(r, hash) // second player, same torrent

	if w := deleteViewer(r, hash); w.Code != http.StatusOK {
		t.Fatalf("close #1 = %d, want 200", w.Code)
	}
	if len(fetcher.cancelled) != 0 {
		t.Fatalf("Cancel fired on a non-last viewer close: %v", fetcher.cancelled)
	}
	deleteViewer(r, hash) // last viewer leaves
	if len(fetcher.cancelled) != 1 || fetcher.cancelled[0] != hash {
		t.Errorf("Cancel calls = %v, want exactly one for %s", fetcher.cancelled, hash)
	}
}

// TestStreamViewerClose_UnknownHashNoCancel: a close for a torrent the
// streamer does not know releases nothing (ReleaseViewer no-ops) and must not
// cancel a fetch keyed on a foreign hash.
func TestStreamViewerClose_UnknownHashNoCancel(t *testing.T) {
	fetcher := &fakeAutoFetcher{}
	r, _ := newViewerHookRouter(t, fetcher)
	if w := deleteViewer(r, autoStatusHash); w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200 (idempotent)", w.Code)
	}
	if len(fetcher.cancelled) != 0 {
		t.Errorf("Cancel calls = %v, want none", fetcher.cancelled)
	}
}

// ─── stream-open hook (StartTorrent with the file index) ────────────────────
//
// The kick fires ONLY on the live-streamer serve path — after the
// completed-store early-return and after the fileIdx range validation (review
// F1: kicking earlier produced a terminal junk row for every store-served
// play, and kicked out-of-range indexes). The fixture torrent has exactly one
// file, so the valid index is 0; the kick tests use HEAD, which net/http's
// ServeContent answers from metadata alone (no body copy → never blocks on the
// peerless anacrolix reader that a GET/Range read would).

// newStreamHookRouter wires the stream-file route onto a testing streamer with
// one active fixture torrent plus the fake fetcher (store may be nil — the
// completed-store path then always misses). Both GET and HEAD are registered:
// prod only exposes GET, but the kick decision is method-agnostic and HEAD is
// what keeps the serve path metadata-only (see the section comment above).
func newStreamHookRouter(t *testing.T, fetcher *fakeAutoFetcher, store *downloads.Store) (*gin.Engine, metainfo.Hash) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	h, cleanup := s.SeedActiveForTesting("stream-open-hook", time.Now())
	t.Cleanup(cleanup)
	r := gin.New()
	hook := StreamAutoHook{Meta: &fakeTorrentMeta{}, Fetcher: fetcher}
	r.GET("/api/stream/:hash/:file", StreamFile(s, store, hook))
	r.HEAD("/api/stream/:hash/:file", StreamFile(s, store, hook))
	return r, h
}

// TestStreamFile_KicksAutoFetch: opening the stream file kicks StartTorrent
// for (hash, fileIdx) — the only point in the open flow that knows both. A
// VALID index must be used (the kick now lives behind the range validation),
// so the fixture's single file (idx 0) is requested via HEAD.
func TestStreamFile_KicksAutoFetch(t *testing.T) {
	fetcher := &fakeAutoFetcher{}
	r, h := newStreamHookRouter(t, fetcher, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/api/stream/"+h.HexString()+"/0", nil))

	if len(fetcher.started) != 1 || fetcher.started[0] != h.HexString()+"#0" {
		t.Errorf("StartTorrent calls = %v, want [%s#0]", fetcher.started, h.HexString()+"#0")
	}
}

// TestStreamFile_StoreServedDoesNotKick: a completed download served straight
// from the store (the m3u/VLC direct-fetch case — no prior streamAdd) must NOT
// kick. The torrent here IS active and the index IS valid, so the only thing
// preventing the kick is the completed-store early-return: before the review
// fix the kick fired before it and would persist a terminal junk row.
func TestStreamFile_StoreServedDoesNotKick(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	h, cleanup := s.SeedActiveForTesting("stream-open-hook", time.Now())
	t.Cleanup(cleanup)

	store := newDownloadsStore(t)
	destDir := t.TempDir()
	filePath := filepath.Join(destDir, "file.bin")
	if err := os.WriteFile(filePath, []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := store.Create(downloads.Download{
		UserID: 0, InfoHash: h.HexString(), FileIndex: 0, Magnet: "m", Name: "stream-open-hook",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.UpdateMetadata(0, d.ID, "stream-open-hook", filePath, 4); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}
	if err := store.SetStatus(0, d.ID, downloads.StatusCompleted); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	fetcher := &fakeAutoFetcher{}
	r := gin.New()
	r.GET("/api/stream/:hash/:file", StreamFile(s, store, StreamAutoHook{
		Meta:    &fakeTorrentMeta{},
		Fetcher: fetcher,
	}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/stream/"+h.HexString()+"/0", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 from the store row; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "AAAA" {
		t.Fatalf("body = %q, want the completed file's bytes served from disk", w.Body.String())
	}
	if len(fetcher.started) != 0 {
		t.Errorf("StartTorrent calls = %v, want none on the store-served path", fetcher.started)
	}
}

// TestStreamFile_OutOfRangeDoesNotKick: fileIdx beyond the torrent's file list
// must NOT kick (a fetch keyed on a file that can never be served is doomed)
// and still 404s on the serve path, as before the fix.
func TestStreamFile_OutOfRangeDoesNotKick(t *testing.T) {
	fetcher := &fakeAutoFetcher{}
	r, h := newStreamHookRouter(t, fetcher, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/stream/"+h.HexString()+"/7", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an out-of-range index", w.Code)
	}
	if len(fetcher.started) != 0 {
		t.Errorf("StartTorrent calls = %v, want none for an out-of-range index", fetcher.started)
	}
}

// TestStreamFile_HookDisabledByDefault: call sites without a full hook
// argument (no Meta) stay inert even though (hash, fileIdx) is servable — and
// nothing may panic.
func TestStreamFile_HookDisabledByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	h, cleanup := s.SeedActiveForTesting("no-hook", time.Now())
	t.Cleanup(cleanup)

	fetcher := &fakeAutoFetcher{}
	r := gin.New()
	r.HEAD("/api/stream/:hash/:file", StreamFile(s, nil, StreamAutoHook{Fetcher: fetcher}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/api/stream/"+h.HexString()+"/0", nil))
	if len(fetcher.started) != 0 {
		t.Errorf("StartTorrent calls = %v, want none (Meta missing)", fetcher.started)
	}
}
