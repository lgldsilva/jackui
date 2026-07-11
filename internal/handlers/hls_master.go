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

// probeSource returns the source's parsed tracks/dims from the shared ffprobe
// cache (streamer.Probe), false on a cold/failed probe. Cheap on the 2nd+ call:
// the player probes to decide direct-play vs HLS BEFORE requesting the master,
// so by master time the result is cached.
func probeSource(hc *hlsCtx) (streamer.ProbeResult, bool) {
	if hc.s == nil {
		return streamer.ProbeResult{}, false
	}
	ctx, cancel := context.WithTimeout(hc.c.Request.Context(), 60*time.Second)
	defer cancel()
	pr, err := hc.s.Probe(ctx, hc.h, hc.fileIdx)
	if err != nil {
		return streamer.ProbeResult{}, false
	}
	return pr, true
}

// probeVideoHeight is the source video height (0 = unknown), for the ladder.
func probeVideoHeight(hc *hlsCtx) int {
	pr, ok := probeSource(hc)
	if !ok {
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
// (GET /api/stream/hls/:hash/:file/v/:variant/index.m3u8). The video variant
// keeps its default audio muxed (M2a); alternate audio tracks are served as
// separate audio-only renditions (StreamHLSAudio).
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

// StreamHLSAudio serves the audio-only media playlist for ONE alternate audio
// rendition (GET /api/stream/hls/:hash/:file/a/:track/index.m3u8). startHLSSession
// detects the :track param → an AudioOnly session mapping that absolute stream
// index (key -ao{track}, its own Dir/segments).
func StreamHLSAudio(s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		hc, ok := newHLSCtx(c, s, mgr, store)
		if !ok {
			return
		}
		serveHLSMediaPlaylist(hc)
	}
}

// serveMasterIfMultiVariant serves a synthetic MASTER playlist (probe-only, no
// ffmpeg) when the source warrants one — ≥2 ABR rungs (multi-resolution) OR ≥2
// audio tracks (audio renditions). Otherwise returns false and the caller serves
// the legacy single-variant media playlist (unchanged, backward compatible). The
// encode begins only when the client fetches a v/:variant or a/:track playlist.
func serveMasterIfMultiVariant(hc *hlsCtx) bool {
	pr, ok := probeSource(hc)
	if !ok {
		return false
	}
	ladder := transcode.VariantLadder(pr.VideoHeight)
	subs := textSubs(pr.Subtitles)
	// Master vale a pena com ≥2 rungs de vídeo, ≥2 faixas de áudio (renditions),
	// OU ≥1 legenda de texto (rendition WebVTT).
	if len(ladder) < 2 && len(pr.Audio) < 2 && len(subs) == 0 {
		return false
	}
	writeMasterPlaylist(hc.c, ladder, pr.VideoWidth, pr.VideoHeight, pr.Audio, subs)
	return true
}

// textSubs filtra as legendas de TEXTO (SRT/ASS/…) — as bitmap (PGS/VOBSUB,
// Track.Image) continuam burn-in, sem rendition (não há WebVTT sem OCR).
func textSubs(subs []streamer.Track) []streamer.Track {
	out := make([]streamer.Track, 0, len(subs))
	for _, s := range subs {
		if !s.Image {
			out = append(out, s)
		}
	}
	return out
}

// writeMasterPlaylist renders the master and writes it to the response
// (no-store; MPEG-URL). token/native_hls come from the request query so they
// propagate onto the variant/rendition URIs (see buildMasterPlaylist).
func writeMasterPlaylist(c *gin.Context, ladder []transcode.Variant, w, h int, audio, subs []streamer.Track) {
	body := buildMasterPlaylist(ladder, w, h, audio, subs, c.Query("token"), httpshared.NativeHLSParam(c))
	c.Header(httpshared.CacheControl, httpshared.CacheNoStore)
	c.Data(http.StatusOK, httpshared.MIMEMPEGURL, body)
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

// audioTrackName is the human label for an EXT-X-MEDIA NAME (Title → Language →
// "Audio N"). NAME is required and must be unique per group.
func audioTrackName(a streamer.Track, i int) string {
	if a.Title != "" {
		return a.Title
	}
	if a.Language != "" {
		return a.Language
	}
	return fmt.Sprintf("Audio %d", i+1)
}

// writeAudioRenditions emits one EXT-X-MEDIA TYPE=AUDIO per track. The FIRST
// track is the DEFAULT and carries NO URI — it is the audio muxed into every
// video variant (v/:variant), the RFC 8216 §8.6 "default muxed + alternates"
// pattern (no video re-encode per language). Alternates get a URI pointing to an
// audio-only session (a/:track) mapping their absolute stream index.
func writeAudioRenditions(b *strings.Builder, audio []streamer.Track, q string) {
	for i, a := range audio {
		b.WriteString(`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME=`)
		b.WriteString(strconv.Quote(audioTrackName(a, i)))
		if i == 0 {
			b.WriteString(",DEFAULT=YES")
		}
		b.WriteString(",AUTOSELECT=YES")
		if a.Language != "" {
			b.WriteString(",LANGUAGE=" + strconv.Quote(a.Language))
		}
		if i > 0 {
			fmt.Fprintf(b, `,URI="a/%d/index.m3u8%s"`, a.Index, q)
		}
		b.WriteString("\n")
	}
}

// buildMasterPlaylist synthesises the multi-variant master. Video rungs come
// from the deterministic ladder (CODECS/BANDWIDTH/RESOLUTION stable → ABR
// stable). When the source has ≥2 audio tracks, EXT-X-MEDIA TYPE=AUDIO renditions
// are emitted and every STREAM-INF references AUDIO="aud" (audio switching via
// hls.audioTrack, not URL reload). Variant/rendition URIs are RELATIVE to the
// master URL (.../:file/index.m3u8) so they resolve to the v/:variant and
// a/:track routes; token+native_hls propagate so each child authenticates and
// resolves to the same EffectiveKey.
func buildMasterPlaylist(ladder []transcode.Variant, srcW, srcH int, audio, subs []streamer.Track, token string, nativeHLS bool) []byte {
	q := mediaSegQuery(token, nativeHLS)
	hasAudioRenditions := len(audio) >= 2
	hasSubs := len(subs) > 0
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	if hasAudioRenditions {
		writeAudioRenditions(&b, audio, q)
	}
	if hasSubs {
		writeSubtitleRenditions(&b, subs, q)
	}
	for i, v := range ladder {
		b.WriteString("#EXT-X-STREAM-INF:BANDWIDTH=")
		b.WriteString(strconv.Itoa(v.Bandwidth()))
		if w := variantWidth(srcW, srcH, v.Height); w > 0 {
			fmt.Fprintf(&b, ",RESOLUTION=%dx%d", w, v.Height)
		}
		fmt.Fprintf(&b, ",CODECS=%q", v.Codecs())
		if hasAudioRenditions {
			b.WriteString(`,AUDIO="aud"`)
		}
		if hasSubs {
			b.WriteString(`,SUBTITLES="sub"`)
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "v/%d/index.m3u8%s\n", i, q)
	}
	return []byte(b.String())
}

// writeSubtitleRenditions emits one EXT-X-MEDIA TYPE=SUBTITLES per text sub
// track (URI → sub/:track WebVTT mini-playlist). The first is DEFAULT but subs
// are never auto-shown (AUTOSELECT=NO) — the user opts in.
func writeSubtitleRenditions(b *strings.Builder, subs []streamer.Track, q string) {
	for i, s := range subs {
		b.WriteString(`#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="sub",NAME=`)
		b.WriteString(strconv.Quote(audioTrackName(s, i)))
		if i == 0 {
			b.WriteString(",DEFAULT=YES")
		}
		b.WriteString(",AUTOSELECT=NO,FORCED=NO")
		if s.Language != "" {
			b.WriteString(",LANGUAGE=" + strconv.Quote(s.Language))
		}
		fmt.Fprintf(b, `,URI="sub/%d/index.m3u8%s"`, s.Index, q)
		b.WriteString("\n")
	}
}
