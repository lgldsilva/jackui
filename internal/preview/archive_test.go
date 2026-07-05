package preview

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
)

// ─── fixtures ────────────────────────────────────────────────────────────────

func sourceFromBytes(data []byte) Source {
	return Source{
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
		OpenSeq: func() (io.ReadCloser, error) {
			return NopCloser(bytes.NewReader(data)), nil
		},
	}
}

func makeZip(t *testing.T, files map[string][]byte) Source {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return sourceFromBytes(buf.Bytes())
}

func makeTar(t *testing.T, files map[string][]byte, gzipped bool) Source {
	t.Helper()
	var buf bytes.Buffer
	var w io.Writer = &buf
	var gz *gzip.Writer
	if gzipped {
		gz = gzip.NewWriter(&buf)
		w = gz
	}
	tw := tar.NewWriter(w)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			t.Fatalf("gzip close: %v", err)
		}
	}
	return sourceFromBytes(buf.Bytes())
}

// ─── format detection ────────────────────────────────────────────────────────

func TestDetectFormat(t *testing.T) {
	cases := map[string]Format{
		"a.zip": FormatZip, "B.CBZ": FormatZip, "book.epub": FormatZip,
		"a.tar": FormatTar, "a.tar.gz": FormatTarGz, "a.tgz": FormatTarGz,
		"a.rar": FormatRar, "comic.CBR": FormatRar,
		"movie.mkv": FormatUnknown, "noext": FormatUnknown, "a.gz": FormatUnknown,
	}
	for name, want := range cases {
		if got := DetectFormat(name); got != want {
			t.Errorf("DetectFormat(%q) = %q, want %q", name, got, want)
		}
	}
}

// ─── entry name safety ───────────────────────────────────────────────────────

func TestSafeEntryName(t *testing.T) {
	safe := []string{"a.txt", "dir/sub/file.jpg", "weird name (1).png", "a..b/c.txt"}
	for _, n := range safe {
		if !SafeEntryName(n) {
			t.Errorf("SafeEntryName(%q) = false, want true", n)
		}
	}
	unsafe := []string{"", "/etc/passwd", "../evil.txt", "a/../../evil", "..", `C:\boot.ini`, "a\x00b", `\\server\share`}
	for _, n := range unsafe {
		if SafeEntryName(n) {
			t.Errorf("SafeEntryName(%q) = true, want false", n)
		}
	}
}

// ─── zip ─────────────────────────────────────────────────────────────────────

func TestListZipSkipsUnsafeAndDirs(t *testing.T) {
	src := makeZip(t, map[string][]byte{
		"readme.txt":    []byte("hello"),
		"sub/photo.jpg": []byte("jpgbytes"),
		"sub/":          nil, // explicit dir entry
		"../evil.txt":   []byte("traversal"),
	})
	entries, truncated, err := List(src, FormatZip)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if truncated {
		t.Error("truncated = true, want false")
	}
	names := map[string]int64{}
	for _, e := range entries {
		names[e.Name] = e.Size
	}
	if len(names) != 2 {
		t.Fatalf("entries = %v, want 2 safe files", names)
	}
	if names["readme.txt"] != 5 {
		t.Errorf("readme.txt size = %d, want 5", names["readme.txt"])
	}
	if _, ok := names["../evil.txt"]; ok {
		t.Error("unsafe entry leaked into listing")
	}
}

func TestReadZipEntry(t *testing.T) {
	src := makeZip(t, map[string][]byte{"notes/readme.txt": []byte("conteúdo")})
	got, err := ReadEntry(src, FormatZip, "notes/readme.txt", MaxEntryBytes)
	if err != nil {
		t.Fatalf("ReadEntry: %v", err)
	}
	if string(got) != "conteúdo" {
		t.Errorf("content = %q", got)
	}
	if _, err := ReadEntry(src, FormatZip, "missing.txt", MaxEntryBytes); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("missing entry err = %v, want ErrEntryNotFound", err)
	}
}

func TestReadEntryRejectsTraversalName(t *testing.T) {
	src := makeZip(t, map[string][]byte{"../evil.txt": []byte("boom")})
	if _, err := ReadEntry(src, FormatZip, "../evil.txt", MaxEntryBytes); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("traversal name err = %v, want ErrEntryNotFound", err)
	}
}

func TestReadZipEntryTooLarge(t *testing.T) {
	src := makeZip(t, map[string][]byte{"big.txt": bytes.Repeat([]byte("x"), 4096)})
	if _, err := ReadEntry(src, FormatZip, "big.txt", 1024); !errors.Is(err, ErrEntryTooLarge) {
		t.Errorf("err = %v, want ErrEntryTooLarge", err)
	}
}

func TestListZipTruncates(t *testing.T) {
	files := make(map[string][]byte, MaxListEntries+5)
	for i := 0; i < MaxListEntries+5; i++ {
		files[fmt.Sprintf("f%05d.txt", i)] = nil
	}
	entries, truncated, err := List(makeZip(t, files), FormatZip)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !truncated {
		t.Error("truncated = false, want true")
	}
	if len(entries) != MaxListEntries {
		t.Errorf("len(entries) = %d, want %d", len(entries), MaxListEntries)
	}
}

// ─── tar / tar.gz ────────────────────────────────────────────────────────────

