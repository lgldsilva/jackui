package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRevealHidden(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		header string
		query  string
		expect bool
	}{
		{"header 1", "1", "", true},
		{"header true", "true", "", true},
		{"header on", "on", "", true},
		{"header 0", "0", "", false},
		{"empty", "", "", false},
		{"query 1 (SSE/media)", "", "1", true},
		{"query false", "", "false", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			url := "/"
			if tc.query != "" {
				url = "/?revealHidden=" + tc.query
			}
			req, _ := http.NewRequest("GET", url, nil)
			if tc.header != "" {
				req.Header.Set("X-JackUI-Reveal-Hidden", tc.header)
			}
			ctx.Request = req

			var got bool
			RevealHidden()(ctx)
			got = IsRevealHidden(ctx)
			if got != tc.expect {
				t.Errorf("IsRevealHidden = %v, want %v", got, tc.expect)
			}
		})
	}
}

// IsRevealHidden on a bare context (middleware not installed) must default false.
func TestIsRevealHidden_DefaultFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	if IsRevealHidden(ctx) {
		t.Error("expected false when middleware not installed")
	}
}
