package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/middleware"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// favName is the title every scenario hides — the favourite and its watched
// library row always share it (that's the identity the curtain must match).
const favName = "Some.Show.S01E01.1080p.WEB-DL"

// hiddenIdentityEnv wires a library store + a streamer with a real favourites
// store on the shared test pool, and seeds:
//
//   - a hidden favourite folder for user 7, containing a favourite named
//     favName with the given (possibly EMPTY) info_hash;
//   - a library "continue watching" row for the SAME name under a DIFFERENT
//     hash (what actually lands in the library when the play flow resolves a
//     different release), plus a visible control row.
func hiddenIdentityEnv(t *testing.T, favHash, libHash string) (*library.Store, *streamer.Streamer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	lib, err := library.New(seededPool(t))
	if err != nil {
		t.Fatalf("library.New: %v", err)
	}
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	s.SetFavorites(fav)

	const uid = 7
	folder, err := fav.CreateFolder(uid, "Vault", nil, true)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if err := fav.Add(favName, favHash, "", "manual", uid); err != nil {
		t.Fatalf("fav.Add: %v", err)
	}
	if err := fav.MoveFavoriteToFolder(uid, favName, &folder.ID); err != nil {
		t.Fatalf("MoveFavoriteToFolder: %v", err)
	}
	// The watched rows: same title, different hash (the release the user ended
	// up streaming), and an unrelated control row.
	for _, e := range []library.UpsertInput{
		{UserID: uid, InfoHash: libHash, Magnet: "magnet:?xt=urn:btih:" + libHash, Name: favName},
		{UserID: uid, InfoHash: "1111111111111111111111111111111111111111", Magnet: "magnet:x", Name: "Control Row"},
	} {
		if _, err := lib.Upsert(e); err != nil {
			t.Fatalf("lib.Upsert: %v", err)
		}
	}
	return lib, s
}

// THE Continue-Watching regression (user report: hidden favourites kept
// appearing on the Continuar tab with the easter egg OFF): a hidden favourite
// whose info_hash never resolved (quick-favourite, empty hash) must still hide
// the library row it names — by TITLE, not only by hash. The easter egg must
// reveal it again.
func TestLibraryList_HidesHashlessFavouriteByName(t *testing.T) {
	lib, s := hiddenIdentityEnv(t, "", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	listFor := func(reveal bool) []library.Entry {
		t.Helper()
		router := gin.New()
		router.Use(func(c *gin.Context) {
			setAuth(c, 7, false)
			c.Next()
		})
		router.Use(middleware.RevealHidden())
		router.GET("/api/library", LibraryList(lib, s))
		req := httptest.NewRequest("GET", "/api/library", nil)
		if reveal {
			req.Header.Set("X-JackUI-Reveal-Hidden", "1")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
		}
		var out []library.Entry
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return out
	}

	got := listFor(false)
	for _, e := range got {
		if e.Name == favName {
			t.Fatalf("hash-less hidden favourite leaked into Continue Watching: %+v", got)
		}
	}
	if len(got) != 1 || got[0].Name != "Control Row" {
		t.Fatalf("expected only the control row, got %+v", got)
	}

	revealed := listFor(true)
	found := false
	for _, e := range revealed {
		if e.Name == favName {
			found = true
		}
	}
	if !found {
		t.Errorf("easter egg must reveal the hidden row: %+v", revealed)
	}
}

// Same regression when the favourite DID link a hash, but the library row came
// from a DIFFERENT release (hash mismatch) — the name fallback catches it.
func TestLibraryList_HidesFavouriteByNameAcrossReleases(t *testing.T) {
	lib, s := hiddenIdentityEnv(t,
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", // favourite's hash
		"cccccccccccccccccccccccccccccccccccccccc") // watched row's (different) hash

	router := gin.New()
	router.Use(func(c *gin.Context) {
		setAuth(c, 7, false)
		c.Next()
	})
	router.Use(middleware.RevealHidden())
	router.GET("/api/library", LibraryList(lib, s))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/library", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got []library.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, e := range got {
		if e.Name == favName {
			t.Fatalf("hidden favourite leaked via a different-release hash: %+v", got)
		}
	}
}

