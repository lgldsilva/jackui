package handlers

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/library"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

// newRecLib returns a fresh library store in a temp dir.
func newRecLib(t *testing.T) *library.Store {
	t.Helper()
	lib, err := library.New(filepath.Join(t.TempDir(), "lib.db"))
	if err != nil {
		t.Fatalf("library.New: %v", err)
	}
	t.Cleanup(func() { lib.Close() })
	return lib
}

// watch records a played title for the user (so it would normally seed recs).
func watch(t *testing.T, lib *library.Store, userID int, hash, name string) {
	t.Helper()
	if _, err := lib.Upsert(library.UpsertInput{
		UserID: userID, InfoHash: hash, Magnet: "magnet:?xt=urn:btih:" + hash, Name: name,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}

// hideFavourite favourites the title then files it under a hidden folder so its
// info_hash lands in HiddenHashSet — the same path the UI's privacy curtain uses.
func hideFavourite(t *testing.T, fav *streamer.FavoritesStore, userID int, hash, name string) {
	t.Helper()
	if err := fav.Add(name, hash, "", "", userID); err != nil {
		t.Fatalf("fav.Add: %v", err)
	}
	folder, err := fav.CreateFolder(userID, "Vault", nil, true)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if err := fav.MoveFavoriteToFolder(userID, name, &folder.ID); err != nil {
		t.Fatalf("MoveFavoriteToFolder: %v", err)
	}
}

// recStreamer builds a streamer backed by a real favourites store.
func recStreamer(t *testing.T) (*streamer.Streamer, *streamer.FavoritesStore) {
	t.Helper()
	s := streamer.NewForTesting()
	fav, err := streamer.NewFavorites(filepath.Join(t.TempDir(), "fav.db"))
	if err != nil {
		t.Fatalf("NewFavorites: %v", err)
	}
	t.Cleanup(func() { fav.Close() })
	s.SetFavorites(fav)
	return s, fav
}

func doRecs(t *testing.T, lib *library.Store, s *streamer.Streamer, tc *tmdb.Client) *httptest.ResponseRecorder {
	t.Helper()
	recCacheInvalidate(0) // anonymous user; isolate from other tests' cache
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/recommendations", nil)
	Recommendations(lib, s, tc)(c)
	return w
}

// A watched title that lives in a hidden favourite folder must NOT seed recs.
// Proof: with an apiKey-less client, ANY seed would force a 503 (Match→ErrDisabled).
// If the hidden entry were still seeded we'd get 503; getting 200 [] means it was
// filtered out before the seed loop ever touched TMDB.
func TestRecommendations_HiddenTitleDoesNotSeed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib := newRecLib(t)
	s, fav := recStreamer(t)

	watch(t, lib, 0, "hiddenhash", "Secret Movie")
	hideFavourite(t, fav, 0, "hiddenhash", "Secret Movie")

	w := doRecs(t, lib, s, &tmdb.Client{}) // empty key: a real seed would 503
	if w.Code != http.StatusOK {
		t.Fatalf("hidden-only library must not reach TMDB; status=%d body=%s", w.Code, w.Body.String())
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("expected empty recs, got %s", got)
	}
}

// Control: a NON-hidden watched title still seeds (so the test above proves the
// filter, not just an empty library). With the apiKey-less client that seed
// forces the disabled 503 — confirming the entry reached the seed loop.
func TestRecommendations_VisibleTitleStillSeeds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib := newRecLib(t)
	s, _ := recStreamer(t)

	watch(t, lib, 0, "visiblehash", "Public Movie")

	w := doRecs(t, lib, s, &tmdb.Client{})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("a visible seed with no TMDB key must 503; status=%d body=%s", w.Code, w.Body.String())
	}
}

// recHiddenHashSet ignores the reveal curtain on purpose: hidden content must
// never seed recs even when the request opened the curtain elsewhere.
func TestRecHiddenHashSet_IgnoresReveal(t *testing.T) {
	_, fav := recStreamer(t)
	s := streamer.NewForTesting()
	s.SetFavorites(fav)
	hideFavourite(t, fav, 0, "h", "T")

	set := recHiddenHashSet(s, 0)
	if !set["h"] {
		t.Fatalf("expected hidden hash in set, got %v", set)
	}
	if recHiddenHashSet(nil, 0) != nil {
		t.Error("nil streamer must yield nil set")
	}
}

