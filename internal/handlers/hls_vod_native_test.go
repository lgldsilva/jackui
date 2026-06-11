package handlers

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// stubTranscodeCaps points the HLS manager at a fake ffmpeg (sleeps, never
// writes) so sessions launch without a real encoder — the same stub pattern as
// transcode's hls_lifecycle_test.go. No ffprobe sibling exists, so an
// in-session duration probe fails fast (duration 0 → EVENT).
func stubTranscodeCaps(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nsleep 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	transcode.SetCachedForTesting(&transcode.Capabilities{FFmpegPath: ffmpeg, Preferred: "libx264"})
	t.Cleanup(transcode.ResetCachedForTesting)
}

func newVODAllManager(t *testing.T) *transcode.HLSSessionManager {
	t.Helper()
	mgr, err := transcode.NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	// O default novo do config ("all") — VOD inclusive para Safari nativo.
	mgr.SetVODMode(transcode.ParseVODMode("all"))
	return mgr
}

// The production bug: Safari/iOS (native_hls=1) playing an IN-PROGRESS torrent
// (ForceVOD=false — only completed downloads force it) received an EVENT
// playlist because the old "hlsjs" default excluded native clients. Under
// mode=all with a known duration the master playlist must be finite VOD.
func TestServeHLSPlaylist_NativeHLSInProgressGetsVOD(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stubTranscodeCaps(t)
	mgr := newVODAllManager(t)

	sess, err := mgr.GetOrStart(context.Background(), transcode.HLSStartOpts{
		Key:              "deadbeef-0",
		Source:           bytes.NewReader([]byte("x")),
		SourceSize:       1,
		NativeHLS:        true,
		ForceVOD:         false, // in-progress torrent, not a completed download
		KnownDurationSec: 10,
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	t.Cleanup(func() { mgr.Close(sess.Key) })
	if !sess.IsVOD() {
		t.Fatal("native+in-progress+known-duration under mode=all must be VOD")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/master.m3u8?token=TOK&native_hls=1", nil)
	serveHLSPlaylist(c, sess)

	body := w.Body.String()
	if !strings.Contains(body, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Errorf("missing #EXT-X-PLAYLIST-TYPE:VOD:\n%s", body)
	}
	if !strings.Contains(body, "#EXT-X-ENDLIST") {
		t.Errorf("missing #EXT-X-ENDLIST (Safari needs a finite playlist for the seekbar):\n%s", body)
	}
	// Segment URLs must keep token + native_hls so the segment handler resolves
	// the SAME session key the master created.
	if !strings.Contains(body, "seg_00000.ts?token=TOK&native_hls=1") {
		t.Errorf("segment URLs must carry token+native_hls:\n%s", body)
	}
}

// Unknown duration stays EVENT — the correct last resort — even under mode=all
// with a native client. The handler then serves ffmpeg's own (live) playlist.
func TestServeHLSPlaylist_NativeHLSUnknownDurationStaysEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stubTranscodeCaps(t)
	mgr := newVODAllManager(t)

	sess, err := mgr.GetOrStart(context.Background(), transcode.HLSStartOpts{
		Key:        "deadbeef-1",
		Source:     bytes.NewReader([]byte("x")),
		SourceSize: 1,
		NativeHLS:  true,
		// KnownDurationSec omitted → probe runs and fails (no ffprobe stub).
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}
	t.Cleanup(func() { mgr.Close(sess.Key) })
	if sess.IsVOD() {
		t.Fatal("unknown duration must fall back to EVENT")
	}

	// The stub ffmpeg never writes a playlist; simulate the encoder's EVENT
	// output so the handler's read path is exercised.
	event := "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:4.0,\nseg_00000.ts\n"
	if err := os.WriteFile(filepath.Join(sess.Dir, "index.m3u8"), []byte(event), 0o644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/master.m3u8?token=TOK&native_hls=1", nil)
	serveHLSPlaylist(c, sess)

	body := w.Body.String()
	if strings.Contains(body, "#EXT-X-ENDLIST") || strings.Contains(body, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Errorf("EVENT session must not serve a finite/VOD playlist:\n%s", body)
	}
	if !strings.Contains(body, "seg_00000.ts?token=TOK&native_hls=1") {
		t.Errorf("EVENT playlist segments must carry token+native_hls:\n%s", body)
	}
}
