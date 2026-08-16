package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/jackett"
)

func TestProxyResponse_WithHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	upstream := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": {"application/x-bittorrent"},
		},
		Body: http.NoBody,
	}

	proxyResponse(c, upstream)
}

func TestIsJackettURL_WithClient(t *testing.T) {
	client := jackett.New("http://jackett:9117", "")
	u, _ := url.Parse("http://jackett:9117/dl/test")
	if !isJackettURL(u, client) {
		t.Error("expected true for jackett URL")
	}
}

func TestIsJackettURL_NonJackettHost(t *testing.T) {
	client := jackett.New("http://jackett:9117", "")
	u, _ := url.Parse("http://example.com/dl/test")
	if isJackettURL(u, client) {
		t.Error("expected false for non-jackett URL")
	}
}

func TestInjectAPIKey_Noop(t *testing.T) {
	client := jackett.New("http://jackett:9117", "")
	u, _ := url.Parse("http://jackett:9117/dl/test")
	injectAPIKey(u, client)
	if u.Query().Get("apikey") != "" {
		t.Errorf("unexpected apikey: %s", u.Query().Get("apikey"))
	}
}

func TestInjectAPIKey_WithKey(t *testing.T) {
	client := jackett.New("http://jackett:9117", "mykey")
	u, _ := url.Parse("http://jackett:9117/dl/test")
	injectAPIKey(u, client)
	if u.Query().Get("apikey") != "mykey" {
		t.Errorf("expected apikey=mykey, got %s", u.Query().Get("apikey"))
	}
}

func TestIsJackettURL_RejectsUserinfo(t *testing.T) {
	client := jackett.New("http://jackett:9117", "k")
	// userinfo smuggling: host is evil.com even though the user info looks like the jackett host
	u, _ := url.Parse("http://jackett:9117@evil.com/x")
	if isJackettURL(u, client) {
		t.Fatal("URL with userinfo must be rejected")
	}
}

func TestIsJackettURL_RejectsNonHTTPScheme(t *testing.T) {
	client := jackett.New("http://jackett:9117", "k")
	for _, raw := range []string{"file://jackett:9117/x", "gopher://jackett:9117/x"} {
		u, _ := url.Parse(raw)
		if isJackettURL(u, client) {
			t.Fatalf("scheme %q must be rejected", u.Scheme)
		}
	}
}

func TestIsJackettURL_AcceptsValidJackett(t *testing.T) {
	client := jackett.New("http://jackett:9117", "k")
	u, _ := url.Parse("http://jackett:9117/dl?file=test.torrent")
	if !isJackettURL(u, client) {
		t.Fatal("valid Jackett URL was rejected")
	}
}

// sanitizeJackettURL é a barreira de request-forgery do proxy: só o retorno
// dela chega ao proxyHTTP.Get, e preserva os códigos 400/403 do fluxo antigo.
func TestSanitizeJackettURL(t *testing.T) {
	client := jackett.New("http://jackett:9117", "k")

	if u, code, err := sanitizeJackettURL("://bad", client); err == nil || code != http.StatusBadRequest || u != nil {
		t.Errorf("invalid URL: (%v, %d, %v), want (nil, 400, err)", u, code, err)
	}
	for _, raw := range []string{
		"http://evil.com/t.torrent",
		"http://jackett:9117@evil.com/t.torrent",
		"file://jackett:9117/x",
	} {
		if u, code, err := sanitizeJackettURL(raw, client); err == nil || code != http.StatusForbidden || u != nil {
			t.Errorf("sanitizeJackettURL(%q) = (%v, %d, %v), want (nil, 403, err)", raw, u, code, err)
		}
	}
	u, code, err := sanitizeJackettURL("http://jackett:9117/dl?file=t.torrent", client)
	if err != nil || code != http.StatusOK || u == nil || u.Host != "jackett:9117" {
		t.Errorf("valid URL: (%v, %d, %v), want (url, 200, nil)", u, code, err)
	}
}
