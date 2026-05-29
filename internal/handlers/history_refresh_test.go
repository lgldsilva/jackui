package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/jackett"
)

func TestNewRefreshCache(t *testing.T) {
	rc := newRefreshCache()
	if rc == nil {
		t.Fatal("newRefreshCache() returned nil")
	}
	if rc.m == nil {
		t.Error("map is nil")
	}
}

func TestRefreshCacheGetSet(t *testing.T) {
	rc := newRefreshCache()

	_, ok := rc.get(42)
	if ok {
		t.Error("get on empty should return false")
	}

	rc.set(42, refreshCacheEntry{Seeders: 10, Leechers: 5, FetchedAt: time.Now()})
	e, ok := rc.get(42)
	if !ok {
		t.Fatal("get after set should return true")
	}
	if e.Seeders != 10 || e.Leechers != 5 {
		t.Errorf("got seeders=%d leechers=%d, want 10, 5", e.Seeders, e.Leechers)
	}
}

func TestRefreshCacheStale(t *testing.T) {
	rc := newRefreshCache()
	rc.m[1] = refreshCacheEntry{
		Seeders:   3,
		Leechers:  1,
		FetchedAt: time.Now().Add(-10 * time.Minute),
	}

	_, ok := rc.get(1)
	if ok {
		t.Error("stale entry should NOT be fresh")
	}
}

func TestFindFreshMatch_InfoHash(t *testing.T) {
	orig := &history.CachedResult{
		Result: jackett.Result{InfoHash: "abc123", Title: "Old Title"},
	}
	fresh := []jackett.Result{
		{InfoHash: "def456", Title: "Other", Seeders: 5},
		{InfoHash: "ABC123", Title: "New Title", Seeders: 10},
	}
	match := findFreshMatch(orig, fresh)
	if match == nil {
		t.Fatal("expected match by infoHash")
	}
	if match.Seeders != 10 {
		t.Errorf("seeders = %d, want 10", match.Seeders)
	}
}

func TestFindFreshMatch_ExactTitle(t *testing.T) {
	orig := &history.CachedResult{
		Result: jackett.Result{InfoHash: "", Title: "My Movie 2024"},
	}
	fresh := []jackett.Result{
		{InfoHash: "x1", Title: "Different Movie", Seeders: 1},
		{InfoHash: "x2", Title: "My Movie 2024", Seeders: 15},
	}
	match := findFreshMatch(orig, fresh)
	if match == nil {
		t.Fatal("expected match by exact title")
	}
	if match.Seeders != 15 {
		t.Errorf("seeders = %d, want 15", match.Seeders)
	}
}

func TestFindFreshMatch_CaseInsensitive(t *testing.T) {
	orig := &history.CachedResult{
		Result: jackett.Result{InfoHash: "", Title: "My Movie 2024"},
	}
	fresh := []jackett.Result{
		{InfoHash: "x1", Title: "my movie 2024", Seeders: 8},
	}
	match := findFreshMatch(orig, fresh)
	if match == nil {
		t.Fatal("expected match by case-insensitive title")
	}
	if match.Seeders != 8 {
		t.Errorf("seeders = %d, want 8", match.Seeders)
	}
}

func TestFindFreshMatch_NoMatch(t *testing.T) {
	orig := &history.CachedResult{
		Result: jackett.Result{InfoHash: "abc", Title: "Unique"},
	}
	fresh := []jackett.Result{
		{InfoHash: "xyz", Title: "Different"},
	}
	match := findFreshMatch(orig, fresh)
	if match != nil {
		t.Error("expected nil match")
	}
}

func TestRefreshQueryStr(t *testing.T) {
	q, err := refreshQueryStr(&history.CachedResult{
		Result: jackett.Result{Title: "Test Movie 2024"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q != "Test Movie 2024" {
		t.Errorf("q = %q", q)
	}

	q, err = refreshQueryStr(&history.CachedResult{Query: "saved query"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q != "saved query" {
		t.Errorf("q = %q", q)
	}

	_, err = refreshQueryStr(&history.CachedResult{})
	if err == nil {
		t.Error("expected error for empty title and query")
	}
}

func TestSeedersFromMatch(t *testing.T) {
	s, l := seedersFromMatch(nil)
	if s != 0 || l != 0 {
		t.Errorf("nil match: s=%d, l=%d", s, l)
	}

	s, l = seedersFromMatch(&jackett.Result{Seeders: 10, Leechers: 5})
	if s != 10 || l != 5 {
		t.Errorf("s=%d, l=%d", s, l)
	}
}

func TestTryServeCachedRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rc := newRefreshCache()
	rc.set(1, refreshCacheEntry{Seeders: 7, Leechers: 3, FetchedAt: time.Now()})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	ok := tryServeCachedRefresh(c, 1, rc)
	if !ok {
		t.Error("expected cached response")
	}
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "cached") {
		t.Error("response should mention cached")
	}

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest("GET", "/", nil)

	ok = tryServeCachedRefresh(c2, 999, rc)
	if ok {
		t.Error("expected no cached response for missing id")
	}
}
