package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
)

func TestRateLimit_AllowsWithinLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := auth.NewIPRateLimiter(5, time.Minute)
	handler := RateLimit(limiter)

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		req, _ := http.NewRequest("POST", "/", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		ctx.Request = req

		handler(ctx)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}
}

func TestRateLimit_BlocksAfterThreshold(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := auth.NewIPRateLimiter(3, time.Minute)
	handler := RateLimit(limiter)

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		req, _ := http.NewRequest("POST", "/", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		ctx.Request = req

		handler(ctx)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// 4th request: should be blocked.
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("POST", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	ctx.Request = req

	handler(ctx)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header on 429 response")
	}
}

func TestRateLimit_DifferentIPsIndependent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := auth.NewIPRateLimiter(2, time.Minute)
	handler := RateLimit(limiter)

	// Exhaust IP A.
	allowIP := func(ip string) int {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		req, _ := http.NewRequest("POST", "/", nil)
		req.RemoteAddr = ip + ":12345"
		ctx.Request = req
		handler(ctx)
		return w.Code
	}

	allowIP("127.0.0.1")
	allowIP("127.0.0.1")
	code := allowIP("127.0.0.1") // 3rd → blocked
	if code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for exhausted IP, got %d", code)
	}

	// Different IP should still be allowed.
	code = allowIP("127.0.0.1")
	if code != http.StatusOK {
		t.Fatalf("expected 200 for different IP, got %d", code)
	}
}

func TestRateLimit_NilLimiter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := RateLimit(nil)

	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		req, _ := http.NewRequest("POST", "/", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		ctx.Request = req

		handler(ctx)

		if w.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200 with nil limiter, got %d", i+1, w.Code)
		}
	}
}

func TestRateLimit_RetryAfterHeaderFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limiter := auth.NewIPRateLimiter(1, time.Minute)
	handler := RateLimit(limiter)

	// Consume the only allowed request.
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("POST", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	ctx.Request = req
	handler(ctx)

	// Block.
	w = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(w)
	req, _ = http.NewRequest("POST", "/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	ctx.Request = req
	handler(ctx)

	retryAfter := w.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header")
	}
	// Should be a positive integer.
	if retryAfter == "0" || retryAfter[0] < '0' || retryAfter[0] > '9' {
		t.Fatalf("Retry-After should be a positive integer, got %q", retryAfter)
	}
}
