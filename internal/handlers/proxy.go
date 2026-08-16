package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/jackett"
)

var proxyHTTP = &http.Client{Timeout: 30 * time.Second}

func ProxyTorrentDownload(client *jackett.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawURL := c.Query("url")
		if rawURL == "" {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "url requerida")
			return
		}
		// A URL vem do request: parse + validação contra o Jackett configurado
		// em uma única barreira — o retorno é o único objeto que chega ao Get.
		u, code, err := sanitizeJackettURL(rawURL, client)
		if err != nil {
			httpshared.RespondErrorMessage(c, code, err.Error())
			return
		}
		injectAPIKey(u, client)

		resp, err := proxyHTTP.Get(u.String())
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadGateway, "falha ao contactar Jackett")
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			httpshared.RespondErrorMessage(c, resp.StatusCode, "Jackett retornou erro")
			return
		}
		proxyResponse(c, resp)
	}
}

// sanitizeJackettURL parses a user-provided proxy target and validates it
// against the configured Jackett instance (host match, http(s) only, no
// userinfo smuggling). Only the returned URL is safe to fetch.
func sanitizeJackettURL(rawURL string, client *jackett.Client) (*url.URL, int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, http.StatusBadRequest, errors.New("url inválida")
	}
	if !isJackettURL(u, client) {
		return nil, http.StatusForbidden, errors.New("URL não pertence ao Jackett configurado")
	}
	return u, http.StatusOK, nil
}

func isJackettURL(u *url.URL, client *jackett.Client) bool {
	jackettBase, err := url.Parse(client.URL)
	if err != nil {
		return false
	}
	// Reject userinfo so an attacker can't smuggle a foreign host via
	// "http://jackett:9117@evil/..." (u.Host would be evil, but even a
	// matching host with credentials is never a legit Jackett request).
	if u.User != nil {
		return false
	}
	// Restrict to http(s) so a file:// or javascript: URL sharing the
	// configured host can't be proxied.
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return strings.EqualFold(u.Host, jackettBase.Host)
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
	ct := resp.Header.Get(httpshared.ContentType)
	if ct == "" {
		ct = "application/x-bittorrent"
	}
	cd := resp.Header.Get(HeaderContentDisp)
	if cd == "" {
		cd = "attachment; filename=\"download.torrent\""
	}
	c.Header(httpshared.ContentType, ct)
	c.Header(HeaderContentDisp, cd)
	c.Status(http.StatusOK)
	// #nosec G104 -- proxy stream; erro tipico = cliente desconectou
	io.Copy(c.Writer, resp.Body) //nolint:errcheck
}
