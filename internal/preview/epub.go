package preview

import (
	"encoding/xml"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Epub is the parsed reading manifest of an EPUB: the chapter documents in
// spine (reading) order, plus the title. Chapter hrefs are zip-entry paths
// resolved against the OPF directory — ready to feed back into ReadEntry.
type Epub struct {
	Title    string   `json:"title"`
	Chapters []string `json:"chapters"`
}

// MaxEpubChapters caps how many spine items we surface — hostile EPUBs can
// declare an absurd spine in a few KB.
const MaxEpubChapters = 1000

type epubContainer struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type epubPackage struct {
	Metadata struct {
		Title string `xml:"title"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID   string `xml:"id,attr"`
			Href string `xml:"href,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Itemrefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

// ParseEpub reads the OCF container + OPF package of an EPUB (which is a zip)
// and returns the spine in reading order. Pure stdlib: archive/zip +
// encoding/xml.
func ParseEpub(src Source) (*Epub, error) {
	containerXML, err := ReadEntry(src, FormatZip, "META-INF/container.xml", MaxChapterBytes)
	if err != nil {
		return nil, fmt.Errorf("epub container: %w", err)
	}
	var cont epubContainer
	if err := xml.Unmarshal(containerXML, &cont); err != nil {
		return nil, fmt.Errorf("epub container: %w", err)
	}
	if len(cont.Rootfiles) == 0 || cont.Rootfiles[0].FullPath == "" {
		return nil, fmt.Errorf("epub container: no rootfile")
	}
	opfPath := cont.Rootfiles[0].FullPath
	if !SafeEntryName(opfPath) {
		return nil, fmt.Errorf("epub container: unsafe rootfile path")
	}
	opfXML, err := ReadEntry(src, FormatZip, opfPath, MaxChapterBytes)
	if err != nil {
		return nil, fmt.Errorf("epub opf: %w", err)
	}
	var pkg epubPackage
	if err := xml.Unmarshal(opfXML, &pkg); err != nil {
		return nil, fmt.Errorf("epub opf: %w", err)
	}

	hrefByID := make(map[string]string, len(pkg.Manifest.Items))
	for _, it := range pkg.Manifest.Items {
		hrefByID[it.ID] = it.Href
	}
	opfDir := path.Dir(opfPath)
	chapters := make([]string, 0, len(pkg.Spine.Itemrefs))
	for _, ref := range pkg.Spine.Itemrefs {
		href, ok := hrefByID[ref.IDRef]
		if !ok || href == "" {
			continue
		}
		resolved := ResolveEpubRef(opfDir, href)
		if resolved == "" {
			continue
		}
		if len(chapters) >= MaxEpubChapters {
			break
		}
		chapters = append(chapters, resolved)
	}
	if len(chapters) == 0 {
		return nil, fmt.Errorf("epub opf: empty spine")
	}
	return &Epub{Title: strings.TrimSpace(pkg.Metadata.Title), Chapters: chapters}, nil
}

// ResolveEpubRef resolves a (possibly URL-encoded) relative href against the
// directory of the referencing document, returning a zip-entry path — or ""
// when the ref escapes the archive or isn't a relative file reference.
func ResolveEpubRef(baseDir, ref string) string {
	// Strip fragment and query; decode %20 etc.
	if i := strings.IndexAny(ref, "#?"); i >= 0 {
		ref = ref[:i]
	}
	if ref == "" || strings.Contains(ref, "://") || strings.HasPrefix(ref, "//") {
		return ""
	}
	decoded, err := url.PathUnescape(ref)
	if err != nil {
		return ""
	}
	var joined string
	if baseDir != "." && baseDir != "" {
		joined = path.Join(baseDir, decoded)
	} else {
		joined = path.Clean(decoded)
	}
	if !SafeEntryName(joined) {
		return ""
	}
	return joined
}

// ─── chapter sanitization ────────────────────────────────────────────────────
//
// The REAL security boundary for EPUB chapters is transport-level: the handler
// serves them with `Content-Security-Policy: sandbox` and the frontend loads
// them in an <iframe sandbox> (no allow-scripts, no allow-same-origin). The
// regex pass below is belt-and-braces: it strips the obvious active content so
// even a misconfigured client doesn't execute book JavaScript.

var (
	reScriptBlock = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>|<script\b[^>]*/\s*>`)
	reEmbedOpen   = regexp.MustCompile(`(?is)</?(iframe|object|embed|form|base)\b[^>]*>`)
	reOnAttr      = regexp.MustCompile(`(?i)\s+on\w+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	reRefAttr     = regexp.MustCompile(`(?i)(src|href|xlink:href)\s*=\s*("([^"]*)"|'([^']*)')`)
	reHeadOpen    = regexp.MustCompile(`(?i)<head[^>]*>`)
)

// epubBaseCSS keeps chapters readable inside the dark-themed viewer without
// trusting the book's own stylesheets to define sane colors.
const epubBaseCSS = `<style>
body{font-family:Georgia,'Times New Roman',serif;line-height:1.6;max-width:42em;
margin:0 auto;padding:1.5em;background:#fff;color:#1a1a1a;word-wrap:break-word}
img,svg{max-width:100%;height:auto}
</style>`

// SanitizeChapter neutralizes active content in an EPUB chapter document and
// rewrites relative resource references (images, stylesheets) through the
// resolve callback. resolve receives the raw relative ref and returns the URL
// to substitute, or ok=false to neutralize the ref entirely ("#").
func SanitizeChapter(doc []byte, resolve func(ref string) (string, bool)) []byte {
	out := reScriptBlock.ReplaceAll(doc, nil)
	out = reEmbedOpen.ReplaceAll(out, nil)
	out = reOnAttr.ReplaceAll(out, nil)
	out = rewriteRefs(out, resolve)
	return injectBaseCSS(out)
}

func rewriteRefs(doc []byte, resolve func(ref string) (string, bool)) []byte {
	return reRefAttr.ReplaceAllFunc(doc, func(m []byte) []byte {
		sub := reRefAttr.FindSubmatch(m)
		attr := string(sub[1])
		val := string(sub[3])
		if val == "" {
			val = string(sub[4])
		}
		lower := strings.ToLower(strings.TrimSpace(val))
		// Keep harmless in-document anchors and data images; kill every other
		// absolute scheme (javascript:, http:, file:, vbscript:, ...).
		if strings.HasPrefix(lower, "#") || strings.HasPrefix(lower, "data:image/") {
			return m
		}
		if strings.Contains(lower, ":") || strings.HasPrefix(lower, "//") {
			return []byte(attr + `="#"`)
		}
		if newURL, ok := resolve(val); ok {
			return []byte(attr + `="` + newURL + `"`)
		}
		return []byte(attr + `="#"`)
	})
}

func injectBaseCSS(doc []byte) []byte {
	if loc := reHeadOpen.FindIndex(doc); loc != nil {
		out := make([]byte, 0, len(doc)+len(epubBaseCSS))
		out = append(out, doc[:loc[1]]...)
		out = append(out, []byte(epubBaseCSS)...)
		out = append(out, doc[loc[1]:]...)
		return out
	}
	return append([]byte(epubBaseCSS), doc...)
}
