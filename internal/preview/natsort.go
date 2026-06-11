package preview

import (
	"strings"
	"unicode"
)

// NaturalLess compares two strings "the way humans expect": digit runs compare
// numerically, the rest case-insensitively. Needed for comic pages — plain
// lexicographic sort puts "page10.jpg" before "page2.jpg" and scrambles the
// reading order of most CBZ files in the wild.
func NaturalLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	for la != "" && lb != "" {
		if isDigit(la[0]) && isDigit(lb[0]) {
			na, ra := takeNumber(la)
			nb, rb := takeNumber(lb)
			if na != nb {
				return numLess(na, nb)
			}
			la, lb = ra, rb
			continue
		}
		if la[0] != lb[0] {
			return la[0] < lb[0]
		}
		la, lb = la[1:], lb[1:]
	}
	return len(la) < len(lb)
}

func isDigit(c byte) bool { return unicode.IsDigit(rune(c)) }

// takeNumber splits a leading digit run (zeros trimmed) from the rest.
func takeNumber(s string) (digits, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	digits = strings.TrimLeft(s[:i], "0")
	if digits == "" {
		digits = "0"
	}
	return digits, s[i:]
}

// numLess compares two non-negative integers given as (zero-trimmed) decimal
// strings without parsing — page numbers can exceed int64 in hostile names.
func numLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
