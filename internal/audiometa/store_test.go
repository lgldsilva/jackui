package audiometa

import (
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/dbtest"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return s
}

func TestStoreSaveAndGet(t *testing.T) {
	s := newTestStore(t)

	absPath := "/music/album/song.mp3"
	modTime := time.Now().Unix()
	tags := Tags{
		Title:       "Test Song",
		Artist:      "Test Artist",
		Album:       "Test Album",
		AlbumArtist: "Test Album Artist",
		Genre:       "Rock",
		Year:        2024,
		TrackNumber: 1,
		DiscNumber:  1,
		HasCover:    true,
	}

	if err := s.Save(absPath, modTime, tags); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok := s.Get(absPath, modTime)
	if !ok {
		t.Fatal("Get: expected ok=true, got false")
	}

	if got.Title != tags.Title {
		t.Errorf("Title: got %q, want %q", got.Title, tags.Title)
	}
	if got.Artist != tags.Artist {
		t.Errorf("Artist: got %q, want %q", got.Artist, tags.Artist)
	}
	if got.Album != tags.Album {
		t.Errorf("Album: got %q, want %q", got.Album, tags.Album)
	}
	if got.AlbumArtist != tags.AlbumArtist {
		t.Errorf("AlbumArtist: got %q, want %q", got.AlbumArtist, tags.AlbumArtist)
	}
	if got.Genre != tags.Genre {
		t.Errorf("Genre: got %q, want %q", got.Genre, tags.Genre)
	}
	if got.Year != tags.Year {
		t.Errorf("Year: got %d, want %d", got.Year, tags.Year)
	}
	if got.TrackNumber != tags.TrackNumber {
		t.Errorf("TrackNumber: got %d, want %d", got.TrackNumber, tags.TrackNumber)
	}
	if got.DiscNumber != tags.DiscNumber {
		t.Errorf("DiscNumber: got %d, want %d", got.DiscNumber, tags.DiscNumber)
	}
	if got.HasCover != tags.HasCover {
		t.Errorf("HasCover: got %v, want %v", got.HasCover, tags.HasCover)
	}
}

func TestStoreGetMiss(t *testing.T) {
	s := newTestStore(t)

	absPath := "/music/unknown/song.mp3"
	modTime := time.Now().Unix()

	got, ok := s.Get(absPath, modTime)
	if ok {
		t.Fatal("Get: expected ok=false for unknown path, got true")
	}
	if got.Title != "" {
		t.Errorf("Title: expected empty, got %q", got.Title)
	}
}

func TestStoreGetStaleModtime(t *testing.T) {
	s := newTestStore(t)

	absPath := "/music/album/song.mp3"
	oldModTime := time.Now().Unix()
	newModTime := oldModTime + 100
	tags := Tags{
		Title:       "Test Song",
		Artist:      "Test Artist",
		Album:       "Test Album",
		AlbumArtist: "Test Album Artist",
		Genre:       "Rock",
		Year:        2024,
		TrackNumber: 1,
		DiscNumber:  1,
		HasCover:    true,
	}

	if err := s.Save(absPath, oldModTime, tags); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Get with newer modtime should return miss (stale detection)
	got, ok := s.Get(absPath, newModTime)
	if ok {
		t.Fatal("Get: expected ok=false for stale modtime, got true")
	}
	if got.Title != "" {
		t.Errorf("Title: expected empty, got %q", got.Title)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := newTestStore(t)

	absPath := "/music/album/song.mp3"
	modTime := time.Now().Unix()

	// Initial save
	tags1 := Tags{
		Title:       "Song Title",
		Artist:      "Original Artist",
		Album:       "Album Name",
		AlbumArtist: "Album Artist",
		Genre:       "Pop",
		Year:        2023,
		TrackNumber: 5,
		DiscNumber:  1,
		HasCover:    false,
	}

	if err := s.Save(absPath, modTime, tags1); err != nil {
		t.Fatalf("Save first: %v", err)
	}

	got, ok := s.Get(absPath, modTime)
	if !ok {
		t.Fatal("Get first: expected ok=true, got false")
	}
	if got.Artist != tags1.Artist {
		t.Errorf("Artist first: got %q, want %q", got.Artist, tags1.Artist)
	}

	// Update with new artist
	tags2 := Tags{
		Title:       "Song Title",
		Artist:      "Updated Artist",
		Album:       "Album Name",
		AlbumArtist: "Album Artist",
		Genre:       "Pop",
		Year:        2023,
		TrackNumber: 5,
		DiscNumber:  1,
		HasCover:    false,
	}

	if err := s.Save(absPath, modTime, tags2); err != nil {
		t.Fatalf("Save update: %v", err)
	}

	got, ok = s.Get(absPath, modTime)
	if !ok {
		t.Fatal("Get update: expected ok=true, got false")
	}
	if got.Artist != tags2.Artist {
		t.Errorf("Artist update: got %q, want %q", got.Artist, tags2.Artist)
	}
	if got.Title != tags2.Title {
		t.Errorf("Title: got %q, want %q", got.Title, tags2.Title)
	}
}

func TestStoreNilSafety(t *testing.T) {
	var s *Store

	absPath := "/music/test/song.mp3"
	modTime := time.Now().Unix()
	tags := Tags{
		Title:  "Test",
		Artist: "Artist",
	}

	// Get on nil store should not panic and return ok=false
	got, ok := s.Get(absPath, modTime)
	if ok {
		t.Fatal("Get nil store: expected ok=false, got true")
	}
	if got.Title != "" {
		t.Errorf("Get nil store: expected empty, got %q", got.Title)
	}

	// Save on nil store should not panic and return nil
	if err := s.Save(absPath, modTime, tags); err != nil {
		t.Errorf("Save nil store: expected nil error, got %v", err)
	}
}
