package handlers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localstream"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// LocalPlayResp tells the frontend HOW to load the source — either as a direct
// progressive download (browser handles the container/codec natively) or as an
// HLS playlist (we transcode on the fly). Mirrors the torrent-side decision so
// the player can stay codec-agnostic.
type LocalPlayResp struct {
	Kind      string `json:"kind"`             // "direct" | "hls"
	URL       string `json:"url"`              // ready-to-use URL with ?token= when applicable
	Reason    string `json:"reason,omitempty"` // why HLS was chosen (codec/container) — debugging aid
	VCodec    string `json:"vcodec,omitempty"`
	ACodec    string `json:"acodec,omitempty"`
	Container string `json:"container,omitempty"`
}

// browserSafeContainers / browserSafeVideoCodecs / browserSafeAudioCodecs:
// conservative whitelists for direct-play. Anything outside requires transcode.
// Goals:
//   - MKV → HLS unconditionally (Safari refuses it; Chrome/Edge tolerate it via
//     experimental support but it's not in any spec, and AC3/DTS audio still
//     breaks playback even when the container loads).
//   - HEVC/AV1/VP9-in-MP4 → HLS (Chrome/Safari progress on AV1 is patchy; HEVC
//     in <video> only works on Safari with hardware decode, never on Chrome
//     desktop).
//   - AC3/EAC3/DTS audio → HLS (no browser plays these inline).
var browserSafeContainers = map[string]bool{
	"mp4":  true,
	"m4v":  true,
	"mov":  true,
	"webm": true,
	"isom": true, // ffprobe sometimes reports the brand
	"mp42": true,
	"qt":   true,
}
var browserSafeVideoCodecs = map[string]bool{
	"h264": true,
	"vp8":  true,
	"vp9":  true, // good Chrome support; Safari 14+ via WebM
}
var browserSafeAudioCodecs = map[string]bool{
	"aac":    true,
	"mp3":    true,
	"opus":   true,
	"vorbis": true,
}

// probeLocal runs ffprobe on a local file and returns the container short name
// plus the FIRST video and audio codec names. Fast (a few hundred ms typical);
// happens once when the user clicks Play.
type localProbe struct {
	Container  string
	VideoCodec string
	AudioCodec string
}

func probeLocalFile(ctx context.Context, path string) (localProbe, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// -show_format gives format_name (e.g. "matroska,webm" or "mov,mp4,m4a,3gp,3g2,mj2"),
	// -show_streams gives codec_name per stream. JSON is easy to parse.
	cmd := exec.CommandContext(cctx, "ffprobe",
		ffHideBanner, ffLogLevel, "error",
		"-of", "json",
		"-show_format", "-show_streams",
		path,
	)
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return localProbe{}, fmt.Errorf("ffprobe: %w", err)
	}
	var parsed struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
		} `json:"format"`
	}
	if jerr := json.Unmarshal(out, &parsed); jerr != nil {
		return localProbe{}, fmt.Errorf("decode ffprobe: %w", jerr)
	}
	p := localProbe{}
	// format_name is comma-separated; first entry is usually the canonical one
	// for our matching ("matroska,webm" → "matroska"). Lowercase for the map.
	if parsed.Format.FormatName != "" {
		first := parsed.Format.FormatName
		if i := strings.IndexByte(first, ','); i >= 0 {
			first = first[:i]
		}
		p.Container = strings.ToLower(first)
	}
	for _, st := range parsed.Streams {
		if p.VideoCodec == "" && st.CodecType == "video" {
			p.VideoCodec = strings.ToLower(st.CodecName)
		}
		if p.AudioCodec == "" && st.CodecType == "audio" {
			p.AudioCodec = strings.ToLower(st.CodecName)
		}
	}
	return p, nil
}

