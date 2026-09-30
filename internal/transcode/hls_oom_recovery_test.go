package transcode

import (
	"context"
	"testing"
)

// Recovery must be tied to the generation of the run that died. A concurrent
// seek-restart (EnsureSegment → RestartAt → launch) bumps s.gen; a recovery
// that ignores it relaunches the OLD startSeg over the newer seek encoder —
// two ffmpegs, one of them uncancellable from the session's point of view.

// staleOOMWatcher builds an oomWatcher that has already seen the recoverable
// CUDA-OOM signature.
func staleOOMWatcher(t *testing.T) *oomWatcher {
	t.Helper()
	oom := newOOMWatcher("test/")
	if _, err := oom.Write([]byte("cuvidCreateDecoder(...) failed -> CUDA_ERROR_OUT_OF_MEMORY: out of memory\n")); err != nil {
		t.Fatalf("oomWatcher.Write: %v", err)
	}
	return oom
}

// TestTryRecoverFromCUDAOOM_StaleGenBails: a seek-restart landed between the
// exit watcher's superseded check and the recovery (s.gen advanced past
// myGen). The recovery must bail: no swDecode flip, no relaunch, no gen churn.
func TestTryRecoverFromCUDAOOM_StaleGenBails(t *testing.T) {
	dir := t.TempDir()
	spec := &encodeSpec{dir: dir, inputURL: "http://127.0.0.1:1/source", encoder: "h264_nvenc", ffmpegPath: "true", vod: true}
	s := &HLSSession{Key: "stale-gen", Dir: dir, spec: spec, gen: 2, startSeg: 0}
	t.Cleanup(func() { s.stop() })

	if s.tryRecoverFromCUDAOOM(context.Background(), staleOOMWatcher(t), 0, 1) {
		t.Fatal("recovery for a superseded run (s.gen=2 > myGen=1) must bail")
	}
	s.mu.Lock()
	sw := spec.swDecode
	cmd := s.Cmd
	gen := s.gen
	fallback := s.swFallbackTried
	s.mu.Unlock()
	if sw {
		t.Error("swDecode must NOT flip for a superseded run — the new encoder keeps HW decode")
	}
	if cmd != nil {
		t.Error("no ffmpeg may be relaunched for a superseded run")
	}
	if gen != 2 {
		t.Errorf("gen = %d, want 2 (recovery must not churn the generation)", gen)
	}
	if fallback {
		t.Error("swFallbackTried must stay unset when the recovery bails")
	}
}

// TestTryRecoverFromCUDAOOM_MatchingGenRecovers: control case — with the
// generation matching, the recovery still flips to software decode and
// relaunches the session at the same segment.
func TestTryRecoverFromCUDAOOM_MatchingGenRecovers(t *testing.T) {
	dir := t.TempDir()
	spec := &encodeSpec{dir: dir, inputURL: "http://127.0.0.1:1/source", encoder: "h264_nvenc", ffmpegPath: "true", vod: true}
	s := &HLSSession{Key: "match-gen", Dir: dir, spec: spec, gen: 1, startSeg: 0}
	t.Cleanup(func() { s.stop() })

	if !s.tryRecoverFromCUDAOOM(context.Background(), staleOOMWatcher(t), 0, 1) {
		t.Fatal("matching-gen CUDA-OOM must be recovered (returns true)")
	}
	s.mu.Lock()
	sw := spec.swDecode
	cmd := s.Cmd
	gen := s.gen
	s.mu.Unlock()
	if !sw {
		t.Error("swDecode must flip on a CUDA-OOM recovery")
	}
	if cmd == nil {
		t.Error("recovery must relaunch ffmpeg")
	}
	if gen != 2 {
		t.Errorf("gen = %d, want 2 (recovery relaunch bumps the generation)", gen)
	}
}
