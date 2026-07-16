package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lh "github.com/lgldsilva/jackui/internal/handlers/local"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/preview"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// Universal viewer endpoints — GET /api/preview/* — let the frontend inspect
// container files (zip/tar/rar archives, CBZ/CBR comics, EPUB books) that live
// either inside an active torrent (?hash=&idx=) or on a local mount
// (?mount=&path=). Bytes come from the existing readers (torrent FileReader /
// os.File), so a torrent archive previews before its download completes: the
// seek-driven reads pull just the pieces they need (zip central directory at
// EOF, like MP4 moov).
//
// /api/preview/ is whitelisted in auth.isMediaPath because comic pages, EPUB
// chapter iframes and inner images load via <img>/<iframe>, which can't send
// an Authorization header — they ride ?token= exactly like /api/stream/*.

const (
	headerNosniff = "X-Content-Type-Options"
	nosniffValue  = "nosniff"
	// cspSandbox neutralizes active content (scripts, plugins, forms) in
	// documents we serve from inside hostile archives (EPUB chapters, SVG):
	// even opened as a top-level tab the response can't script our origin.
	headerCSP  = "Content-Security-Policy"
	cspSandbox = "sandbox"
)

// previewSrc bundles a resolved byte source with its display name and cleanup.
type previewSrc struct {
	src     preview.Source
	name    string
	cleanup func()
}

// PreviewDeps groups what the preview handlers need to resolve bytes.
type PreviewDeps struct {
	Streamer  *streamer.Streamer
	Downloads *downloads.Store
	Local     *local.Browser
}

// resolveSource picks the byte source from the request: ?hash=&idx= (torrent)
// or ?mount=&path= (local mount). On failure it writes the JSON error and
// returns ok=false.
func (d PreviewDeps) resolveSource(c *gin.Context) (*previewSrc, bool) {
	if c.Query("hash") != "" {
		return d.resolveTorrentSource(c)
	}
	if c.Query("mount") != "" {
		return d.resolveLocalSource(c)
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "hash+idx or mount+path is required"})
	return nil, false
}

func (d PreviewDeps) resolveTorrentSource(c *gin.Context) (*previewSrc, bool) {
	if d.Streamer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "streaming disabled"})
		return nil, false
	}
	h, err := parseHash(c.Query("hash"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	idx, err := strconv.Atoi(c.Query("idx"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errInvalidFileIndex})
		return nil, false
	}
	// Completed download on disk → real file, free random access.
	if d.Downloads != nil {
		userID, _, _ := auth.UserIDFromCtx(c)
		if path, err := d.Downloads.GetCompletedPathRel(h.HexString(), idx, d.Streamer.FileRelPath(h, idx), userID); err == nil && path != "" {
			if st, err := os.Stat(path); err == nil && !st.IsDir() {
				return openLocalPreviewFile(c, path)
			}
		}
	}
	reader, file, err := d.Streamer.FileReader(h, idx)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return nil, false
	}
	return &previewSrc{
		src: preview.Source{
			ReaderAt: preview.NewReaderAt(reader),
			Size:     file.Length(),
			OpenSeq: func() (io.ReadCloser, error) {
				if _, err := reader.Seek(0, io.SeekStart); err != nil {
					return nil, err
				}
				return preview.NopCloser(reader), nil
			},
		},
		name:    file.DisplayPath(),
		cleanup: func() { _ = reader.Close() },
	}, true
}

func (d PreviewDeps) resolveLocalSource(c *gin.Context) (*previewSrc, bool) {
	mount, path := c.Query("mount"), c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": lh.ErrMissingMountOrPathParam})
		return nil, false
	}
	if d.Local == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "local browsing disabled"})
		return nil, false
	}
	if !lh.CheckMountAccess(d.Local, c, mount) {
		return nil, false
	}
	abs, err := d.Local.ResolvePath(mount, lh.ScopePath(d.Local, c, mount, path))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	if !lh.StatLocalFile(c, abs) {
		return nil, false
	}
	return openLocalPreviewFile(c, abs)
}

