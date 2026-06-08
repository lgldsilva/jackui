package local

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
)

func TestMounts_EmptyReturnsPublic(t *testing.T) {
	b := NewBrowser([]config.ExternalMount{
		{Name: "Public", Path: "/mnt/public"},
		{Name: "Restricted", Path: "/mnt/restricted", AllowedUsers: []string{"alice"}},
	})
	all := b.MountsFor("")
	if len(all) != 1 || all[0].Name != "Public" {
		t.Fatalf("expected only public mount for empty user, got %+v", all)
	}
	alice := b.MountsFor("alice")
	if len(alice) != 2 {
		t.Fatalf("expected 2 mounts for alice, got %d", len(alice))
	}
}

func TestUserCanAccess(t *testing.T) {
	b := NewBrowser([]config.ExternalMount{
		{Name: "Public", Path: "/mnt/public"},
		{Name: "Restricted", Path: "/mnt/restricted", AllowedUsers: []string{"alice"}},
	})
	if !b.UserCanAccess("bob", "Public") {
		t.Fatal("bob should access public mount")
	}
	if b.UserCanAccess("bob", "Restricted") {
		t.Fatal("bob should not access restricted mount")
	}
	if !b.UserCanAccess("alice", "Restricted") {
		t.Fatal("alice should access restricted mount")
	}
	if b.UserCanAccess("", "Restricted") {
		t.Fatal("empty user should not access restricted mount")
	}
	if b.UserCanAccess("alice", "Nonexistent") {
		t.Fatal("alice should not access nonexistent mount")
	}
}

func TestIsUserSubpath(t *testing.T) {
	b := NewBrowser([]config.ExternalMount{
		{Name: "Regular", Path: "/mnt/regular"},
		{Name: "PerUser", Path: "/mnt/peruser", UserSubpath: true},
	})
	if b.IsUserSubpath("Regular") {
		t.Fatal("Regular should not be user subpath")
	}
	if !b.IsUserSubpath("PerUser") {
		t.Fatal("PerUser should be user subpath")
	}
	if b.IsUserSubpath("Nonexistent") {
		t.Fatal("Nonexistent should not be user subpath")
	}
}

func TestUserScopedPath(t *testing.T) {
	b := NewBrowser([]config.ExternalMount{
		{Name: "Regular", Path: "/mnt/regular"},
		{Name: "PerUser", Path: "/mnt/peruser", UserSubpath: true},
	})
	if p := b.UserScopedPath("Regular", "movies/file.mkv", "alice"); p != "movies/file.mkv" {
		t.Fatalf("Regular mount: %q", p)
	}
	if p := b.UserScopedPath("PerUser", "movies/file.mkv", "alice"); p != "alice/movies/file.mkv" {
		t.Fatalf("PerUser mount: %q", p)
	}
	if p := b.UserScopedPath("PerUser", "movies/file.mkv", ""); p != "movies/file.mkv" {
		t.Fatalf("PerUser mount no user: %q", p)
	}
	if p := b.UserScopedPath("PerUser", "", "alice"); p != "alice" {
		t.Fatalf("PerUser empty path: %q", p)
	}
	if p := b.UserScopedPath("PerUser", ".", "alice"); p != "alice" {
		t.Fatalf("PerUser dot path: %q", p)
	}
}

func TestStripUserScope(t *testing.T) {
	b := NewBrowser([]config.ExternalMount{
		{Name: "PerUser", Path: "/mnt/peruser", UserSubpath: true},
		{Name: "Regular", Path: "/mnt/regular"},
	})
	// Fresh copy for each test to avoid slice mutation across cases
	makeEntries := func() []Entry {
		return []Entry{
			{Name: "file.mkv", Path: "alice/movies/file.mkv"},
			{Name: "song.mp3", Path: "alice/music/song.mp3"},
		}
	}

	result := b.StripUserScope("Regular", "alice", makeEntries())
	if result[0].Path != "alice/movies/file.mkv" {
		t.Fatalf("Regular mount should not strip: %q", result[0].Path)
	}

	result = b.StripUserScope("PerUser", "alice", makeEntries())
	if result[0].Path != "movies/file.mkv" {
		t.Fatalf("PerUser mount should strip: %q", result[0].Path)
	}

	result = b.StripUserScope("PerUser", "", makeEntries())
	if result[0].Path != "alice/movies/file.mkv" {
		t.Fatalf("Empty user should not strip: %q", result[0].Path)
	}
}

