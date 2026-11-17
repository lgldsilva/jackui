package handlers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
)

// adminStreamPath stands in for the admin-only GETs that live under the media
// prefix (GET /api/stream/cache, GET /api/stream/settings).
const adminStreamPath = "/api/stream/cache"

func newDeviceMediaEnv(t *testing.T) *deviceEnv {
	t.Helper()
	e := newDeviceEnv(t)
	api := e.r.Group("/api", auth.Required(e.tm), auth.GuestRestrict(), auth.DeviceRestrict())
	api.POST("/auth/media-token", MediaToken(e.store, e.tm))
	api.GET("/stream/cache", auth.AdminOnly(), func(c *gin.Context) { c.Status(http.StatusOK) })
	return e
}

func (e *deviceEnv) mintMedia(t *testing.T, authz string) string {
	t.Helper()
	w, body := e.do(http.MethodPost, "/api/auth/media-token", authz, nil)
	tok, _ := body["token"].(string)
	if w.Code != http.StatusOK || tok == "" {
		t.Fatalf("media-token = %d %s", w.Code, w.Body)
	}
	return tok
}

// A paired TV must stay off the admin surface even when it presents a media
// token: both the one handed out with the device session and one it mints
// itself through /api/auth/media-token have to keep amr=device.
func TestDeviceMediaToken_KeepsDeviceRestrictionOnAdminStreamRoutes(t *testing.T) {
	e := newDeviceMediaEnv(t)
	sess := e.pair(t, e.bob, "tv-admin")
	tokens := map[string]string{
		"device session mediaToken": sess["mediaToken"].(string),
		"minted via media-token":    e.mintMedia(t, "Bearer "+sess["access"].(string)),
	}
	for name, tok := range tokens {
		claims, err := e.tm.ParseAccess(tok)
		if err != nil || claims.Scope != auth.ScopeMedia || claims.AMR != auth.AMRDevice {
			t.Errorf("%s: claims = %+v %v, want scope=media amr=device", name, claims, err)
		}
		if w, _ := e.do(http.MethodGet, adminStreamPath+"?token="+tok, "", nil); w.Code != http.StatusForbidden {
			t.Errorf("%s: GET %s = %d, want 403", name, adminStreamPath, w.Code)
		}
	}
}

func TestInteractiveAdminMediaToken_StillReachesAdminStreamRoutes(t *testing.T) {
	e := newDeviceMediaEnv(t)
	tok := e.mintMedia(t, e.bearer(t, e.bob))
	claims, err := e.tm.ParseAccess(tok)
	if err != nil || claims.Scope != auth.ScopeMedia || claims.AMR != "" {
		t.Fatalf("claims = %+v %v, want scope=media amr empty", claims, err)
	}
	if w, _ := e.do(http.MethodGet, adminStreamPath+"?token="+tok, "", nil); w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", adminStreamPath, w.Code)
	}
}
