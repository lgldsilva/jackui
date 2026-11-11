package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/library"
)

const ctlHash = "0123456789abcdef0123456789abcdef01234567"

type fakeOwners struct {
	ids map[int]bool
	err error
}

func (f fakeOwners) HashOwnerIDs(string) (map[int]bool, error) { return f.ids, f.err }

type fakeLib map[int]bool

func (f fakeLib) GetByHash(uid int, _ string) (*library.Entry, error) {
	if f[uid] {
		return &library.Entry{UserID: uid}, nil
	}
	return nil, nil
}

type fakeViewers map[string]bool

func (f fakeViewers) ViewedBy(_ metainfo.Hash, key string) bool { return f[key] }

// ctlDo runs POST /stream/:hash/pause through the guard as the given claims
// (nil = unauthenticated) and returns the status; 204 means the guard passed.
func ctlDo(t *testing.T, a TorrentControlAuthz, claims *auth.Claims, hash string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if claims != nil {
			c.Set("auth.claims", claims)
		}
	})
	r.POST("/stream/:hash/pause", TorrentControlGuard(a), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/stream/"+hash+"/pause", nil))
	return w.Code
}

func user(id int) *auth.Claims { return &auth.Claims{UserID: id, Role: auth.RoleUser} }

// Regression (#200): any non-guest could pause/reprioritize another user's
// queued download through the per-hash controls.
func TestTorrentControlGuard_ForeignDownloadDenied(t *testing.T) {
	a := TorrentControlAuthz{Downloads: fakeOwners{ids: map[int]bool{1: true}}, Viewers: fakeViewers{"u2": true}, Library: fakeLib{2: true}}
	if code := ctlDo(t, a, user(2), ctlHash); code != http.StatusForbidden {
		t.Fatalf("foreign user = %d, want 403 (watching/library must not override a download owner)", code)
	}
	if code := ctlDo(t, a, user(1), ctlHash); code != http.StatusNoContent {
		t.Fatalf("owner = %d, want pass", code)
	}
	admin := &auth.Claims{UserID: 9, Role: auth.RoleAdmin}
	if code := ctlDo(t, a, admin, ctlHash); code != http.StatusNoContent {
		t.Fatalf("admin = %d, want pass", code)
	}
	device := &auth.Claims{UserID: 9, Role: auth.RoleAdmin, AMR: auth.AMRDevice}
	if code := ctlDo(t, a, device, ctlHash); code != http.StatusForbidden {
		t.Fatalf("admin via device key = %d, want 403", code)
	}
}

func TestTorrentControlGuard_StreamOnly(t *testing.T) {
	a := TorrentControlAuthz{Downloads: fakeOwners{ids: map[int]bool{}}, Viewers: fakeViewers{"u2": true}, Library: fakeLib{3: true}}
	cases := map[int]int{2: http.StatusNoContent, 3: http.StatusNoContent, 4: http.StatusForbidden}
	for uid, want := range cases {
		if code := ctlDo(t, a, user(uid), ctlHash); code != want {
			t.Fatalf("user %d = %d, want %d", uid, code, want)
		}
	}
	// No library wired: only viewers count.
	a.Library = nil
	if code := ctlDo(t, a, user(3), ctlHash); code != http.StatusForbidden {
		t.Fatalf("library-less user 3 = %d, want 403", code)
	}
}

func TestTorrentControlGuard_EdgeCases(t *testing.T) {
	a := TorrentControlAuthz{Downloads: fakeOwners{ids: map[int]bool{1: true}}}
	if code := ctlDo(t, a, nil, ctlHash); code != http.StatusForbidden {
		t.Fatalf("unauthenticated = %d, want 403", code)
	}
	// Malformed hash is left to the handler (which answers 400 itself).
	if code := ctlDo(t, a, user(2), "nothex"); code != http.StatusNoContent {
		t.Fatalf("bad hash = %d, want guard passthrough", code)
	}
	a.Downloads = fakeOwners{err: errors.New("db down")}
	if code := ctlDo(t, a, user(1), ctlHash); code != http.StatusInternalServerError {
		t.Fatalf("store error = %d, want 500", code)
	}
}
