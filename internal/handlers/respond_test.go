package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// newRespondCtx builds a test gin.Context whose path params and query string are
// set, so the bind* helpers can be exercised directly.
func newRespondCtx(query string, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
	c.Params = params
	return c, w
}

func TestRespondError(t *testing.T) {
	c, w := newRespondCtx("", nil)
	respondError(c, http.StatusTeapot, errors.New("boom"))
	if w.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusTeapot)
	}
	if !strings.Contains(w.Body.String(), `"error":"boom"`) {
		t.Fatalf("body = %s, want standard error shape", w.Body.String())
	}
}

func TestBindHash(t *testing.T) {
	valid := strings.Repeat("a", 40) // 40 hex chars = a valid infohash
	c, w := newRespondCtx("", gin.Params{{Key: "hash", Value: valid}})
	h, ok := bindHash(c)
	if !ok {
		t.Fatalf("bindHash ok=false for a valid hash; body=%s", w.Body.String())
	}
	if h.HexString() != valid {
		t.Fatalf("hash = %s, want %s", h.HexString(), valid)
	}

	c2, w2 := newRespondCtx("", gin.Params{{Key: "hash", Value: "not-a-hash"}})
	if _, ok := bindHash(c2); ok {
		t.Fatal("bindHash ok=true for an invalid hash")
	}
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w2.Code)
	}
}

func TestBindFileIndex(t *testing.T) {
	c, _ := newRespondCtx("", gin.Params{{Key: "file", Value: "7"}})
	idx, ok := bindFileIndex(c, "file")
	if !ok || idx != 7 {
		t.Fatalf("bindFileIndex = (%d,%v), want (7,true)", idx, ok)
	}

	c2, w2 := newRespondCtx("", gin.Params{{Key: "file", Value: "x"}})
	if _, ok := bindFileIndex(c2, "file"); ok {
		t.Fatal("bindFileIndex ok=true for a non-integer")
	}
	if w2.Code != http.StatusBadRequest || !strings.Contains(w2.Body.String(), errInvalidFileIndex) {
		t.Fatalf("status/body = %d/%s, want 400 + %q", w2.Code, w2.Body.String(), errInvalidFileIndex)
	}
}

func TestQueryBool(t *testing.T) {
	cases := map[string]bool{"1": true, "true": true, "t": true, "TRUE": true, "0": false, "false": false, "": false, "nonsense": false}
	for v, want := range cases {
		c, _ := newRespondCtx("all="+v, nil)
		if got := queryBool(c, "all"); got != want {
			t.Errorf("queryBool(all=%q) = %v, want %v", v, got, want)
		}
	}
}
