// Package clientcaps decides direct-play vs transcode from the codecs a
// client says it can play natively (issue #201). Native clients probe with
// ?caps=mkv,hevc,av1,ac3,eac3,dts,truehd,ass,pgs and get a playMethod of
// direct|transcode plus the blocking reason; callers without caps keep the
// browser whitelist decision untouched.
package clientcaps

import "strings"

// The playMethod values carried in the probe/local-play responses.
const (
	PlayDirect    = "direct"
	PlayTranscode = "transcode"
)

// Browser-safe whitelists — the same sets the browser-side classifiers use
// (internal/streamer/probe.go, handlers/local). Anything outside plays in
// NO browser inline, so caps can only ever extend, never shrink, this set.
var (
	browserSafeContainers  = map[string]bool{"mp4": true, "m4v": true, "mov": true, "webm": true, "isom": true, "mp42": true, "qt": true}
	browserSafeVideoCodecs = map[string]bool{"h264": true, "vp8": true, "vp9": true}
	browserSafeAudioCodecs = map[string]bool{"aac": true, "mp3": true, "opus": true, "vorbis": true}
)

// containerAliases maps ffprobe container names to the tokens clients use in
// caps (ffprobe reports "matroska"; the client token is "mkv").
var containerAliases = map[string]string{
	"matroska": "mkv",
}

// Parse reads a comma-separated caps query value ("mkv,hevc,ac3") into a
// lowercase set. Whitespace and empty tokens are ignored. Subtitle tokens
// (ass, pgs) are accepted for forward compatibility but never influence the
// decision — subtitles ride the sidecar/burn-in pipeline, not playback.
func Parse(q string) map[string]bool {
	caps := map[string]bool{}
	for _, token := range strings.Split(q, ",") {
		if token = strings.ToLower(strings.TrimSpace(token)); token != "" {
			caps[token] = true
		}
	}
	return caps
}

// Decide returns the playMethod (PlayDirect|PlayTranscode) and the reason —
// the FIRST blocking dimension in container → vcodec → acodec order, styled
// "container=matroska" like the existing browser-side classifiers. A nil
// caps set decides by the browser whitelist alone (legacy behavior); a
// non-nil set lets the client's advertised tokens unlock the dimensions the
// whitelist would block. Unknown facts (empty codec/container) never block.
func Decide(container, vcodec, acodec string, caps map[string]bool) (playMethod, reason string) {
	if !allowed(container, caps, browserSafeContainers) {
		return PlayTranscode, "container=" + container
	}
	if vcodec != "" && !allowed(vcodec, caps, browserSafeVideoCodecs) {
		return PlayTranscode, "vcodec=" + vcodec
	}
	if acodec != "" && !allowed(acodec, caps, browserSafeAudioCodecs) {
		return PlayTranscode, "acodec=" + acodec
	}
	return PlayDirect, ""
}

// allowed reports whether a dimension passes: whitelisted always, otherwise
// only when the client advertised the token (or its ffprobe alias) in caps.
// An empty fact is unknown — it never blocks.
func allowed(fact string, caps map[string]bool, whitelist map[string]bool) bool {
	if fact == "" || whitelist[fact] {
		return true
	}
	if caps == nil {
		return false
	}
	if caps[fact] {
		return true
	}
	if alias, ok := containerAliases[fact]; ok {
		return caps[alias]
	}
	return false
}
