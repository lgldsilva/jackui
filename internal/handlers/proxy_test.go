package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/jackett"
)

func TestProxyTorrentDownload_NoURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := jackett.New("http://jackett:9117", "testkey")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/proxy", nil)

	ProxyTorrentDownload(client)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestProxyTorrentDownload_InvalidURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := jackett.New("http://jackett:9117", "testkey")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/proxy?url=://bad", nil)

	ProxyTorrentDownload(client)(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestProxyTorrentDownload_NonJackettURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := jackett.New("http://jackett:9117", "testkey")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/proxy?url=http://evil.com/torrent.torrent", nil)

	ProxyTorrentDownload(client)(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body.String())
	}
}

func TestInjectAPIKey(t *testing.T) {
	client := jackett.New("http://jackett:9117", "myapikey")

	u, _ := url.Parse("http://jackett:9117/dl?file=test.torrent")
	injectAPIKey(u, client)

	if u.Query().Get("apikey") != "myapikey" {
		t.Errorf("apikey = %q, want 'myapikey'", u.Query().Get("apikey"))
	}
}

func TestInjectAPIKey_ExistingKey(t *testing.T) {
	client := jackett.New("http://jackett:9117", "myapikey")

	u, _ := url.Parse("http://jackett:9117/dl?file=test.torrent&apikey=existing")
	injectAPIKey(u, client)

	if u.Query().Get("apikey") != "existing" {
		t.Errorf("apikey should not be overwritten, got %q", u.Query().Get("apikey"))
	}
}

func TestInjectAPIKey_EmptyAPIKey(t *testing.T) {
	client := jackett.New("http://jackett:9117", "")

	u, _ := url.Parse("http://jackett:9117/dl?file=test.torrent")
	injectAPIKey(u, client)

	if u.Query().Get("apikey") != "" {
		t.Errorf("apikey should be empty, got %q", u.Query().Get("apikey"))
	}
}

func TestProxyResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte("torrent data"))),
		Header:     http.Header{},
	}
	resp.Header.Set("Content-Type", "application/x-bittorrent")
	resp.Header.Set("Content-Disposition", "attachment; filename=test.torrent")

	proxyResponse(c, resp)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/x-bittorrent" {
		t.Errorf("Content-Type = %q, want application/x-bittorrent", w.Header().Get("Content-Type"))
	}
	if w.Body.String() != "torrent data" {
		t.Errorf("body = %q, want 'torrent data'", w.Body.String())
	}
}

func TestProxyResponse_FallbackHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte("data"))),
		Header:     http.Header{},
	}

	proxyResponse(c, resp)

	if w.Header().Get("Content-Type") != "application/x-bittorrent" {
		t.Errorf("Content-Type = %q, want application/x-bittorrent", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Content-Disposition") != `attachment; filename="download.torrent"` {
		t.Errorf("Content-Disposition = %q, want fallback", w.Header().Get("Content-Disposition"))
	}
}
