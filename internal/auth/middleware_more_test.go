package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestExtractToken_Bearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set(HeaderAuthorization, BearerPrefix+"mytoken")

	tok := extractToken(c)
	if tok != "mytoken" {
		t.Fatalf("token = %q", tok)
	}
}

func TestExtractToken_QueryParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/abc?token=streamtoken", nil)

	tok := extractToken(c)
	if tok != "streamtoken" {
		t.Fatalf("token = %q", tok)
	}
}

func TestExtractToken_QueryParamNonMedia(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/config?token=sometoken", nil)

	tok := extractToken(c)
	if tok != "" {
		t.Fatalf("expected empty for non-media path, got %q", tok)
	}
}

func TestExtractToken_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	tok := extractToken(c)
	if tok != "" {
		t.Fatalf("expected empty, got %q", tok)
	}
}

func TestRequired_MissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-xxx"), time.Hour)
	Required(tm)(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRequired_InvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set(HeaderAuthorization, BearerPrefix+"invalidtoken")

	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-xxx"), time.Hour)
	Required(tm)(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRequired_ValidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/search", nil)

	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-xxx"), time.Hour)
	u := &User{ID: 1, Username: "test", Role: RoleUser}
	tok, _, _ := tm.SignAccess(u)
	c.Request.Header.Set(HeaderAuthorization, BearerPrefix+tok)

	Required(tm)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	claims, ok := ClaimsFromCtx(c)
	if !ok || claims.UserID != 1 {
		t.Fatal("expected claims on context")
	}
}

func TestRequired_MediaTokenOnSensitivePath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/config", nil)

	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-xxx"), time.Hour)
	u := &User{ID: 1, Username: "test", Role: RoleUser}
	tok, _, _ := tm.SignMedia(u)
	c.Request.Header.Set(HeaderAuthorization, BearerPrefix+tok)

	Required(tm)(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for media token on sensitive path, got %d", w.Code)
	}
}

func TestRequired_MediaTokenOnMediaPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/stream/abc/0", nil)

	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-xxx"), time.Hour)
	u := &User{ID: 1, Username: "test", Role: RoleUser}
	tok, _, _ := tm.SignMedia(u)
	c.Request.Header.Set(HeaderAuthorization, BearerPrefix+tok)

	Required(tm)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for media token on media path, got %d", w.Code)
	}
}

func TestOptional_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-xxx"), time.Hour)
	Optional(tm)(c)

	_, ok := ClaimsFromCtx(c)
	if ok {
		t.Fatal("expected no claims")
	}
}

func TestOptional_ValidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-xxx"), time.Hour)
	u := &User{ID: 42, Username: "bob", Role: RoleAdmin}
	tok, _, _ := tm.SignAccess(u)
	c.Request.Header.Set(HeaderAuthorization, BearerPrefix+tok)

	Optional(tm)(c)

	claims, ok := ClaimsFromCtx(c)
	if !ok || claims.UserID != 42 || claims.Role != RoleAdmin {
		t.Fatal("expected admin claims")
	}
}

func TestOptional_MediaTokenOnNonMedia(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/config", nil)

	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-xxx"), time.Hour)
	u := &User{ID: 1, Username: "test", Role: RoleUser}
	tok, _, _ := tm.SignMedia(u)
	c.Request.Header.Set(HeaderAuthorization, BearerPrefix+tok)

	Optional(tm)(c)

	_, ok := ClaimsFromCtx(c)
	if ok {
		t.Fatal("expected no claims for media token on non-media path")
	}
}

func TestAdminOnly_NotAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(ctxClaimsKey, &Claims{UserID: 1, Username: "user", Role: RoleUser})

	AdminOnly()(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestAdminOnly_IsAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(ctxClaimsKey, &Claims{UserID: 1, Username: "admin", Role: RoleAdmin})

	AdminOnly()(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAdminOnly_NoClaims(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	AdminOnly()(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestGuestRestrict_BlocksMutationForGuests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		method string
		path   string
		block  bool
	}{
		{"POST", "/api/library/1", true},
		{"DELETE", "/api/library/1", true},
		{"PUT", "/api/library/1", true},
		{"PATCH", "/api/library/1", true},
		{"GET", "/api/library/1", false},
		{"POST", "/api/stream/abc/0", false},
		{"POST", "/api/local/file", true},
	}

	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tc.method, tc.path, nil)
			c.Set(ctxClaimsKey, &Claims{UserID: 1, Username: "guest", Role: RoleGuest})

			GuestRestrict()(c)

			if tc.block && w.Code != http.StatusForbidden {
				t.Errorf("expected 403, got %d", w.Code)
			}
			if !tc.block && w.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", w.Code)
			}
		})
	}
}

func TestGuestRestrict_NoClaims(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/library/1", nil)

	GuestRestrict()(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 without claims, got %d", w.Code)
	}
}

