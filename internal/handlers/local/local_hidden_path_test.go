package local

import "testing"

func TestLocalPathHidden(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		hidden map[string]bool
		want   bool
	}{
		{"empty set", "a/b.mkv", nil, false},
		{"exact file match", "secret/x.mkv", map[string]bool{"secret/x.mkv": true}, true},
		{"ancestor folder hidden", "secret/sub/x.mkv", map[string]bool{"secret": true}, true},
		{"intermediate folder hidden", "a/secret/x.mkv", map[string]bool{"a/secret": true}, true},
		{"no match", "public/x.mkv", map[string]bool{"secret": true}, false},
		{"sibling prefix not matched", "secretly/x.mkv", map[string]bool{"secret": true}, false},
		{"trailing slash tolerated", "secret/", map[string]bool{"secret": true}, true},
		{"root file not hidden", "x.mkv", map[string]bool{"secret": true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := localPathHidden(tc.path, tc.hidden); got != tc.want {
				t.Fatalf("localPathHidden(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
