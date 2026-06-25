package handlers

import (
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

func testDestService() *DestinationService {
	return &DestinationService{
		Mounts: []config.ExternalMount{
			{Name: "Public", Path: "/mnt/public"},
			{Name: "Alice only", Path: "/mnt/alice", AllowedUsers: []string{"alice"}},
			{Name: "Per-user", Path: "/mnt/home", UserSubpath: true},
		},
		Promote:   []PromoteDest{{Name: "Extra", Path: "/mnt/extra"}},
		SharedDir: "/shared",
		ResolveUser: func(id int) string {
			if id == 1 {
				return "alice"
			}
			return "bob"
		},
	}
}

func paths(dests []DownloadDestination) map[string]DownloadDestination {
	m := map[string]DownloadDestination{}
	for _, d := range dests {
		m[d.Path] = d
	}
	return m
}

func TestDestinationService_For_FiltersByAllowedUsers(t *testing.T) {
	ds := testDestService()
	alice := paths(ds.For(1))
	if _, ok := alice["/mnt/alice"]; !ok {
		t.Error("alice should see her restricted mount")
	}
	bob := paths(ds.For(2))
	if _, ok := bob["/mnt/alice"]; ok {
		t.Error("bob must NOT see alice's restricted mount")
	}
	// Public + promote dests visible to everyone.
	if _, ok := bob["/mnt/public"]; !ok {
		t.Error("public mount should be visible to all")
	}
	if _, ok := bob["/shared"]; !ok {
		t.Error("sharedDir (Biblioteca) should be a destination")
	}
	if _, ok := bob["/mnt/extra"]; !ok {
		t.Error("promote dir should be a destination")
	}
}

func TestDestinationService_For_UserSubpath(t *testing.T) {
	ds := testDestService()
	alice := paths(ds.For(1))
	want := filepath.Join("/mnt/home", "alice")
	d, ok := alice[want]
	if !ok {
		t.Fatalf("per-user mount should resolve to %q; got %v", want, alice)
	}
	if !d.UserSubpath {
		t.Error("per-user destination should be flagged UserSubpath")
	}
}

func TestDestinationService_Resolve(t *testing.T) {
	ds := testDestService()
	// Empty base → default (no error).
	if base, sub, err := ds.Resolve(1, "", ""); err != nil || base != "" || sub != "" {
		t.Errorf("empty base: got (%q,%q,%v)", base, sub, err)
	}
	// Valid base + subdir.
	base, sub, err := ds.Resolve(1, "/mnt/public", "movies/2026")
	if err != nil || base != "/mnt/public" || sub != filepath.FromSlash("movies/2026") {
		t.Errorf("valid: got (%q,%q,%v)", base, sub, err)
	}
	// A base alice can't see → rejected.
	if _, _, err := ds.Resolve(2, "/mnt/alice", ""); err == nil {
		t.Error("bob picking alice's mount should be rejected")
	}
	// Arbitrary path → rejected.
	if _, _, err := ds.Resolve(1, "/etc", ""); err == nil {
		t.Error("arbitrary path should be rejected")
	}
	// Traversal subdir → rejected.
	if _, _, err := ds.Resolve(1, "/mnt/public", "../escape"); err == nil {
		t.Error("traversal subdir should be rejected")
	}
}

func TestDestinationService_NilSafe(t *testing.T) {
	var ds *DestinationService
	if got := ds.For(1); len(got) != 0 {
		t.Errorf("nil service For → empty, got %v", got)
	}
	if _, _, err := ds.Resolve(1, "", ""); err != nil {
		t.Errorf("nil service empty base → no error, got %v", err)
	}
	if _, _, err := ds.Resolve(1, "/mnt/x", ""); err == nil {
		t.Error("nil service with a base → rejected")
	}
}
