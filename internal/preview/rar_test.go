package preview

import (
	"encoding/base64"
	"errors"
	"testing"
)

// rarFixtureB64 is a real RAR5 archive (206 bytes) generated with `rar a`:
//
//	readme.txt        ("hello from rar")
//	sub/page2.jpg     ("jpegbytes")
//	sub/page10.jpg    ("jpegbytes2")
//
// Embedded as base64 so the test suite never depends on the proprietary rar
// binary being installed.
const rarFixtureB64 = "UmFyIRoHAQAzkrXlCgEFBgAFAQGAgADlXlyXKAIDC44ABI4AtIMCBkaxaIAAAQpyZWFkbWUudHh0CgMTqT8qam+BxABoZWxsbyBmcm9tIHJhcmWA9McrAgMLiQAEiQC0gwJ3ZtA4gAABDXN1Yi9wYWdlMi5qcGcKAxOpPypqb4HEAGpwZWdieXRlc9qe8bwsAgMLigAEigC0gwL0iozUgAABDnN1Yi9wYWdlMTAuanBnCgMTqT8qam+BxABqcGVnYnl0ZXMyHXdWUQMFBAA="

func rarSource(t *testing.T) Source {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(rarFixtureB64)
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return sourceFromBytes(data)
}

func TestListRar(t *testing.T) {
	entries, truncated, err := List(rarSource(t), FormatRar)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if truncated {
		t.Error("truncated = true, want false")
	}
	sizes := map[string]int64{}
	for _, e := range entries {
		sizes[e.Name] = e.Size
	}
	if len(sizes) != 3 {
		t.Fatalf("entries = %v, want 3", sizes)
	}
	if sizes["readme.txt"] != int64(len("hello from rar")) {
		t.Errorf("readme.txt size = %d", sizes["readme.txt"])
	}
}

func TestReadRarEntry(t *testing.T) {
	src := rarSource(t)
	got, err := ReadEntry(src, FormatRar, "readme.txt", MaxEntryBytes)
	if err != nil || string(got) != "hello from rar" {
		t.Errorf("ReadEntry = %q, %v", got, err)
	}
	if _, err := ReadEntry(src, FormatRar, "missing.txt", MaxEntryBytes); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("missing err = %v, want ErrEntryNotFound", err)
	}
	if _, err := ReadEntry(src, FormatRar, "readme.txt", 4); !errors.Is(err, ErrEntryTooLarge) {
		t.Errorf("cap err = %v, want ErrEntryTooLarge", err)
	}
}

// CBR is just RAR — the comic path must order pages naturally.
func TestComicPagesCBR(t *testing.T) {
	pages, err := ComicPages(rarSource(t), FormatRar)
	if err != nil {
		t.Fatalf("ComicPages: %v", err)
	}
	if len(pages) != 2 || pages[0] != "sub/page2.jpg" || pages[1] != "sub/page10.jpg" {
		t.Errorf("pages = %v, want [sub/page2.jpg sub/page10.jpg]", pages)
	}
}

func TestIsImageEntry(t *testing.T) {
	if !IsImageEntry("a.png") || !IsImageEntry("b.svg") {
		t.Error("raster/vector images should be image entries")
	}
	if IsImageEntry("a.txt") {
		t.Error("text is not an image entry")
	}
}
