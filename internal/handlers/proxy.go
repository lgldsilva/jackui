package handlers

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/jackett"
)

var proxyHTTP = &http.Client{Timeout: 30 * time.Second}

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
		if !isJackettURL(u, client) {
			c.JSON(http.StatusForbidden, gin.H{"error": "URL não pertence ao Jackett configurado"})
			return
		}
		injectAPIKey(u, client)

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
		proxyResponse(c, resp)
	}
}

func isJackettURL(u *url.URL, client *jackett.Client) bool {
	jackettBase, err := url.Parse(client.URL)
	return err == nil && strings.EqualFold(u.Host, jackettBase.Host)
}

func injectAPIKey(u *url.URL, client *jackett.Client) {
	if client.APIKey == "" || u.Query().Get("apikey") != "" {
		return
	}
	q := u.Query()
	q.Set("apikey", client.APIKey)
	u.RawQuery = q.Encode()
}

func proxyResponse(c *gin.Context, resp *http.Response) {
	ct := resp.Header.Get(ContentType)
	if ct == "" {
		ct = "application/x-bittorrent"
	}
	cd := resp.Header.Get(HeaderContentDisp)
	if cd == "" {
		cd = "attachment; filename=\"download.torrent\""
	}
	c.Header(ContentType, ct)
	c.Header(HeaderContentDisp, cd)
	c.Status(http.StatusOK)
	io.Copy(c.Writer, resp.Body) //nolint:errcheck
}
