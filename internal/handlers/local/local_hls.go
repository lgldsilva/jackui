package local

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/localcache"
	"github.com/lgldsilva/jackui/internal/localstream"
	"github.com/lgldsilva/jackui/internal/transcode"
)

func LocalHLSMaster(b *lb.Browser, mgr *transcode.HLSSessionManager, reg *localstream.Registry, cache *localcache.Cache) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		if mount == "" || path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrMissingMountOrPathParam})
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		// scoped is the on-disk path (prefixed with the user's subdir on
		// UserSubpath mounts); it also keys the HLS session so two users with
		// same-named files don't collide. path stays logical for the seg URLs.
		scoped := ScopePath(b, c, mount, path)
		abs, stat, knownDur, ok := resolveLocalHLSInput(c, b, cache, mount, scoped)
		if !ok {
			return
		}
		sess, err := startLocalHLSSession(c, mgr, reg, localHLSSource{
			mount: mount, path: scoped, abs: abs, stat: stat,
			nativeHLS: httpshared.NativeHLSParam(c), knownDur: knownDur,
			audioOnly:  isAudioByExt(path),
			audioTrack: httpshared.ParseIntOr(c.Query("audio"), -1),
		})
		if err != nil {
			return
		}
		if !waitLocalPlaylist(c, sess) {
			return
		}
		buildSegURL := segURLBuilder(mount, path, c.Query("token"), c.Query("user"), httpshared.NativeHLSParam(c), c.Query("audio"))
		serveLocalPlaylist(c, sess, buildSegURL)
	}
}

// resolveLocalHLSInput resolves the file the HLS session should transcode:
// prefers the cached local copy when ready (fast disk, no rclone EIO), and
// probes the duration off it so the session can skip the slow 30s seekable
// probe (the rclone "slow to load" win). Returns ok=false (after writing any
// error) when the file can't be resolved.
func resolveLocalHLSInput(c *gin.Context, b *lb.Browser, cache *localcache.Cache, mount, scoped string) (string, os.FileInfo, float64, bool) {
	abs, stat, err := resolveLocalFileStat(b, mount, scoped)
	if err != nil || abs == "" {
		return "", nil, 0, false
	}
	if cp, ok := cacheReady(cache, mount, scoped); ok {
		abs, stat = cp.abs, cp.stat
	}
	knownDur := 0.0
	if probe, perr := probeLocalFile(c.Request.Context(), abs); perr == nil {
		knownDur = probe.DurationSec
	}
	return abs, stat, knownDur, true
}

func resolveLocalFileStat(b *lb.Browser, mount, path string) (string, os.FileInfo, error) {
	abs, err := b.ResolvePath(mount, path)
	if err != nil {
		return "", nil, err
	}
	stat, err := os.Stat(abs)
	if err != nil {
		return "", nil, err
	}
	if stat.IsDir() {
		return "", nil, fmt.Errorf(httpshared.ErrPathIsDir)
	}
	return abs, stat, nil
}

// localHLSSource bundles everything needed to start a local-file HLS session:
// where the file lives (mount/path key it under, abs is what we open), its stat
// (size), and the per-request transcode hints (native HLS client, known
// duration). Grouped into a struct to keep startLocalHLSSession's signature lean.
type localHLSSource struct {
	mount, path, abs string
	stat             os.FileInfo
	nativeHLS        bool
	knownDur         float64
	audioOnly        bool // pure-audio file → `-vn` AAC HLS (no video map)
	audioTrack       int  // faixa de áudio escolhida (índice absoluto do probe; <0 = primeira/default)
}

