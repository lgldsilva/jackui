package parser

import "regexp"

// Jackett categories que tipicamente contêm mídia tocável. Espelha
// VIDEO_CATEGORIES / AUDIO_CATEGORIES de web/src/lib/playable.ts — a fonte
// de verdade vive aqui agora; o frontend só lê os campos retornados.
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

// MediaKind é "video" | "audio" | "other". "video" é o default quando o
// classificador não tem sinal claro — o <video> do PlayerModal toca áudio
// também, então não-perda; já o AudioBar não renderiza vídeo. Espelhe
// detectKind() do playable.ts.
type MediaKind string

const (
	KindVideo MediaKind = "video"
	KindAudio MediaKind = "audio"
	KindOther MediaKind = "other"
)

// DetectKind decide entre audio / video com base no título + categoria
// Jackett. Mesma ordem de fallback que o frontend usava (ext > category >
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

// IsPlayable retorna true se o player provavelmente consegue tocar este
// release. Magnet vazio → false. Rejeições duras (epub/zip/iso/ebook).
// Caso contrário usa allowlist de categorias/exts/hints com fallback "true"
// (preferir oferecer Play e deixar o decoder reclamar do que esconder).
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
