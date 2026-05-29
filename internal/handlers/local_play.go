package handlers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/local"
	"github.com/luizg/jackui/internal/transcode"
)

// LocalPlayResp tells the frontend HOW to load the source — either as a direct
// progressive download (browser handles the container/codec natively) or as an
// HLS playlist (we transcode on the fly). Mirrors the torrent-side decision so
// the player can stay codec-agnostic.
type LocalPlayResp struct {
	Kind    string `json:"kind"`           // "direct" | "hls"
	URL     string `json:"url"`            // ready-to-use URL with ?token= when applicable
	Reason  string `json:"reason,omitempty"` // why HLS was chosen (codec/container) — debugging aid
	VCodec  string `json:"vcodec,omitempty"`
	ACodec  string `json:"acodec,omitempty"`
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
	"mp4":      true,
	"m4v":      true,
	"mov":      true,
	"webm":     true,
	"isom":     true, // ffprobe sometimes reports the brand
	"mp42":     true,
	"qt":       true,
}
var browserSafeVideoCodecs = map[string]bool{
	"h264": true,
	"vp8":  true,
	"vp9":  true, // good Chrome support; Safari 14+ via WebM
}
var browserSafeAudioCodecs = map[string]bool{
	"aac":  true,
	"mp3":  true,
	"opus": true,
	"vorbis": true,
}

// probeLocal runs ffprobe on a local file and returns the container short name
// plus the FIRST video and audio codec names. Fast (a few hundred ms typical);
// happens once when the user clicks Play.
type localProbe struct {
	Container string
	VideoCodec string
	AudioCodec string
}

func probeLocalFile(ctx context.Context, path string) (localProbe, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// -show_format gives format_name (e.g. "matroska,webm" or "mov,mp4,m4a,3gp,3g2,mj2"),
	// -show_streams gives codec_name per stream. JSON is easy to parse.
	cmd := exec.CommandContext(cctx, "ffprobe",
		"-hide_banner", "-loglevel", "error",
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

func resolveLocalFile(b *local.Browser, c *gin.Context, mount, path string) (string, bool) {
	if mount == "" || path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
		return "", false
	}
	if !checkMountAccess(b, c, mount) {
		return "", false
	}
	abs, err := b.ResolvePath(mount, path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return "", false
	}
	stat, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return "", false
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return "", false
	}
	if stat.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is a directory"})
		return "", false
	}
	return abs, true
}

func localPlayToken(c *gin.Context) string {
	if h := c.Request.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
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

// LocalHLSMaster handles GET /api/local/hls/index.m3u8?mount=&path= — kicks off
// (or attaches to) an HLS transcode session whose source is a local file. The
// pipeline is the same transcode.HLSSessionManager that torrents use; the only
// difference is the source reader (os.File instead of an anacrolix Reader).
//
// We deliberately put the segment key in query params instead of the URL path
// so the file/mount can carry unicode/spaces without URL-encoding gymnastics in
// the segment URLs — segments are addressed by name (e.g. seg_00001.ts) and
// resolved against the same session.
func LocalHLSMaster(b *local.Browser, mgr *transcode.HLSSessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount or path parameter"})
			return
		}
		if !checkMountAccess(b, c, mount) {
			return
		}
		abs, err := b.ResolvePath(mount, path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		stat, err := os.Stat(abs)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
			return
		}
		if stat.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "path is a directory"})
			return
		}

		key := localSessionKey(mount, path)

		// Open the file as the source. os.File is io.ReadSeeker, exactly what
		// HLSStartOpts wants. The session keeps the handle alive until ffmpeg
		// exits (via context cancellation in stop()) — we don't Close() here.
		f, oerr := os.Open(abs)
		if oerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": oerr.Error()})
			return
		}

		sess, err := mgr.GetOrStart(c.Request.Context(), transcode.HLSStartOpts{
			Key:        key,
			Source:     f,
			SourceSize: stat.Size(),
		})
		if err != nil {
			_ = f.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// First segment latency on a local file is fast (no piece download
		// gating). 60s is plenty even for libx264 cold-start fallback.
		if err := sess.WaitForMaster(60 * time.Second); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "code": "transcode_failed"})
			return
		}

		token := c.Query("token")

		// Segments are served from /api/local/hls/seg?name=...&mount=...&path=...
		// — segments don't naturally appear in the playlist path because the
		// playlist URL has query params (Safari resolves relative segment names
		// against the playlist URL, stripping query). We rewrite the playlist so
		// each segment line points at the segment endpoint with explicit params.
		segURL := func(name string) string {
			p := url.Values{}
			p.Set("mount", mount)
			p.Set("path", path)
			p.Set("seg", name)
			if token != "" {
				p.Set("token", token)
			}
			return "/api/local/hls/seg?" + p.Encode()
		}

		if sess.IsVOD() {
			c.Header("Cache-Control", "no-store")
			c.Data(http.StatusOK, "application/vnd.apple.mpegurl",
				buildLocalVODPlaylist(sess.DurationSec, segURL))
			return
		}

		// EVENT fallback: read ffmpeg's m3u8 and rewrite each segment line.
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
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "application/vnd.apple.mpegurl", []byte(strings.Join(lines, "\n")))
	}
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

// LocalHLSSegment handles GET /api/local/hls/seg?mount=&path=&seg=NAME — serves
// a transcoded .ts segment from the disk cache, triggering a seek-restart when
// the requested index is well beyond what's already on disk (VOD mode only).
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
		// Defensive: reject anything with separators in the segment name.
		if strings.ContainsAny(segName, "/\\") || strings.Contains(segName, "..") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid segment name"})
			return
		}
		// Validate the mount/path resolves — defends against probing for
		// segments of arbitrary keys without a valid file.
		if _, err := b.ResolvePath(mount, path); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		key := localSessionKey(mount, path)
		sess, err := mgr.Peek(key)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not active — request the playlist again"})
			return
		}

		if sess.IsVOD() {
			if idx, ok := transcode.ParseSegIndex(segName); ok {
				if _, statErr := os.Stat(filepath.Join(sess.Dir, segName)); statErr != nil {
					sess.EnsureSegment(idx)
				}
			}
		}

		segPath, werr := sess.WaitForSegment(segName, 30*time.Second)
		if werr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": werr.Error()})
			return
		}
		c.Header("Content-Type", "video/mp2t")
		c.Header("Cache-Control", "max-age=3600")
		c.File(segPath)
	}
}
