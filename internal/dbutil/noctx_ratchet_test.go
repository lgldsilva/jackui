package dbutil

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Ratchet for issue #223 (Wave 5): database access must go through the
// *Context variants so request cancellation and timeouts reach Postgres. This
// test counts the remaining ctx-less calls per file under internal/ (non-test
// code) and fails when any file has MORE than the checked-in baseline, or a new
// file appears with any. The debt can only go down.
//
// Lowering the baseline after converting a store:
//
//	UPDATE_NOCTX_BASELINE=1 go test ./internal/dbutil -run TestNoContextDBCallRatchet
//
// then commit noctx_baseline.txt. A per-file baseline (not one total) means a
// conversion in one package cannot hide a regression in another.
const baselineFile = "noctx_baseline.txt"

// ctx-less database/sql calls. HTTP-ish lookalikes (gin c.Query, url.Query)
// are excluded by receiver name; rebind.go defines the wrappers themselves
// and dbtest is test plumbing.
var (
	noCtxCall    = regexp.MustCompile(`\.(Query|QueryRow|Exec|Prepare)\(|\.Begin\(\)`)
	notDBReceive = regexp.MustCompile(`\b(c|ctx|u|su|res)\.Query\(|URL\.Query\(`)
	ratchetSkip  = map[string]bool{"dbutil/rebind.go": true}
)

func countNoCtxCalls(t *testing.T, root string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == ".git" || rel == "dbtest" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || ratchetSkip[rel] {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(line[:i], `"`) {
				line = line[:i] // comment-only lines and trailing // comments
			}
			line = notDBReceive.ReplaceAllString(line, "")
			counts[rel] += len(noCtxCall.FindAllString(line, -1))
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range counts {
		if v == 0 {
			delete(counts, k)
		}
	}
	return counts
}

func readBaseline(t *testing.T) map[string]int {
	t.Helper()
	b, err := os.ReadFile(baselineFile)
	if err != nil {
		t.Fatalf("read %s: %v", baselineFile, err)
	}
	out := map[string]int{}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		var path string
		var n int
		parts := strings.Fields(ln)
		if len(parts) != 2 {
			t.Fatalf("bad baseline line %q", ln)
		}
		path = parts[0]
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			t.Fatalf("bad baseline line %q: %v", ln, err)
		}
		out[path] = n
	}
	return out
}

func writeBaseline(t *testing.T, counts map[string]int) {
	t.Helper()
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString("# Ctx-less database calls per file (see noctx_ratchet_test.go). Only ever lower these.\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s %d\n", k, counts[k])
	}
	if err := os.WriteFile(baselineFile, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNoContextDBCallRatchet(t *testing.T) {
	got := countNoCtxCalls(t, "..")
	if os.Getenv("UPDATE_NOCTX_BASELINE") == "1" {
		writeBaseline(t, got)
		return
	}
	base := readBaseline(t)
	for file, n := range got {
		if n > base[file] {
			t.Errorf("%s: %d ctx-less DB calls, baseline %d - use QueryContext/QueryRowContext/ExecContext/BeginTx and take ctx from the caller. Do NOT raise the baseline; UPDATE_NOCTX_BASELINE=1 go test ./internal/dbutil -run TestNoContextDBCallRatchet is only for when a count went DOWN", file, n, base[file])
		}
	}
	for file, n := range base {
		if got[file] < n {
			t.Logf("%s improved (%d -> %d): lower the baseline with UPDATE_NOCTX_BASELINE=1", file, n, got[file])
		}
	}
}

func TestCountNoCtxCallsIgnoresTestsAndLookalikes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\nfunc f(){ db.Query(q); db.Exec(q); db.QueryContext(ctx,q); tx, _ := db.Begin(); c.Query(\"x\"); u.Query() }\n")
	write("c.go", "package a\n// db.Exec(q) in a comment\nfunc h(){ db.Exec(q) // db.Query(q)\n}\n")
	write("a_test.go", "package a\nfunc g(){ db.Exec(q) }\n")
	got := countNoCtxCalls(t, dir)
	if got["a.go"] != 3 || got["c.go"] != 1 || len(got) != 2 {
		t.Fatalf("got %v, want a.go:3 and c.go:1 only", got)
	}
}
