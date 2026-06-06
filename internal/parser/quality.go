// Package parser extracts release metadata from torrent titles.
// Designed for scene/p2p naming conventions: e.g. "Show.S01E02.1080p.BluRay.x265-GROUP".
package parser

import (
	"regexp"
	"strings"
	"time"
)

// Quality holds parsed release metadata. Empty fields mean "not detected".
type Quality struct {
	Resolution string   `json:"resolution,omitempty"` // 480p, 720p, 1080p, 2160p, 4K
	Codec      string   `json:"codec,omitempty"`      // x265, x264, AV1
	Source     string   `json:"source,omitempty"`     // BluRay, WEB-DL, WEBRip, HDTV, CAM, TS, DVDRip
	Audio      []string `json:"audio,omitempty"`      // DTS, EAC3, TrueHD, AC3, Atmos, AAC
	Group      string   `json:"group,omitempty"`      // YIFY, RARBG, etc.
	Year       int      `json:"year,omitempty"`       // detected release year
	Season     int      `json:"season,omitempty"`     // S__ when matched
	Episode    int      `json:"episode,omitempty"`    // E__ when matched
	HDR        bool     `json:"hdr,omitempty"`
	DolbyVis   bool     `json:"dv,omitempty"`
	TenBit     bool     `json:"tenBit,omitempty"`
	Repack     bool     `json:"repack,omitempty"`
	Proper     bool     `json:"proper,omitempty"`
	Extended   bool     `json:"extended,omitempty"`
	Remux      bool     `json:"remux,omitempty"`
	Multi      bool     `json:"multi,omitempty"`     // multi-audio
	Dubbed     bool     `json:"dubbed,omitempty"`
	Subbed     bool     `json:"subbed,omitempty"`
}

var (
	reResolution = regexp.MustCompile(`(?i)\b(2160p|1080p|720p|480p|4k|uhd)\b`)
	reCodec      = regexp.MustCompile(`(?i)\b(x265|x\.265|h\.?265|hevc|x264|x\.264|h\.?264|avc|av1|xvid|divx)\b`)
	reSource     = regexp.MustCompile(`(?i)\b(bluray|blu-ray|brrip|bdrip|web-?dl|webrip|web|hdrip|hdtv|hdcam|hd-?ts|hd-?cam|dvdrip|dvdscr|dvd|cam|telesync|ts|workprint|wp)\b`)
	reAudio      = regexp.MustCompile(`(?i)\b(dts-?hd|dts:?x|dts|truehd|atmos|eac3|ac3|aac|opus|mp3|flac|dd5\.1|dd\+|ddp5\.1)\b`)
	reGroup      = regexp.MustCompile(`-([A-Za-z0-9_]{2,20})\s*$`)
	reYear       = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	// A year inside parentheses is the release year even when the title itself
	// contains a number that looks like a year ("Blade Runner 2049 (2017)").
	reYearParen = regexp.MustCompile(`\((19\d{2}|20\d{2})\)`)
	reSE        = regexp.MustCompile(`(?i)[Ss](\d{1,2})[Ee](\d{1,3})`)
	// Season-only: "Season 3" / "S03". The "S" form requires digits IMMEDIATELY
	// after (no space) so the bare "s" in "Ocean's 11" doesn't read as Season 11.
	reSeasonOnly = regexp.MustCompile(`(?i)\b(?:Season[ ._]?|S)(\d{1,2})\b`)
	reHDR        = regexp.MustCompile(`(?i)\b(hdr10\+?|hdr|hlg)\b`)
	reDolby      = regexp.MustCompile(`(?i)\b(dv|dolby[. _-]?vision)\b`)
	re10bit      = regexp.MustCompile(`(?i)\b10[ .-]?bit\b`)
	reRepack     = regexp.MustCompile(`(?i)\brepack\b`)
	reProper     = regexp.MustCompile(`(?i)\bproper\b`)
	reExtended   = regexp.MustCompile(`(?i)\b(extended|director'?s[. ]?cut|theatrical[. ]?cut|unrated)\b`)
	reRemux      = regexp.MustCompile(`(?i)\bremux\b`)
	reMulti      = regexp.MustCompile(`(?i)\b(multi|dual[. ]?audio|2audio)\b`)
	reDubbed     = regexp.MustCompile(`(?i)\b(dubbed|dublado|dub)\b`)
	reSubbed     = regexp.MustCompile(`(?i)\b(subbed|legendado|subs?)\b`)
)

// Parse extracts what it can from a release title. Always returns a value (never nil).
func Parse(title string) Quality {
	seasonVal, episodeVal := parseSeasonAndEpisode(title)
	return Quality{
		Resolution: parseResolution(title),
		Codec:      parseCodec(title),
		Source:     parseSource(title),
		Audio:      parseAudioTracks(title),
		Group:      parseGroup(title),
		Year:       parseYear(title),
		Season:     seasonVal,
		Episode:    episodeVal,
		HDR:        reHDR.MatchString(title),
		DolbyVis:   reDolby.MatchString(title),
		TenBit:     re10bit.MatchString(title),
		Repack:     reRepack.MatchString(title),
		Proper:     reProper.MatchString(title),
		Extended:   reExtended.MatchString(title),
		Remux:      reRemux.MatchString(title),
		Multi:      reMulti.MatchString(title),
		Dubbed:     reDubbed.MatchString(title),
		Subbed:     reSubbed.MatchString(title),
	}
}

