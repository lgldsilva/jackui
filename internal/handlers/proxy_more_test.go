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
