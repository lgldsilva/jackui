package handlers

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

type hlsCtx struct {
	c       *gin.Context
	s       *streamer.Streamer
	mgr     *transcode.HLSSessionManager
	store   *downloads.Store
	h       metainfo.Hash
	fileIdx int
	// variant is the ABR ladder rung THIS session encodes (HLS master, Phase
	// 2). Zero-value (Height 0) = legacy single-variant; the variant handler
	// (v/:variant) resolves srcHeight→ladder and populates this before startHLSSession.
	variant transcode.Variant
	// mediaRenditions wires the EXT-X-MEDIA renditions (audio/subtitle) into the
	// master (config JACKUI_HLS_MEDIA_RENDITIONS). false = M2a behavior.
	mediaRenditions bool
}

// mediaSegQuery builds the query string appended to each segment URL. It
// carries the token (so <video> can authenticate) and, when set, the native_hls
// flag — the segment request must resolve to the SAME session key the master
// created (see HLSSessionManager.EffectiveKey), so both sides need the flag.
// With only a token the output is identical to the previous `?token=X`.
func mediaSegQuery(token string, nativeHLS bool) string {
	return mediaSegQueryWithPlayback(token, nativeHLS, "")
}

func mediaSegQueryWithPlayback(token string, nativeHLS bool, playback string) string {
	q := ""
	if token != "" {
		q = "?token=" + token
	}
	if nativeHLS {
		if q == "" {
			q = "?native_hls=1"
		} else {
			q += "&native_hls=1"
		}
	}
	if playback != "" {
		if q == "" {
			q = "?playback=" + playback
		} else {
			q += "&playback=" + playback
		}
	}
	return q
}

// withSegAudio appends `audio=<n>` to each SEGMENT line of the playlist when the
// client picked an audio track. That way segment requests carry the track and hit
// the SAME session (keyed by audio) as the master — otherwise the segment would
// fall into the default session. Done via post-processing so the (tested)
// signatures of mediaSegQuery/buildVODPlaylist stay unchanged.
func withSegAudio(data []byte, audio string) []byte {
	if audio == "" {
		return data
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		sep := "?"
		if strings.Contains(trim, "?") {
			sep = "&"
		}
		lines[i] = trim + sep + "audio=" + audio
	}
	return []byte(strings.Join(lines, "\n"))
}

// buildVODPlaylist synthesises a finite HLS playlist covering the whole media
// duration: every segment is declared up front (with a token on each line) and
// EXT-X-ENDLIST marks it complete, so Safari renders a full seekbar instead of
// treating the stream as headless LIVE. Segments the encoder hasn't produced
// yet are generated on demand (seek-restart) when the player requests them.
func buildVODPlaylist(durationSec float64, token string, nativeHLS bool) []byte {
	return buildVODPlaylistWithPlayback(durationSec, token, nativeHLS, "")
}

func buildVODPlaylistWithPlayback(durationSec float64, token string, nativeHLS bool, playback string) []byte {
	n := int(math.Ceil(durationSec / httpshared.HLSVODSegDur))
	if n < 1 {
		n = 1
	}
	q := mediaSegQueryWithPlayback(token, nativeHLS, playback)
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
	// TARGETDURATION must be >= the longest EXTINF; segments are ~4s but allow
	// slack for the trailing partial segment and minor keyframe rounding.
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", httpshared.HLSVODSegDur+1)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i := 0; i < n; i++ {
		d := float64(httpshared.HLSVODSegDur)
		if i == n-1 {
			if last := durationSec - float64(i*httpshared.HLSVODSegDur); last > 0 && last < d {
				d = last
			}
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", d)
		fmt.Fprintf(&b, "seg_%05d.ts%s\n", i, q)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}

// StreamHLSMaster serves the master (or legacy single-variant). It reads
// cfg.Stream.HLSMediaRenditions LIVE per request so the admin toggle
// (PUT /api/stream/settings) takes effect on the next play, no restart.
func StreamHLSMaster(s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		hc, ok := newHLSCtx(c, s, mgr, store)
		if !ok {
			return
		}
		hc.mediaRenditions = cfg != nil && cfg.Stream.HLSMediaRenditions
		// Probe-only MASTER when the source warrants one (≥2 ABR rungs, or — with
		// renditions on — ≥2 audio tracks / text subs). Else the legacy
		// single-variant media playlist (sub-1080p / unknown-height sources).
		if serveMasterIfMultiVariant(hc) {
			return
		}
		serveHLSMediaPlaylist(hc)
	}
}

