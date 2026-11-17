package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The file value under an env override survives a save; a value the admin
// changes at runtime is persisted even when the env overrides that field.
func TestSave_KeepsFileValueUnderEnvOverride(t *testing.T) {
	path := writeConfigFile(t, "jackett:\n  url: http://file-host:9117\n  api_key: file-key\nport: 8989\n")
	t.Setenv("JACKETT_API_KEY", "env-key")
	t.Setenv("JACKETT_URL", "http://env-host:9117")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Update(func(c *Config) { c.Jackett.URL = "http://admin-host:9117" })
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if !strings.Contains(got, "api_key: file-key") || strings.Contains(got, "env-key") {
		t.Errorf("api_key must keep the file value:\n%s", got)
	}
	if !strings.Contains(got, "url: http://admin-host:9117") {
		t.Errorf("runtime change not persisted:\n%s", got)
	}

	// The snapshots follow the save: changing the value back is a change too.
	cfg.Update(func(c *Config) { c.Jackett.URL = "http://env-host:9117" })
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !strings.Contains(got, "url: http://env-host:9117") {
		t.Errorf("second change not persisted:\n%s", got)
	}
}

// Env-provided AI provider keys live in a map: the provider entry the file
// never had must not appear, and a file entry keeps its own fields.
func TestSave_AIProviderKeysFromEnvStayOut(t *testing.T) {
	path := writeConfigFile(t, "ai:\n  enabled: false\n  providers:\n    ollama:\n      base_url: http://file-ollama/v1\n")
	t.Setenv("JACKUI_AI_ENABLED", "0") // no model discovery over the network
	t.Setenv("GROQ_API_KEY", "env-groq-key")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Providers["groq"].APIKey != "env-groq-key" {
		t.Fatal("env provider key must be effective in memory")
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if strings.Contains(got, "env-groq-key") || strings.Contains(got, "groq") {
		t.Errorf("env-only provider was persisted:\n%s", got)
	}
	if !strings.Contains(got, "base_url: http://file-ollama/v1") {
		t.Errorf("file provider lost:\n%s", got)
	}
}

// An absent auto_fetch key means ON; a save of something else must not turn it
// into an explicit false, and an explicit runtime false must stick.
func TestSave_AbsentAutoFetchStaysOn(t *testing.T) {
	path := writeConfigFile(t, "port: 8989\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Update(func(c *Config) { c.Port = 9000 })
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Subtitles.AutoFetch || reloaded.Port != 9000 {
		t.Fatalf("auto_fetch=%v port=%d, want true/9000", reloaded.Subtitles.AutoFetch, reloaded.Port)
	}

	s := reloaded.SubtitlesSnapshot()
	s.AutoFetch = false
	reloaded.SetSubtitles(s)
	if err := reloaded.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Subtitles.AutoFetch {
		t.Error("explicit auto_fetch=false was lost")
	}
}

func TestMergeFileLayer_Shapes(t *testing.T) {
	node := func(src string) *yaml.Node {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(src), &n); err != nil {
			t.Fatal(err)
		}
		return n.Content[0]
	}
	render := func(n *yaml.Node) string {
		if n == nil {
			return "<nil>"
		}
		out, err := yaml.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	cases := []struct{ name, cur, eff, file, want string }{
		{"unchanged scalar keeps file", "a: env", "a: env", "a: file", "a: file"},
		{"changed scalar wins", "a: new", "a: env", "a: file", "a: new"},
		{"unchanged without file value is dropped", "a: env\nb: 1", "a: env\nb: 0", "{}", "b: 1"},
		{"list is one value", "a: [1, 2]", "a: [1, 2]", "a: [1]", "a: [1]"},
		{"changed list wins", "a: [1, 3]", "a: [1, 2]", "a: [1]", "a: [1, 3]"},
		{"new key has no baseline", "a: 1\nb: 2", "a: 1", "a: 1", "a: 1\nb: 2"},
		{"file is not a mapping", "m: {k: v2}", "m: {k: v}", "m: 1", "m:\n    k: v2"},
		{"shape changed", "m: {k: v}", "m: 1", "m: 1", "m: {k: v}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := render(mergeFileLayer(node(tc.cur), node(tc.eff), node(tc.file)))
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
	if got := mergeFileLayer(node("a: 1"), nil, nil); render(got) != "a: 1" {
		t.Errorf("no baseline must return cur, got %q", render(got))
	}
}

func TestSave_ReplacesFileWithoutLeavingTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := &Config{Port: 1}
	for i := 0; i < 3; i++ {
		if err := cfg.Save(path); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		t.Errorf("unexpected files left behind: %v", entries)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestSave_KeepsSymlinkedConfig(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.yaml")
	link := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(real, []byte("port: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := (&Config{Port: 4242}).Save(link); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.yaml is no longer a symlink (err=%v)", err)
	}
	if !strings.Contains(readFile(t, real), "port: 4242") {
		t.Error("the symlink target was not updated")
	}
}

// A directory the process cannot create files in (or a single-file bind mount)
// still gets the config written, in place.
func TestSave_FallsBackToInPlaceWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("port: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	if err := (&Config{Port: 4242}).Save(path); err != nil {
		t.Fatalf("save into a read-only directory: %v", err)
	}
	if !strings.Contains(readFile(t, path), "port: 4242") {
		t.Error("in-place fallback did not write the config")
	}
}

// A single-file bind mount cannot be renamed over (EBUSY): the config is then
// written in place and the temp file removed.
func TestSave_RenameFailureFallsBackInPlace(t *testing.T) {
	orig := renameFile
	renameFile = func(string, string) error { return errors.New("device or resource busy") }
	t.Cleanup(func() { renameFile = orig })

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("port: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := (&Config{Port: 4242}).Save(path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, path), "port: 4242") {
		t.Error("in-place fallback did not write the config")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

// A path that is not a writable file is reported, with nothing left behind.
func TestSave_UnwritableTargetIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.MkdirAll(filepath.Join(path, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	err := (&Config{}).Save(path)
	if err == nil || !strings.Contains(err.Error(), "failed to write config") {
		t.Fatalf("err = %v, want a write failure", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

// A read-only config.yaml in a writable directory must stay untouched: a bare
// rename would silently replace it.
func TestSave_ReadOnlyFileIsNotReplaced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	path := writeConfigFile(t, "port: 1\n")
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if err := (&Config{Port: 4242}).Save(path); err == nil {
		t.Fatal("saving over a read-only file must fail")
	}
	if got := readFile(t, path); got != "port: 1\n" {
		t.Errorf("read-only file was replaced: %q", got)
	}
}

func TestFillTemp_ReportsWriteFailure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fillTemp(f, []byte("data"), 0600); err == nil {
		t.Error("writing to a closed file must fail")
	}
}

func TestWriteFileAtomic_TempWriteFailureLeavesOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	orig := fillTempFile
	fillTempFile = func(*os.File, []byte, os.FileMode) error { return errors.New("disk full") }
	t.Cleanup(func() { fillTempFile = orig })

	if err := writeFileAtomic(path, []byte("new"), 0600); err == nil {
		t.Fatal("want the temp-file error")
	}
	if got := readFile(t, path); got != "original" {
		t.Errorf("original file was touched: %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestEncodeFailuresAreReported(t *testing.T) {
	orig := toNode
	t.Cleanup(func() { toNode = orig })
	boom := errors.New("boom")

	t.Run("save", func(t *testing.T) {
		toNode = func(*Config) (*yaml.Node, error) { return nil, boom }
		defer func() { toNode = orig }()
		if err := (&Config{}).Save(filepath.Join(t.TempDir(), "c.yaml")); !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("load file layer", func(t *testing.T) {
		path := writeConfigFile(t, "port: 1\n")
		toNode = func(*Config) (*yaml.Node, error) { return nil, boom }
		defer func() { toNode = orig }()
		if _, err := Load(path); !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
		if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); !errors.Is(err, boom) {
			t.Errorf("fresh boot err = %v", err)
		}
	})
	t.Run("load effective layer", func(t *testing.T) {
		path := writeConfigFile(t, "port: 1\n")
		calls := 0
		toNode = func(c *Config) (*yaml.Node, error) {
			if calls++; calls == 2 {
				return nil, boom
			}
			return orig(c)
		}
		defer func() { toNode = orig }()
		if _, err := Load(path); !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
	})
}

// Run under -race: every runtime section is written through Update and read
// through its snapshot while Save marshals the whole struct.
func TestConfig_UpdateSnapshotSaveAreRaceFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	run := func(fn func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				fn(i)
			}
		}()
	}
	run(func(i int) {
		cfg.Update(func(c *Config) {
			c.Port = 9000 + i
			c.Jackett.URL, c.Jackett.APIKey = "http://j", "k"
			c.Stream.SeedTrackers = []string{"a", "b"}
			c.DownloadsQueue.MaxActive = i + 1
			c.External.Mounts = []ExternalMount{{Name: "m", Path: "/m"}}
			c.DownloadClients = []DownloadClient{{ID: "x"}}
		})
	})
	run(func(int) {
		_ = cfg.StreamSnapshot()
		_ = cfg.DownloadsQueueSnapshot()
		_ = cfg.MountsSnapshot()
		_ = cfg.DownloadClientsSnapshot()
		_ = cfg.PortSnapshot()
		_, _ = cfg.JackettSnapshot()
		_ = cfg.SubtitlesSnapshot()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if err := cfg.Save(path); err != nil {
				t.Errorf("save: %v", err)
			}
		}
	}()
	wg.Wait()

	if got := cfg.PortSnapshot(); got != 9199 {
		t.Errorf("port = %d, want 9199", got)
	}
	if url, key := cfg.JackettSnapshot(); url != "http://j" || key != "k" {
		t.Errorf("jackett = %q/%q", url, key)
	}
	if n := len(cfg.MountsSnapshot()) + len(cfg.DownloadClientsSnapshot()); n != 2 {
		t.Errorf("mounts+clients = %d, want 2", n)
	}
	if cfg.DownloadsQueueSnapshot().MaxActive != 200 || len(cfg.StreamSnapshot().SeedTrackers) != 2 {
		t.Error("queue/stream snapshot does not reflect the last update")
	}
}
