package parser

import "regexp"

// Jackett categories that typically contain playable media. Mirrors
// VIDEO_CATEGORIES / AUDIO_CATEGORIES from web/src/lib/playable.ts — the
// source of truth lives here now; the frontend only reads the returned fields.
var (
	videoCategories = map[int]bool{
		2000: true, 2010: true, 2020: true, 2030: true, 2040: true, 2045: true,
		2050: true, 2060: true, 2070: true, 2080: true,
		5000: true, 5010: true, 5020: true, 5030: true, 5040: true, 5045: true,
		5050: true, 5060: true, 5070: true, 5080: true, 5090: true,
		100022: true,
	}
	audioCategories = map[int]bool{
		3000: true, 3010: true, 3020: true, 3030: true, 3040: true, 3050: true,
		3060: true,
	}
)

var (
	reVideoExt    = regexp.MustCompile(`(?i)\.(mp4|mkv|avi|mov|webm|m4v|wmv|flv|ts|m2ts|vob)$`)
	reAudioExt    = regexp.MustCompile(`(?i)\.(mp3|flac|ogg|wav|m4a|aac|opus|alac|wma)$`)
	reVideoHint   = regexp.MustCompile(`(?i)\b(1080p|720p|480p|2160p|4k|bluray|web-dl|webrip|hdtv|x264|x265|hevc|h264|h265)\b`)
	reAudioHint   = regexp.MustCompile(`(?i)\b(flac|mp3|320kbps|256kbps|192kbps|lossless|hi-?res|24bit|discography|album|ost|soundtrack)\b`)
	reNeverPlay   = regexp.MustCompile(`(?i)\.(epub|pdf|mobi|cbr|cbz|zip|rar|7z|tar|gz|iso|exe|dmg)$`)
	reNeverPlayTg = regexp.MustCompile(`(?i)\b(ebook|audiobook[. ]?pdf|programs?|software|game[. ]?iso)\b`)
)

// MediaKind is "video" | "audio" | "other". "video" is the default when the
// classifier has no clear signal — the PlayerModal <video> plays audio too,
// so nothing is lost; AudioBar does not render video, though. Mirror
// detectKind() from playable.ts.
type MediaKind string

const (
	KindVideo MediaKind = "video"
	KindAudio MediaKind = "audio"
	KindOther MediaKind = "other"
)

// DetectKind decides between audio / video based on the title + Jackett
// category. Same fallback order the frontend used (ext > category >
// hint > default).
func DetectKind(title string, categoryID int) MediaKind {
	if reAudioExt.MatchString(title) {
		return KindAudio
	}
	if reVideoExt.MatchString(title) {
		return KindVideo
	}
	if audioCategories[categoryID] {
		return KindAudio
	}
	if videoCategories[categoryID] {
		return KindVideo
	}
	if reAudioHint.MatchString(title) {
		return KindAudio
	}
	if reVideoHint.MatchString(title) {
		return KindVideo
	}
	return KindVideo
}

// IsPlayable returns true if the player can probably play this release.
// Empty magnet → false. Hard rejections (epub/zip/iso/ebook).
// Otherwise uses an allowlist of categories/exts/hints with a "true"
// fallback (better to offer Play and let the decoder complain than to hide).
func IsPlayable(title string, categoryID int, magnetURI string, resolution string) bool {
	if magnetURI == "" {
		return false
	}
	if reNeverPlay.MatchString(title) {
		return false
	}
	if reNeverPlayTg.MatchString(title) {
		return false
	}
	if videoCategories[categoryID] || audioCategories[categoryID] {
		return true
	}
	if resolution != "" {
		return true
	}
	if reVideoExt.MatchString(title) || reAudioExt.MatchString(title) {
		return true
	}
	if reVideoHint.MatchString(title) || reAudioHint.MatchString(title) {
		return true
	}
	return true
}
