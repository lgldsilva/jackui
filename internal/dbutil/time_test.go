package dbutil

import (
	"testing"
)

func TestParseTimeAcceptsBothSQLiteFormats(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // RFC3339 of expected value (empty = zero time)
	}{
		{"sqlite default", "2026-05-26 11:13:45", "2026-05-26T11:13:45Z"},
		{"sqlite with ms", "2026-05-26 11:13:45.123456", "2026-05-26T11:13:45.123456Z"},
		{"rfc3339", "2026-05-26T11:13:45Z", "2026-05-26T11:13:45Z"},
		{"rfc3339 nano", "2026-05-26T11:13:45.987654321Z", "2026-05-26T11:13:45.987654321Z"},
		{"empty string", "", ""},
		{"garbage", "not-a-date", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseTime(c.in)
			if c.want == "" {
				if !got.IsZero() {
					t.Errorf("expected zero time for %q, got %v", c.in, got)
				}
				return
			}
			if got.IsZero() {
				t.Errorf("got zero time for %q, want %s", c.in, c.want)
				return
			}
			if got.UTC().Format("2006-01-02T15:04:05.999999999Z") != c.want {
				t.Errorf("parse(%q) = %v, want %s", c.in, got.UTC(), c.want)
			}
		})
	}
}
