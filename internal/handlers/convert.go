package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/luizg/jackui/internal/streamer"
)

// ConvertTorrentToMagnet converts a .torrent file URL into a magnet link
// by downloading the torrent file, parsing its infohash and metainfo, and building the URI.
func ConvertTorrentToMagnet() gin.HandlerFunc {
	return func(c *gin.Context) {
		torrentURL := c.Query("url")
		if torrentURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "URL requerida"})
			return
		}

		resp, err := proxyHTTP.Get(torrentURL)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("falha ao baixar .torrent: %v", err)})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("servidor retornou erro %d", resp.StatusCode)})
			return
		}

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "falha ao ler bytes do torrent"})
			return
		}

		mi, err := metainfo.Load(bytes.NewReader(data))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("falha ao ler metainfo do torrent: %v", err)})
			return
		}

		infoHash := mi.HashInfoBytes().HexString()
		magnet := "magnet:?xt=urn:btih:" + infoHash
		
		name := ""
		if info, err := mi.UnmarshalInfo(); err == nil && info.Name != "" {
			name = info.Name
			magnet += "&dn=" + url.QueryEscape(name)
		}

		for _, group := range mi.AnnounceList {
			for _, tr := range group {
				magnet += "&tr=" + url.QueryEscape(tr)
			}
		}
		if len(mi.AnnounceList) == 0 && mi.Announce != "" {
			magnet += "&tr=" + url.QueryEscape(mi.Announce)
		}

		c.JSON(http.StatusOK, gin.H{
			"magnet":   magnet,
			"infoHash": infoHash,
			"name":     name,
		})
	}
}

// ConvertMagnetToTorrent converts a magnet link (or infohash) into a bencoded .torrent file download.
// If the metainfo is not cached on disk, it registers the magnet to start resolving metadata.
func ConvertMagnetToTorrent(s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		magnet := c.Query("magnet")
		if magnet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "magnet link requerido"})
			return
		}

		mi, err := metainfo.ParseMagnetUri(magnet)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "magnet link inválido"})
			return
		}

		h := mi.InfoHash
		path := s.MetainfoPath(h)

		// Check if the cached .torrent file exists.
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// Start resolving metainfo using streamer. Blocks up to 30 seconds forGotInfo.
			ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
			defer cancel()

			_, err = s.Add(ctx, magnet)
			if err != nil {
				c.JSON(http.StatusGatewayTimeout, gin.H{"error": fmt.Sprintf("tempo limite atingido aguardando metadados: %v", err)})
				return
			}
		}

		// Read the bencoded .torrent file from cache
		f, err := os.Open(path)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("falha ao ler arquivo .torrent gerado: %v", err)})
			return
		}
		defer f.Close()

		filename := h.HexString() + ".torrent"
		if loaded, err := metainfo.LoadFromFile(path); err == nil {
			if info, err := loaded.UnmarshalInfo(); err == nil && info.Name != "" {
				filename = info.Name + ".torrent"
			}
		} else if friendlyName := mi.DisplayName; friendlyName != "" {
			filename = friendlyName + ".torrent"
		}

		c.Header("Content-Type", "application/x-bittorrent")
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", url.PathEscape(filename)))
		c.Status(http.StatusOK)
		_, _ = io.Copy(c.Writer, f)
	}
}