// newHLSCtx parses :hash/:file into an hlsCtx, answering 400 on a bad param.
func newHLSCtx(c *gin.Context, s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store) (*hlsCtx, bool) {
	h, err := parseHash(c.Param("hash"))
	if err != nil {
		httpshared.RespondError(c, http.StatusBadRequest, err)
		return nil, false
	}
	fileIdx, err := strconv.Atoi(c.Param("file"))
	if err != nil {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, errInvalidFileIndex)
		return nil, false
	}
	return &hlsCtx{c: c, s: s, mgr: mgr, store: store, h: h, fileIdx: fileIdx}, true
}

// serveHLSMediaPlaylist resolves the source, (re)starts the session for hc's
// variant and serves its media playlist. Shared by the legacy master route and
// the per-variant route (they differ only in whether hc.variant is pinned).
func serveHLSMediaPlaylist(hc *hlsCtx) {
	source, size, complete := resolveTranscodeSource(hc)
	if source == nil {
		return
	}
	sess, err := startHLSSession(hc, source, size, complete)
	if err != nil {
		return
	}
	if !waitForMasterPlaylist(hc, sess) {
		return
	}
	serveHLSPlaylist(hc.c, sess)
}

// hlsSessionKey separates HLS sessions by VARIANT (ABR ladder rung) and by the
// chosen audio track. Every dimension that changes the transcode goes into the
// key → its own Dir/segments (EffectiveKey still appends -vod/-evt). Without the
// track, switching audio reused the cached session (old track). variant/audioTrack < 0 =
// missing dimension (no suffix) → the legacy single-variant key stays
// identical. Master and segments MUST derive the SAME key (the segment carries
// ?audio= and the variant in the path, plus native_hls, to rebuild the EffectiveKey).
func hlsSessionKey(h metainfo.Hash, fileIdx, variant, audioTrack int) string {
	k := fmt.Sprintf("%s-%d", h.HexString(), fileIdx)
	if variant >= 0 {
		k += fmt.Sprintf("-v%d", variant)
	}
	if audioTrack >= 0 {
		k += fmt.Sprintf("-a%d", audioTrack)
	}
	return k
}

// hlsVariantParam reads the variant index from the path (`v/:variant/...`); -1 when
// missing (legacy single-variant route) or invalid.
func hlsVariantParam(c *gin.Context) int {
	return httpshared.ParseIntOr(c.Param("variant"), -1)
}

// hlsAudioTrackParam reads the ABSOLUTE track index of an audio-only rendition
// from the path (`a/:track/...`); -1 when missing.
func hlsAudioTrackParam(c *gin.Context) int {
	return httpshared.ParseIntOr(c.Param("track"), -1)
}

// hlsAudioOnlyKey is the key of an audio-only session (standalone EXT-X-MEDIA
// TYPE=AUDIO rendition): `{hash}-{file}-ao{track}`. The distinct `-ao` prefix
// (vs the legacy AV remux `-a`) avoids Dir/encodeSpec collisions.
func hlsAudioOnlyKey(h metainfo.Hash, fileIdx, track int) string {
	return fmt.Sprintf("%s-%d-ao%d", h.HexString(), fileIdx, track)
}

// hlsSessionKeyFromReq derives the key from the request: audio-only rendition
// (`a/:track`) → -ao{track}; otherwise the video path (-v{variant}[-a{audio}]).
// Master, playlist and segments MUST use this SAME function to match the Dir.
func hlsSessionKeyFromReq(c *gin.Context, h metainfo.Hash, fileIdx int) string {
	var key string
	if t := hlsAudioTrackParam(c); t >= 0 {
		key = hlsAudioOnlyKey(h, fileIdx, t)
	} else {
		key = hlsSessionKey(h, fileIdx, hlsVariantParam(c), httpshared.ParseIntOr(c.Query("audio"), -1))
	}
	return key + httpshared.PlaybackSessionSuffix(c)
}

