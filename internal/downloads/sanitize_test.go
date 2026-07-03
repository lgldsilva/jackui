package downloads

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeFolderName(t *testing.T) {
	cases := map[string]string{
		"Breaking Bad S01 1080p": "Breaking Bad S01 1080p",
		"":                       "download",
		"   ":                    "download",
		".":                      "download",
		"..":                     "download",
		"a/b/c":                  "a_b_c",    // path separators neutralized
		"foo\\bar":               "foo_bar",  // backslash too
		"trailing...":            "trailing", // trailing dots stripped
		"name.with.dots.mkv":     "name.with.dots.mkv",
	}
	for in, want := range cases {
		if got := sanitizeFolderName(in); got != want {
			t.Errorf("sanitizeFolderName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeFolderName_NeverEscapes(t *testing.T) {
	// Whatever the input, the result must be a single, safe path segment: no
	// separators, not "." or "..", and joining it must stay under the base dir.
	base := "/data/downloads/user"
	for _, in := range []string{"../../etc/passwd", "..", ".", "/abs/path", "a/../../b", "", "\x00\x01weird"} {
		seg := sanitizeFolderName(in)
		if strings.ContainsAny(seg, `/\`) {
			t.Errorf("sanitizeFolderName(%q)=%q contains a path separator", in, seg)
		}
		if seg == "." || seg == ".." || seg == "" {
			t.Errorf("sanitizeFolderName(%q)=%q is an unsafe segment", in, seg)
		}
		joined := filepath.Join(base, seg)
		if !strings.HasPrefix(joined, base+"/") {
			t.Errorf("sanitizeFolderName(%q)=%q escapes base: %q", in, seg, joined)
		}
	}
}

func TestSanitizeFolderName_CapsLength(t *testing.T) {
	long := strings.Repeat("x", 500)
	if got := sanitizeFolderName(long); len(got) > 200 {
		t.Errorf("expected length capped at 200, got %d", len(got))
	}
}
