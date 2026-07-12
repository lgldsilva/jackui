package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// probeVideoDims returns the source's video width/height (0,0 = unknown) from
// the shared ffprobe cache (streamer.Probe). It is cheap on the 2nd+ call for a
// given (torrent, file): the player probes the file to decide direct-play vs HLS
// BEFORE requesting the HLS master, so by master time the dims are cached. A
// cold/failed probe → (0,0) → a single-variant fallback (never a wrong ladder).
func probeVideoDims(hc *hlsCtx) (w, h int) {
	if hc.s == nil {
		return 0, 0
	}
	ctx, cancel := context.WithTimeout(hc.c.Request.Context(), 60*time.Second)
	defer cancel()
	pr, err := hc.s.Probe(ctx, hc.h, hc.fileIdx)
	if err != nil {
		return 0, 0
	}
	return pr.VideoWidth, pr.VideoHeight
}

// resolveVariant pins hc.variant from the v/:variant path param by probing the
// source height and indexing the ABR ladder. idx < 0 (legacy single-variant
// route) leaves the zero-value. Returns false when the index is out of range —
// the caller answers 404 so a stale master URI can't spin up ffmpeg at the
// wrong rung.
func resolveVariant(hc *hlsCtx) bool {
	idx := hlsVariantParam(hc.c)
	if idx < 0 {
		return true
	}
	_, h := probeVideoDims(hc)
	ladder := transcode.VariantLadder(h)
	if idx >= len(ladder) {
		return false
	}
	hc.variant = ladder[idx]
	return true
}

// StreamHLSVariant serves the media playlist for ONE ABR ladder rung
// (GET /api/stream/hls/:hash/:file/v/:variant/index.m3u8). Same session
// lifecycle as the legacy playlist; resolveVariant pins the encode to the rung.
func StreamHLSVariant(s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		hc, ok := newHLSCtx(c, s, mgr, store)
		if !ok {
			return
		}
		if !resolveVariant(hc) {
			c.JSON(http.StatusNotFound, gin.H{"error": "variant out of range"})
			return
		}
		serveHLSMediaPlaylist(hc)
	}
}

// serveMasterIfMultiVariant serves a synthetic multi-variant MASTER playlist
// when the source ladder has ≥2 rungs, returning true. It is PROBE-ONLY: no
// ffmpeg is started (the master just advertises variant URIs; the encode begins
// only when the client fetches a v/:variant playlist). Sub-1080p / unknown-
// height sources have a single rung → returns false and the caller serves the
// legacy single-variant media playlist (unchanged body, backward compatible).
func serveMasterIfMultiVariant(hc *hlsCtx) bool {
	w, h := probeVideoDims(hc)
	ladder := transcode.VariantLadder(h)
	if len(ladder) < 2 {
		return false
	}
	body := buildMasterPlaylist(ladder, w, h, hc.c.Query("token"), httpshared.NativeHLSParam(hc.c))
	hc.c.Header(httpshared.CacheControl, httpshared.CacheNoStore)
	hc.c.Data(http.StatusOK, httpshared.MIMEMPEGURL, body)
	return true
}

// variantWidth derives a rung's pixel width from the source aspect ratio,
// rounded to an even number (yuv420p requires it). 0 when the source dims are
// unknown → RESOLUTION is omitted (it is optional in EXT-X-STREAM-INF).
func variantWidth(srcW, srcH, variantH int) int {
	if srcW <= 0 || srcH <= 0 || variantH <= 0 {
		return 0
	}
	w := srcW * variantH / srcH
	if w%2 != 0 {
		w++
	}
	return w
}

// buildMasterPlaylist synthesises the multi-variant master (M2a: video rungs
// only — audio/subtitle EXT-X-MEDIA renditions land in M2b). Each variant URI is
// RELATIVE to the master URL (.../:file/index.m3u8) so `v/0/index.m3u8` resolves
// to .../:file/v/0/index.m3u8 — matching the v/:variant route. token + native_hls
// are propagated onto every variant URI (mediaSegQuery) so the variant request
// authenticates AND resolves to the same EffectiveKey. CODECS/BANDWIDTH/RESOLUTION
// come from the deterministic ladder (transcode.Variant) so ABR selection in
// hls.js/Safari is stable.
func buildMasterPlaylist(ladder []transcode.Variant, srcW, srcH int, token string, nativeHLS bool) []byte {
	q := mediaSegQuery(token, nativeHLS)
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i, v := range ladder {
		b.WriteString("#EXT-X-STREAM-INF:BANDWIDTH=")
		b.WriteString(strconv.Itoa(v.Bandwidth()))
		if w := variantWidth(srcW, srcH, v.Height); w > 0 {
			fmt.Fprintf(&b, ",RESOLUTION=%dx%d", w, v.Height)
		}
		fmt.Fprintf(&b, ",CODECS=%q\n", v.Codecs())
		fmt.Fprintf(&b, "v/%d/index.m3u8%s\n", i, q)
	}
	return []byte(b.String())
}
