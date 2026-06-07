package version

import (
	"runtime"
	"testing"
)

func TestGet_Defaults(t *testing.T) {
	got := Get()
	if got.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", got.GoVersion, runtime.Version())
	}
	// Version/Commit carry their package defaults unless injected via ldflags.
	if got.Version == "" || got.Commit == "" {
		t.Errorf("Version/Commit should never be empty: %+v", got)
	}
}

func TestNormaliseBuildTime(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"epoch", "1717800000", "2024-06-07T22:40:00Z"},
		{"zero", "0", "1970-01-01T00:00:00Z"},
		{"unknown passthrough", "unknown", "unknown"},
		{"rfc3339 passthrough", "2026-06-07T20:40:48Z", "2026-06-07T20:40:48Z"},
		{"empty passthrough", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normaliseBuildTime(tc.in); got != tc.want {
				t.Errorf("normaliseBuildTime(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestGet_NormalisesEpochBuildTime(t *testing.T) {
	orig := BuildTime
	defer func() { BuildTime = orig }()
	BuildTime = "1717800000"
	if got := Get().BuildTime; got != "2024-06-07T22:40:00Z" {
		t.Errorf("BuildTime = %q, want normalised RFC3339", got)
	}
}
