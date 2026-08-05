package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
)

func destinationServiceForTest() *DestinationService {
	return &DestinationService{
		Mounts: []config.ExternalMount{
			{Name: "Library", Path: "/mnt/library"},
			{Name: "Private", Path: "/mnt/private", UserSubpath: true},
			{Name: "Hidden", Path: "/mnt/hidden", AllowedUsers: []string{"alice"}},
		},
		Promote:   []httpshared.PromoteDest{{Name: "Promote", Path: "/mnt/promote"}},
		SharedDir: "/mnt/shared",
		ResolveUser: func(userID int) string {
			if userID == 1 {
				return "alice"
			}
			return ""
		},
	}
}

func invokeWithAuth(t *testing.T, h gin.HandlerFunc, method, path, body string, userID int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Set("auth.claims", &auth.Claims{UserID: userID, Username: "alice"})
	h(c)
	return w
}

func TestDownloadsDestinations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ds := destinationServiceForTest()

	w := invokeWithAuth(t, DownloadsDestinations(ds), http.MethodGet, "/api/downloads/destinations", "", 1)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Library") {
		t.Errorf("expected Library destination, got %s", w.Body.String())
	}
}

func TestDownloadsDestinationBrowse_InvalidBase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ds := destinationServiceForTest()

	w := invokeWithAuth(t, DownloadsDestinationBrowse(ds), http.MethodGet, "/api/downloads/dest/browse?base=/nope", "", 1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsDestinationBrowse_InvalidPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ds := destinationServiceForTest()

	w := invokeWithAuth(t, DownloadsDestinationBrowse(ds), http.MethodGet, "/api/downloads/dest/browse?base=/mnt/library&path=../escape", "", 1)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDestinationServiceFor(t *testing.T) {
	ds := destinationServiceForTest()

	// alice sees all mounts (Private resolves to /mnt/private/alice)
	got := ds.For(1)
	names := map[string]string{}
	for _, d := range got {
		names[d.Name] = d.Path
	}
	if names["Library"] != "/mnt/library" {
		t.Errorf("Library = %q", names["Library"])
	}
	if names["Private"] != "/mnt/private/alice" {
		t.Errorf("Private = %q", names["Private"])
	}
	if names["Hidden"] != "/mnt/hidden" {
		t.Errorf("Hidden = %q", names["Hidden"])
	}
	if names["Promote"] != "/mnt/promote" {
		t.Errorf("Promote = %q", names["Promote"])
	}

	// unknown user sees only public mounts
	got2 := ds.For(999)
	for _, d := range got2 {
		if d.Name == "Hidden" {
			t.Error("Hidden mount must not be visible to unknown user")
		}
		if d.Name == "Private" && d.Path == "/mnt/private/alice" {
			t.Error("Private mount must not resolve alice for unknown user")
		}
	}
}

func TestDestinationServiceResolve(t *testing.T) {
	ds := destinationServiceForTest()

	// Empty base is valid.
	base, sub, err := ds.Resolve(1, "", "")
	if err != nil || base != "" || sub != "" {
		t.Fatalf("empty base: (%q,%q,%v)", base, sub, err)
	}

	// Valid base returns canonical path.
	base, sub, err = ds.Resolve(1, "/mnt/library", "movies")
	if err != nil || base != "/mnt/library" || sub != "movies" {
		t.Fatalf("valid base: (%q,%q,%v)", base, sub, err)
	}

	// Invalid base is rejected.
	_, _, err = ds.Resolve(1, "/etc", "")
	if err == nil {
		t.Fatal("expected error for invalid base")
	}

	// Traversal subdir is rejected.
	_, _, err = ds.Resolve(1, "/mnt/library", "../escape")
	if err == nil {
		t.Fatal("expected error for traversal subdir")
	}
}

func TestMountVisibleTo(t *testing.T) {
	if !mountVisibleTo(config.ExternalMount{Name: "A"}, "anyone") {
		t.Error("no allowed users means visible to all")
	}
	if !mountVisibleTo(config.ExternalMount{Name: "A", AllowedUsers: []string{"alice"}}, "alice") {
		t.Error("alice should be visible")
	}
	if mountVisibleTo(config.ExternalMount{Name: "A", AllowedUsers: []string{"alice"}}, "bob") {
		t.Error("bob should not be visible")
	}
}
