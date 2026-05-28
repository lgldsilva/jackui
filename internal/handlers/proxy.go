package handlers

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/luizg/jackui/internal/jackett"
)

var proxyHTTP = &http.Client{Timeout: 30 * time.Second}

// ProxyTorrentDownload proxies a Jackett .torrent file download through JackUI,
// re-injecting the API key server-side so the browser never needs direct Jackett access.
func ProxyTorrentDownload(client *jackett.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawURL := c.Query("url")
		if rawURL == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "url requerida"})
			return
		}

		u, err := url.Parse(rawURL)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "url inválida"})
			return
		}

		// Only allow URLs that point to the configured Jackett host to prevent SSRF.
		jackettBase, err := url.Parse(client.URL)
		if err != nil || !strings.EqualFold(u.Host, jackettBase.Host) {
			c.JSON(http.StatusForbidden, gin.H{"error": "URL não pertence ao Jackett configurado"})
			return
		}

		// Re-inject API key (it was stripped before reaching the browser).
		if client.APIKey != "" && u.Query().Get("apikey") == "" {
			q := u.Query()
			q.Set("apikey", client.APIKey)
			u.RawQuery = q.Encode()
		}

		resp, err := proxyHTTP.Get(u.String())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "falha ao contactar Jackett"})
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			c.JSON(resp.StatusCode, gin.H{"error": "Jackett retornou erro"})
			return
		}

		ct := resp.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/x-bittorrent"
		}
		cd := resp.Header.Get("Content-Disposition")
		if cd == "" {
			cd = "attachment; filename=\"download.torrent\""
		}
		c.Header("Content-Type", ct)
		c.Header("Content-Disposition", cd)
		c.Status(http.StatusOK)
		io.Copy(c.Writer, resp.Body) //nolint:errcheck
	}
}