func TestEffectivePath(t *testing.T) {
	m := config.ExternalMount{Name: "Test", Path: "/base", UserSubpath: false}
	if p := effectivePath(m, "alice"); p != "/base" {
		t.Fatalf("non-subpath: %q", p)
	}
	m.UserSubpath = true
	if p := effectivePath(m, "alice"); p != "/base/alice" {
		t.Fatalf("subpath: %q", p)
	}
	if p := effectivePath(m, ""); p != "/base" {
		t.Fatalf("subpath empty user: %q", p)
	}
}

func TestResolvePathFor_UserSubpath(t *testing.T) {
	b := NewBrowser([]config.ExternalMount{
		{Name: "PerUser", Path: t.TempDir(), UserSubpath: true},
	})
	abs, err := b.ResolvePathFor("PerUser", "file.mkv", "alice")
	if err != nil {
		t.Fatalf("ResolvePathFor: %v", err)
	}
	if !strings.Contains(abs, "alice") {
		t.Fatalf("unexpected resolved path: %q", abs)
	}
	if !strings.Contains(abs, "file.mkv") {
		t.Fatalf("expected file.mkv in path: %q", abs)
	}
}

func TestHasPathTraversal_Unit(t *testing.T) {
	if hasPathTraversal("") {
		t.Error("empty should not be traversal")
	}
	if hasPathTraversal("normal/path") {
		t.Error("normal path should not be traversal")
	}
	if !hasPathTraversal("../escape") {
		t.Error("../escape should be traversal")
	}
	if !hasPathTraversal("foo/../bar") {
		t.Error("foo/../bar should be traversal")
	}
	if !hasPathTraversal("foo\\..\\bar") {
		t.Error("foo\\..\\bar should be traversal")
	}
}

func TestIsUnderDir_Unit(t *testing.T) {
	if !isUnderDir("/mnt/mount", "/mnt/mount") {
		t.Error("same dir")
	}
	if !isUnderDir("/mnt/mount/file.txt", "/mnt/mount") {
		t.Error("child")
	}
	if isUnderDir("/other/file.txt", "/mnt/mount") {
		t.Error("outside")
	}
}

func TestSymlinkOrSelf_Nonexistent(t *testing.T) {
	path := symlinkOrSelf("/nonexistent")
	if path != "/nonexistent" {
		t.Fatalf("expected /nonexistent, got %q", path)
	}
}

func TestFindMountPath(t *testing.T) {
	b := NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: "/mnt/test"},
	})
	if p := b.findMountPath("Test"); p != "/mnt/test" {
		t.Fatalf("findMountPath = %q", p)
	}
	if p := b.findMountPath("Nonexistent"); p != "" {
		t.Fatalf("expected empty, got %q", p)
	}
}

func TestWalk_MediaOnly(t *testing.T) {
	_, b := setupTestMount(t)
	entries, err := b.Walk("test", "", true)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	found := make(map[string]bool)
	for _, e := range entries {
		found[e.Name] = true
	}
	if !found["film.mkv"] {
		t.Error("expected film.mkv")
	}
	if !found["song.mp3"] {
		t.Error("expected song.mp3")
	}
	if found["notes.txt"] {
		t.Error("notes.txt should not be in media-only walk")
	}
}

func TestWalk_NotADirectory(t *testing.T) {
	_, b := setupTestMount(t)
	_, err := b.Walk("test", "movies/film.mkv", false)
	if err == nil {
		t.Fatal("expected error walking a file")
	}
}

func TestFindMount_Exists(t *testing.T) {
	b := NewBrowser([]config.ExternalMount{
		{Name: "Test", Path: "/mnt/test", UserSubpath: true, AllowedUsers: []string{"alice"}},
	})
	m, ok := b.findMount("Test")
	if !ok || m.Name != "Test" || m.Path != "/mnt/test" || !m.UserSubpath || m.AllowedUsers[0] != "alice" {
		t.Fatalf("findMount wrong: %+v", m)
	}
	_, ok = b.findMount("Nonexistent")
	if ok {
		t.Fatal("should not find nonexistent mount")
	}
}

func TestNewBrowser_Nil(t *testing.T) {
	mounts := []config.ExternalMount{{Name: "Test", Path: "/test"}}
	b := NewBrowser(mounts)
	if b == nil || len(b.Mounts()) != 1 {
		t.Fatal("expected browser with 1 mount")
	}
}

func TestList_Root_DoesNotShowHidden(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ".hidden"))
	mustWrite(t, filepath.Join(root, "visible.txt"), "content")
	b := NewBrowser([]config.ExternalMount{{Name: "test", Path: root}})
	entries, err := b.List("test", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, e := range entries {
		if e.Name == ".hidden" {
			t.Fatal("hidden directory should not appear")
		}
	}
}
