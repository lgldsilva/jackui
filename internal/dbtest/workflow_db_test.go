package dbtest

// Guard for the wiring that dbtest.requireURL assumes: any CI job that runs
// tests under CI=true must provide a test database. A nightly gremlins job
// once ran "go test ./..." without one and died during coverage gathering
// (PR #293), but no PR-level job exercised that workflow.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const testDatabaseURLKey = "JACKUI_TEST_DATABASE_URL"

type workflowDoc struct {
	Env  map[string]any         `yaml:"env"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Env      map[string]any `yaml:"env"`
	Services map[string]any `yaml:"services"`
	Steps    []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	Run string         `yaml:"run"`
	Env map[string]any `yaml:"env"`
}

var (
	goTestLine   = regexp.MustCompile(`(^|\s)go test(\s|$)`)
	gremlinsLine = regexp.MustCompile(`gremlins.*unleash`)
)

func hasTestDatabaseURL(env map[string]any) bool {
	value, ok := env[testDatabaseURLKey]
	if !ok || value == nil {
		return false
	}
	if s, isString := value.(string); isString {
		return strings.TrimSpace(s) != ""
	}
	return true
}

// runsTests reports whether a single run line executes tests. Compile-only
// benchmark runs (-run '^$') never reach dbtest, so they are exempt.
func runsTests(line string) bool {
	if strings.Contains(line, `-run '^$'`) || strings.Contains(line, `-run "^$"`) {
		return false
	}
	return goTestLine.MatchString(line) || gremlinsLine.MatchString(line)
}

// workflowDBViolations returns one message per offending run line, sorted for
// stable output. A job with no steps (reusable workflow call) is skipped.
func workflowDBViolations(name string, doc workflowDoc) []string {
	topURL := hasTestDatabaseURL(doc.Env)
	var violations []string
	for jobName, job := range doc.Jobs {
		if len(job.Steps) == 0 {
			continue
		}
		_, hasPostgres := job.Services["postgres"]
		jobURL := topURL || hasTestDatabaseURL(job.Env)
		for _, step := range job.Steps {
			for _, line := range strings.Split(step.Run, "\n") {
				if !runsTests(line) {
					continue
				}
				kind := "go test"
				if gremlinsLine.MatchString(line) {
					kind = "gremlins unleash"
				}
				var missing []string
				if !hasPostgres {
					missing = append(missing, "postgres service")
				}
				if !jobURL && !hasTestDatabaseURL(step.Env) {
					missing = append(missing, testDatabaseURLKey)
				}
				if len(missing) != 0 {
					violations = append(violations, fmt.Sprintf(
						"FAIL: %s: job %q runs %s but missing %s",
						name, jobName, kind, strings.Join(missing, " and "),
					))
				}
			}
		}
	}
	sort.Strings(violations)
	return violations
}

func TestWorkflowTestDatabaseWiring(t *testing.T) {
	var paths []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		matches, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", pattern))
		if err != nil {
			t.Fatalf("glob workflows (%s): %v", pattern, err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		t.Fatalf("no workflow files found — CI guard would pass vacuously")
	}
	parsed := 0
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		var doc workflowDoc
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Errorf("parse %s: %v", path, err)
			continue
		}
		parsed++
		for _, violation := range workflowDBViolations(filepath.Base(path), doc) {
			t.Error(violation)
		}
	}
	if parsed != len(paths) {
		t.Fatalf("parsed %d of %d workflow files", parsed, len(paths))
	}
}

func TestWorkflowTestDatabaseWiringFixtures(t *testing.T) {
	const gremlinsStep = `      - name: unleash
        run: |
          go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash \
            --output mutation-report.json
`
	cases := []struct {
		name        string
		doc         string
		want        int
		wantMissing string
	}{
		{
			name: "pre-fix: no service and no URL",
			doc: `jobs:
  gremlins:
    steps:
` + gremlinsStep,
			want:        1,
			wantMissing: "postgres service and " + testDatabaseURLKey,
		},
		{
			name: "service kept, URL stripped",
			doc: `jobs:
  gremlins:
    services:
      postgres:
        image: postgres:16.15-bookworm
    steps:
` + gremlinsStep,
			want:        1,
			wantMissing: testDatabaseURLKey,
		},
		{
			name: "URL kept, service stripped",
			doc: `jobs:
  gremlins:
    env:
      JACKUI_TEST_DATABASE_URL: postgres://jackui@127.0.0.1:5432/jackui_test
    steps:
` + gremlinsStep,
			want:        1,
			wantMissing: "postgres service",
		},
		{
			name: "both wired at job level",
			doc: `jobs:
  gremlins:
    services:
      postgres:
        image: postgres:16.15-bookworm
    env:
      JACKUI_TEST_DATABASE_URL: postgres://jackui@127.0.0.1:5432/jackui_test
    steps:
` + gremlinsStep,
			want: 0,
		},
		{
			name: "top-level URL only",
			doc: `env:
  JACKUI_TEST_DATABASE_URL: postgres://jackui@127.0.0.1:5432/jackui_test
jobs:
  gremlins:
    steps:
` + gremlinsStep,
			want:        1,
			wantMissing: "postgres service",
		},
		{
			name: "top-level URL with service",
			doc: `env:
  JACKUI_TEST_DATABASE_URL: postgres://jackui@127.0.0.1:5432/jackui_test
jobs:
  gremlins:
    services:
      postgres:
        image: postgres:16.15-bookworm
    steps:
` + gremlinsStep,
			want: 0,
		},
		{
			name: "URL on the flagged step",
			doc: `jobs:
  gremlins:
    services:
      postgres:
        image: postgres:16.15-bookworm
    steps:
      - name: unleash
        env:
          JACKUI_TEST_DATABASE_URL: postgres://jackui@127.0.0.1:5432/jackui_test
        run: |
          go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash
`,
			want: 0,
		},
		{
			name: "compile-only benchmark needs no database",
			doc: `jobs:
  bench:
    steps:
      - name: benchmark
        run: |
          go test -run '^$' -bench . -benchmem -count 6 ./internal/config/
`,
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var doc workflowDoc
			if err := yaml.Unmarshal([]byte(tc.doc), &doc); err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			got := workflowDBViolations("fixture.yml", doc)
			if len(got) != tc.want {
				t.Fatalf("got %d violations (%v), want %d", len(got), got, tc.want)
			}
			if tc.wantMissing != "" && !strings.HasSuffix(got[0], "but missing "+tc.wantMissing) {
				t.Errorf("got %q, want it to mention %q", got[0], tc.wantMissing)
			}
		})
	}
}
