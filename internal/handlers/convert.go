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

const extTorrent = ".torrent"

func ConvertTorrentToMagnet() gin.HandlerFunc {
	return func(c *gin.Context) {
		torrentURL := c.Query("url")
		if torrentURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "URL requerida"})
			return
		}

		mi, name, err := downloadAndParseTorrent(torrentURL)
		if err != nil {
			c.JSON(err.Code, gin.H{"error": err.Message})
			return
		}

		infoHash := mi.HashInfoBytes().HexString()
		magnet := buildMagnetFromMetainfo(mi, infoHash, name)

		c.JSON(http.StatusOK, gin.H{
			"magnet":   magnet,
			"infoHash": infoHash,
			"name":     name,
		})
	}
}

type convertErr struct {
	Code    int
	Message string
}

func downloadAndParseTorrent(torrentURL string) (*metainfo.MetaInfo, string, *convertErr) {
	resp, err := proxyHTTP.Get(torrentURL)
	if err != nil {
		return nil, "", &convertErr{http.StatusBadGateway, fmt.Sprintf("falha ao baixar .torrent: %v", err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", &convertErr{http.StatusBadGateway, fmt.Sprintf("servidor retornou erro %d", resp.StatusCode)}
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", &convertErr{http.StatusInternalServerError, "falha ao ler bytes do torrent"}
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, "", &convertErr{http.StatusBadRequest, fmt.Sprintf("falha ao ler metainfo do torrent: %v", err)}
	}
	name := ""
	if info, err := mi.UnmarshalInfo(); err == nil && info.Name != "" {
		name = info.Name
	}
	return mi, name, nil
}

func buildMagnetFromMetainfo(mi *metainfo.MetaInfo, infoHash, name string) string {
	magnet := "magnet:?xt=urn:btih:" + infoHash
	if name != "" {
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
	return magnet
}

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
		if ensureMetainfo(c, s, h, magnet) {
			return
		}
		serveTorrentFile(c, s, h, &mi)
	}
}

func ensureMetainfo(c *gin.Context, s *streamer.Streamer, h metainfo.Hash, magnet string) bool {
	path := s.MetainfoPath(h)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return false
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if _, err := s.Add(ctx, magnet); err != nil {
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": fmt.Sprintf("tempo limite atingido aguardando metadados: %v", err)})
		return true
	}
	return false
}

func serveTorrentFile(c *gin.Context, s *streamer.Streamer, h metainfo.Hash, mi *metainfo.Magnet) {
	path := s.MetainfoPath(h)
	f, err := os.Open(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("falha ao ler arquivo .torrent gerado: %v", err)})
		return
	}
	defer func() { _ = f.Close() }()

	filename := resolveTorrentFilename(path, h, mi)
	c.Header("Content-Type", "application/x-bittorrent")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", url.PathEscape(filename)))
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, f)
}

func resolveTorrentFilename(path string, h metainfo.Hash, mi *metainfo.Magnet) string {
	if loaded, err := metainfo.LoadFromFile(path); err == nil {
		if info, err := loaded.UnmarshalInfo(); err == nil && info.Name != "" {
			return info.Name + extTorrent
		}
	} else if mi.DisplayName != "" {
		return mi.DisplayName + extTorrent
	}
	return h.HexString() + extTorrent
}
