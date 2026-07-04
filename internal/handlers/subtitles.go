package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/parser"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
)

// SubtitlesSearch handles GET /api/subtitles/search?q=...&season=...&episode=...&langs=pt-BR,pt
func SubtitlesSearch(c *subtitles.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		query := ctx.Query("q")
		if query == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": ErrQueryRequired})
			return
		}
		langs := ctx.DefaultQuery("langs", "pt-BR,pt")
		season, _ := strconv.Atoi(ctx.Query("season"))
		episode, _ := strconv.Atoi(ctx.Query("episode"))

		results, err := c.Search(query, langs, season, episode)
		if err != nil {
			ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if results == nil {
			results = []subtitles.Subtitle{}
		}
		ctx.JSON(http.StatusOK, results)
	}
}

// SubtitlesAuto handles GET /api/subtitles/auto/:hash/:file?langs=pt-BR,pt
// Stremio-style flow:
//  1. Computes the OpenSubtitles file hash from the streaming torrent (waits up to ~30s)
//  2. Searches OpenSubtitles by hash (frame-exact match) AND by title (fallback)
//  3. Returns merged + ranked results — hash matches first
func SubtitlesAuto(s *streamer.Streamer, c *subtitles.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hashStr := ctx.Param("hash")
		fileIdx, err := strconv.Atoi(ctx.Param("file"))
		if err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
			return
		}
		var h metainfo.Hash
		if err := h.FromHexString(hashStr); err != nil {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		langs := ctx.DefaultQuery("langs", "pt-BR,pt")

		// Fetch torrent info to get title & file metadata
		info, err := s.Get(h)
		if err != nil {
			ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if fileIdx < 0 || fileIdx >= len(info.Files) {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": ErrFileIdxOutOfRange})
			return
		}
		file := info.Files[fileIdx]

		// Compute OS hash with a budget — don't block forever
		hashCtx, cancel := context.WithTimeout(ctx.Request.Context(), 30*time.Second)
		defer cancel()
		osHash, hashErr := s.OSHash(hashCtx, h, fileIdx)
		// Hash errors aren't fatal — we still try query-based search

		// Parse title for season/episode/etc.
		parsed := parser.Parse(info.Name)

		opts := subtitles.SearchOpts{
			Query:     info.Name,
			Languages: langs,
			Season:    parsed.Season,
			Episode:   parsed.Episode,
		}
		if hashErr == nil {
			opts.MovieHash = osHash.Hash
			opts.MovieBytesize = osHash.Size
		}

		results, err := c.SearchAuto(opts)
		if err != nil {
			ctx.JSON(http.StatusBadGateway, gin.H{
				"error":  err.Error(),
				"osHash": osHash.Hash,
				"file":   file.Path,
			})
			return
		}
		if results == nil {
			results = []subtitles.Subtitle{}
		}

		ctx.JSON(http.StatusOK, gin.H{
			"osHash":  osHash.Hash,
			"osSize":  osHash.Size,
			"hashErr": errStr(hashErr),
			"file":    file.Path,
			"results": results,
		})
	}
}

func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

// SubtitlesDownload handles GET /api/subtitles/download/:fileId
// Returns the subtitle as text/vtt so HTML5 <track> can consume it directly.
func SubtitlesDownload(c *subtitles.Client) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		fileID := ctx.Param("fileId")
		if fileID == "" {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "fileId required"})
			return
		}
		vtt, err := c.Download(fileID)
		if err != nil {
			ctx.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		ctx.Header(httpshared.ContentType, httpshared.MIMEVTT)
		ctx.Header("Access-Control-Allow-Origin", "*")
		// VTT content for a given file_id is immutable — cache aggressively in the browser
		ctx.Header(httpshared.CacheControl, "public, max-age=2592000, immutable")
		ctx.Header("ETag", `"sub-`+fileID+`"`)
		_, _ = ctx.Writer.Write(vtt)
	}
}
