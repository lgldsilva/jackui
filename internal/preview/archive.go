package preview

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// Format identifies how to decode a container by its file name.
type Format string

const (
	FormatZip     Format = "zip"
	FormatTar     Format = "tar"
	FormatTarGz   Format = "tar.gz"
	FormatRar     Format = "rar"
	FormatUnknown Format = ""
)

// DetectFormat maps a container file name to its format. CBZ/CBR are just
// zip/rar with a different extension; EPUB is zip (handled by epub.go but
// listable as zip too).
func DetectFormat(name string) Format {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".zip"), strings.HasSuffix(n, ".cbz"), strings.HasSuffix(n, ".epub"):
		return FormatZip
	case strings.HasSuffix(n, ".tar.gz"), strings.HasSuffix(n, ".tgz"):
		return FormatTarGz
	case strings.HasSuffix(n, ".tar"):
		return FormatTar
	case strings.HasSuffix(n, ".rar"), strings.HasSuffix(n, ".cbr"):
		return FormatRar
	default:
		return FormatUnknown
	}
}

// List enumerates the regular files inside the container, truncating at
// MaxListEntries. Unsafe entry names (absolute, "..") are silently skipped —
// they're hostile by definition and have no legitimate preview use.
func List(src Source, format Format) (entries []Entry, truncated bool, err error) {
	switch format {
	case FormatZip:
		return listZip(src)
	case FormatTar, FormatTarGz:
		return listTar(src, format == FormatTarGz)
	case FormatRar:
		return listRar(src)
	default:
		return nil, false, fmt.Errorf("unsupported archive format")
	}
}

// ReadEntry decompresses ONE entry (exact name match) capped at capBytes.
// Returns ErrEntryNotFound / ErrEntryTooLarge for the handler to map to
// 404/413.
func ReadEntry(src Source, format Format, name string, capBytes int64) ([]byte, error) {
	if !SafeEntryName(name) {
		return nil, ErrEntryNotFound
	}
	switch format {
	case FormatZip:
		return readZipEntry(src, name, capBytes)
	case FormatTar, FormatTarGz:
		return readTarEntry(src, format == FormatTarGz, name, capBytes)
	case FormatRar:
		return readRarEntry(src, name, capBytes)
	default:
		return nil, fmt.Errorf("unsupported archive format")
	}
}

// ComicPages lists the raster image entries of a CBZ/CBR in natural reading
// order (page2 < page10).
func ComicPages(src Source, format Format) ([]string, error) {
	entries, _, err := List(src, format)
	if err != nil {
		return nil, err
	}
	pages := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.Dir && IsComicPage(e.Name) {
			pages = append(pages, e.Name)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return NaturalLess(pages[i], pages[j]) })
	return pages, nil
}

// ─── zip ────────────────────────────────────────────────────────────────────

func listZip(src Source) ([]Entry, bool, error) {
	zr, err := zip.NewReader(src.ReaderAt, src.Size)
	if err != nil {
		return nil, false, err
	}
	entries := make([]Entry, 0, min(len(zr.File), MaxListEntries))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !SafeEntryName(f.Name) {
			continue
		}
		if len(entries) >= MaxListEntries {
			return entries, true, nil
		}
		entries = append(entries, Entry{Name: f.Name, Size: int64(f.UncompressedSize64)})
	}
	return entries, false, nil
}

func readZipEntry(src Source, name string, capBytes int64) ([]byte, error) {
	zr, err := zip.NewReader(src.ReaderAt, src.Size)
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name != name || f.FileInfo().IsDir() {
			continue
		}
		// Early reject on the declared size; readAllCapped still enforces the
		// cap on the REAL output in case the header lies.
		if int64(f.UncompressedSize64) > capBytes {
			return nil, ErrEntryTooLarge
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		return readAllCapped(rc, capBytes)
	}
	return nil, ErrEntryNotFound
}

// ─── tar / tar.gz ───────────────────────────────────────────────────────────

func openTar(src Source, gzipped bool) (*tar.Reader, io.Closer, error) {
	seq, err := src.OpenSeq()
	if err != nil {
		return nil, nil, err
	}
	var r io.Reader = seq
	if gzipped {
		gz, err := gzip.NewReader(seq)
		if err != nil {
			_ = seq.Close()
			return nil, nil, err
		}
		r = gz
	}
	return tar.NewReader(r), seq, nil
}

func listTar(src Source, gzipped bool) ([]Entry, bool, error) {
	tr, closer, err := openTar(src, gzipped)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = closer.Close() }()
	var entries []Entry
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries, false, nil
		}
		if err != nil {
			return entries, false, err
		}
		if hdr.Typeflag != tar.TypeReg || !SafeEntryName(hdr.Name) {
			continue
		}
		if len(entries) >= MaxListEntries {
			return entries, true, nil
		}
		entries = append(entries, Entry{Name: hdr.Name, Size: hdr.Size})
	}
}

func readTarEntry(src Source, gzipped bool, name string, capBytes int64) ([]byte, error) {
	tr, closer, err := openTar(src, gzipped)
	if err != nil {
		return nil, err
	}
	defer func() { _ = closer.Close() }()
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, ErrEntryNotFound
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Name != name {
			continue
		}
		if hdr.Size > capBytes {
			return nil, ErrEntryTooLarge
		}
		return readAllCapped(tr, capBytes)
	}
}

// ─── rar ────────────────────────────────────────────────────────────────────

func listRar(src Source) ([]Entry, bool, error) {
	seq, err := src.OpenSeq()
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = seq.Close() }()
	rr, err := rardecode.NewReader(seq)
	if err != nil {
		return nil, false, err
	}
	var entries []Entry
	for {
		hdr, err := rr.Next()
		if errors.Is(err, io.EOF) {
			return entries, false, nil
		}
		if err != nil {
			return entries, false, err
		}
		if hdr.IsDir || !SafeEntryName(hdr.Name) {
			continue
		}
		if len(entries) >= MaxListEntries {
			return entries, true, nil
		}
		entries = append(entries, Entry{Name: hdr.Name, Size: hdr.UnPackedSize})
	}
}

func readRarEntry(src Source, name string, capBytes int64) ([]byte, error) {
	seq, err := src.OpenSeq()
	if err != nil {
		return nil, err
	}
	defer func() { _ = seq.Close() }()
	rr, err := rardecode.NewReader(seq)
	if err != nil {
		return nil, err
	}
	for {
		hdr, err := rr.Next()
		if errors.Is(err, io.EOF) {
			return nil, ErrEntryNotFound
		}
		if err != nil {
			return nil, err
		}
		if hdr.IsDir || hdr.Name != name {
			continue
		}
		if !hdr.UnKnownSize && hdr.UnPackedSize > capBytes {
			return nil, ErrEntryTooLarge
		}
		return readAllCapped(rr, capBytes)
	}
}