// classifyForBrowser decides whether a local file is direct-playable by a
// generic <video> element. Returns (directPlay, reason). The reason is shown to
// the client when HLS is chosen so we can debug why (e.g. "container=matroska").
func classifyForBrowser(p localProbe) (bool, string) {
	if !browserSafeContainers[p.Container] {
		return false, "container=" + p.Container
	}
	if p.VideoCodec != "" && !browserSafeVideoCodecs[p.VideoCodec] {
		return false, "vcodec=" + p.VideoCodec
	}
	if p.AudioCodec != "" && !browserSafeAudioCodecs[p.AudioCodec] {
		return false, "acodec=" + p.AudioCodec
	}
	return true, ""
}

// localSessionKey derives a stable, filesystem-safe HLS session key from
// (mount, relPath). sha1 keeps the key short and avoids leaking the path.
func localSessionKey(mount, relPath string) string {
	sum := sha1.Sum([]byte(mount + "|" + relPath))
	return "local-" + hex.EncodeToString(sum[:])
}

// transferKeyDirect / transferKeyHLS key the localstream metering Session for
// the direct-play and HLS-transcode paths respectively. They are kept distinct
// (a file plays via one path or the other) so a solo direct session never
// evicts a shared HLS session from the registry. The transfer-status endpoint
// checks the HLS key first, then the direct key.
func transferKeyDirect(mount, scoped string) string { return localSessionKey(mount, scoped) }
func transferKeyHLS(mount, scoped string) string    { return localSessionKey(mount, scoped) + "-hls" }

func resolveLocalFile(b *local.Browser, c *gin.Context, mount, path string) (string, bool) {
	if mount == "" || path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
		return "", false
	}
	if !checkMountAccess(b, c, mount) {
		return "", false
	}
	abs, err := b.ResolvePath(mount, scopePath(b, c, mount, path))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return "", false
	}
	stat, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": ErrFileNotFound})
			return "", false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return "", false
	}
	if stat.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrPathIsDir})
		return "", false
	}
	return abs, true
}

func localPlayToken(c *gin.Context) string {
	if h := c.Request.Header.Get(HeaderAuthorization); strings.HasPrefix(h, auth.BearerPrefix) {
		return strings.TrimPrefix(h, auth.BearerPrefix)
	}
	return c.Query("token")
}

func appendTokenToURL(token, base string) string {
	if token == "" {
		return base
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "token=" + url.QueryEscape(token)
}

func localPlayVideoResp(c *gin.Context, abs, mount, path, token string) LocalPlayResp {
	probe, perr := probeLocalFile(c.Request.Context(), abs)
	if perr != nil {
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".mp4" || ext == ".m4v" || ext == ".mov" || ext == ".webm" {
			return LocalPlayResp{
				Kind:   "direct",
				URL:    appendTokenToURL(token, buildLocalFileURL(mount, path)),
				Reason: "probe_failed_safe_ext",
			}
		}
		return LocalPlayResp{
			Kind:   "hls",
			URL:    appendTokenToURL(token, buildLocalHLSURL(mount, path)),
			Reason: "probe_failed",
		}
	}
	direct, reason := classifyForBrowser(probe)
	if direct {
		return LocalPlayResp{
			Kind:      "direct",
			URL:       appendTokenToURL(token, buildLocalFileURL(mount, path)),
			VCodec:    probe.VideoCodec,
			ACodec:    probe.AudioCodec,
			Container: probe.Container,
		}
	}
	return LocalPlayResp{
		Kind:      "hls",
		URL:       appendTokenToURL(token, buildLocalHLSURL(mount, path)),
		Reason:    reason,
		VCodec:    probe.VideoCodec,
		ACodec:    probe.AudioCodec,
		Container: probe.Container,
	}
}

// LocalPlay handles GET /api/local/play?mount=NAME&path=REL — probes the file
// and returns either { kind: "direct", url } or { kind: "hls", url }. The
// frontend just consumes `url` and trusts the kind for any wrapper logic
// (subtitles via WebVTT, etc.).
func LocalPlay(b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")

		abs, ok := resolveLocalFile(b, c, mount, path)
		if !ok {
			return
		}

		token := localPlayToken(c)

		if isAudioByExt(path) {
			c.JSON(http.StatusOK, LocalPlayResp{
				Kind: "direct",
				URL:  appendTokenToURL(token, buildLocalFileURL(mount, path)),
			})
			return
		}

		c.JSON(http.StatusOK, localPlayVideoResp(c, abs, mount, path, token))
	}
}

