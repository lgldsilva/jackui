package handlers

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
)

const maxTorrentBytes = 8 << 20 // 8 MB

type importReq struct {
	Magnet     string `json:"magnet"`
	TorrentB64 string `json:"torrentB64"`
	Name       string `json:"name"`
	FolderID   *int   `json:"folderId"`
}

// StreamImport handles POST /api/stream/import — adds a torrent to favorites without
// going through search. Body: { magnet?, torrentB64?, name?, folderId? }.
//
// We resolve the info hash + display name locally (no DHT round-trip): magnets
// carry both in the URI; .torrent files are parsed and their metainfo cached
// so a later play is instant. The favorite stores a magnet so playback works
// through the existing streamer.Add path.
func StreamImport(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		streamImportHandler(c, s)
	}
}

func streamImportHandler(c *gin.Context, s *streamer.Streamer) {
	var req importReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, "corpo inválido")
		return
	}

	favs := s.Favorites()
	if favs == nil {
		httpshared.RespondErrorMessage(c, http.StatusServiceUnavailable, "favorites store não inicializado")
		return
	}

	hash, name, magnet, err := resolveImportSource(s, &req)
	if err != nil {
		httpshared.RespondError(c, http.StatusBadRequest, err)
		return
	}

	if strings.TrimSpace(req.Name) != "" {
		name = strings.TrimSpace(req.Name)
	}

	userID, _, _ := auth.UserIDFromCtx(c)
	if err := favs.Add(name, hash, magnet, "import", userID); err != nil {
		httpshared.RespondError(c, http.StatusInternalServerError, err)
		return
	}
	if req.FolderID != nil {
		_ = favs.MoveFavoriteToFolder(userID, name, req.FolderID)
	}

	// %q nos verbos: o query Go de log-injection só aceita como sanitizer o
	// operando %q ou ReplaceAll no próprio argumento — %s/%v deixavam o fluxo
	// apontado mesmo com SanitizeForLog/SanitizeIntPtr na frente.
	log.Printf("streamer: import favorite user=%d name=%q infoHash=%q folderId=%q", userID, httpshared.SanitizeForLog(name), httpshared.SanitizeForLog(hash), httpshared.SanitizeIntPtr(req.FolderID))
	c.JSON(http.StatusOK, gin.H{"infoHash": hash, "name": name, "magnet": magnet})
}

func resolveImportSource(s *streamer.Streamer, req *importReq) (hash, name, magnet string, err error) {
	if strings.TrimSpace(req.Magnet) != "" {
		return resolveMagnetImport(s, req.Magnet)
	}
	if strings.TrimSpace(req.TorrentB64) != "" {
		return resolveTorrentB64Import(s, req.TorrentB64)
	}
	return "", "", "", fmt.Errorf("informe um magnet ou um arquivo .torrent")
}

func resolveMagnetImport(s *streamer.Streamer, raw string) (hash, name, magnet string, err error) {
	hash, name, err = s.ParseMagnet(raw)
	if err != nil {
		return "", "", "", err
	}
	magnet = strings.TrimSpace(raw)
	if i := strings.Index(magnet, "magnet:"); i > 0 {
		magnet = magnet[i:]
	}
	return hash, name, magnet, nil
}

func resolveTorrentB64Import(s *streamer.Streamer, b64 string) (hash, name, magnet string, err error) {
	if i := strings.Index(b64, "base64,"); i >= 0 {
		b64 = b64[i+len("base64,"):]
	}
	data, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if derr != nil {
		return "", "", "", fmt.Errorf("base64 inválido")
	}
	if len(data) > maxTorrentBytes {
		return "", "", "", fmt.Errorf(".torrent excede 8 MB")
	}
	hash, name, err = s.ImportTorrentBytes(data)
	if err != nil {
		return "", "", "", err
	}
	return hash, name, MagnetPrefix + hash, nil
}