func TestTarRoundTrip(t *testing.T) {
	for _, gzipped := range []bool{false, true} {
		format := FormatTar
		if gzipped {
			format = FormatTarGz
		}
		src := makeTar(t, map[string][]byte{
			"docs/a.txt":  []byte("alpha"),
			"../evil.txt": []byte("nope"),
		}, gzipped)

		entries, truncated, err := List(src, format)
		if err != nil {
			t.Fatalf("[%s] List: %v", format, err)
		}
		if truncated || len(entries) != 1 || entries[0].Name != "docs/a.txt" || entries[0].Size != 5 {
			t.Errorf("[%s] entries = %+v, truncated=%v", format, entries, truncated)
		}

		got, err := ReadEntry(src, format, "docs/a.txt", MaxEntryBytes)
		if err != nil || string(got) != "alpha" {
			t.Errorf("[%s] ReadEntry = %q, %v", format, got, err)
		}
		if _, err := ReadEntry(src, format, "docs/a.txt", 2); !errors.Is(err, ErrEntryTooLarge) {
			t.Errorf("[%s] cap err = %v, want ErrEntryTooLarge", format, err)
		}
		if _, err := ReadEntry(src, format, "nope.txt", MaxEntryBytes); !errors.Is(err, ErrEntryNotFound) {
			t.Errorf("[%s] missing err = %v, want ErrEntryNotFound", format, err)
		}
	}
}

// ─── rar ─────────────────────────────────────────────────────────────────────

// Crafting a valid RAR requires the proprietary compressor, so the positive
// path is exercised manually; here we lock in the error behavior on garbage.
func TestRarInvalidInput(t *testing.T) {
	src := sourceFromBytes([]byte("definitely not a rar file"))
	if _, _, err := List(src, FormatRar); err == nil {
		t.Error("List(garbage rar) err = nil, want error")
	}
	if _, err := ReadEntry(src, FormatRar, "x.txt", MaxEntryBytes); err == nil {
		t.Error("ReadEntry(garbage rar) err = nil, want error")
	}
}

func TestUnsupportedFormat(t *testing.T) {
	src := sourceFromBytes([]byte("x"))
	if _, _, err := List(src, FormatUnknown); err == nil {
		t.Error("List(unknown) err = nil, want error")
	}
	if _, err := ReadEntry(src, FormatUnknown, "a", 10); err == nil {
		t.Error("ReadEntry(unknown) err = nil, want error")
	}
}

// ─── comics ──────────────────────────────────────────────────────────────────

func TestComicPagesNaturalOrder(t *testing.T) {
	src := makeZip(t, map[string][]byte{
		"page10.jpg":  []byte("j"),
		"page2.jpg":   []byte("j"),
		"page1.jpg":   []byte("j"),
		"info.txt":    []byte("not a page"),
		"cover.svg":   []byte("<svg/>"), // vector excluded from comics
		"sub/p11.png": []byte("p"),
	})
	pages, err := ComicPages(src, FormatZip)
	if err != nil {
		t.Fatalf("ComicPages: %v", err)
	}
	want := []string{"page1.jpg", "page2.jpg", "page10.jpg", "sub/p11.png"}
	if len(pages) != len(want) {
		t.Fatalf("pages = %v, want %v", pages, want)
	}
	for i := range want {
		if pages[i] != want[i] {
			t.Errorf("pages[%d] = %q, want %q", i, pages[i], want[i])
		}
	}
}

// ─── content-type policy ─────────────────────────────────────────────────────

func TestEntryContentType(t *testing.T) {
	cases := []struct {
		name string
		ct   string
		ok   bool
	}{
		{"a.jpg", "image/jpeg", true},
		{"a.PNG", "image/png", true},
		{"a.avif", "image/avif", true},
		{"a.svg", "image/svg+xml", true},
		{"a.nfo", "text/plain; charset=utf-8", true},
		{"page.html", "text/plain; charset=utf-8", true}, // html shows as SOURCE
		{"README", "text/plain; charset=utf-8", true},
		{"a.exe", "", false},
		{"inner.zip", "", false}, // no nested-archive recursion
	}
	for _, tc := range cases {
		ct, ok := EntryContentType(tc.name)
		if ct != tc.ct || ok != tc.ok {
			t.Errorf("EntryContentType(%q) = (%q, %v), want (%q, %v)", tc.name, ct, ok, tc.ct, tc.ok)
		}
	}
	if strings.HasPrefix(t.Name(), "nope") {
		t.Fatal("unreachable")
	}
}

func TestNaturalLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"page2", "page10", true},
		{"page10", "page2", false},
		{"a", "b", true},
		{"Page2", "page010", true}, // case-insensitive + zero padding
		{"x", "x1", true},
		{"9999999999999999999999", "10000000000000000000000", true}, // > int64
		{"abc", "abc", false},
	}
	for _, tc := range cases {
		if got := NaturalLess(tc.a, tc.b); got != tc.want {
			t.Errorf("NaturalLess(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestSafeZipSize cobre a conversão uint64→int64 de tamanhos de header não
// confiáveis: o caminho normal e o clamp anti-overflow (#480, G115). Um header
// mentindo >MaxInt64 não pode virar negativo (burlaria os checks de tamanho).
func TestSafeZipSize(t *testing.T) {
	if got := safeZipSize(0); got != 0 {
		t.Errorf("safeZipSize(0) = %d, want 0", got)
	}
	if got := safeZipSize(1500); got != 1500 {
		t.Errorf("safeZipSize(1500) = %d, want 1500", got)
	}
	if got := safeZipSize(math.MaxInt64); got != math.MaxInt64 {
		t.Errorf("safeZipSize(MaxInt64) = %d, want MaxInt64", got)
	}
	// Acima de MaxInt64 → clamp (sem wrap negativo).
	if got := safeZipSize(math.MaxUint64); got != math.MaxInt64 {
		t.Errorf("safeZipSize(MaxUint64) = %d, want MaxInt64 (clamp)", got)
	}
	if got := safeZipSize(math.MaxInt64 + 1); got != math.MaxInt64 {
		t.Errorf("safeZipSize(MaxInt64+1) = %d, want MaxInt64 (clamp)", got)
	}
}