func openLocalPreviewFile(c *gin.Context, abs string) (*previewSrc, bool) {
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	f, err := os.Open(abs)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, false
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return nil, false
	}
	return &previewSrc{
		src: preview.Source{
			ReaderAt: f,
			Size:     st.Size(),
			OpenSeq: func() (io.ReadCloser, error) {
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					return nil, err
				}
				return preview.NopCloser(f), nil
			},
		},
		name:    filepath.Base(abs),
		cleanup: func() { _ = f.Close() },
	}, true
}

// withPreviewSource wraps a handler body with source resolution + cleanup.
func (d PreviewDeps) withPreviewSource(fn func(c *gin.Context, src preview.Source, name string)) gin.HandlerFunc {
	return func(c *gin.Context) {
		ps, ok := d.resolveSource(c)
		if !ok {
			return
		}
		defer ps.cleanup()
		fn(c, ps.src, ps.name)
	}
}

// previewError maps package errors to HTTP statuses.
func previewError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, preview.ErrEntryNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, preview.ErrEntryTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	}
}

// PreviewArchiveList handles GET /api/preview/archive — lists the regular
// files inside the container (name + size), flagging truncation.
func PreviewArchiveList(d PreviewDeps) gin.HandlerFunc {
	return d.withPreviewSource(func(c *gin.Context, src preview.Source, name string) {
		format := preview.DetectFormat(name)
		if format == preview.FormatUnknown {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported archive format"})
			return
		}
		entries, truncated, err := preview.List(src, format)
		if err != nil {
			previewError(c, err)
			return
		}
		if entries == nil {
			entries = []preview.Entry{}
		}
		c.JSON(http.StatusOK, gin.H{"format": format, "entries": entries, "truncated": truncated})
	})
}

// PreviewArchiveEntry handles GET /api/preview/archive/entry?name= — serves
// ONE inner file inline, restricted to safe preview types (text/images) and
// capped at MaxEntryBytes of decompressed output.
func PreviewArchiveEntry(d PreviewDeps) gin.HandlerFunc {
	return d.withPreviewSource(func(c *gin.Context, src preview.Source, name string) {
		entryName := c.Query("name")
		format := preview.DetectFormat(name)
		if format == preview.FormatUnknown {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported archive format"})
			return
		}
		ct, ok := preview.EntryContentType(entryName)
		if !ok {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "entry type has no inline preview"})
			return
		}
		data, err := preview.ReadEntry(src, format, entryName, preview.MaxEntryBytes)
		if err != nil {
			previewError(c, err)
			return
		}
		servePreviewBytes(c, ct, data)
	})
}

// PreviewComicManifest handles GET /api/preview/comic — page names of a
// CBZ/CBR in natural reading order.
func PreviewComicManifest(d PreviewDeps) gin.HandlerFunc {
	return d.withPreviewSource(func(c *gin.Context, src preview.Source, name string) {
		format := preview.DetectFormat(name)
		if format == preview.FormatUnknown {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported comic format"})
			return
		}
		pages, err := preview.ComicPages(src, format)
		if err != nil {
			previewError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"pages": pages})
	})
}

// PreviewComicPage handles GET /api/preview/comic/page?name= — one page image.
func PreviewComicPage(d PreviewDeps) gin.HandlerFunc {
	return d.withPreviewSource(func(c *gin.Context, src preview.Source, name string) {
		pageName := c.Query("name")
		if !preview.IsComicPage(pageName) {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "not a comic page image"})
			return
		}
		format := preview.DetectFormat(name)
		if format == preview.FormatUnknown {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported comic format"})
			return
		}
		data, err := preview.ReadEntry(src, format, pageName, preview.MaxComicPageBytes)
		if err != nil {
			previewError(c, err)
			return
		}
		ct, _ := preview.EntryContentType(pageName)
		c.Header(httpshared.CacheControl, httpshared.CachePublicDay)
		servePreviewBytes(c, ct, data)
	})
}

// PreviewEpubManifest handles GET /api/preview/epub — title + spine chapters.
func PreviewEpubManifest(d PreviewDeps) gin.HandlerFunc {
	return d.withPreviewSource(func(c *gin.Context, src preview.Source, _ string) {
		book, err := preview.ParseEpub(src)
		if err != nil {
			previewError(c, err)
			return
		}
		c.JSON(http.StatusOK, book)
	})
}

