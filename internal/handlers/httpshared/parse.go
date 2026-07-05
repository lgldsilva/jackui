package httpshared

import "strconv"

// ParseIntOr parses s as an int, returning def when s is empty or invalid.
func ParseIntOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
