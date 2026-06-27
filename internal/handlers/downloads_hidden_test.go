package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// dlHiddenFixture wires a downloads store, a streamer with a real favourites
// store, an auth store with two users, and a local browser over two mounts (one
// per-user). The mount root is a temp dir so ResolvePathFor yields real
// absolute prefixes for the path-based curtain.
type dlHiddenFixture struct {
	dl      *downloads.Store
	s       *streamer.Streamer
	fav     *streamer.FavoritesStore
	authSt  *auth.Store
	browser *local.Browser
	mount   string // absolute path of the regular "Movies" mount
	perUser string // absolute path of the per-user "Home" mount
	alice   int
	bob     int
}

func newDLHiddenFixture(t *testing.T) *dlHiddenFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	mountDir := t.TempDir()
	perUserDir := t.TempDir()

	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(filepath.Join(t.TempDir(), "fav.db"))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	s.SetFavorites(fav)

	authSt, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	t.Cleanup(func() { authSt.Close() })
	alice, err := authSt.CreateUser("alice", "pw-alice-123", auth.RoleUser)
	if err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}
	bob, err := authSt.CreateUser("bob", "pw-bob-123", auth.RoleUser)
	if err != nil {
		t.Fatalf("CreateUser bob: %v", err)
	}

	browser := local.NewBrowser([]config.ExternalMount{
		{Name: "Movies", Path: mountDir},
		{Name: "Home", Path: perUserDir, UserSubpath: true},
	})

	return &dlHiddenFixture{
		dl:      newDownloadsStore(t),
		s:       s,
		fav:     fav,
		authSt:  authSt,
		browser: browser,
		mount:   mountDir,
		perUser: perUserDir,
		alice:   alice,
		bob:     bob,
	}
}

// listFor runs DownloadsList as the given user, optionally with the curtain open.
func (f *dlHiddenFixture) listFor(t *testing.T, userID int, reveal bool) []downloads.Download {
	t.Helper()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth.claims", &auth.Claims{UserID: userID, Username: usernameOf(userID, f), Role: auth.RoleUser})
		c.Next()
	})
	router.Use(middleware.RevealHidden())
	router.GET("/api/downloads", DownloadsList(f.dl, f.s, f.browser, f.authSt, ""))

	req := httptest.NewRequest("GET", "/api/downloads", nil)
	if reveal {
		req.Header.Set("X-JackUI-Reveal-Hidden", "1")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var out []downloads.Download
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	return out
}

// listAll runs the admin DownloadsListAll (includeAll spans every user).
func (f *dlHiddenFixture) listAll(t *testing.T, reveal bool) []downloads.Download {
	t.Helper()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth.claims", &auth.Claims{UserID: 99, Username: "root", Role: auth.RoleAdmin})
		c.Next()
	})
	router.Use(middleware.RevealHidden())
	router.GET("/api/downloads/all", DownloadsListAll(f.dl, f.authSt, f.s, f.browser))

	req := httptest.NewRequest("GET", "/api/downloads/all", nil)
	if reveal {
		req.Header.Set("X-JackUI-Reveal-Hidden", "1")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	var out []downloads.Download
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	return out
}

func usernameOf(userID int, f *dlHiddenFixture) string {
	switch userID {
	case f.alice:
		return "alice"
	case f.bob:
		return "bob"
	default:
		return ""
	}
}

func containsHash(list []downloads.Download, hash string) bool {
	for _, d := range list {
		if d.InfoHash == hash {
			return true
		}
	}
	return false
}

// createDL inserts a download row owned by userID with the given infoHash/path.
func (f *dlHiddenFixture) createDL(t *testing.T, userID int, hash, name, path string) {
	t.Helper()
	if _, err := f.dl.Create(downloads.Download{
		UserID:   userID,
		InfoHash: hash,
		Magnet:   "magnet:?xt=urn:btih:" + hash,
		Name:     name,
		FilePath: path,
		FileSize: 100,
	}); err != nil {
		t.Fatalf("create download %s: %v", hash, err)
	}
}