// PreviewEpubChapter handles GET /api/preview/epub/chapter?name= — one spine
// document, sanitized (scripts/embeds/on* stripped, refs rewritten to the res
// endpoint) and served under CSP sandbox for the frontend's <iframe sandbox>.
func PreviewEpubChapter(d PreviewDeps) gin.HandlerFunc {
	return d.withPreviewSource(func(c *gin.Context, src preview.Source, _ string) {
		chapterName := c.Query("name")
		book, err := preview.ParseEpub(src)
		if err != nil {
			previewError(c, err)
			return
		}
		if !epubHasChapter(book, chapterName) {
			c.JSON(http.StatusNotFound, gin.H{"error": "chapter not in spine"})
			return
		}
		raw, err := preview.ReadEntry(src, preview.FormatZip, chapterName, preview.MaxChapterBytes)
		if err != nil {
			previewError(c, err)
			return
		}
		baseDir := pathDir(chapterName)
		resBase := epubResBaseURL(c)
		sanitized := preview.SanitizeChapter(raw, func(ref string) (string, bool) {
			resolved := preview.ResolveEpubRef(baseDir, ref)
			if resolved == "" {
				return "", false
			}
			return resBase + "&name=" + url.QueryEscape(resolved), true
		})
		c.Header(headerNosniff, nosniffValue)
		c.Header(headerCSP, cspSandbox)
		c.Data(http.StatusOK, "text/html; charset=utf-8", sanitized)
	})
}

// PreviewEpubResource handles GET /api/preview/epub/res?name= — images and
// stylesheets referenced by chapters. Anything else is refused.
func PreviewEpubResource(d PreviewDeps) gin.HandlerFunc {
	return d.withPreviewSource(func(c *gin.Context, src preview.Source, _ string) {
		resName := c.Query("name")
		ct, ok := epubResContentType(resName)
		if !ok {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "resource type not allowed"})
			return
		}
		data, err := preview.ReadEntry(src, preview.FormatZip, resName, preview.MaxResourceBytes)
		if err != nil {
			previewError(c, err)
			return
		}
		c.Header(httpshared.CacheControl, httpshared.CachePublicDay)
		servePreviewBytes(c, ct, data)
	})
}

// servePreviewBytes writes data with the hardened header set. SVG (scriptable
// XML) additionally gets CSP sandbox so opening the URL directly can't run
// scripts in our origin; <img> rendering is unaffected.
func servePreviewBytes(c *gin.Context, contentType string, data []byte) {
	c.Header(headerNosniff, nosniffValue)
	if strings.HasPrefix(contentType, "image/svg") {
		c.Header(headerCSP, cspSandbox)
	}
	c.Data(http.StatusOK, contentType, data)
}

func epubHasChapter(book *preview.Epub, name string) bool {
	for _, ch := range book.Chapters {
		if ch == name {
			return true
		}
	}
	return false
}

// epubResBaseURL rebuilds the source-identifying query (hash/idx or
// mount/path/user, plus the media token when it rode the query) so rewritten
// chapter refs point back at the res endpoint with the same credentials.
func epubResBaseURL(c *gin.Context) string {
	params := url.Values{}
	for _, k := range []string{"hash", "idx", "mount", "path", "user", "token"} {
		if v := c.Query(k); v != "" {
			params.Set(k, v)
		}
	}
	return "/api/preview/epub/res?" + params.Encode()
}

// epubResContentType allows images (EntryContentType) plus stylesheets and
// fonts — what a chapter legitimately references. text/css must be exact or
// nosniff blocks the stylesheet inside the iframe.
func epubResContentType(name string) (string, bool) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".css"):
		return "text/css; charset=utf-8", true
	case strings.HasSuffix(lower, ".ttf"):
		return "font/ttf", true
	case strings.HasSuffix(lower, ".otf"):
		return "font/otf", true
	case strings.HasSuffix(lower, ".woff"):
		return "font/woff", true
	case strings.HasSuffix(lower, ".woff2"):
		return "font/woff2", true
	}
	if preview.IsImageEntry(name) {
		ct, ok := preview.EntryContentType(name)
		return ct, ok
	}
	return "", false
}

// pathDir is path.Dir for zip-entry paths (always '/'-separated).
func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}