func buildLocalFileURL(mount, path string) string {
	p := url.Values{}
	p.Set("mount", mount)
	p.Set("path", path)
	return "/api/local/file?" + p.Encode()
}

func buildLocalHLSURL(mount, path string) string {
	p := url.Values{}
	p.Set("mount", mount)
	p.Set("path", path)
	return "/api/local/hls/index.m3u8?" + p.Encode()
}

func isAudioByExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".m4a", ".aac", ".flac", ".ogg", ".wav", ".opus":
		return true
	}
	return false
}

func LocalHLSMaster(b *local.Browser, mgr *transcode.HLSSessionManager, reg *localstream.Registry) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMissingMountOrPathParam})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		// scoped is the on-disk path (prefixed with the user's subdir on
		// UserSubpath mounts); it also keys the HLS session so two users with
		// same-named files don't collide. path stays logical for the seg URLs.
		scoped := scopePath(b, c, mount, path)
		abs, stat, err := resolveLocalFileStat(b, mount, scoped)
		if err != nil || abs == "" {
			return
		}
		sess, err := startLocalHLSSession(c, mgr, reg, mount, scoped, abs, stat)
		if err != nil {
			return
		}
		if !waitLocalPlaylist(c, sess) {
			return
		}
		buildSegURL := segURLBuilder(mount, path, c.Query("token"), c.Query("user"))
		serveLocalPlaylist(c, sess, buildSegURL)
	}
}

func resolveLocalFileStat(b *local.Browser, mount, path string) (string, os.FileInfo, error) {
	abs, err := b.ResolvePath(mount, path)
	if err != nil {
		return "", nil, err
	}
	stat, err := os.Stat(abs)
	if err != nil {
		return "", nil, err
	}
	if stat.IsDir() {
		return "", nil, fmt.Errorf(ErrPathIsDir)
	}
	return abs, stat, nil
}

func startLocalHLSSession(c *gin.Context, mgr *transcode.HLSSessionManager, reg *localstream.Registry, mount, path, abs string, stat os.FileInfo) (*transcode.HLSSession, error) {
	key := localSessionKey(mount, path)
	f, oerr := os.Open(abs)
	if oerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": oerr.Error()})
		return nil, oerr
	}
	// Wrap the file in a metered, read-ahead Session before handing it to the
	// transcoder: ffmpeg's Range reads now feed the speed indicator and benefit
	// from aligned read-ahead on slow mounts. The registry owns the handle and
	// reaps it when ffmpeg stops pulling (it outlives this request). The metering
	// key (transferKeyHLS == key+"-hls") is what /local/transfer-status looks up.
	source, meterKey := mountSource(reg, key+"-hls", f, stat.Size())
	sess, err := mgr.GetOrStart(c.Request.Context(), transcode.HLSStartOpts{
		Key:        key,
		Source:     source,
		SourceSize: stat.Size(),
	})
	if err != nil {
		closeSource(reg, meterKey, source, f)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, err
	}
	return sess, nil
}

// mountSource returns the io.ReadSeeker the transcoder reads through plus the
// registry key it was filed under. With a registry it is a shared metered
// Session; without one it is the raw file (keeps tests / nil-reg paths working).
func mountSource(reg *localstream.Registry, meterKey string, f *os.File, size int64) (io.ReadSeeker, string) {
	if reg == nil {
		return f, meterKey
	}
	return reg.OpenShared(meterKey, f, size), meterKey
}

