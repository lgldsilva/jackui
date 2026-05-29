package handlers

import (
	"net/http"
	"os"
	"strconv"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/streamer"
	"github.com/luizg/jackui/internal/transcode"
)

// TranscodeCapabilities handles GET /api/transcode/capabilities — returns the cached
// or freshly-probed encoder/decoder matrix. ?refresh=1 forces re-detection (e.g. after GPU upgrade).
func TranscodeCapabilities(c *gin.Context) {
	force := c.Query("refresh") == "1" || c.Query("refresh") == "true"
	caps, err := transcode.Probe(c.Request.Context(), force)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, caps)
}

// TranscodeStream handles GET /api/stream/transcode/:hash/:file?audio=N&video=h264&burn=N
// Pipes the torrent file through ffmpeg with chosen options and streams the result.
// Note: no Range support — browsers can't seek transcoded streams.
func TranscodeStream(s *streamer.Streamer, store *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var h metainfo.Hash
		if err := h.FromHexString(c.Param("hash")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file index"})
			return
		}

		opts := transcode.Options{
			AudioTrack:   parseIntOr(c.Query("audio"), -1),
			SubBurnTrack: parseIntOr(c.Query("burn"), -1),
			VideoCodec:   c.Query("video"),
			AudioCodec:   c.Query("acodec"),
			// Default to fragmented MP4 — Safari (macOS + iOS) does NOT play MKV
			// in <video>, only MP4/HLS. Chrome/Edge tolerate matroska via
			// experimental media support but it's not in any spec. Caller can
			// still opt back into matroska explicitly via ?container=matroska
			// (useful for VLC handoff or HEVC passthrough scenarios).
			Container:    c.DefaultQuery("container", "mp4"),
		}

		if store != nil {
			if path, err := store.GetCompletedPath(h.HexString(), fileIdx); err == nil && path != "" {
				if _, err := os.Stat(path); err == nil {
					f, err := os.Open(path)
					if err == nil {
						defer f.Close()
						if err := transcode.Run(c.Request.Context(), f, c.Writer, opts); err != nil {
							c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
						}
						return
					}
				}
			}
		}

		reader, _, err := s.FileReader(h, fileIdx)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		defer reader.Close()

		if err := transcode.Run(c.Request.Context(), reader, c.Writer, opts); err != nil {
			// Headers may already be written; log and bail
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
	}
}

func parseIntOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
