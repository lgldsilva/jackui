package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIncognito_Header(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		header string
		expect bool
	}{
		{"header 1", "1", true},
		{"header true", "true", true},
		{"header yes", "yes", true},
		{"header on", "on", true},
		{"header 0", "0", false},
		{"header false", "false", false},
		{"empty header", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			req, _ := http.NewRequest("GET", "/", nil)
			if tc.header != "" {
				req.Header.Set("X-JackUI-Incognito", tc.header)
			}
			ctx.Request = req

			Incognito()(ctx)

			if got := IsIncognito(ctx); got != tc.expect {
				t.Errorf("IsIncognito = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestIncognito_Query(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		query  string
		expect bool
	}{
		{"incognito=1", "incognito=1", true},
		{"incognito=true", "incognito=true", true},
		{"incognito=0", "incognito=0", false},
		{"no query", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			url := "/"
			if tc.query != "" {
				url += "?" + tc.query
			}
			req, _ := http.NewRequest("GET", url, nil)
			ctx.Request = req

			Incognito()(ctx)

			if got := IsIncognito(ctx); got != tc.expect {
				t.Errorf("IsIncognito = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestIsIncognito_NoMiddleware(t *testing.T) {
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/", nil)
	ctx.Request = req

	if got := IsIncognito(ctx); got {
		t.Error("expected false without middleware")
	}
}