func startHLSSession(hc *hlsCtx, source io.ReadSeekCloser, sourceSize int64, complete bool) (*transcode.HLSSession, error) {
	opts := transcode.HLSStartOpts{
		Key:        hlsSessionKeyFromReq(hc.c, hc.h, hc.fileIdx),
		Source:     source,
		SourceSize: sourceSize,
		NativeHLS:  httpshared.NativeHLSParam(hc.c),
		// A fully-downloaded torrent (served from the completed path on disk) is
		// complete & seekable — same VOD case as a local file. An in-progress
		// stream stays under the global vodMode (#61 Safari seek guard).
		ForceVOD: complete,
		// Player already probed to pick HLS vs direct-play — reuse that
		// duration so we don't block first-frame on a second 30s probe.
		KnownDurationSec: pickKnownDuration(probeSource(hc)),
	}
	if t := hlsAudioTrackParam(hc.c); t >= 0 {
		// Audio-only rendition (a/:track): audio-only session for track t, no video.
		opts.AudioOnly = true
		opts.AudioTrack = t
	} else {
		opts.AudioTrack = httpshared.ParseIntOr(hc.c.Query("audio"), -1)
		opts.Variant = hc.variant
	}
	sess, err := hc.mgr.GetOrStart(hc.c.Request.Context(), opts)
	if err != nil {
		httpshared.RespondError(hc.c, http.StatusInternalServerError, err)
		return nil, err
	}
	return sess, nil
}

func serveHLSPlaylist(c *gin.Context, sess *transcode.HLSSession) {
	if sess.IsVOD() {
		c.Header(httpshared.CacheControl, httpshared.CacheNoStore)
		c.Data(http.StatusOK, httpshared.MIMEMPEGURL,
			withSegAudio(buildVODPlaylistWithPlayback(sess.DurationSec, c.Query("token"), httpshared.NativeHLSParam(c), httpshared.PlaybackSession(c)), c.Query("audio")))
		return
	}
	data := readEventPlaylist(c, sess)
	if data == nil {
		return
	}
	c.Header(httpshared.CacheControl, httpshared.CacheNoStore)
	c.Data(http.StatusOK, httpshared.MIMEMPEGURL, data)
}

func readEventPlaylist(c *gin.Context, sess *transcode.HLSSession) []byte {
	data, err := os.ReadFile(filepath.Join(sess.Dir, "index.m3u8"))
	if err != nil {
		httpshared.RespondErrorMessage(c, http.StatusInternalServerError, "playlist not readable")
		return nil
	}
	if q := mediaSegQueryWithPlayback(c.Query("token"), httpshared.NativeHLSParam(c), httpshared.PlaybackSession(c)); q != "" {
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "#") {
				continue
			}
			lines[i] = trim + q
		}
		data = []byte(strings.Join(lines, "\n"))
	}
	return withSegAudio(data, c.Query("audio"))
}

func StreamHLSSegment(s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, errInvalidFileIndex)
			return
		}
		segName := c.Param("seg")
		sess := resolveHLSSession(c, s, mgr, store, h, fileIdx, segName)
		if sess == nil {
			return
		}
		httpshared.EnsureVODSegment(sess, segName)
		httpshared.ServeSegment(c, sess, segName)
	}
}

// resolveHLSSession looks up the active session; if it is gone (reaped/closed),
// RESURRECTS it from the requested segment instead of returning 404. Without this,
// Safari (VOD, static playlist) responds to the 404 by walking the ENTIRE playlist
// into 404s — a burst of hundreds of requests — before refetching the playlist.
// Respawning server-side makes the recovery transparent: the requested segment is
// generated and served (200) within the same request.
func resolveHLSSession(c *gin.Context, s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store, h metainfo.Hash, fileIdx int, segName string) *transcode.HLSSession {
	// EffectiveKey must match the one the master used — hence native_hls is
	// carried on every segment URL (see mediaSegQuery).
	key := mgr.EffectiveKey(hlsSessionKeyFromReq(c, h, fileIdx), httpshared.NativeHLSParam(c))
	if sess, err := getSession(mgr, key); err == nil {
		return sess
	}
	// Without a streamer (degraded path/test) there is no way to respawn → 404 and
	// the client refetches the playlist.
	if s == nil {
		httpshared.RespondErrorMessage(c, http.StatusNotFound, "session not active — request the playlist again")
		return nil
	}
	// Missing session → respawn. resolveTranscodeSource resolves from the store or
	// the torrent (and already answers 404 if the source is gone for good). resolveVariant
	// pins the rung from v/:variant so that respawning a variant segment re-encodes
	// at the RIGHT resolution (otherwise it would encode default 1080 in the -vN dir).
	hc := &hlsCtx{c: c, s: s, mgr: mgr, store: store, h: h, fileIdx: fileIdx}
	if !resolveVariant(hc) {
		httpshared.RespondErrorMessage(c, http.StatusNotFound, "variant out of range")
		return nil
	}
	source, size, complete := resolveTranscodeSource(hc)
	if source == nil {
		return nil
	}
	sess, err := startHLSSession(hc, source, size, complete)
	if err != nil {
		return nil
	}
	// The respawn starts at segment 0; repositions the encoder at the requested
	// segment so the player doesn't have to wait for the transcode to get there
	// sequentially.
	if idx, ok := transcode.ParseSegIndex(segName); ok && idx > 0 && sess.IsVOD() {
		_ = sess.RestartAt(idx)
	}
	return sess
}

