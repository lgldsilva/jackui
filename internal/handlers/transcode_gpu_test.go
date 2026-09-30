package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFakeNvidiaSmi drops an executable nvidia-smi stub in a fresh dir and
// points the fixed lookup dirs at it, so getGPUStats resolves the stub instead
// of a real GPU (nvidia-smi is never resolved through PATH).
func writeFakeNvidiaSmi(t *testing.T, script string) {
	t.Helper()
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "nvidia-smi")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := nvidiaSMIDirs
	nvidiaSMIDirs = []string{binDir}
	t.Cleanup(func() { nvidiaSMIDirs = prev })
}

// TestNvidiaSMIPath_FixedDirsOnly: the binary is looked up only in the
// configured fixed directories — a PATH entry pointing at a stub must not be
// picked up, and a non-executable file in a fixed dir does not count.
func TestNvidiaSMIPath_FixedDirsOnly(t *testing.T) {
	pathDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pathDir, "nvidia-smi"), []byte("#!/bin/sh\necho hijacked\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)

	prev := nvidiaSMIDirs
	t.Cleanup(func() { nvidiaSMIDirs = prev })

	empty := t.TempDir()
	nvidiaSMIDirs = []string{empty}
	if got := nvidiaSMIPath(); got != "" {
		t.Fatalf("no nvidia-smi in fixed dirs, got %q (PATH must be ignored)", got)
	}
	if info := getGPUStats(); info != nil && info.Type == "nvidia" {
		t.Fatalf("PATH stub must not be executed, got %+v", info)
	}

	if err := os.WriteFile(filepath.Join(empty, "nvidia-smi"), []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := nvidiaSMIPath(); got != "" {
		t.Fatalf("non-executable file must be ignored, got %q", got)
	}

	if err := os.Chmod(filepath.Join(empty, "nvidia-smi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := nvidiaSMIPath(); got != filepath.Join(empty, "nvidia-smi") {
		t.Fatalf("executable in a fixed dir must resolve, got %q", got)
	}
}

// TestGetGPUStats_NvidiaSmiTimeout: nvidia-smi can hang on a wedged driver.
// The exec had no deadline, so getGPUStats — called by /api/transcode/active —
// blocked until the tool returned (or forever), stalling the poll. It must
// bail out within its bounded budget and fall through to the next backend.
func TestGetGPUStats_NvidiaSmiTimeout(t *testing.T) {
	// Absolute /bin/sleep: PATH now points at the stub dir only, and `exec`
	// makes the kill land on the sleeping process itself.
	writeFakeNvidiaSmi(t, "#!/bin/sh\nexec /bin/sleep 30\n")

	start := time.Now()
	info := getGPUStats()
	elapsed := time.Since(start)
	if elapsed > 10*time.Second {
		t.Fatalf("getGPUStats blocked %s on a hung nvidia-smi — exec must be bounded by a context timeout", elapsed)
	}
	if info != nil && info.Type == "nvidia" {
		t.Errorf("a hung nvidia-smi must not be reported as an nvidia GPU, got %+v", info)
	}
}

// TestGetGPUStats_NvidiaSmiParsed: the happy path is unchanged — a fast,
// well-formed nvidia-smi answer is still parsed into the GPUInfo.
func TestGetGPUStats_NvidiaSmiParsed(t *testing.T) {
	writeFakeNvidiaSmi(t, "#!/bin/sh\necho '15, 1024, 8192'\n")

	info := getGPUStats()
	if info == nil || info.Type != "nvidia" {
		t.Fatalf("expected nvidia info from stub output, got %+v", info)
	}
	if info.GPU != 15 || info.VRAMUsed != 1024 || info.VRAMTotal != 8192 {
		t.Errorf("parsed = %+v, want GPU=15 VRAMUsed=1024 VRAMTotal=8192", info)
	}
}
