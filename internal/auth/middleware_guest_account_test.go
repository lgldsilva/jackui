package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// Guests must be able to manage their OWN account (password, email, sessions,
// media token) but never reach the admin surface under /api/auth/users.
func TestGuestRestrict_AccountSelfService(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		method string
		path   string
		block  bool
	}{
		// Self-service: open.
		{"POST", "/api/auth/password", false},
		{"POST", "/api/auth/email", false},
		{"POST", "/api/auth/sessions", false},
		{"POST", "/api/auth/sessions/revoke-others", false},
		{"POST", "/api/auth/media-token", false},
		{"DELETE", "/api/auth/sessions/abc123hash", false},

		// Admin/user management: blocked.
		{"POST", "/api/auth/users", true},
		{"DELETE", "/api/auth/users/2", true},
		{"PATCH", "/api/auth/users/2/status", true},
		{"POST", "/api/auth/users/invite", true},
		{"POST", "/api/auth/users/2/reset-password", true},
		{"DELETE", "/api/auth/users/2/sessions", true},
		{"DELETE", "/api/auth/users/2/sessions/abc", true},

		// Nothing nested deeper than one segment rides the sessions exception.
		{"DELETE", "/api/auth/sessions/abc/extra", true},
		{"DELETE", "/api/auth/sessions/", true},
		// Other mutating methods on the allowlisted paths stay blocked.
		{"PUT", "/api/auth/password", true},
		{"PATCH", "/api/auth/email", true},
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
				t.Errorf("expected pass-through (200), got %d", w.Code)
			}
		})
	}
}