func parseResolution(title string) string {
	m := reResolution.FindString(title)
	if m == "" {
		return ""
	}
	v := strings.ToLower(m)
	switch v {
	case "4k", "uhd":
		return "2160p"
	default:
		return v
	}
}

func parseCodec(title string) string {
	m := reCodec.FindString(title)
	if m == "" {
		return ""
	}
	v := strings.ToLower(m)
	v = strings.ReplaceAll(v, ".", "")
	switch {
	case strings.Contains(v, "265") || v == "hevc":
		return "x265"
	case strings.Contains(v, "264") || v == "avc":
		return "x264"
	case v == "av1":
		return "AV1"
	default:
		return strings.ToUpper(v)
	}
}

func parseSource(title string) string {
	if m := reSource.FindString(title); m != "" {
		return normalizeSource(m)
	}
	return ""
}

func parseAudioTracks(title string) []string {
	matches := reAudio.FindAllString(title, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, m := range matches {
		n := normalizeAudio(m)
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func parseGroup(title string) string {
	m := reGroup.FindStringSubmatch(title)
	if len(m) <= 1 {
		return ""
	}
	candidate := m[1]
	if looksLikeFalseGroup(candidate) {
		return ""
	}
	return candidate
}

func parseYear(title string) int {
	// A parenthesized year wins — it's the canonical release year even when the
	// title carries a year-like number ("Blade Runner 2049 (2017)" → 2017).
	if m := reYearParen.FindStringSubmatch(title); len(m) > 1 {
		return atoiSafe(m[1])
	}
	all := reYear.FindAllString(title, -1)
	if len(all) == 0 {
		return 0
	}
	// First PLAUSIBLE year (1900..next year). The old code took the max, which
	// picked a sequel number in the title ("2049") or a trailing dual-audio year
	// over the real release year. A far-future number that's part of the title
	// (e.g. "2049") is not a release year → leave year unknown so TMDB matches by
	// title instead of filtering to a bogus year.
	maxPlausible := time.Now().Year() + 1
	for _, y := range all {
		if n := atoiSafe(y); n >= 1900 && n <= maxPlausible {
			return n
		}
	}
	return 0
}

func parseSeasonAndEpisode(title string) (int, int) {
	if m := reSE.FindStringSubmatch(title); len(m) == 3 {
		return atoiSafe(m[1]), atoiSafe(m[2])
	}
	if m := reSeasonOnly.FindStringSubmatch(title); len(m) > 1 {
		return atoiSafe(m[1]), 0
	}
	return 0, 0
}

func normalizeSource(s string) string {
	v := strings.ToLower(strings.ReplaceAll(s, "-", ""))
	v = strings.ReplaceAll(v, ".", "")
	switch {
	case strings.Contains(v, "bluray") || v == "bdrip" || v == "brrip":
		return "BluRay"
	case strings.Contains(v, "webdl"):
		return "WEB-DL"
	case v == "webrip" || v == "web":
		return "WEBRip"
	case v == "hdtv":
		return "HDTV"
	case v == "hdrip":
		return "HDRip"
	case v == "dvdrip" || v == "dvd":
		return "DVDRip"
	case v == "dvdscr":
		return "DVDScr"
	case v == "cam" || v == "hdcam":
		return "CAM"
	case v == "ts" || v == "telesync" || v == "hdts":
		return "TS"
	case v == "wp" || v == "workprint":
		return "Workprint"
	}
	return strings.ToUpper(s)
}

func normalizeAudio(a string) string {
	v := strings.ToLower(strings.ReplaceAll(a, "-", ""))
	v = strings.ReplaceAll(v, ".", "")
	switch {
	case strings.Contains(v, "dtshd"):
		return "DTS-HD"
	case strings.Contains(v, "dtsx"):
		return "DTS:X"
	case v == "dts":
		return "DTS"
	case v == "truehd":
		return "TrueHD"
	case v == "atmos":
		return "Atmos"
	case strings.HasPrefix(v, "eac3") || strings.HasPrefix(v, "ddp") || strings.HasPrefix(v, "dd+"):
		return "EAC3"
	case v == "ac3" || v == "dd51":
		return "AC3"
	case v == "aac":
		return "AAC"
	case v == "opus":
		return "Opus"
	case v == "mp3":
		return "MP3"
	case v == "flac":
		return "FLAC"
	}
	return strings.ToUpper(a)
}

func looksLikeFalseGroup(s string) bool {
	u := strings.ToUpper(s)
	switch u {
	case "X264", "X265", "HEVC", "AVC", "AAC", "AC3", "DTS", "MP4", "MKV":
		return true
	}
	if _, err := atoiStrict(s); err == nil && len(s) == 4 {
		return true // year
	}
	return false
}

func atoiSafe(s string) int {
	n, _ := atoiStrict(s)
	return n
}

func atoiStrict(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errInvalid
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

var errInvalid = stringError("not a number")

type stringError string

func (e stringError) Error() string { return string(e) }