// GET /api/library/:id must honour the curtain exactly like /hash does: a
// curtain-hidden entry 404s (anti-probe), and returns with the easter egg on.
func TestLibraryGetByID_HonoursCurtain(t *testing.T) {
	lib, s := hiddenIdentityEnv(t, "", "dddddddddddddddddddddddddddddddddddddddd")

	// Find the hidden row's numeric id.
	list, err := lib.List(7, false, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var hiddenID int
	for _, e := range list {
		if e.Name == favName {
			hiddenID = e.ID
		}
	}
	if hiddenID == 0 {
		t.Fatalf("hidden library row not seeded: %+v", list)
	}

	fetch := func(reveal bool) *httptest.ResponseRecorder {
		t.Helper()
		router := gin.New()
		router.Use(func(c *gin.Context) {
			setAuth(c, 7, false)
			c.Next()
		})
		router.Use(middleware.RevealHidden())
		router.GET("/api/library/:id", LibraryGet(lib, s))
		req := httptest.NewRequest("GET", "/api/library/"+strconv.Itoa(hiddenID), nil)
		if reveal {
			req.Header.Set("X-JackUI-Reveal-Hidden", "1")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	if w := fetch(false); w.Code != http.StatusNotFound {
		t.Errorf("hidden entry by id: status = %d, want 404; body %s", w.Code, w.Body.String())
	}
	if w := fetch(true); w.Code != http.StatusOK {
		t.Errorf("revealed entry by id: status = %d, want 200; body %s", w.Code, w.Body.String())
	}
}

// dropHiddenActive: the active-swarm listing drops entries matching the
// requester's identity curtain (hash OR name); nil rows are tolerated.
func TestDropHiddenActive(t *testing.T) {
	curtain := streamer.HiddenCurtain{
		Hashes: map[string]bool{"hash1": true},
		Names:  map[string]bool{"secret show": true},
	}
	list := []*streamer.TorrentInfo{
		{InfoHash: "hash1", Name: "One"},
		{InfoHash: "hash2", Name: "Two"},
		{InfoHash: "hash3", Name: "SECRET SHOW"},
		nil,
	}
	got := dropHiddenActive(list, curtain)
	if len(got) != 1 || got[0].InfoHash != "hash2" {
		t.Errorf("dropHiddenActive = %+v", got)
	}
	if got := dropHiddenActive(list, streamer.HiddenCurtain{}); len(got) != 4 {
		t.Errorf("empty curtain must be a no-op, got %d", len(got))
	}
}

// StreamFavorite normalizes what it stores: an uppercase 40-hex arrives as
// lowercase (so the curtain's hash join can match it), and garbage becomes the
// empty string instead of a permanently unmatchable value.
func TestStreamFavorite_NormalizesInfoHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	s.SetFavorites(fav)

	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		router := gin.New()
		router.Use(func(c *gin.Context) {
			setAuth(c, 3, false)
			c.Next()
		})
		router.POST("/api/stream/favorite", StreamFavorite(s))
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/stream/favorite", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		return w
	}

	if w := post(`{"name":"Upper Movie","infoHash":"ABCDEF0123456789ABCDEF0123456789ABCDEF01"}`); w.Code != http.StatusOK {
		t.Fatalf("uppercase hash POST: status = %d, body %s", w.Code, w.Body.String())
	}
	if w := post(`{"name":"Junk Movie","infoHash":"not-a-hash"}`); w.Code != http.StatusOK {
		t.Fatalf("garbage hash POST: status = %d, body %s", w.Code, w.Body.String())
	}

	favs, err := fav.List(3, false, true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byName := map[string]streamer.Favorite{}
	for _, f := range favs {
		byName[f.Name] = f
	}
	if got := byName["Upper Movie"].InfoHash; got != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("uppercase hash not stored normalized: %q", got)
	}
	if got := byName["Junk Movie"].InfoHash; got != "" {
		t.Errorf("garbage hash must be dropped, got %q", got)
	}
}

// buildEnricher: with the curtain closed a hidden favourite's hash must NOT
// mark search/history rows isFavorited (state leak); the easter egg restores it.
func TestBuildEnricher_SubtractsHiddenFavorites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fav, err := streamer.NewFavorites(seededPool(t))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	const h = "abcdef0123456789abcdef0123456789abcdef01"
	if err := fav.Add("Hidden Heart", h, "", "manual", 5); err != nil {
		t.Fatalf("Add: %v", err)
	}
	folder, err := fav.CreateFolder(5, "Vault", nil, true)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if err := fav.MoveFavoriteToFolder(5, "Hidden Heart", &folder.ID); err != nil {
		t.Fatalf("MoveFavoriteToFolder: %v", err)
	}

	closed := buildEnricher(fav, nil, 5, false, false)
	if closed.favHashes[h] {
		t.Error("curtain closed: hidden favourite must not flag isFavorited")
	}
	open := buildEnricher(fav, nil, 5, false, true)
	if !open.favHashes[h] {
		t.Error("curtain open: favourite should flag isFavorited again")
	}
}
