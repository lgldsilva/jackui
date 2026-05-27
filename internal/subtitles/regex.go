package subtitles

import "regexp"

// arrowRe matches an SRT timing line (any line with "-->").
var arrowRe = regexp.MustCompile(`(?m)^.*-->.*$`)