// Favourite-hidden (by info_hash) AND library-hidden (which reuses the same
// favourite hashes) — a download whose info_hash sits in a hidden favourite
// folder must not surface, but does when the curtain opens.
func TestDownloadsList_FavouriteHiddenByHash(t *testing.T) {
	f := newDLHiddenFixture(t)
	folder, err := f.fav.CreateFolder(f.alice, "Private", nil, true) // hidden=true
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if err := f.fav.Add("Secret Movie", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "magnet:x", "manual", f.alice); err != nil {
		t.Fatalf("Add favourite: %v", err)
	}
	if err := f.fav.MoveFavoriteToFolder(f.alice, "Secret Movie", &folder.ID); err != nil {
		t.Fatalf("MoveFavoriteToFolder: %v", err)
	}
	f.createDL(t, f.alice, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Secret Movie", "/dl/secret.mkv")
	f.createDL(t, f.alice, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "Public Movie", "/dl/public.mkv")

	got := f.listFor(t, f.alice, false)
	if containsHash(got, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Errorf("favourite-hidden download leaked into list: %+v", got)
	}
	if !containsHash(got, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb") {
		t.Errorf("non-hidden download missing: %+v", got)
	}

	// Curtain open → everything shows.
	revealed := f.listFor(t, f.alice, true)
	if !containsHash(revealed, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Errorf("revealed list should contain the hidden download: %+v", revealed)
	}
}

// Local-hidden by PATH: the user hid a local folder; a background download whose
// completed file lives under that folder must not surface. THIS is the leak the
// old filter missed (it only matched by info_hash).
func TestDownloadsList_LocalHiddenByPath(t *testing.T) {
	f := newDLHiddenFixture(t)
	// alice hides Movies/secret in the local browser.
	if err := f.fav.SetLocalPathHidden(f.alice, "Movies", "secret", true); err != nil {
		t.Fatalf("SetLocalPathHidden: %v", err)
	}
	// A completed download lives under that hidden folder (no favourite at all).
	hiddenPath := filepath.Join(f.mount, "secret", "movie.mkv")
	f.createDL(t, f.alice, "cccccccccccccccccccccccccccccccccccccccc", "Secret", hiddenPath)
	// A sibling NOT under the hidden folder must survive.
	visiblePath := filepath.Join(f.mount, "other", "movie.mkv")
	f.createDL(t, f.alice, "dddddddddddddddddddddddddddddddddddddddd", "Other", visiblePath)

	got := f.listFor(t, f.alice, false)
	if containsHash(got, "cccccccccccccccccccccccccccccccccccccccc") {
		t.Errorf("local-hidden (by path) download leaked: %+v", got)
	}
	if !containsHash(got, "dddddddddddddddddddddddddddddddddddddddd") {
		t.Errorf("non-hidden sibling missing: %+v", got)
	}

	revealed := f.listFor(t, f.alice, true)
	if !containsHash(revealed, "cccccccccccccccccccccccccccccccccccccccc") {
		t.Errorf("revealed list should contain the local-hidden download: %+v", revealed)
	}
}

// A path that merely shares a prefix string ("secret-extra" vs hidden "secret")
// must NOT be dropped — the separator guard prevents the false positive.
func TestDownloadsList_LocalHiddenPrefixGuard(t *testing.T) {
	f := newDLHiddenFixture(t)
	if err := f.fav.SetLocalPathHidden(f.alice, "Movies", "secret", true); err != nil {
		t.Fatalf("SetLocalPathHidden: %v", err)
	}
	siblingPath := filepath.Join(f.mount, "secret-extra", "movie.mkv")
	f.createDL(t, f.alice, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "Sibling", siblingPath)

	got := f.listFor(t, f.alice, false)
	if !containsHash(got, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee") {
		t.Errorf("'secret-extra' wrongly matched hidden 'secret': %+v", got)
	}
}

// Per-user scope: alice hiding a path must not hide bob's identical download,
// and bob's curtain state must not affect alice.
func TestDownloadsList_HiddenScopedPerUser(t *testing.T) {
	f := newDLHiddenFixture(t)
	if err := f.fav.SetLocalPathHidden(f.alice, "Movies", "secret", true); err != nil {
		t.Fatalf("SetLocalPathHidden: %v", err)
	}
	hiddenPath := filepath.Join(f.mount, "secret", "movie.mkv")
	f.createDL(t, f.alice, "1111111111111111111111111111111111111111", "AliceSecret", hiddenPath)
	f.createDL(t, f.bob, "2222222222222222222222222222222222222222", "BobSecret", hiddenPath)

	// alice: her secret is hidden.
	aliceList := f.listFor(t, f.alice, false)
	if containsHash(aliceList, "1111111111111111111111111111111111111111") {
		t.Errorf("alice's hidden download leaked: %+v", aliceList)
	}

	// bob: same path, but HE didn't hide it → still visible.
	bobList := f.listFor(t, f.bob, false)
	if !containsHash(bobList, "2222222222222222222222222222222222222222") {
		t.Errorf("alice's hide leaked across to bob: %+v", bobList)
	}
}

// Admin "all downloads" view: must honour every user's hidden paths (resolved
// under the OWNER's scope for UserSubpath mounts), and reveal them all when the
// curtain is open.
func TestDownloadsListAll_HonoursPerUserHidden(t *testing.T) {
	f := newDLHiddenFixture(t)
	// bob hides his per-user Home/private. Resolution must use bob's scope:
	// {perUser}/bob/private.
	if err := f.fav.SetLocalPathHidden(f.bob, "Home", "private", true); err != nil {
		t.Fatalf("SetLocalPathHidden: %v", err)
	}
	bobHidden := filepath.Join(f.perUser, "bob", "private", "movie.mkv")
	f.createDL(t, f.bob, "3333333333333333333333333333333333333333", "BobPrivate", bobHidden)
	f.createDL(t, f.alice, "4444444444444444444444444444444444444444", "AlicePublic", "/dl/pub.mkv")

	all := f.listAll(t, false)
	if containsHash(all, "3333333333333333333333333333333333333333") {
		t.Errorf("admin view leaked bob's per-user hidden download: %+v", all)
	}
	if !containsHash(all, "4444444444444444444444444444444444444444") {
		t.Errorf("admin view dropped a non-hidden download: %+v", all)
	}

	revealed := f.listAll(t, true)
	if !containsHash(revealed, "3333333333333333333333333333333333333333") {
		t.Errorf("admin revealed view should contain bob's hidden download: %+v", revealed)
	}
}

// A nil browser (no mounts configured) must degrade gracefully: favourite-hash
// hiding still works; no panic from path resolution.
func TestDownloadsList_NilBrowserStillFiltersByHash(t *testing.T) {
	f := newDLHiddenFixture(t)
	folder, _ := f.fav.CreateFolder(f.alice, "Private", nil, true)
	_ = f.fav.Add("Hidden", "5555555555555555555555555555555555555555", "magnet:x", "manual", f.alice)
	_ = f.fav.MoveFavoriteToFolder(f.alice, "Hidden", &folder.ID)
	f.createDL(t, f.alice, "5555555555555555555555555555555555555555", "Hidden", "/dl/h.mkv")

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("auth.claims", &auth.Claims{UserID: f.alice, Username: "alice", Role: auth.RoleUser})
		c.Next()
	})
	router.Use(middleware.RevealHidden())
	router.GET("/api/downloads", DownloadsList(f.dl, f.s, nil, f.authSt, ""))

	req := httptest.NewRequest("GET", "/api/downloads", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var got []downloads.Download
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if containsHash(got, "5555555555555555555555555555555555555555") {
		t.Errorf("hash-hidden download leaked with nil browser: %+v", got)
	}
}
