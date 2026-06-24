package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/audiometa"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/imagesearch"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/lyrics"
)

// --- minimal ID3v2.3 fixture (no ffmpeg / committed binaries; dhowden/tag
// parses a bare ID3 tag). Mirrors the builder in internal/audiometa tests. ---

func ssz(n int) []byte {
	return []byte{byte((n >> 21) & 0x7f), byte((n >> 14) & 0x7f), byte((n >> 7) & 0x7f), byte(n & 0x7f)}
}

func id3frame(id string, data []byte) []byte {
	out := []byte(id)
	out = append(out, byte(len(data)>>24), byte(len(data)>>16), byte(len(data)>>8), byte(len(data)))
	out = append(out, 0x00, 0x00)
	return append(out, data...)
}

func id3mp3(withCover bool) []byte {
	frames := id3frame("TIT2", append([]byte{0x00}, []byte("My Song")...))
	frames = append(frames, id3frame("TPE1", append([]byte{0x00}, []byte("My Artist")...))...)
	if withCover {
		apic := []byte{0x00}
		apic = append(apic, []byte("image/jpeg")...)
		apic = append(apic, 0x00, 0x03, 0x00)
		apic = append(apic, []byte("\xff\xd8\xff\xe0img")...)
		frames = append(frames, id3frame("APIC", apic)...)
	}
	h := append([]byte("ID3"), 0x03, 0x00, 0x00)
	h = append(h, ssz(len(frames))...)
	return append(h, frames...)
}

func audioMount(t *testing.T, withCover bool) (*local.Browser, *audiometa.Store) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "song.mp3"), id3mp3(withCover), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	b := local.NewBrowser([]config.ExternalMount{{Name: "M", Path: dir}})
	st, err := audiometa.New(filepath.Join(t.TempDir(), "am.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)
	return b, st
}

func ctxFor(method, target string) (*httptest.ResponseRecorder, *gin.Context) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return w, c
}

func TestNormalizeAudioCodec(t *testing.T) {
	cases := map[string]string{
		"aac": "aac", "AAC": "aac", "mp3": "mp3", "flac": "flac",
		"opus": "opus", "vorbis": "vorbis", "alac": "alac",
		"pcm_s16le": "wav", "ac3": "", "dts": "", "wmav2": "", "": "",
	}
	for in, want := range cases {
		if got := normalizeAudioCodec(in); got != want {
			t.Errorf("normalizeAudioCodec(%q)=%q want %q", in, got, want)
		}
	}
}

func TestAudioDirectPlayable(t *testing.T) {
	none := map[string]bool{}
	if !audioDirectPlayable("aac", none) || !audioDirectPlayable("mp3", none) {
		t.Error("aac/mp3 are universal → always direct")
	}
	if audioDirectPlayable("flac", none) {
		t.Error("flac must transcode when client didn't advertise it")
	}
	if !audioDirectPlayable("flac", map[string]bool{"flac": true}) {
		t.Error("flac must direct-play when client advertised it")
	}
	if audioDirectPlayable("dts", map[string]bool{"dts": true}) {
		t.Error("unknown codec (token \"\") must never direct-play")
	}
}

func TestParseAudioCaps(t *testing.T) {
	caps := parseAudioCaps(" flac , OPUS ,, wav ")
	if !caps["flac"] || !caps["opus"] || !caps["wav"] || len(caps) != 3 {
		t.Errorf("parseAudioCaps mismatch: %v", caps)
	}
}

func TestAudioExtUniversallySafe(t *testing.T) {
	if !audioExtUniversallySafe("a.mp3") || !audioExtUniversallySafe("b.M4A") {
		t.Error("mp3/m4a are safe exts")
	}
	if audioExtUniversallySafe("c.flac") {
		t.Error("flac is not a universally-safe ext")
	}
}

func TestNormalizeKind(t *testing.T) {
	if normalizeKind("audio") != "audio" || normalizeKind("video") != "video" {
		t.Error("audio/video must pass through")
	}
	if normalizeKind("garbage") != "" || normalizeKind("") != "" {
		t.Error("non-whitelisted kind must normalize to empty")
	}
}

func TestLocalAudioMetaHandler(t *testing.T) {
	b, st := audioMount(t, true)
	w, c := ctxFor("GET", "/api/local/audio/meta?mount=M&path=song.mp3")
	LocalAudioMeta(b, st)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var meta audiometa.Tags
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if meta.Title != "My Song" || meta.Artist != "My Artist" || !meta.HasCover {
		t.Errorf("meta mismatch: %+v", meta)
	}
	// Second call must be served from the cache (same result).
	w2, c2 := ctxFor("GET", "/api/local/audio/meta?mount=M&path=song.mp3")
	LocalAudioMeta(b, st)(c2)
	if w2.Code != http.StatusOK || !json.Valid(w2.Body.Bytes()) {
		t.Errorf("cached call failed: %d", w2.Code)
	}
}

