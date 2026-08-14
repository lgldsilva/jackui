package httpshared

import (
	"fmt"
	"strings"
)

// SanitizeForLog removes control characters that could be used for log
// injection (newlines, carriage returns, tabs and NUL) and trims the value
// to a reasonable length so user-provided input cannot pollute log entries.
// It is intended for values that originate from request bodies, URLs, or
// external systems before they are written to plain-text or structured logs.
func SanitizeForLog(v string) string {
	const maxLen = 512
	if len(v) > maxLen {
		v = v[:maxLen]
	}
	v = strings.ReplaceAll(v, "\x00", "")
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "␊")
	v = strings.ReplaceAll(v, "\t", "␉")
	return v
}

// SanitizeInt formats an int for logging. It exists so numeric request
// parameters can be logged through the same sanitization path used for strings.
func SanitizeInt(v int) string {
	return fmt.Sprintf("%d", v)
}

// SanitizeIntSlice formats a slice of ints for logging. The rendered value is
// passed through SanitizeForLog so any embedded formatting cannot be exploited.
func SanitizeIntSlice(v []int) string {
	return SanitizeForLog(fmt.Sprintf("%v", v))
}

// SanitizeIntPtr formats a pointer to int for logging, returning "<nil>" when
// the pointer is nil.
func SanitizeIntPtr(v *int) string {
	if v == nil {
		return "<nil>"
	}
	return SanitizeInt(*v)
}
