package handlers

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/audiometa"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
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
// timeout. The read deadline is wired INTO the reader (SetContext) — not just
// into the select — because Reader.Close() does not unblock a Read blocked in
// waitAvailable: without it, a stalled swarm left the spawned goroutine, the
// reader and its buffers alive long after this call gave up (the same leak
// class Prefetch/warmTail fixed in the streamer). The reader is still ALWAYS
// closed by the goroutine once the read finishes — which, with the ctx wired
// in, now actually happens after a timeout. ok=false on timeout/error → caller
// falls back to the filename. Readers that don't support SetContext keep the
// plain select behavior.
func readTorrentTags(ctx context.Context, rc io.ReadSeekCloser, timeout time.Duration) (audiometa.Tags, bool) {
	type result struct {
		tags audiometa.Tags
		err  error
	}
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	ch := make(chan result, 1)
	go func() {
		if sc, ok := rc.(interface{ SetContext(context.Context) }); ok {
			sc.SetContext(readCtx)
		}
		t, err := audiometa.ReadTagsFrom(rc)
		_ = rc.Close()
		ch <- result{t, err}
		// Only AFTER the send: canceling first would close readCtx.Done() while
		// the parent's select still has the result case pending, and Go picks
		// randomly between ready cases. After the send the result is already
		// buffered, so the close can only reach a parent that had effectively
		// given up (a microsecond-wide flap at the exact deadline boundary
		// remains possible and is harmless: tags fall back to the filename).
		cancel()
	}()
	select {
	case r := <-ch:
		return r.tags, r.err == nil
	case <-readCtx.Done():
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
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, errInvalidFileIndex)
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
		tags, ok := readTorrentTags(c.Request.Context(), r, audioTagTimeout)
		if ok {
			torrentTagCache.Store(key, tags)
		}
		c.JSON(http.StatusOK, tags)
	}
}
