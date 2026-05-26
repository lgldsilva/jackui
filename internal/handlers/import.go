package handlers

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/streamer"
)

// maxTorrentBytes caps the decoded .torrent payload. Real .torrent files are
// a few hundred KB even for huge multi-file packs; 8 MB is generous and stops
// a malicious client from sending a gigabyte of base64.
const maxTorrentBytes = 8 << 20

// StreamImport handles POST /api/stream/import — adds a torrent to favorites
// directly from a pasted magnet URI or an uploaded .torrent file, without
// going through search. Body: { magnet?, torrentB64?, name?, folderId? }.
//
// We resolve the info hash + display name locally (no DHT round-trip): magnets
// carry both in the URI; .torrent files are parsed and their metainfo cached
// so a later play is instant. The favorite stores a magnet so playback works
// through the existing streamer.Add path.
func StreamImport(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Magnet     string `json:"magnet"`
			TorrentB64 string `json:"torrentB64"`
			Name       string `json:"name"`
			FolderID   *int   `json:"folderId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "corpo inválido"})
			return
		}

		favs := s.Favorites()
		if favs == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "favorites store não inicializado"})
			return
		}

		var hash, name, magnet string
		var err error

		switch {
		case strings.TrimSpace(req.Magnet) != "":
			hash, name, err = s.ParseMagnet(req.Magnet)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			magnet = strings.TrimSpace(req.Magnet)
			if i := strings.Index(magnet, "magnet:"); i > 0 {
				magnet = magnet[i:]
			}
		case strings.TrimSpace(req.TorrentB64) != "":
			// Accept an optional data-URL prefix ("data:...;base64,") that the
			// browser FileReader.readAsDataURL produces.
			b64 := req.TorrentB64
			if i := strings.Index(b64, "base64,"); i >= 0 {
				b64 = b64[i+len("base64,"):]
			}
			data, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
			if derr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "base64 inválido"})
				return
			}
			if len(data) > maxTorrentBytes {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": ".torrent excede 8 MB"})
				return
			}
			hash, name, err = s.ImportTorrentBytes(data)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			magnet = "magnet:?xt=urn:btih:" + hash
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "informe um magnet ou um arquivo .torrent"})
			return
		}

		// Caller may override the auto-detected name (e.g. a cleaner title).
		if strings.TrimSpace(req.Name) != "" {
			name = strings.TrimSpace(req.Name)
		}

		userID, _, _ := auth.UserIDFromCtx(c)
		if err := favs.Add(name, hash, magnet, "import", userID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if req.FolderID != nil {
			_ = favs.MoveFavoriteToFolder(userID, name, req.FolderID)
		}

		c.JSON(http.StatusOK, gin.H{"infoHash": hash, "name": name, "magnet": magnet})
	}
}
