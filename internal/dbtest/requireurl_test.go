package dbtest

import "testing"

type fakeTB struct{ skipped, fataled bool }

func (f *fakeTB) Helper()               {}
func (f *fakeTB) Skipf(string, ...any)  { f.skipped = true }
func (f *fakeTB) Fatalf(string, ...any) { f.fataled = true }

func TestRequireURL(t *testing.T) {
	cases := []struct {
		name        string
		url         string
		ci          bool
		skip, fatal bool
	}{
		{"set, local", "postgres://x", false, false, false},
		{"set, ci", "postgres://x", true, false, false},
		{"unset, local skips", "", false, true, false},
		{"unset, ci fails", "", true, false, true},
	}
	for _, c := range cases {
		f := &fakeTB{}
		got := requireURL(f, c.url, c.ci)
		if f.skipped != c.skip || f.fataled != c.fatal || (c.url != "" && got != c.url) {
			t.Errorf("%s: skipped=%v fataled=%v got=%q", c.name, f.skipped, f.fataled, got)
		}
	}
}
