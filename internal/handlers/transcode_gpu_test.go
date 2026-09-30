package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFakeNvidiaSmi drops an executable nvidia-smi stub in a fresh dir and
// points PATH at it, so getGPUStats resolves the stub instead of a real GPU.
func writeFakeNvidiaSmi(t *testing.T, script string) {
	t.Helper()
	binDir := t.TempDir()
	fake := filepath.Join(binDir, "nvidia-smi")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
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
