package handlers

import (
	"fmt"
	"net/http"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

const errInvalidFileIndex = "invalid file index"

type streamAddReq struct {
	Magnet string `json:"magnet"`
	// Kind is the player's classification ("audio" | "video"), sent by the
	// frontend (detectKind). Persisted so Continue Watching / stats group audio
	// correctly. Empty leaves the column untouched (the Upsert only overwrites
	// kind when non-empty), so an older client doesn't wipe a known value.
	Kind string `json:"kind,omitempty"`
}

// normalizeKind whitelists the player's kind hint to the values the library
// column understands ("audio" | "video"); anything else becomes "" so a bogus
// value can't poison the row (and "" leaves the existing column untouched).
func normalizeKind(k string) string {
	switch k {
	case "audio", "video":
		return k
	}
	return ""
}

// StreamAdd handles POST /api/stream/add — registers a magnet, waits for metadata.
// Side-effect: persists the magnet in the user's library so they can re-play after restart
// or from /favorites without going through a new search.
func StreamAdd(s *streamer.Streamer, lib *library.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req streamAddReq
		if err := c.ShouldBindJSON(&req); err != nil || req.Magnet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "magnet is required"})
			return
		}
		info, err := s.Add(c.Request.Context(), req.Magnet)
		if err != nil {
			// Log enough context to debug pipeline issues without leaking the full magnet
			preview := req.Magnet
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
			fmt.Printf("[stream/add] failed for %q: %v\n", preview, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		// Persist into the user's library (idempotent upsert). Kind comes from the
		// player (detectKind); empty leaves the column untouched.
		// In incognito mode: still upsert so the entry exists for resume tracking,
		// but mark it with incognito=1 so it is excluded from normal listings and
		// deleted when the user ends their incognito session.
		if lib != nil {
			userID, _, _ := auth.UserIDFromCtx(c)
			_, _ = lib.Upsert(library.UpsertInput{UserID: userID, InfoHash: info.InfoHash, Magnet: req.Magnet, Name: info.Name, PrimaryFile: info.PrimaryFile, TotalSize: info.TotalSize, Kind: normalizeKind(req.Kind), Incognito: middleware.IsIncognito(c)})
		}
		c.JSON(http.StatusOK, info)
	}
}

// StreamAddTorrentFile handles POST /api/stream/add-file — adds a torrent from uploaded .torrent file.
func StreamAddTorrentFile(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		file, err := c.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
			return
		}
		src, err := file.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer func() { _ = src.Close() }()

		mi, err := metainfo.Load(src)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid torrent file: " + err.Error()})
			return
		}

		t, err := s.Client().AddTorrent(mi)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add torrent: " + err.Error()})
			return
		}

		// Wait for metadata
		select {
		case <-t.GotInfo():
		default:
		}

		m, merr := mi.MagnetV2()
		if merr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": merr.Error()})
			return
		}
		magnet := m.String()
		info, err := s.Add(c.Request.Context(), magnet)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, info)
	}
}

// StreamInfo handles GET /api/stream/info/:hash — current torrent state + progress.
func StreamInfo(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		h, ok := bindHash(c)
		if !ok {
			return
		}
		info, err := s.Get(h)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, info)
	}
}

func parseHash(s string) (metainfo.Hash, error) {
	var h metainfo.Hash
	return h, h.FromHexString(s)
}
