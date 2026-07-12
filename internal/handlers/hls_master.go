package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// probeVideoHeight returns the source's video height (0 = unknown), reading it
// from the shared ffprobe cache (streamer.Probe). Cheap on the 2nd+ call for a
// given (torrent, file) — the master handler probes first, so by the time a
// client fetches a variant playlist the height is already cached.
func probeVideoHeight(hc *hlsCtx) int {
	if hc.s == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(hc.c.Request.Context(), 60*time.Second)
	defer cancel()
	pr, err := hc.s.Probe(ctx, hc.h, hc.fileIdx)
	if err != nil {
		return 0
	}
	return pr.VideoHeight
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
	ladder := transcode.VariantLadder(probeVideoHeight(hc))
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
// (probe-only, no ffmpeg) when the source ladder has ≥2 rungs, returning true.
// Otherwise it returns false and the caller serves the legacy single-variant
// media playlist. Implemented in Phase 5 (buildMasterPlaylist); the stub keeps
// the routing/session work of Phase 4 shippable on its own.
func serveMasterIfMultiVariant(_ *hlsCtx) bool { return false }
