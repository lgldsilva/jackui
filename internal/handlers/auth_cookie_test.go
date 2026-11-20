package handlers

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
)

// pseudoTLSState marks a test request as TLS without a real listener.
var pseudoTLSState tls.ConnectionState

// httpOnly refresh cookie (issue #201): the web stops keeping the refresh
// token in localStorage; native clients keep the body-based flow. The cookie
// is additive — bodies are unchanged.

func cookieLogin(t *testing.T, remember bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	store := newAuthStore(t)
	createTestUser(t, store, "cookuser", "correctpass")
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, _ := json.Marshal(map[string]any{"username": "cookuser", "password": "correctpass", "remember": remember})
	c.Request = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	Login(store, tm, nil)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return w, resp
}

func cookieRefreshRequest(t *testing.T, store *auth.Store, tm *auth.TokenManager, bodyRefresh string, cookieToken string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := map[string]any{"refresh": bodyRefresh}
	b, _ := json.Marshal(body)
	c.Request = httptest.NewRequest("POST", "/api/auth/refresh", bytes.NewReader(b))
	c.Request.Header.Set("Content-Type", "application/json")
	if cookieToken != "" {
		c.Request.AddCookie(&http.Cookie{Name: RefreshCookieName, Value: cookieToken})
	}
	Refresh(store, tm)(c)
	return w
}

func cookieValue(setCookie string) string {
	for _, part := range strings.Split(setCookie, ";") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(part, RefreshCookieName+"="); ok {
			return v
		}
	}
	return ""
}

func cookieAttr(setCookie, attr string) string {
	for _, part := range strings.Split(setCookie, ";") {
		part = strings.TrimSpace(part)
		if v, ok := strings.CutPrefix(strings.ToLower(part), strings.ToLower(attr)+"="); ok {
			return v
		}
		if strings.EqualFold(part, attr) {
			return "present"
		}
	}
	return ""
}

func TestLogin_SetsHttpOnlyRefreshCookie(t *testing.T) {
	w, resp := cookieLogin(t, false)

	setCookie := w.Header().Get("Set-Cookie")
	if setCookie == "" {
		t.Fatal("login must set the refresh cookie")
	}
	if !strings.Contains(setCookie, RefreshCookieName+"=") {
		t.Fatalf("Set-Cookie = %q, want %s=…", setCookie, RefreshCookieName)
	}
	if cookieAttr(setCookie, "HttpOnly") != "present" {
		t.Errorf("cookie not HttpOnly: %q", setCookie)
	}
	if !strings.Contains(setCookie, "Path=/api/auth") {
		t.Errorf("cookie path wrong: %q", setCookie)
	}
	if got := cookieValue(setCookie); got != resp["refresh"] {
		t.Errorf("cookie value != body refresh (cookie %q)", setCookie)
	}
	if maxAge := cookieAttr(setCookie, "Max-Age"); maxAge != "86400" {
		t.Errorf("Max-Age = %q, want 86400 (normal session)", maxAge)
	}
	// Body keeps the refresh for native clients.
	if resp["refresh"] == "" {
		t.Error("body refresh must stay for native clients")
	}
}

func TestLogin_RememberExtendsCookie(t *testing.T) {
	w, _ := cookieLogin(t, true)
	maxAge := cookieAttr(w.Header().Get("Set-Cookie"), "Max-Age")
	if maxAge != "2592000" {
		t.Errorf("Max-Age = %q, want 2592000 (remember)", maxAge)
	}
}

func TestRefresh_AcceptsCookieOnlyRotation(t *testing.T) {
	_, login := cookieLogin(t, false)
	refresh, _ := login["refresh"].(string)

	store := newAuthStore(t)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	w := cookieRefreshRequest(t, store, tm, "", refresh)
	if w.Code != http.StatusOK {
		t.Fatalf("cookie-only refresh status = %d, body %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["access"] == "" || resp["refresh"] == "" || resp["refresh"] == refresh {
		t.Errorf("expected a rotated pair, got %v", resp)
	}
	// The new cookie carries the rotated token.
	newCookie := cookieValue(w.Header().Get("Set-Cookie"))
	if newCookie == "" || newCookie == refresh {
		t.Errorf("Set-Cookie must carry the rotated refresh, got %q", newCookie)
	}
}

func TestRefresh_BodyStillSupported(t *testing.T) {
	_, login := cookieLogin(t, false)
	refresh, _ := login["refresh"].(string)

	store := newAuthStore(t)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	w := cookieRefreshRequest(t, store, tm, refresh, "")
	if w.Code != http.StatusOK {
		t.Fatalf("body-based refresh status = %d, body %s", w.Code, w.Body.String())
	}
}

func TestRefresh_MissingEverywhereUnauthorized(t *testing.T) {
	store := newAuthStore(t)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	w := cookieRefreshRequest(t, store, tm, "", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestLogout_ClearsRefreshCookie(t *testing.T) {
	_, _ = cookieLogin(t, false)

	store := newAuthStore(t)
	lw := httptest.NewRecorder()
	lc, _ := gin.CreateTestContext(lw)
	lc.Request = httptest.NewRequest("POST", "/api/auth/logout", bytes.NewReader([]byte(`{}`)))
	lc.Request.Header.Set("Content-Type", "application/json")
	Logout(store)(lc)

	setCookie := lw.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, RefreshCookieName+"=;") && !strings.Contains(setCookie, RefreshCookieName+"= ") {
		t.Fatalf("logout Set-Cookie = %q, want empty %s", setCookie, RefreshCookieName)
	}
	if !strings.Contains(setCookie, "Max-Age=0") {
		t.Errorf("logout cookie must expire: %q", setCookie)
	}
}

// Secure is always true (S2092): browsers exempt Secure cookies on
// http://localhost, production terminates TLS at the proxy — so even a
// plain-HTTP request carries the Secure attribute.
func TestRefreshCookie_SecureAlwaysSet(t *testing.T) {
	store := newAuthStore(t)
	createTestUser(t, store, "secureuser", "correctpass")
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)

	for name, mutate := range map[string]func(*http.Request){
		"plain http":  func(r *http.Request) {},
		"TLS request": func(r *http.Request) { r.TLS = &pseudoTLSState },
		"https proxy": func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") },
		"http proxy":  func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "http") },
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			body, _ := json.Marshal(map[string]any{"username": "secureuser", "password": "correctpass"})
			c.Request = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			mutate(c.Request)
			Login(store, tm, nil)(c)

			if !strings.Contains(w.Header().Get("Set-Cookie"), "Secure") {
				t.Errorf("%s: cookie must always carry Secure: %q", name, w.Header().Get("Set-Cookie"))
			}
		})
	}
}