func TestDismissRecommendation_PersistsAndScopesPerUser(t *testing.T) {
	lib := newRecLib(t)

	// User 1 dismisses movie:603; user 2 dismisses nothing.
	if err := lib.DismissRecommendation(1, "movie", 603); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	set1, err := lib.DismissedRecommendations(1)
	if err != nil {
		t.Fatalf("DismissedRecommendations(1): %v", err)
	}
	if !set1[library.DismissKey("movie", 603)] {
		t.Errorf("user 1 dismissal missing: %v", set1)
	}
	set2, err := lib.DismissedRecommendations(2)
	if err != nil {
		t.Fatalf("DismissedRecommendations(2): %v", err)
	}
	if len(set2) != 0 {
		t.Errorf("dismissal leaked to user 2: %v", set2)
	}
}

func TestDismissRecommendation_Idempotent(t *testing.T) {
	lib := newRecLib(t)
	for i := 0; i < 3; i++ {
		if err := lib.DismissRecommendation(1, "tv", 1399); err != nil {
			t.Fatalf("dismiss #%d: %v", i, err)
		}
	}
	set, err := lib.DismissedRecommendations(1)
	if err != nil {
		t.Fatalf("DismissedRecommendations: %v", err)
	}
	if len(set) != 1 || !set[library.DismissKey("tv", 1399)] {
		t.Errorf("re-dismiss must stay a single row: %v", set)
	}
}

func TestDismissRecommendation_Validation(t *testing.T) {
	lib := newRecLib(t)
	if err := lib.DismissRecommendation(1, "", 1); err == nil {
		t.Error("empty kind must error")
	}
	if err := lib.DismissRecommendation(1, "movie", 0); err == nil {
		t.Error("non-positive tmdbId must error")
	}
}

// The HTTP handler validates the body and rejects bad input.
func TestDismissHandler_BadRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib := newRecLib(t)

	cases := []string{
		`{"tmdbId":0,"kind":"movie"}`,   // bad id
		`{"tmdbId":5,"kind":"person"}`,  // bad kind
		`{"tmdbId":5}`,                  // missing kind
		`not json`,                      // malformed
	}
	for _, body := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/recommendations/dismiss", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		DismissRecommendation(lib)(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q → status %d, want 400", body, w.Code)
		}
	}
}

// A valid dismiss returns 200 and persists the row.
func TestDismissHandler_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lib := newRecLib(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/recommendations/dismiss",
		strings.NewReader(`{"tmdbId":603,"kind":"movie"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	DismissRecommendation(lib)(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	set, _ := lib.DismissedRecommendations(0) // anonymous → userID 0
	if !set[library.DismissKey("movie", 603)] {
		t.Errorf("handler did not persist dismissal: %v", set)
	}
}

// A nil store yields 503 (matches the package's "feature unavailable" pattern).
func TestDismissHandler_NilStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/recommendations/dismiss",
		strings.NewReader(`{"tmdbId":1,"kind":"movie"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	DismissRecommendation(nil)(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil store → status %d, want 503", w.Code)
	}
}

// End-to-end of the result filter: a dismissed recommendation is dropped by
// rankRecs (the same set the handler loads from the store).
func TestRankRecs_DropsDismissedFromStore(t *testing.T) {
	lib := newRecLib(t)
	if err := lib.DismissRecommendation(0, "movie", 10); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	dismissed, err := dismissedSet(lib, 0)
	if err != nil {
		t.Fatalf("dismissedSet: %v", err)
	}
	seeds := []seed{{title: "S", recs: []tmdb.Match{m(10, "Ignored", 9), m(11, "Kept", 1)}}}
	out := rankRecs(seeds, map[int]bool{}, dismissed)
	if len(out) != 1 || out[0].TmdbID != 11 {
		t.Fatalf("store-backed dismissal must drop movie:10; got %+v", out)
	}
}

// dismissedSet tolerates a nil store (empty set, no error).
func TestDismissedSet_NilStore(t *testing.T) {
	set, err := dismissedSet(nil, 0)
	if err != nil || len(set) != 0 {
		t.Errorf("nil store → empty set, no error; got %v err=%v", set, err)
	}
}
