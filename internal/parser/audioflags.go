package parser

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// AudioFlags holds the audio-language signals detected in a release title
// (docs/PLAN_PTBR_SUBS_DUAL.md §5.5).
type AudioFlags struct {
	Dual      bool `json:"dual"`
	Dub       bool `json:"dub"`
	PTBR      bool `json:"ptbr"`
	Multi     bool `json:"multi"`
	Legendado bool `json:"legendado"`
	Latino    bool `json:"latino"`
}

// reTrailingGroup mirrors the group regex in quality.go: the release group is
// the trailing "-NAME" tag at the end of a title.
var reTrailingGroup = regexp.MustCompile(`-([A-Za-z0-9_]{2,20})\s*$`)

// DetectAudioFlags reports which audio-language flags a release title carries.
//
// Matching is case-insensitive and token-bounded: a flag must be its own token
// (tokens are delimited by '.', '_', '-', '[', ']', '(', ')' and whitespace),
// so words and group names that merely CONTAIN a flag substring never match —
// "DUALBOX", "DUALAUDIO" and "Dubstep" are not flags — while a group suffix
// attached with '-' ("DUAL-PiA", "DUAL-SiGLA") still lets "DUAL" match, since
// the suffix is a separate token.
//
// Locked semantics (docs/PLAN_PTBR_SUBS_DUAL.md §5.5):
//   - dual / dual audio → Dual ONLY (does NOT imply PTBR)
//   - dublado / dub → Dub; a trailing release group named "-DUB" is a group
//     tag, not a flag (same false-group spirit as looksLikeFalseGroup)
//   - pt-br / ptbr / pt br / portugues / português (with or without accent) → PTBR
//   - legendado / legendas → Legendado
//   - multi (scene tag MULTi, usually FR/EN) → Multi ONLY, never Dual/PTBR
//   - latino / latina (Spanish audio) → Latino ONLY, never Dual/PTBR/Dub
func DetectAudioFlags(title string) AudioFlags {
	// Scraped titles can carry NFD-decomposed accents (e.g. "PORTUGUE\u0302S",
	// the classic macOS form); normalize to NFC so accented tokens still match.
	title = norm.NFC.String(title)
	tokens := tokenizeRelease(title)
	// "Movie.2023.1080p.BluRay.x264-DUB" ends with a release group named
	// "DUB": that occurrence is a group tag and must not count as a flag.
	// Mid-title DUB tags (e.g. "Movie.DUB.1080p-GROUP") still count.
	dubGroupAtEnd := trailingDubReleaseGroup(title)

	var flags AudioFlags
	for i, tok := range tokens {
		switch tok {
		case "dual":
			flags.Dual = true
		case "dublado":
			flags.Dub = true
		case "dub":
			if i == len(tokens)-1 && dubGroupAtEnd {
				continue
			}
			flags.Dub = true
		case "ptbr", "portugues", "português":
			flags.PTBR = true
		case "pt":
			// "PT.BR" (the most common BR scene form), "PT-BR", "PT BR".
			if i+1 < len(tokens) && tokens[i+1] == "br" {
				flags.PTBR = true
			}
		case "legendado", "legendas":
			flags.Legendado = true
		case "multi":
			flags.Multi = true
		case "latino", "latina":
			flags.Latino = true
		}
	}
	return flags
}

// tokenizeRelease splits a title into lowercase tokens on the scene/P2P
// separators ('.', '_', '-', '[', ']', '(', ')' and any Unicode whitespace,
// including NBSP).
func tokenizeRelease(title string) []string {
	parts := strings.FieldsFunc(title, isFlagSeparator)
	tokens := make([]string, len(parts))
	for i, p := range parts {
		tokens[i] = strings.ToLower(p)
	}
	return tokens
}

// isFlagSeparator reports whether r separates tokens in a release title: the
// explicit scene/P2P punctuation or any Unicode whitespace (unicode.IsSpace
// covers NBSP and \v/\f seen in scraped titles).
func isFlagSeparator(r rune) bool {
	switch r {
	case '.', '_', '-', '[', ']', '(', ')':
		return true
	}
	return unicode.IsSpace(r)
}

// trailingDubReleaseGroup reports whether the title ends with a release-group
// tag named "DUB" (e.g. "Movie.2023.1080p.BluRay.x264-DUB"). Trailing
// separators ("x264-DUB.", "x264-DUB]") are dropped by tokenization, so they
// are trimmed here too, keeping the guard in sync with the token view.
func trailingDubReleaseGroup(title string) bool {
	m := reTrailingGroup.FindStringSubmatch(strings.TrimRightFunc(title, isFlagSeparator))
	return len(m) > 1 && strings.EqualFold(m[1], "dub")
}
