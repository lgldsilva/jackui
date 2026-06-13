package handlers

import (
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/audiometa"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// audioTagTimeout bounds the tag read: a torrent file reader can block waiting
// for pieces. ID3v2/Vorbis/FLAC tags live at the file HEAD (downloaded first
// while streaming), so a healthy read returns well under this; the cap just
// stops a cold read (or an ID3v1 tail-seek on undownloaded pieces) from hanging
// the request.
const audioTagTimeout = 6 * time.Second

// torrentTagCache memoises parsed tags by "hash:idx" — tags never change for a
// given file, so one successful read serves every later request for free.
var torrentTagCache sync.Map // map[string]audiometa.Tags

// readTorrentTags parses tags from a (possibly slow) torrent reader under a
// timeout. The reader is ALWAYS closed by the spawned goroutine once the read
// finishes — even after a timeout — so we never close it out from under an
// in-flight read. ok=false on timeout/error → caller falls back to the filename.
func readTorrentTags(rc io.ReadSeekCloser, timeout time.Duration) (audiometa.Tags, bool) {
	type result struct {
		tags audiometa.Tags
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		t, err := audiometa.ReadTagsFrom(rc)
		_ = rc.Close()
		ch <- result{t, err}
	}()
	select {
	case r := <-ch:
		return r.tags, r.err == nil
	case <-time.After(timeout):
		return audiometa.Tags{}, false
	}
}

// StreamAudioMeta handles GET /api/stream/audio/meta/:hash/:file — reads the
// audio tags of a file INSIDE a torrent (the artist/album/year a filename like
// "01 - Track.flac" omits). Best-effort: returns empty tags (200) on any failure
// so the UI falls back to the parsed filename.
func StreamAudioMeta(s *streamer.Streamer) gin.HandlerFunc {
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
		key := h.HexString() + ":" + strconv.Itoa(fileIdx)
		if cached, ok := torrentTagCache.Load(key); ok {
			c.JSON(http.StatusOK, cached)
			return
		}
		r, _, ferr := s.FileReader(h, fileIdx)
		if ferr != nil {
			c.JSON(http.StatusOK, audiometa.Tags{})
			return
		}
		tags, ok := readTorrentTags(r, audioTagTimeout)
		if ok {
			torrentTagCache.Store(key, tags)
		}
		c.JSON(http.StatusOK, tags)
	}
}
