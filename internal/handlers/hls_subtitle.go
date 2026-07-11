package handlers

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// subFallbackDurationSec cobre a EXTINF quando a duração é desconhecida (o VTT
// tem seus próprios timestamps; a duração do segmento é só um hint).
const subFallbackDurationSec = 10800 // 3h

// StreamHLSSubtitle serves the WebVTT rendition mini-playlist for ONE text
// subtitle track (GET .../sub/:track/index.m3u8). RFC 8216 §3.5: a subtitle
// rendition is a Media Playlist whose segment is WebVTT. Emits a single VOD
// segment spanning the whole media, pointing at the EXISTING subtrack endpoint
// (which runs ExtractSubtitle) — reuse, not a new extractor.
func StreamHLSSubtitle(s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		hc, ok := newHLSCtx(c, s, mgr, store)
		if !ok {
			return
		}
		track, err := strconv.Atoi(c.Param("track"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid track index"})
			return
		}
		pr, _ := probeSource(hc)
		dur := pr.DurationSec
		if dur <= 0 {
			dur = subFallbackDurationSec
		}
		body := buildSubtitlePlaylist(hc.h.HexString(), hc.fileIdx, track, dur, c.Query("token"))
		c.Header(httpshared.CacheControl, httpshared.CacheNoStore)
		c.Data(http.StatusOK, httpshared.MIMEMPEGURL, body)
	}
}

// buildSubtitlePlaylist synthesises the single-segment WebVTT VOD playlist for a
// subtitle track. The segment URI is the ABSOLUTE subtrack VTT endpoint (carries
// the token so <video>/hls.js can fetch it). NOTE: Safari native é sensível a
// WebVTT-HLS (X-TIMESTAMP-MAP) — validar no Safari real antes do merge.
func buildSubtitlePlaylist(hash string, fileIdx, track int, durationSec float64, token string) []byte {
	tq := ""
	if token != "" {
		tq = "?token=" + token
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(durationSec)))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	fmt.Fprintf(&b, "#EXTINF:%.3f,\n", durationSec)
	fmt.Fprintf(&b, "/api/stream/subtrack/%s/%d/%d%s\n", hash, fileIdx, track, tq)
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}
