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
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/streamer"
	"github.com/luizg/jackui/internal/transcode"
)

type hlsCtx struct {
	c       *gin.Context
	s       *streamer.Streamer
	mgr     *transcode.HLSSessionManager
	store   *downloads.Store
	h       metainfo.Hash
	fileIdx int
}

// hlsVODSegDur must match transcode.hlsSegDur — the segment length the encoder
// targets with forced keyframes. The synthesised playlist declares each
// segment as this long so Safari's timeline (sum of EXTINF) matches the media.
const hlsVODSegDur = 4

// buildVODPlaylist synthesises a finite HLS playlist covering the whole media
// duration: every segment is declared up front (with a token on each line) and
// EXT-X-ENDLIST marks it complete, so Safari renders a full seekbar instead of
// treating the stream as headless LIVE. Segments the encoder hasn't produced
// yet are generated on demand (seek-restart) when the player requests them.
func buildVODPlaylist(durationSec float64, token string) []byte {
	n := int(math.Ceil(durationSec / hlsVODSegDur))
	if n < 1 {
		n = 1
	}
	q := ""
	if token != "" {
		q = "?token=" + token
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:6\n")
	// TARGETDURATION must be >= the longest EXTINF; segments are ~4s but allow
	// slack for the trailing partial segment and minor keyframe rounding.
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
		fmt.Fprintf(&b, "seg_%05d.ts%s\n", i, q)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}

func StreamHLSMaster(s *streamer.Streamer, mgr *transcode.HLSSessionManager, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		hc := &hlsCtx{c: c, s: s, mgr: mgr, store: store, h: h, fileIdx: fileIdx}
		transcodeSource, transcodeSourceSize := resolveTranscodeSource(hc)
		if transcodeSource == nil {
			return
		}
		sess, err := startHLSSession(hc, transcodeSource, transcodeSourceSize)
		if err != nil {
			return
		}
		if !waitForMasterPlaylist(hc, sess) {
			return
		}
		serveHLSPlaylist(c, sess)
	}
}

func startHLSSession(hc *hlsCtx, source io.ReadSeekCloser, sourceSize int64) (*transcode.HLSSession, error) {
	key := fmt.Sprintf("%s-%d", hc.h.HexString(), hc.fileIdx)
	sess, err := hc.mgr.GetOrStart(hc.c.Request.Context(), transcode.HLSStartOpts{
		Key:        key,
		Source:     source,
		SourceSize: sourceSize,
	})
	if err != nil {
		hc.c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, err
	}
	return sess, nil
}

func serveHLSPlaylist(c *gin.Context, sess *transcode.HLSSession) {
	if sess.IsVOD() {
		c.Header(CacheControl, CacheNoStore)
		c.Data(http.StatusOK, MIMEMPEGURL,
			buildVODPlaylist(sess.DurationSec, c.Query("token")))
		return
	}
	data := readEventPlaylist(c, sess)
	if data == nil {
		return
	}
	c.Header(CacheControl, CacheNoStore)
	c.Data(http.StatusOK, MIMEMPEGURL, data)
}

func readEventPlaylist(c *gin.Context, sess *transcode.HLSSession) []byte {
	data, err := os.ReadFile(filepath.Join(sess.Dir, "index.m3u8"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "playlist not readable"})
		return nil
	}
	if tok := c.Query("token"); tok != "" {
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "#") {
				continue
			}
			lines[i] = trim + "?token=" + tok
		}
		data = []byte(strings.Join(lines, "\n"))
	}
	return data
}

func StreamHLSSegment(mgr *transcode.HLSSessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, err := parseHash(c.Param("hash"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		segName := c.Param("seg")
		sess := resolveHLSSession(c, mgr, h, fileIdx)
		if sess == nil {
			return
		}
		ensureVODSegment(sess, segName)
		serveSegment(c, sess, segName)
	}
}

func resolveHLSSession(c *gin.Context, mgr *transcode.HLSSessionManager, h metainfo.Hash, fileIdx int) *transcode.HLSSession {
	key := fmt.Sprintf("%s-%d", h.HexString(), fileIdx)
	sess, err := getSession(mgr, key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not active — request the playlist again"})
		return nil
	}
	return sess
}

func ensureVODSegment(sess *transcode.HLSSession, segName string) {
	if !sess.IsVOD() {
		return
	}
	idx, ok := transcode.ParseSegIndex(segName)
	if !ok {
		return
	}
	if _, statErr := os.Stat(filepath.Join(sess.Dir, segName)); statErr != nil {
		sess.EnsureSegment(idx)
	}
}

func serveSegment(c *gin.Context, sess *transcode.HLSSession, segName string) {
	path, err := sess.WaitForSegment(segName, 30*time.Second)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Header(ContentType, "video/mp2t")
	c.Header(CacheControl, "max-age=3600")
	c.File(path)
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
// back to activating the torrent and opening a streaming reader.
func resolveTranscodeSource(hc *hlsCtx) (io.ReadSeekCloser, int64) {
	if hc.store != nil {
		if path, err := hc.store.GetCompletedPath(hc.h.HexString(), hc.fileIdx); err == nil && path != "" {
			if stat, err := os.Stat(path); err == nil {
				if f, err := os.Open(path); err == nil {
					return f, stat.Size()
				}
			}
		}
	}
	if _, err := hc.s.Get(hc.h); err != nil {
		bareMagnet := "magnet:?xt=urn:btih:" + hc.h.HexString()
		if _, addErr := hc.s.Add(hc.c.Request.Context(), bareMagnet); addErr != nil {
			hc.c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return nil, 0
		}
	}
	reader, file, err := hc.s.FileReader(hc.h, hc.fileIdx)
	if err != nil {
		hc.c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return nil, 0
	}
	return reader, file.Length()
}

// waitForMasterPlaylist blocks until the first HLS segment is ready. On failure
// it classifies the reason (no_seeds, slow_download) and responds with 503.
func waitForMasterPlaylist(hc *hlsCtx, sess *transcode.HLSSession) bool {
	if err := sess.WaitForMaster(2 * time.Minute); err != nil {
		resp := gin.H{"error": err.Error(), "code": "transcode_failed"}
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
		hc.c.JSON(http.StatusServiceUnavailable, resp)
		return false
	}
	return true
}
