package preview

import (
	"strings"
	"testing"
)

const testOPF = `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Livro de Teste</dc:title>
  </metadata>
  <manifest>
    <item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="text/ch%202.xhtml" media-type="application/xhtml+xml"/>
    <item id="css" href="style.css" media-type="text/css"/>
    <item id="evil" href="../../etc/passwd" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="c1"/>
    <itemref idref="c2"/>
    <itemref idref="evil"/>
    <itemref idref="ghost"/>
  </spine>
</package>`

const testContainerXML = `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`

func makeEpub(t *testing.T) Source {
	t.Helper()
	return makeZip(t, map[string][]byte{
		"mimetype":               []byte("application/epub+zip"),
		"META-INF/container.xml": []byte(testContainerXML),
		"OEBPS/content.opf":      []byte(testOPF),
		"OEBPS/ch1.xhtml":        []byte(`<html><head><title>1</title></head><body><p>cap 1</p></body></html>`),
		"OEBPS/text/ch 2.xhtml":  []byte(`<html><body><img src="../img/pic.png"/></body></html>`),
		"OEBPS/style.css":        []byte("p{color:red}"),
		"OEBPS/img/pic.png":      []byte("pngbytes"),
	})
}

func TestParseEpub(t *testing.T) {
	book, err := ParseEpub(makeEpub(t))
	if err != nil {
		t.Fatalf("ParseEpub: %v", err)
	}
	if book.Title != "Livro de Teste" {
		t.Errorf("title = %q", book.Title)
	}
	want := []string{"OEBPS/ch1.xhtml", "OEBPS/text/ch 2.xhtml"}
	if len(book.Chapters) != len(want) {
		t.Fatalf("chapters = %v, want %v (escaping href must be dropped)", book.Chapters, want)
	}
	for i := range want {
		if book.Chapters[i] != want[i] {
			t.Errorf("chapters[%d] = %q, want %q", i, book.Chapters[i], want[i])
		}
	}
}

func TestParseEpubNotAnEpub(t *testing.T) {
	src := makeZip(t, map[string][]byte{"random.txt": []byte("x")})
	if _, err := ParseEpub(src); err == nil {
		t.Error("ParseEpub(plain zip) err = nil, want error")
	}
}

func TestResolveEpubRef(t *testing.T) {
	cases := []struct {
		base, ref, want string
	}{
		{"OEBPS", "ch1.xhtml", "OEBPS/ch1.xhtml"},
		{"OEBPS/text", "../img/pic.png", "OEBPS/img/pic.png"},
		{"OEBPS", "ch1.xhtml#frag", "OEBPS/ch1.xhtml"},
		{"OEBPS", "ch%202.xhtml", "OEBPS/ch 2.xhtml"},
		{".", "cover.jpg", "cover.jpg"},
		{"OEBPS", "../../etc/passwd", ""},
		{"OEBPS", "https://evil.example/x.png", ""},
		{"OEBPS", "//evil.example/x.png", ""},
		{"OEBPS", "", ""},
	}
	for _, tc := range cases {
		if got := ResolveEpubRef(tc.base, tc.ref); got != tc.want {
			t.Errorf("ResolveEpubRef(%q, %q) = %q, want %q", tc.base, tc.ref, got, tc.want)
		}
	}
}

func TestSanitizeChapter(t *testing.T) {
	in := []byte(`<html><head><title>x</title></head><body>
<script>alert(1)</script>
<script src="evil.js"/>
<p onclick="alert(2)" class="keep">text</p>
<a href="javascript:alert(3)">link</a>
<a href="#anchor">anchor stays</a>
<img src="img/pic.png"/>
<img src="data:image/png;base64,AAAA"/>
<iframe src="https://evil.example"></iframe>
<link rel="stylesheet" href="style.css"/>
</body></html>`)
	out := string(SanitizeChapter(in, func(ref string) (string, bool) {
		if ref == "img/pic.png" || ref == "style.css" {
			return "/api/preview/epub/res?name=" + ref, true
		}
		return "", false
	}))

	for _, banned := range []string{"<script", "onclick", "javascript:", "<iframe", "evil.example"} {
		if strings.Contains(out, banned) {
			t.Errorf("sanitized output still contains %q:\n%s", banned, out)
		}
	}
	for _, required := range []string{
		`src="/api/preview/epub/res?name=img/pic.png"`,
		`href="/api/preview/epub/res?name=style.css"`,
		`href="#anchor"`,
		`src="data:image/png;base64,AAAA"`,
		`class="keep"`,
		"<style>", // injected base CSS
	} {
		if !strings.Contains(out, required) {
			t.Errorf("sanitized output missing %q:\n%s", required, out)
		}
	}
	// Base CSS must land inside <head>, right after the opening tag.
	if !strings.Contains(out, "<head><style>") {
		t.Errorf("base CSS not injected after <head>:\n%s", out)
	}
}

func TestSanitizeChapterNoHead(t *testing.T) {
	out := string(SanitizeChapter([]byte(`<p>solto</p>`), func(string) (string, bool) { return "", false }))
	if !strings.HasPrefix(out, "<style>") {
		t.Errorf("headless doc should get CSS prepended:\n%s", out)
	}
}