func startLocalHLSSession(c *gin.Context, mgr *transcode.HLSSessionManager, reg *localstream.Registry, src localHLSSource) (*transcode.HLSSession, error) {
	key := localSessionKey(src.mount, src.path)
	if src.audioTrack >= 0 {
		key += fmt.Sprintf("-a%d", src.audioTrack) // sessão por faixa: trocar áudio não reusa o cache
	}
	f, oerr := os.Open(src.abs)
	if oerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": oerr.Error()})
		return nil, oerr
	}
	// Wrap the file in a metered, read-ahead Session before handing it to the
	// transcoder: ffmpeg's Range reads now feed the speed indicator and benefit
	// from aligned read-ahead on slow mounts. The registry owns the handle and
	// reaps it when ffmpeg stops pulling (it outlives this request). The metering
	// key (transferKeyHLS == key+"-hls") is what /local/transfer-status looks up.
	source, meterKey := mountSource(reg, key+"-hls", f, src.stat.Size())
	sess, err := mgr.GetOrStart(c.Request.Context(), transcode.HLSStartOpts{
		Key:              key,
		Source:           source,
		SourceSize:       src.stat.Size(),
		NativeHLS:        src.nativeHLS,
		KnownDurationSec: src.knownDur,
		// Local files are complete & seekable → always VOD when the duration is
		// known (incl. Safari/iOS native HLS), regardless of the global vodMode.
		// EVENT/live is the last resort for unknown-duration streams only.
		ForceVOD:   true,
		AudioOnly:  src.audioOnly,
		AudioTrack: src.audioTrack,
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
	// OpenSolo (NOT OpenShared): each transcode session must read through its OWN
	// cursor. The meterKey is the same for every client of a (mount,path), but two
	// concurrent sessions for the same file (e.g. a Chrome VOD client + a Safari
	// native-HLS client = different HLS effKeys, or any two ffmpeg launches) would
	// otherwise share ONE OpenShared Session/cursor — their interleaved Seek+Read
	// thrash each other and the second client stalls. OpenSolo gives each its own
	// handle (same fix the direct-play path uses); GetOrStart closes the loser's
	// source when it dedupes same-effKey callers.
	return reg.OpenSolo(meterKey, f, size), meterKey
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

func segURLBuilder(mount, path, token, user string, nativeHLS bool, audio string) func(name string) string {
	return func(name string) string {
		p := url.Values{}
		p.Set("mount", mount)
		p.Set("path", path)
		p.Set("seg", name)
		if token != "" {
			p.Set("token", token)
		}
		// Faixa de áudio: o segmento precisa bater na MESMA sessão (keyed por áudio)
		// que o master, senão cai na sessão default (primeira faixa).
		if audio != "" {
			p.Set("audio", audio)
		}
		// Propagate the admin "view as user" target so each segment request
		// re-scopes to the same subdir the master playlist resolved against.
		if user != "" {
			p.Set("user", user)
		}
		// Carry native_hls so the segment resolves to the same session key the
		// master created (see HLSSessionManager.EffectiveKey).
		if nativeHLS {
			p.Set("native_hls", "1")
		}
		return "/api/local/hls/seg?" + p.Encode()
	}
}

func serveLocalPlaylist(c *gin.Context, sess *transcode.HLSSession, segURL func(string) string) {
	if sess.IsVOD() {
		c.Header(httpshared.CacheControl, httpshared.CacheNoStore)
		c.Data(http.StatusOK, httpshared.MIMEMPEGURL,
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
	c.Header(httpshared.CacheControl, httpshared.CacheNoStore)
	c.Data(http.StatusOK, httpshared.MIMEMPEGURL, []byte(strings.Join(lines, "\n")))
}

// buildLocalVODPlaylist is the local-source analogue of buildVODPlaylist in
// hls.go — same VOD shape, but each segment line is the full segURL (which
// already includes mount/path/seg/token), since segments live under a custom
// query-driven endpoint instead of the torrent path scheme.
func buildLocalVODPlaylist(durationSec float64, segURL func(name string) string) []byte {
	n := int((durationSec + float64(httpshared.HLSVODSegDur) - 1) / float64(httpshared.HLSVODSegDur))
	if n < 1 {
		n = 1
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
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
		segName := fmt.Sprintf("seg_%05d.ts", i)
		b.WriteString(segURL(segName))
		b.WriteString("\n")
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}

func LocalHLSSegment(b *lb.Browser, mgr *transcode.HLSSessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		path := c.Query("path")
		segName := c.Query("seg")
		if mount == "" || path == "" || segName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount, path or seg parameter"})
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		if !validSegName(segName) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid segment name"})
			return
		}
		// Must mirror LocalHLSMaster: scope both the path validation and the
		// session-key lookup with the user's subdir, or the segment serves
		// another user's file / session.
		scoped := ScopePath(b, c, mount, path)
		if !validLocalSegPath(b, mount, scoped) {
			return
		}
		sess := resolveLocalSession(c, mgr, mount, scoped)
		if sess == nil {
			return
		}
		httpshared.EnsureVODSegment(sess, segName)
		httpshared.ServeSegment(c, sess, segName)
	}
}

func validSegName(name string) bool {
	return !strings.ContainsAny(name, "/\\") && !strings.Contains(name, "..")
}

func validLocalSegPath(b *lb.Browser, mount, path string) bool {
	_, err := b.ResolvePath(mount, path)
	return err == nil
}

func resolveLocalSession(c *gin.Context, mgr *transcode.HLSSessionManager, mount, path string) *transcode.HLSSession {
	// EffectiveKey must match the master's — native_hls E audio rodam em toda seg URL.
	raw := localSessionKey(mount, path)
	if a := httpshared.ParseIntOr(c.Query("audio"), -1); a >= 0 {
		raw += fmt.Sprintf("-a%d", a)
	}
	key := mgr.EffectiveKey(raw, httpshared.NativeHLSParam(c))
	sess, err := mgr.Peek(key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not active — request the playlist again"})
		return nil
	}
	return sess
}
