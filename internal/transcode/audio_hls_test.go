package transcode

import (
	"strings"
	"testing"
)

func argString(args []string) string { return strings.Join(args, " ") }

// hasFlagPair reports whether args contains `flag value` adjacently.
func hasFlagPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestAudioArgsIsAudioOnly(t *testing.T) {
	spec := &encodeSpec{dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "libx264", vod: true, audioOnly: true}
	args := spec.args(0)
	s := argString(args)

	if !contains(args, "-vn") {
		t.Errorf("audio-only spec must pass -vn (no video); got: %s", s)
	}
	if !hasFlagPair(args, "-map", "0:a:0") {
		t.Errorf("audio-only spec must map only audio (0:a:0); got: %s", s)
	}
	if contains(args, "0:v:0") {
		t.Errorf("audio-only spec must NOT map video (0:v:0); got: %s", s)
	}
	if !hasFlagPair(args, "-c:a", "aac") {
		t.Errorf("audio-only spec must transcode to aac; got: %s", s)
	}
	// The Safari t=0 stall root cause applies to audio TS too — the muxer offset
	// must be zeroed or seg0 starts at ~1.4s.
	if !hasFlagPair(args, "-muxdelay", "0") || !hasFlagPair(args, "-muxpreload", "0") {
		t.Errorf("audio-only spec must zero muxdelay/muxpreload; got: %s", s)
	}
	if !hasFlagPair(args, "-f", "hls") {
		t.Errorf("audio-only spec must output hls; got: %s", s)
	}
}

func TestAudioArgsSeekRestartOffsets(t *testing.T) {
	spec := &encodeSpec{dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "libx264", vod: true, audioOnly: true}
	args := spec.args(3) // restart at segment 3 → media time 3*hlsSegDur
	want := "12"         // 3 * hlsSegDur(4)
	if !hasFlagPair(args, "-ss", want) {
		t.Errorf("VOD seek-restart must input-seek to %ss; got: %s", want, argString(args))
	}
	if !hasFlagPair(args, "-output_ts_offset", want) {
		t.Errorf("VOD seek-restart must offset output ts to %ss (global timeline); got: %s", want, argString(args))
	}
	if !hasFlagPair(args, "-start_number", "3") {
		t.Errorf("restart must set -start_number 3; got: %s", argString(args))
	}
}

func TestAudioArgsNoSeekAtSegmentZero(t *testing.T) {
	spec := &encodeSpec{dir: "/tmp/x", inputURL: "u", encoder: "libx264", vod: true, audioOnly: true}
	args := spec.args(0)
	if contains(args, "-ss") {
		t.Errorf("segment 0 must not input-seek; got: %s", argString(args))
	}
	if contains(args, "-output_ts_offset") {
		t.Errorf("segment 0 must not offset output ts; got: %s", argString(args))
	}
}

// contains reports whether args contains s as a standalone token.
func contains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}
