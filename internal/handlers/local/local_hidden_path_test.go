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
		// Curtain bypass regression: the gate compared the raw ?path= while the
		// resolver cleaned it, so these dot-prefixed / redundant-slash spellings
		// slipped past yet resolved into the hidden folder. normLocalRel closes it.
		{"dot-slash prefix bypass", "./secret", map[string]bool{"secret": true}, true},
		{"double-slash bypass", ".//secret/x.mkv", map[string]bool{"secret": true}, true},
		{"dot-slash deep-link bypass", "./secret/sub/x.mkv", map[string]bool{"secret": true}, true},
		{"redundant slash inside", "a//secret/x.mkv", map[string]bool{"a/secret": true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := localPathHidden(tc.path, tc.hidden); got != tc.want {
				t.Fatalf("localPathHidden(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
