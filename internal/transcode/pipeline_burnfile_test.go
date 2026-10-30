package transcode

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidBurnFilePath pins the security-barrier semantics of the
// external-subtitle burn-in path guard. This function is registered as a
// CodeQL barrier (.github/codeql/extensions/barriers.yml) — barrier without
// test = PR rejected (repo rule), so these four case classes are contract:
// inside-ok, traversal-rejected, outside-root-rejected, empty-rejected.
func TestValidBurnFilePath(t *testing.T) {
	root := t.TempDir()
	sibling := filepath.Join(filepath.Dir(root), "other-cache")
	cases := []struct {
		name      string
		root      string
		candidate string
		want      bool
	}{
		// ── inside-ok ──
		{"direct child", root, filepath.Join(root, "123.vtt"), true},
		{"nested child", root, filepath.Join(root, "sub", "456.vtt"), true},
		// ── traversal-rejected ──
		{"parent traversal", root, filepath.Join(root, "..", "escape.vtt"), false},
		{"double traversal", root, filepath.Join(root, "..", "..", "escape.vtt"), false},
		{"traversal from relative root", ".", filepath.Join("..", "escape.vtt"), false},
		// ── outside-root-rejected ──
		{"absolute escape", root, "/etc/passwd", false},
		{"sibling dir", root, filepath.Join(sibling, "123.vtt"), false},
		// prefix trick: /root2 is NOT inside /root (no separator at the join)
		{"prefix without separator", root, root + "2" + string(filepath.Separator) + "123.vtt", false},
		{"relative candidate", root, "123.vtt", false},
		// ── empty-rejected ──
		{"empty candidate", root, "", false},
		{"empty root", "", filepath.Join(root, "123.vtt"), false},
		{"both empty", "", "", false},
		// root itself is not a file under root
		{"candidate is root", root, root, false},
		// traversal that RESOLVES back inside the root is fine (resolution wins)
		{"redundant traversal", root, filepath.Join(root, "..", filepath.Base(root), "123.vtt"), true},
	}
	for _, tc := range cases {
		if got := ValidBurnFilePath(tc.root, tc.candidate); got != tc.want {
			t.Errorf("%s: ValidBurnFilePath(%q, %q) = %v, want %v", tc.name, tc.root, tc.candidate, got, tc.want)
		}
	}
}

func TestEscapeSubtitlesFilterPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Plain cache paths (the normal case) pass through unchanged.
		{"/data/subtitles/123.vtt", "/data/subtitles/123.vtt"},
		// ':' is the filter option separator.
		{"/a:b/c.vtt", `/a\:b/c.vtt`},
		// '\'' is the filtergraph quoting character.
		{"/a'b/c.vtt", `/a\'b/c.vtt`},
		// '\\' is the escape character itself.
		{`/a\b/c.vtt`, `/a\\b/c.vtt`},
		// ',' splits filters and ';' splits chains in a filtergraph; '[' ']'
		// delimit link labels — an admin-configured cache root containing them
		// must not end the -vf value mid-option.
		{"/ca,che/123.vtt", `/ca\,che/123.vtt`},
		{"/sub;dir/[x]/123.vtt", `/sub\;dir/\[x\]/123.vtt`},
		// Combined, with no double-escaping (single-pass replacer).
		{`/a:b'c\d.vtt`, `/a\:b\'c\\d.vtt`},
		{`/a,b;c[d]e/f'g:h.vtt`, `/a\,b\;c\[d\]e/f\'g\:h.vtt`},
	}
	for _, tc := range cases {
		if got := escapeSubtitlesFilterPath(tc.in); got != tc.want {
			t.Errorf("escapeSubtitlesFilterPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSubtitlesFilterArg(t *testing.T) {
	got := subtitlesFilterArg("/cache/123.vtt")
	if got != "subtitles=/cache/123.vtt" {
		t.Errorf("subtitlesFilterArg = %q", got)
	}
	if got := subtitlesFilterArg("/a:b.vtt"); got != `subtitles=/a\:b.vtt` {
		t.Errorf("subtitlesFilterArg escaping = %q", got)
	}
}

// TestValidateOptionsBurnFile pins the option-validation gate: invalid
// burn-file options must die here with a typed error, never reach exec.
func TestValidateOptionsBurnFile(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "999.vtt")
	if err := os.WriteFile(existing, []byte("WEBVTT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name            string
		caps            *Capabilities
		subBurnFile     string
		wantErr         error // nil = no errors.Is match; matched with errors.Is
		wantNotExist    bool
		wantErrContains string // substring the error must contain (for non-sentinel errors)
	}{
		{"no burn file, nil caps", nil, "", nil, false, ""},
		{"no burn file ignores caps", &Capabilities{}, "", nil, false, ""},
		{"nil caps defers to Run", nil, existing, nil, false, ""},
		{"libass missing → typed error", &Capabilities{HasSubtitlesFilter: false}, existing, ErrSubtitlesFilterUnavailable, false, ""},
		{"file missing", &Capabilities{HasSubtitlesFilter: true}, filepath.Join(dir, "missing.vtt"), nil, true, ""},
		{"regular file ok", &Capabilities{HasSubtitlesFilter: true}, existing, nil, false, ""},
		{"directory is not a file", &Capabilities{HasSubtitlesFilter: true}, dir, nil, false, "not a regular file"},
	}
	for _, tc := range cases {
		got := ValidateOptions(tc.caps, Options{SubBurnFile: tc.subBurnFile})
		if tc.wantErr != nil {
			if !errors.Is(got, tc.wantErr) {
				t.Errorf("%s: ValidateOptions err = %v, want errors.Is %v", tc.name, got, tc.wantErr)
			}
			continue
		}
		if tc.wantNotExist {
			if got == nil || !errors.Is(got, fs.ErrNotExist) {
				t.Errorf("%s: ValidateOptions err = %v, want not-exist error", tc.name, got)
			}
			continue
		}
		if tc.wantErrContains != "" {
			if got == nil || !strings.Contains(got.Error(), tc.wantErrContains) {
				t.Errorf("%s: ValidateOptions err = %v, want containing %q", tc.name, got, tc.wantErrContains)
			}
			continue
		}
		if got != nil {
			t.Errorf("%s: ValidateOptions err = %v, want nil", tc.name, got)
		}
	}
}

// TestBuildTranscodeArgsWithSubBurnFile asserts the external-subtitle burn-in
// arg shape (pattern of TestBuildTranscodeArgsWithSubBurn): the file must be
// referenced by the subtitles filter, the video map stays single and raw, and
// the HW decode/scale path is fully bypassed.
func TestBuildTranscodeArgsWithSubBurnFile(t *testing.T) {
	caps := &Capabilities{Preferred: "libx264", HasSubtitlesFilter: true}
	args := buildTranscodeArgs(caps, "libx264", "mp4", Options{SubBurnFile: "/cache/123.vtt"})
	joined := strings.Join(args, " ")

	// The external file IS the subtitle source: libass text filter.
	if !strings.Contains(joined, "-vf subtitles=/cache/123.vtt") {
		t.Errorf("expected subtitles filter referencing the file, got %s", joined)
	}
	// Plain simple-filtergraph path: raw video map, NO overlay filter_complex.
	if strings.Contains(joined, "-filter_complex") || strings.Contains(joined, "overlay") || strings.Contains(joined, "-map [v]") {
		t.Errorf("burn-file path must not use the embedded overlay graph, got %s", joined)
	}
	if strings.Count(joined, "-map 0:v:0") != 1 {
		t.Errorf("expected exactly 1 raw video map, got %d in %s", strings.Count(joined, "-map 0:v:0"), joined)
	}
	// Burn forces CPU-side filtering: no HW decode and no scale filter, even
	// though the output is re-encoded (VideoCodec forced to h264 when empty).
	if strings.Contains(joined, "-hwaccel") {
		t.Errorf("burn path must not decode on HW (Error reinitializing filters), got %s", joined)
	}
	if strings.Contains(joined, "scale=") || strings.Contains(joined, "scale_vaapi") {
		t.Errorf("burn path must not add the scale filter, got %s", joined)
	}
	if !strings.Contains(joined, "-c:v libx264") {
		t.Errorf("burn path must force a video re-encode, got %s", joined)
	}
	// Demuxed subtitle streams stay dropped — the burn IS the subtitle output.
	if !strings.Contains(joined, "-sn -dn") {
		t.Errorf("expected -sn -dn to stay, got %s", joined)
	}
}

// TestBuildTranscodeArgsWithSubBurnFileWins covers the precedence rule:
// when both SubBurnFile and SubBurnTrack are set, SubBurnFile wins.
func TestBuildTranscodeArgsWithSubBurnFileWins(t *testing.T) {
	caps := &Capabilities{Preferred: "libx264", HasSubtitlesFilter: true}
	args := buildTranscodeArgs(caps, "libx264", "mp4", Options{SubBurnTrack: 2, SubBurnFile: "/cache/123.vtt"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "subtitles=/cache/123.vtt") {
		t.Errorf("SubBurnFile must win, got %s", joined)
	}
	if strings.Contains(joined, "overlay") || strings.Contains(joined, "[0:2]") {
		t.Errorf("embedded overlay must not run alongside SubBurnFile, got %s", joined)
	}
}

// TestBuildTranscodeArgsWithSubBurnFileHWCodec pins that an explicitly
// requested video codec still bypasses HW decode on the burn path.
func TestBuildTranscodeArgsWithSubBurnFileHWCodec(t *testing.T) {
	caps := &Capabilities{Preferred: "h264_nvenc", PreferredHE: "hevc_nvenc", HasSubtitlesFilter: true}
	args := buildTranscodeArgs(caps, "hevc_nvenc", "mp4", Options{SubBurnFile: "/cache/123.vtt", VideoCodec: "hevc"})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-hwaccel") {
		t.Errorf("burn path must not HW-decode even with an explicit codec, got %s", joined)
	}
	if !strings.Contains(joined, "subtitles=/cache/123.vtt") {
		t.Errorf("subtitles filter missing, got %s", joined)
	}
}

// TestBuildTranscodeArgsWithSubBurnFileEscaping makes sure special characters
// in the (admin-configurable) cache root cannot break the filtergraph parser.
func TestBuildTranscodeArgsWithSubBurnFileEscaping(t *testing.T) {
	caps := &Capabilities{Preferred: "libx264", HasSubtitlesFilter: true}
	args := buildTranscodeArgs(caps, "libx264", "mp4", Options{SubBurnFile: `/c/a:b'c\d.vtt`})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, `subtitles=/c/a\:b\'c\\d.vtt`) {
		t.Errorf("path not escaped for the filtergraph, got %s", joined)
	}
}

// TestListFilters pins the -filters output parsing that backs the
// HasSubtitlesFilter probe: names come from the name column only — never from
// description words like "Render text subtitles …".
func TestListFilters(t *testing.T) {
	out := []byte(`Filters:
  T.. = Timeline support
  .S. = Slice threading
  A.. = Audio-only
  ..C = Command support
 T.C aa             A->A       Audio effect.
 .. ass             V->V       Render ASS subtitles onto input video using the libass library.
 T.| amix           A->A       Audio Mixer.
 ... subtitles      V->V       Render text subtitles onto video input using the libass library.
 .. scale           V->V       Scale the input video.
`)
	filters := listFilters(out)
	for _, want := range []string{"aa", "ass", "amix", "subtitles", "scale"} {
		if !filters[want] {
			t.Errorf("listFilters missing %q in %v", want, filters)
		}
	}
	for _, absent := range []string{"Render", "timeline", "libass", "V"} {
		if filters[absent] {
			t.Errorf("listFilters must not capture %q (description/header text)", absent)
		}
	}
	if len(filters) != 5 {
		t.Errorf("expected exactly 5 filters parsed, got %d: %v", len(filters), filters)
	}
}

// (hasFilter itself is a one-line exec wrapper over listFilters — same shape
// as listEncoders/listDecoders, whose pure parsers are what the tests pin.)
