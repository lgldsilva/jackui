package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func deviceRouter(claims *Claims, mw ...gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(ctxClaimsKey, claims); c.Next() })
	r.Use(mw...)
	r.Any("/*p", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func doPath(r *gin.Engine, method, path string) int {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w.Code
}

func TestDeviceRestrict_BlocksSecurityRoutesForDeviceTokens(t *testing.T) {
	dev := deviceRouter(&Claims{UserID: 1, Role: RoleUser, AMR: AMRDevice}, DeviceRestrict())
	web := deviceRouter(&Claims{UserID: 1, Role: RoleUser}, DeviceRestrict())
	blocked := []string{
		"/api/auth/password", "/api/auth/email", "/api/auth/mfa/disable", "/api/auth/passkey",
		"/api/auth/sessions", "/api/auth/users", "/api/auth/devices/3", "/api/auth/device/approve",
	}
	for _, p := range blocked {
		if got := doPath(dev, http.MethodPost, p); got != http.StatusForbidden {
			t.Errorf("device %s = %d, want 403", p, got)
		}
		if got := doPath(web, http.MethodPost, p); got != http.StatusOK {
			t.Errorf("interactive %s = %d, want 200", p, got)
		}
	}
	for _, p := range []string{"/api/search", "/api/auth/me", "/api/auth/media-token", "/api/stream/x/0"} {
		if got := doPath(dev, http.MethodGet, p); got != http.StatusOK {
			t.Errorf("device %s = %d, want 200", p, got)
		}
	}
}

func TestAdminOnly_RejectsDeviceTokenEvenForAdmin(t *testing.T) {
	if got := doPath(deviceRouter(&Claims{Role: RoleAdmin, AMR: AMRDevice}, AdminOnly()), http.MethodGet, "/api/config"); got != http.StatusForbidden {
		t.Fatalf("admin device token = %d, want 403", got)
	}
	if got := doPath(deviceRouter(&Claims{Role: RoleAdmin}, AdminOnly()), http.MethodGet, "/api/config"); got != http.StatusOK {
		t.Fatalf("interactive admin = %d, want 200", got)
	}
}

func TestGuestRestrict_AllowsOwnDeviceManagement(t *testing.T) {
	r := deviceRouter(&Claims{UserID: 5, Role: RoleGuest}, GuestRestrict())
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/auth/device/approve", http.StatusOK},
		{http.MethodPost, "/api/auth/devices/12/pin", http.StatusOK},
		{http.MethodDelete, "/api/auth/devices/12", http.StatusOK},
		{http.MethodDelete, "/api/auth/devices/12/pin", http.StatusForbidden},
		{http.MethodPost, "/api/auth/devices/12/x/pin", http.StatusForbidden},
		{http.MethodPost, "/api/auth/devices//pin", http.StatusForbidden},
		{http.MethodDelete, "/api/auth/sessions/abc", http.StatusOK},
	}
	for _, tc := range cases {
		if got := doPath(r, tc.method, tc.path); got != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestSignDeviceAccess_CarriesAMR(t *testing.T) {
	tm := NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), 0)
	raw, _, err := tm.SignDeviceAccess(&User{ID: 3, Username: "tv", Role: RoleUser})
	if err != nil {
		t.Fatal(err)
	}
	c, err := tm.ParseAccess(raw)
	if err != nil || c.AMR != AMRDevice || c.Scope != "" || c.UserID != 3 {
		t.Fatalf("claims = %+v, %v", c, err)
	}
	raw, _, _ = tm.SignAccess(&User{ID: 3})
	if c, _ := tm.ParseAccess(raw); c.AMR != "" {
		t.Fatalf("interactive token got amr=%q", c.AMR)
	}
}

func TestDeviceKeyFromHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for hdr, want := range map[string]string{"Device jkd_abc": "jkd_abc", "Bearer jkd_abc": "", "": ""} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Request.Header.Set(HeaderAuthorization, hdr)
		if got := DeviceKeyFromHeader(c); got != want {
			t.Errorf("%q → %q, want %q", hdr, got, want)
		}
	}
}
