package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// persistState keeps the "file layer" apart from the "effective layer". Load
// overlays environment variables (and a few derived values, such as the
// auto-seeded AI chain) on top of config.yaml; marshalling that merged struct
// would copy env-only secrets — JWT secret, DB DSN, API keys — into the file.
//
// Both snapshots are yaml trees: file is what config.yaml holds, effective is
// the in-memory config as of the last Load/Save. Save writes file plus only the
// values that CHANGED since effective, so anything still equal to what
// Load produced keeps its on-disk value. Guarded by saveMu.
type persistState struct {
	file      *yaml.Node
	effective *yaml.Node
}

// saveMu serializes Save end to end (marshal → write → snapshot refresh), so
// two concurrent settings saves cannot leave the older state on disk.
var saveMu sync.Mutex

// applyEnvLayer overlays the environment on a config that so far holds only
// file values, remembering both layers so Save never writes the env one back.
func applyEnvLayer(cfg *Config) error {
	file, err := toNode(cfg)
	if err != nil {
		return fmt.Errorf("failed to snapshot config: %w", err)
	}
	applyEnvOverrides(cfg)
	effective, err := toNode(cfg)
	if err != nil {
		return fmt.Errorf("failed to snapshot config: %w", err)
	}
	cfg.persist = &persistState{file: file, effective: effective}
	return nil
}

// toNode encodes the config as a yaml tree. A variable so tests can make the
// encode fail (a real Config always encodes).
var toNode = func(c *Config) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(c); err != nil {
		return nil, err
	}
	return &n, nil
}

// Save persists the config to path. A Config obtained from Load never writes
// env-sourced values (see persistState); one built in code is written as is.
func (c *Config) Save(path string) error {
	saveMu.Lock()
	defer saveMu.Unlock()

	// Encode under the read lock: any settings handler may Save concurrently
	// with another handler's Update, and the tree must never be built from a
	// half-written section.
	cfgMu.RLock()
	cur, err := toNode(c)
	cfgMu.RUnlock()
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	out := cur
	if c.persist != nil {
		out = mergeFileLayer(cur, c.persist.effective, c.persist.file)
	}
	data, err := yaml.Marshal(out)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// 0600: config.yaml holds credentials (JWT secret, SMTP and download-client
	// passwords, AI API keys) — nothing else on the host needs to read it.
	if err := writeFileAtomic(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	if c.persist != nil {
		c.persist.file, c.persist.effective = out, cur
	}
	return nil
}

// mergeFileLayer returns what to persist for cur: mappings are merged key by
// key; any other value (scalar or list) that still equals effective keeps the
// file's value — nil when the file has none, which drops the key.
func mergeFileLayer(cur, effective, file *yaml.Node) *yaml.Node {
	if effective == nil {
		return cur
	}
	if cur.Kind != yaml.MappingNode || effective.Kind != yaml.MappingNode {
		if sameNode(cur, effective) {
			return file
		}
		return cur
	}
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: cur.Tag}
	for i := 0; i+1 < len(cur.Content); i += 2 {
		key := cur.Content[i]
		v := mergeFileLayer(cur.Content[i+1], mapValue(effective, key.Value), mapValue(file, key.Value))
		if v != nil {
			out.Content = append(out.Content, key, v)
		}
	}
	if len(out.Content) == 0 && file == nil {
		return nil
	}
	return out
}

// mapValue returns the value stored under key in a mapping node, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func sameNode(a, b *yaml.Node) bool {
	if a.Kind != b.Kind || a.Tag != b.Tag || a.Value != b.Value || len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		if !sameNode(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

// writeFileAtomic replaces path with data through a synced temp file in the
// same directory plus a rename, so a reader or a crash never observes a
// truncated config. A failure while WRITING the temp file (disk full, I/O
// error) is returned with the original file untouched.
//
// When the swap itself is impossible — the directory is not writable, or path
// is a single-file bind mount, which cannot be renamed over (EBUSY; the stock
// docker-compose mounts config.yaml that way) — it falls back to writing in
// place, which is not atomic.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target // keep a symlinked config a symlink
	}
	// A rename only needs a writable DIRECTORY; honour a read-only (or
	// foreign-owned) config.yaml the way the in-place write always did.
	if err := CheckWritable(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return os.WriteFile(path, data, perm)
	}
	if err := fillTempFile(tmp, data, perm); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := renameFile(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return os.WriteFile(path, data, perm)
	}
	return nil
}

// Variables so tests can simulate a full disk and a single-file bind mount.
var (
	fillTempFile = fillTemp
	renameFile   = os.Rename
)

func fillTemp(f *os.File, data []byte, perm os.FileMode) error {
	_, err := f.Write(data)
	if err == nil {
		err = f.Chmod(perm)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
