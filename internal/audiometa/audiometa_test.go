package audiometa

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// --- minimal ID3v2.3 builder: lets the parser tests stay hermetic (no ffmpeg,
// no committed binary fixtures). dhowden/tag detects "ID3" and parses the tag
// without needing real MPEG audio frames after it. ---

func synchsafe(n int) []byte {
	return []byte{byte((n >> 21) & 0x7f), byte((n >> 14) & 0x7f), byte((n >> 7) & 0x7f), byte(n & 0x7f)}
}

func id3Frame(id string, data []byte) []byte {
	out := []byte(id)
	sz := len(data)
	out = append(out, byte(sz>>24), byte(sz>>16), byte(sz>>8), byte(sz)) // v2.3 regular 32-bit size
	out = append(out, 0x00, 0x00)                                        // flags
	return append(out, data...)
}

func id3Text(id, text string) []byte {
	return id3Frame(id, append([]byte{0x00}, []byte(text)...)) // 0x00 = ISO-8859-1
}

func id3APIC(mime string, img []byte) []byte {
	var d []byte
	d = append(d, 0x00)            // text encoding
	d = append(d, []byte(mime)...) // MIME type
	d = append(d, 0x00)            // MIME terminator
	d = append(d, 0x03)            // picture type: cover (front)
	d = append(d, 0x00)            // empty description + terminator
	d = append(d, img...)          // image bytes
	return id3Frame("APIC", d)
}

func buildID3v2(title, artist, album string, img []byte) []byte {
	frames := bytes.Join([][]byte{
		id3Text("TIT2", title),
		id3Text("TPE1", artist),
		id3Text("TALB", album),
		id3APIC("image/jpeg", img),
	}, nil)
	h := []byte("ID3")
	h = append(h, 0x03, 0x00, 0x00) // v2.3.0, no flags
	h = append(h, synchsafe(len(frames))...)
	return append(h, frames...)
}

func writeFixture(t *testing.T, name string, img []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buildID3v2("Test Title", "Test Artist", "Test Album", img), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return p
}

func TestReadTags(t *testing.T) {
	p := writeFixture(t, "song.mp3", []byte("\xff\xd8\xff\xe0tinyjpeg"))
	got, err := ReadTags(p)
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if got.Title != "Test Title" || got.Artist != "Test Artist" || got.Album != "Test Album" {
		t.Errorf("tags mismatch: %+v", got)
	}
	if !got.HasCover {
		t.Error("expected HasCover=true (APIC present)")
	}
}

func TestReadTagsErrorOnGarbage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.mp3")
	_ = os.WriteFile(p, []byte("not an audio file at all"), 0o644)
	if _, err := ReadTags(p); err == nil {
		t.Error("expected error parsing garbage, got nil")
	}
}

func TestReadTagsMissingFile(t *testing.T) {
	if _, err := ReadTags("/nonexistent/path/x.mp3"); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadCover(t *testing.T) {
	img := []byte("\xff\xd8\xff\xe0coverbytes")
	p := writeFixture(t, "song.mp3", img)
	cov, has, err := ReadCover(p)
	if err != nil {
		t.Fatalf("ReadCover: %v", err)
	}
	if !has {
		t.Fatal("expected has=true")
	}
	if cov.MIMEType != "image/jpeg" {
		t.Errorf("mime: got %q", cov.MIMEType)
	}
	if !bytes.Equal(cov.Data, img) {
		t.Errorf("cover bytes mismatch: got %q", cov.Data)
	}
}

func TestReadCoverNone(t *testing.T) {
	// Build a tag WITHOUT an APIC frame.
	dir := t.TempDir()
	p := filepath.Join(dir, "nocover.mp3")
	frames := id3Text("TIT2", "No Cover")
	h := append([]byte("ID3"), 0x03, 0x00, 0x00)
	h = append(h, synchsafe(len(frames))...)
	_ = os.WriteFile(p, append(h, frames...), 0o644)

	_, has, err := ReadCover(p)
	if err != nil {
		t.Fatalf("ReadCover: %v", err)
	}
	if has {
		t.Error("expected has=false for a file without embedded art")
	}
}

func TestStoreRoundTripAndStale(t *testing.T) {
	st, err := New(filepath.Join(t.TempDir(), "am.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	tags := Tags{Title: "T", Artist: "A", Album: "Al", Year: 2024, TrackNumber: 3, HasCover: true}
	if err := st.Save("/m/x.flac", 1000, tags); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok := st.Get("/m/x.flac", 1000)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Title != "T" || got.TrackNumber != 3 || !got.HasCover {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// A changed mtime invalidates the row (incremental cache).
	if _, ok := st.Get("/m/x.flac", 2000); ok {
		t.Error("expected miss when mtime differs (stale)")
	}
	// Re-save at the new mtime updates in place.
	if err := st.Save("/m/x.flac", 2000, tags); err != nil {
		t.Fatalf("Save (update): %v", err)
	}
	if _, ok := st.Get("/m/x.flac", 2000); !ok {
		t.Error("expected hit after re-save at new mtime")
	}
}

func TestStoreNilSafe(t *testing.T) {
	var st *Store
	if _, ok := st.Get("/x", 1); ok {
		t.Error("nil store Get should miss")
	}
	if err := st.Save("/x", 1, Tags{}); err != nil {
		t.Errorf("nil store Save should no-op, got %v", err)
	}
	st.Close() // must not panic
}
