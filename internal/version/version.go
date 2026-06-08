// Package version exposes build metadata (git commit, build time, version).
// The values are injected at build time via -ldflags "-X ...":
//
//	go build -ldflags "\
//	  -X github.com/lgldsilva/jackui/internal/version.Commit=$(git rev-parse HEAD) \
//	  -X github.com/lgldsilva/jackui/internal/version.BuildTime=$(date +%s) \
//	  -X github.com/lgldsilva/jackui/internal/version.Version=$(git describe --tags --always)"
//
// Without ldflags (e.g. `go run` in dev) they keep the defaults below, so the
// /status endpoint still answers — it just reports "dev"/"unknown".
package version

import (
	"runtime"
	"strconv"
	"time"
)

// Injected via -ldflags. Defaults cover local `go run`/`go test` (no ldflags).
var (
	// Commit is the full git SHA the binary was built from.
	Commit = "unknown"
	// BuildTime is the build instant. Accepts a unix epoch (what the Docker
	// BUILD_TIMESTAMP arg carries) or an RFC3339 string; Info() normalises it.
	BuildTime = "unknown"
	// Version is a human tag (e.g. `git describe --tags --always`).
	Version = "dev"
)

// Info is the build metadata served by GET /status.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
	GoVersion string `json:"goVersion"`
}

// Get returns the build metadata with BuildTime normalised to RFC3339 when it
// was injected as a unix epoch (the Dockerfile passes `date +%s`).
func Get() Info {
	return Info{
		Version:   Version,
		Commit:    Commit,
		BuildTime: normaliseBuildTime(BuildTime),
		GoVersion: runtime.Version(),
	}
}

// normaliseBuildTime turns a unix-epoch string into RFC3339 (UTC). Anything
// that isn't a plain epoch is returned untouched (already a date, or "unknown").
func normaliseBuildTime(s string) string {
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return s
	}
	return time.Unix(secs, 0).UTC().Format(time.RFC3339)
}