func closeSource(reg *localstream.Registry, meterKey string, source io.ReadSeeker, f *os.File) {
	if s, ok := source.(*localstream.Session); ok && reg != nil {
		reg.Release(meterKey, s)
		return
	}
	_ = f.Close()
}

func waitLocalPlaylist(c *gin.Context, sess *transcode.HLSSession) bool {
	if err := sess.WaitForMaster(60 * time.Second); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "code": "transcode_failed"})
		return false
	}
	return true
}

func segURLBuilder(mount, path, token, user string) func(name string) string {
	return func(name string) string {
		p := url.Values{}
		p.Set("mount", mount)
		p.Set("path", path)
		p.Set("seg", name)
		if token != "" {
			p.Set("token", token)
		}
		// Propagate the admin "view as user" target so each segment request
		// re-scopes to the same subdir the master playlist resolved against.
		if user != "" {
			p.Set("user", user)
		}
		return "/api/local/hls/seg?" + p.Encode()
	}
}

func serveLocalPlaylist(c *gin.Context, sess *transcode.HLSSession, segURL func(string) string) {
	if sess.IsVOD() {
		c.Header(CacheControl, CacheNoStore)
		c.Data(http.StatusOK, MIMEMPEGURL,
			buildLocalVODPlaylist(sess.DurationSec, segURL))
		return
	}
	data, rerr := os.ReadFile(filepath.Join(sess.Dir, "index.m3u8"))
	if rerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "playlist not readable"})
		return
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		lines[i] = segURL(trim)
	}
	c.Header(CacheControl, CacheNoStore)
	c.Data(http.StatusOK, MIMEMPEGURL, []byte(strings.Join(lines, "\n")))
}

// buildLocalVODPlaylist is the local-source analogue of buildVODPlaylist in
// hls.go — same VOD shape, but each segment line is the full segURL (which
// already includes mount/path/seg/token), since segments live under a custom
// query-driven endpoint instead of the torrent path scheme.
func buildLocalVODPlaylist(durationSec float64, segURL func(name string) string) []byte {
	n := int((durationSec + float64(hlsVODSegDur) - 1) / float64(hlsVODSegDur))
	if n < 1 {
		n = 1
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", hlsVODSegDur+1)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	for i := 0; i < n; i++ {
		d := float64(hlsVODSegDur)
		if i == n-1 {
			if last := durationSec - float64(i*hlsVODSegDur); last > 0 && last < d {
				d = last
			}
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", d)
		segName := fmt.Sprintf("seg_%05d.ts", i)
		b.WriteString(segURL(segName))
		b.WriteString("\n")
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}

func LocalHLSSegment(b *local.Browser, mgr *transcode.HLSSessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		segName := c.Query("seg")
		if mount == "" || path == "" || segName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount, path or seg parameter"})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		if !validSegName(segName) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid segment name"})
			return
		}
		// Must mirror LocalHLSMaster: scope both the path validation and the
		// session-key lookup with the user's subdir, or the segment serves
		// another user's file / session.
		scoped := scopePath(b, c, mount, path)
		if !validLocalSegPath(b, mount, scoped) {
			return
		}
		sess := resolveLocalSession(c, mgr, mount, scoped)
		if sess == nil {
			return
		}
		ensureVODSegment(sess, segName)
		serveSegment(c, sess, segName)
	}
}

func validSegName(name string) bool {
	return !strings.ContainsAny(name, "/\\") && !strings.Contains(name, "..")
}

func validLocalSegPath(b *local.Browser, mount, path string) bool {
	_, err := b.ResolvePath(mount, path)
	return err == nil
}

func resolveLocalSession(c *gin.Context, mgr *transcode.HLSSessionManager, mount, path string) *transcode.HLSSession {
	key := localSessionKey(mount, path)
	sess, err := mgr.Peek(key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not active — request the playlist again"})
		return nil
	}
	return sess
}