// getSession is a small helper to look up an existing session without
// going through the start path. Avoids creating a duplicate ffmpeg if the
// client races and hits the segment handler before the playlist handler.
func getSession(mgr *transcode.HLSSessionManager, key string) (*transcode.HLSSession, error) {
	// Manager exposes only GetOrStart; we cheat by passing a nil-ish opts
	// but it'll dedupe on Key. The downside is theoretical: if the session
	// was reaped and the segment request arrived first, we'd start a new
	// ffmpeg without a Source. Mitigate by requiring Source non-nil there.
	// For simplicity, return an error here when the session isn't already
	// tracked — clients refetch the playlist which respawns properly.
	return mgr.Peek(key)
}

// resolveTranscodeSource tries the completed-download store first, then falls
// back to activating the torrent and opening a streaming reader. It returns the
// seekable input plus its size and whether it is a COMPLETE on-disk file (a
// finished download served from the completed path) — the caller forces VOD for
// complete sources. The in-progress streaming path returns complete=false (the
// #61 Safari seek guard).
func resolveTranscodeSource(hc *hlsCtx) (io.ReadSeekCloser, int64, bool) {
	if f, size, ok := openCompletedFile(hc); ok {
		return f, size, true
	}
	if _, err := hc.s.Get(hc.h); err != nil {
		bareMagnet := MagnetPrefix + hc.h.HexString()
		if _, addErr := hc.s.Add(hc.c.Request.Context(), bareMagnet); addErr != nil {
			httpshared.RespondError(hc.c, http.StatusNotFound, err)
			return nil, 0, false
		}
	}
	reader, file, err := hc.s.FileReader(hc.h, hc.fileIdx)
	if err != nil {
		httpshared.RespondError(hc.c, http.StatusNotFound, err)
		return nil, 0, false
	}
	return reader, file.Length(), false
}

// openCompletedFile resolves a finished download (per-file or whole-torrent
// row) to its on-disk file, so completed items play from disk instead of
// re-downloading from the swarm.
func openCompletedFile(hc *hlsCtx) (io.ReadSeekCloser, int64, bool) {
	if hc.store == nil {
		return nil, 0, false
	}
	relPath := hc.s.FileRelPath(hc.h, hc.fileIdx)
	userID, _, _ := auth.UserIDFromCtx(hc.c)
	path, err := hc.store.GetCompletedPathRel(hc.h.HexString(), hc.fileIdx, relPath, userID)
	if err != nil || path == "" {
		return nil, 0, false
	}
	stat, err := os.Stat(path)
	if err != nil || stat.IsDir() {
		return nil, 0, false
	}
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false
	}
	return f, stat.Size(), true
}

// waitForMasterPlaylist blocks until the first HLS segment is ready. On failure
// it classifies the reason (no_seeds, slow_download) and responds with 503.
func waitForMasterPlaylist(hc *hlsCtx, sess *transcode.HLSSession) bool {
	if err := sess.WaitForMaster(2 * time.Minute); err != nil {
		resp := gin.H{"code": "transcode_failed"}
		if info, gerr := hc.s.Get(hc.h); gerr == nil {
			resp["downRate"] = info.DownRate
			resp["peers"] = info.Peers
			if hc.fileIdx >= 0 && hc.fileIdx < len(info.Files) {
				resp["fileProgress"] = info.Files[hc.fileIdx].Progress
				downloaded := info.Files[hc.fileIdx].Downloaded
				switch {
				case info.Peers == 0:
					resp["code"] = "no_seeds"
				case downloaded < 30<<20:
					resp["code"] = "slow_download"
				}
			}
		}
		httpshared.RespondErrorFields(hc.c, http.StatusServiceUnavailable, err, resp)
		return false
	}
	return true
}
