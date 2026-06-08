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
