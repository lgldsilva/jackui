package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyExternalMountsEnv_NormalizedPathDedupe(t *testing.T) {
	cfg := &Config{}
	cfg.External.Mounts = []ExternalMount{
		{Name: "Downloads", Path: "/downloads", AllowedUsers: []string{"luiz"}},
	}
	// Trailing slash + surrounding spaces must still match the saved path.
	t.Setenv("JACKUI_EXTERNAL_MOUNTS", "Downloads: /downloads/ ")

	applyExternalMountsEnv(cfg)

	if len(cfg.External.Mounts) != 1 {
		t.Fatalf("env spawned a twin of a saved mount: %+v", cfg.External.Mounts)
	}
	if len(cfg.External.Mounts[0].AllowedUsers) != 1 {
		t.Fatalf("restricted saved mount must win: %+v", cfg.External.Mounts[0])
	}
}

func TestApplyExternalMountsEnv_CaseInsensitiveNameDedupe(t *testing.T) {
	cfg := &Config{}
	cfg.External.Mounts = []ExternalMount{
		{Name: "Downloads", Path: "/data/dl", AllowedUsers: []string{"luiz"}},
	}
	t.Setenv("JACKUI_EXTERNAL_MOUNTS", "downloads:/other/path,Extra:/extra")

	applyExternalMountsEnv(cfg)

	if len(cfg.External.Mounts) != 2 {
		t.Fatalf("want saved mount + Extra only, got: %+v", cfg.External.Mounts)
	}
	if cfg.External.Mounts[0].Path != "/data/dl" || cfg.External.Mounts[1].Name != "Extra" {
		t.Fatalf("unexpected merge result: %+v", cfg.External.Mounts)
	}
}

func TestApplyExternalMountsEnv_DedupesWithinEnvItself(t *testing.T) {
	cfg := &Config{}
	t.Setenv("JACKUI_EXTERNAL_MOUNTS", "A:/a,a:/a2,B:/a/,C:/c:usersubpath")

	applyExternalMountsEnv(cfg)

	// "a:/a2" collides with A by name; "B:/a/" collides with A by path.
	if len(cfg.External.Mounts) != 2 {
		t.Fatalf("want A + C, got: %+v", cfg.External.Mounts)
	}
	if cfg.External.Mounts[0].Name != "A" || cfg.External.Mounts[1].Name != "C" || !cfg.External.Mounts[1].UserSubpath {
		t.Fatalf("unexpected mounts: %+v", cfg.External.Mounts)
	}
}

func TestParseMountSpec_Malformed(t *testing.T) {
	for _, bad := range []string{"", "noseparator", ":/path", "Name:", "   "} {
		if m, ok := parseMountSpec(bad); ok {
			t.Errorf("parseMountSpec(%q) = %+v, want rejected", bad, m)
		}
	}
}

func TestCheckWritable(t *testing.T) {
	dir := t.TempDir()
	writable := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(writable, []byte("port: 8989\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckWritable(writable); err != nil {
		t.Errorf("writable file reported as non-writable: %v", err)
	}
	if err := CheckWritable(dir); err == nil {
		t.Error("directory reported as writable config")
	}
	if os.Getuid() != 0 {
		ro := filepath.Join(dir, "ro.yaml")
		if err := os.WriteFile(ro, []byte("x: 1\n"), 0o444); err != nil {
			t.Fatal(err)
		}
		if err := CheckWritable(ro); err == nil {
			t.Error("0444 file reported as writable")
		}
	}
}
