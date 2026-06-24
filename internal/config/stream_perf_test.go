package config

import (
	"path/filepath"
	"testing"
)

// Round-trip: campos de performance gravados sobrevivem ao Save→Load.
func TestStreamPerfConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := defaultConfig()
	cfg.Stream.MaxDownloadRate = 5 * 1024 * 1024
	cfg.Stream.MaxUploadRate = 1 * 1024 * 1024
	cfg.Stream.ReadaheadMB = 64
	cfg.Stream.StorageBackend = StorageBackendMmap
	cfg.Stream.MaxConnsPerTorrent = 120
	cfg.Stream.HalfOpenConns = 40
	cfg.Stream.PeersHighWater = 800
	cfg.Stream.PieceHashers = 4
	cfg.Stream.MaxCacheGB = 250

	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := got.Stream
	if s.MaxDownloadRate != 5*1024*1024 || s.MaxUploadRate != 1*1024*1024 {
		t.Errorf("rate limits não sobreviveram: down=%d up=%d", s.MaxDownloadRate, s.MaxUploadRate)
	}
	if s.ReadaheadMB != 64 || s.MaxConnsPerTorrent != 120 || s.HalfOpenConns != 40 ||
		s.PeersHighWater != 800 || s.PieceHashers != 4 || s.MaxCacheGB != 250 {
		t.Errorf("ints não sobreviveram: %+v", s)
	}
	if s.StorageBackend != StorageBackendMmap {
		t.Errorf("StorageBackend = %q, queria mmap", s.StorageBackend)
	}
}

// Env overrides: MB/s vira bytes; ints aplicados; storage sanitizado.
func TestStreamPerfEnvOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := defaultConfig().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("JACKUI_STREAM_DOWN_MBPS", "10")
	t.Setenv("JACKUI_STREAM_UP_MBPS", "2")
	t.Setenv("JACKUI_READAHEAD_MB", "48")
	t.Setenv("JACKUI_MAX_CONNS", "99")
	t.Setenv("JACKUI_STORAGE_BACKEND", "mmap")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	s := cfg.Stream
	if s.MaxDownloadRate != 10*1024*1024 {
		t.Errorf("down = %d, queria %d", s.MaxDownloadRate, 10*1024*1024)
	}
	if s.MaxUploadRate != 2*1024*1024 {
		t.Errorf("up = %d", s.MaxUploadRate)
	}
	if s.ReadaheadMB != 48 || s.MaxConnsPerTorrent != 99 {
		t.Errorf("ints env: readahead=%d conns=%d", s.ReadaheadMB, s.MaxConnsPerTorrent)
	}
	if s.StorageBackend != StorageBackendMmap {
		t.Errorf("StorageBackend = %q, queria mmap", s.StorageBackend)
	}
}

// JACKUI_SEED_TRACKERS (CSV) vira []string, com trim e drop de vazios.
func TestSeedTrackersEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := defaultConfig().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Setenv("JACKUI_SEED_TRACKERS", " amigos-share , ,outro ")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := cfg.Stream.SeedTrackers
	if len(got) != 2 || got[0] != "amigos-share" || got[1] != "outro" {
		t.Errorf("seed_trackers env = %#v, queria [amigos-share outro]", got)
	}
}

func TestSplitCSV(t *testing.T) {
	if got := splitCSV(" a , ,b "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("splitCSV = %#v", got)
	}
	if got := splitCSV("  , "); got != nil {
		t.Errorf("splitCSV all-empty = %#v, want nil", got)
	}
}

// Sanitização: backend inválido (ou vazio) cai pra "file".
func TestStreamStorageBackendSanitized(t *testing.T) {
	for _, in := range []string{"", "bogus", "FILE", "MMAP"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		cfg := defaultConfig()
		cfg.Stream.StorageBackend = in
		if err := cfg.Save(path); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got.Stream.StorageBackend != StorageBackendFile {
			t.Errorf("backend %q deveria sanitizar p/ file, virou %q", in, got.Stream.StorageBackend)
		}
	}
}

func TestEnvIntInvalid(t *testing.T) {
	t.Setenv("JACKUI_TEST_INT", "notanumber")
	if _, ok := envInt("JACKUI_TEST_INT"); ok {
		t.Error("envInt deveria retornar ok=false para valor não-numérico")
	}
	if _, ok := envInt("JACKUI_TEST_INT_UNSET"); ok {
		t.Error("envInt deveria retornar ok=false para var ausente")
	}
	t.Setenv("JACKUI_TEST_INT", "42")
	if n, ok := envInt("JACKUI_TEST_INT"); !ok || n != 42 {
		t.Errorf("envInt = (%d,%v), queria (42,true)", n, ok)
	}
}