func TestGuestRestrict_NonGuest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/library/1", nil)
	c.Set(ctxClaimsKey, &Claims{UserID: 1, Username: "user", Role: RoleUser})

	GuestRestrict()(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-guest, got %d", w.Code)
	}
}

func TestClaimsFromCtx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	_, ok := ClaimsFromCtx(c)
	if ok {
		t.Fatal("expected !ok without claims")
	}

	c.Set(ctxClaimsKey, &Claims{UserID: 5, Username: "alice", Role: RoleAdmin})
	claims, ok := ClaimsFromCtx(c)
	if !ok || claims.UserID != 5 {
		t.Fatal("expected claims")
	}

	c.Set(ctxClaimsKey, "not a claims pointer")
	_, ok = ClaimsFromCtx(c)
	if ok {
		t.Fatal("expected !ok for wrong type")
	}
}

func TestUserIDFromCtx(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	userID, isAdmin, isAuth := UserIDFromCtx(c)
	if isAuth || userID != 0 {
		t.Fatal("expected not authenticated")
	}

	c.Set(ctxClaimsKey, &Claims{UserID: 10, Username: "admin", Role: RoleAdmin})
	userID, isAdmin, isAuth = UserIDFromCtx(c)
	if !isAuth || !isAdmin || userID != 10 {
		t.Fatal("expected authenticated admin")
	}

	c.Set(ctxClaimsKey, &Claims{UserID: 20, Username: "user", Role: RoleUser})
	_, isAdmin, _ = UserIDFromCtx(c)
	if isAdmin {
		t.Fatal("expected non-admin")
	}
}

func TestSignMedia(t *testing.T) {
	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-mmm"), time.Hour)
	u := &User{ID: 7, Username: "mediauser", Role: RoleUser}

	tok, exp, err := tm.SignMedia(u)
	if err != nil {
		t.Fatalf("SignMedia: %v", err)
	}
	if tok == "" {
		t.Fatal("expected non-empty token")
	}
	if time.Until(exp) <= 0 {
		t.Fatal("expected future expiration")
	}

	claims, err := tm.ParseAccess(tok)
	if err != nil {
		t.Fatalf("ParseAccess: %v", err)
	}
	if claims.Scope != ScopeMedia {
		t.Fatalf("scope = %q, want media", claims.Scope)
	}
	if claims.UserID != 7 {
		t.Fatalf("userID = %d", claims.UserID)
	}
}

func TestSetMediaTTL(t *testing.T) {
	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-mmm"), time.Hour)
	if tm.mediaTTL != 6*time.Hour {
		t.Fatalf("default mediaTTL = %v", tm.mediaTTL)
	}
	tm.SetMediaTTL(0)
	if tm.mediaTTL != 6*time.Hour {
		t.Fatalf("expected unchanged, got %v", tm.mediaTTL)
	}
	tm.SetMediaTTL(12 * time.Hour)
	if tm.mediaTTL != 12*time.Hour {
		t.Fatalf("mediaTTL = %v", tm.mediaTTL)
	}
}

func TestNewTokenManager_DefaultAccessTTL(t *testing.T) {
	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-abc"), 0)
	if tm.accessTTL != 15*time.Minute {
		t.Fatalf("accessTTL = %v", tm.accessTTL)
	}
}

func TestSignAccess_NilUser(t *testing.T) {
	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-abc"), time.Hour)
	u := &User{}
	tok, _, err := tm.SignAccess(u)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}
	if tok == "" {
		t.Fatal("expected non-empty token")
	}
}

func TestParseAccess_InvalidSignature(t *testing.T) {
	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-abc"), time.Hour)
	_, err := tm.ParseAccess("eyJhbGciOiJSUzI1NiJ9.eyJkYXRhIjoidGVzdCJ9.signature")
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
}

func TestGuestRestrict_MediaPathExceptions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mediaPaths := []string{"/api/stream/abc"}
	for _, path := range mediaPaths {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", path, nil)
			c.Set(ctxClaimsKey, &Claims{UserID: 1, Username: "guest", Role: RoleGuest})

			GuestRestrict()(c)

			if w.Code != http.StatusOK {
				t.Errorf("expected 200 for %s, got %d", path, w.Code)
			}
		})
	}
}

func TestSignAccess_SubjectIsUserID(t *testing.T) {
	tm := NewTokenManager([]byte("secret-key-at-least-32-bytes-long-xxx"), time.Hour)
	u := &User{ID: 42, Username: "alice", Role: RoleAdmin}
	tok, _, _ := tm.SignAccess(u)
	claims, _ := tm.ParseAccess(tok)
	if !strings.Contains(claims.Subject, "42") {
		t.Fatalf("subject = %q, want containing 42", claims.Subject)
	}
}