func TestLocalAudioMetaMissingParams(t *testing.T) {
	b, st := audioMount(t, true)
	w, c := ctxFor("GET", "/api/local/audio/meta?mount=M")
	LocalAudioMeta(b, st)(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing path must be 400, got %d", w.Code)
	}
}

func TestLocalAudioMetaNonAudio(t *testing.T) {
	b, st := audioMount(t, true)
	_, c := ctxFor("GET", "/api/local/audio/meta?mount=M&path=song.mkv")
	LocalAudioMeta(b, st)(c)
	// c.Status(204) with no body stays pending until the engine flushes it; in a
	// direct handler call assert on the writer's pending status.
	if c.Writer.Status() != http.StatusNoContent {
		t.Errorf("non-audio ext must be 204, got %d", c.Writer.Status())
	}
}

func TestLocalAudioMetaNotFound(t *testing.T) {
	b, st := audioMount(t, true)
	_, c := ctxFor("GET", "/api/local/audio/meta?mount=M&path=ghost.mp3")
	LocalAudioMeta(b, st)(c)
	if c.Writer.Status() != http.StatusNotFound {
		t.Errorf("missing file must be 404, got %d", c.Writer.Status())
	}
}

func TestLocalAudioCoverHandler(t *testing.T) {
	b, st := audioMount(t, true)
	w, c := ctxFor("GET", "/api/local/audio/cover?mount=M&path=song.mp3")
	LocalAudioCover(b, st, nil)(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content-type %q", ct)
	}
	if w.Header().Get("ETag") == "" {
		t.Error("expected ETag")
	}
}

func TestLocalAudioCoverNone(t *testing.T) {
	b, st := audioMount(t, false) // fixture without APIC
	_, c := ctxFor("GET", "/api/local/audio/cover?mount=M&path=song.mp3")
	LocalAudioCover(b, st, nil)(c) // nil chain → no web fallback → 204
	if c.Writer.Status() != http.StatusNoContent {
		t.Errorf("no embedded cover must be 204, got %d", c.Writer.Status())
	}
}

// With a web-search chain wired, a file lacking an embedded cover falls back to
// the searched image instead of 204.
func TestLocalAudioCoverWebFallback(t *testing.T) {
	b, st := audioMount(t, false) // fixture without APIC
	w, c := ctxFor("GET", "/api/local/audio/cover?mount=M&path=song.mp3")
	chain := imagesearch.NewChain(stubSource{data: []byte("WEBIMG"), ct: "image/png"})
	LocalAudioCover(b, st, chain)(c)
	if w.Code != http.StatusOK || w.Body.String() != "WEBIMG" {
		t.Fatalf("web fallback: code=%d body=%q, want 200/WEBIMG", w.Code, w.Body.String())
	}
}

func TestAudioETagStable(t *testing.T) {
	a := audioETag("/m/x.mp3", 123)
	if a != audioETag("/m/x.mp3", 123) {
		t.Error("ETag must be stable for same path+mtime")
	}
	if a == audioETag("/m/x.mp3", 124) {
		t.Error("ETag must change when mtime changes")
	}
}

func TestLyricsGetGuards(t *testing.T) {
	w, c := ctxFor("GET", "/api/lyrics")
	LyricsGet(nil)(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil client must be 503, got %d", w.Code)
	}
}

func TestLyricsGetMissingTitle(t *testing.T) {
	w, c := ctxFor("GET", "/api/lyrics") // no title param
	LyricsGet(lyrics.New())(c)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing title must be 400, got %d", w.Code)
	}
}

// A local audio file whose codec ffprobe can't identify (or with no ffprobe)
// but with a universally-safe extension direct-plays — we don't force HLS on a
// plain MP3 just because the probe was inconclusive.
func TestLocalPlayAudioUnknownSafeExtDirect(t *testing.T) {
	b, _ := audioMount(t, true) // song.mp3 (ID3 tag, no real frames)
	w, c := ctxFor("GET", "/api/local/play?mount=M&path=song.mp3")
	LocalPlay(b, nil)(c)
	var resp LocalPlayResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Kind != "direct" {
		t.Errorf("unidentifiable mp3 should direct-play, got %q", resp.Kind)
	}
}

// A non-safe extension whose codec can't be confirmed now direct-plays too,
// to mirror simple playback.
func TestLocalPlayAudioUnknownUnsafeExtDirect(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "song.flac"), id3mp3(false), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	b := local.NewBrowser([]config.ExternalMount{{Name: "M", Path: dir}})
	w, c := ctxFor("GET", "/api/local/play?mount=M&path=song.flac")
	LocalPlay(b, nil)(c)
	var resp LocalPlayResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Kind != "direct" {
		t.Errorf("unidentifiable flac should direct-play, got %q", resp.Kind)
	}
}
