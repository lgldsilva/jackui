package transcode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// (a) The software-decode fallback spec must NOT emit `-hwaccel cuda` (that's
// the CUVID allocation that ran the GPU out of VRAM), but MUST keep the NVENC
// ENCODER — decode on CPU, encode on the GPU, so playback still succeeds.
func TestEncodeSpecSWDecodeDropsHWAccelKeepsNVENC(t *testing.T) {
	hw := &encodeSpec{dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "h264_nvenc", ffmpegPath: "ffmpeg", vod: true}
	hwJoined := strings.Join(hw.args(0), " ")
	if !strings.Contains(hwJoined, "-hwaccel cuda") {
		t.Fatalf("baseline HW spec must use -hwaccel cuda; got:\n%s", hwJoined)
	}

	sw := &encodeSpec{dir: "/tmp/x", inputURL: "http://127.0.0.1:1/source", encoder: "h264_nvenc", ffmpegPath: "ffmpeg", vod: true, swDecode: true}
	joined := strings.Join(sw.args(0), " ")
	if strings.Contains(joined, "-hwaccel cuda") {
		t.Errorf("swDecode spec must NOT contain -hwaccel cuda; got:\n%s", joined)
	}
	if strings.Contains(joined, "-hwaccel") {
		t.Errorf("swDecode spec must drop -hwaccel entirely; got:\n%s", joined)
	}
	if !strings.Contains(joined, "-c:v h264_nvenc") {
		t.Errorf("swDecode spec must KEEP the NVENC encoder (encode on GPU); got:\n%s", joined)
	}
	// The proven Safari fixes must survive the fallback.
	if !strings.Contains(joined, "-muxdelay 0") || !strings.Contains(joined, "-muxpreload 0") {
		t.Errorf("swDecode spec must keep -muxdelay 0 -muxpreload 0; got:\n%s", joined)
	}
	if !strings.Contains(joined, "setpts=PTS-STARTPTS") {
		t.Errorf("swDecode spec must keep PTS zeroing; got:\n%s", joined)
	}
}

// usesHWDecode drives the semaphore: only specs that would actually launch a HW
// decoder need a GPU slot. CPU encoders and swDecode specs must report false.
func TestEncodeSpecUsesHWDecode(t *testing.T) {
	cases := []struct {
		name   string
		spec   *encodeSpec
		wantHW bool
	}{
		{"nvenc HW", &encodeSpec{encoder: "h264_nvenc"}, true},
		{"vaapi HW", &encodeSpec{encoder: "h264_vaapi"}, true},
		{"nvenc forced SW", &encodeSpec{encoder: "h264_nvenc", swDecode: true}, false},
		{"libx264 CPU", &encodeSpec{encoder: "libx264"}, false},
	}
	for _, c := range cases {
		if got := c.spec.usesHWDecode(); got != c.wantHW {
			t.Errorf("%s: usesHWDecode()=%v want %v", c.name, got, c.wantHW)
		}
	}
}

// (b) The CUDA-OOM detector must recognise all three production signatures
// (and be case-insensitive), and ignore unrelated stderr noise.
func TestIsCUDAOOMRecognisesAllSignatures(t *testing.T) {
	oomLines := []string{
		"decoder->cvdl->cuvidCreateDecoder(...) failed -> CUDA_ERROR_OUT_OF_MEMORY: out of memory",
		"[h264 @ 0x55] Failed setup for format cuda: hwaccel initialisation returned error.",
		"hwaccel initialization returned error", // American spelling variant
		"CUVIDCREATEDECODER failed",             // uppercase
	}
	for _, line := range oomLines {
		if !isCUDAOOM(line) {
			t.Errorf("isCUDAOOM should recognise %q", line)
		}
	}
	notOOM := []string{
		"frame= 120 fps= 30 q=23.0 size=  512kB",
		"Stream #0:0 -> #0:0 (h264 (h264_cuvid) -> h264 (h264_nvenc))",
		"",
	}
	for _, line := range notOOM {
		if isCUDAOOM(line) {
			t.Errorf("isCUDAOOM should NOT match benign line %q", line)
		}
	}
}

// oomWatcher must flag the signature whether it arrives as a complete line or
// as the un-flushed tail of the final (newline-less) write.
func TestOOMWatcherDetectsLineAndTail(t *testing.T) {
	w := newOOMWatcher("test: ")
	w.Write([]byte("frame= 1 fps=30\n"))
	if w.sawOOM() {
		t.Fatal("benign line must not flag OOM")
	}
	w.Write([]byte("cuvidCreateDecoder(...) failed -> CUDA_ERROR_OUT_OF_MEMORY: out of memory\n"))
	if !w.sawOOM() {
		t.Fatal("complete OOM line must flag")
	}

	tail := newOOMWatcher("test: ")
	// No trailing newline → stays in buf; sawOOM must still scan it.
	tail.Write([]byte("hwaccel initialisation returned error."))
	if !tail.sawOOM() {
		t.Fatal("un-flushed OOM tail must flag")
	}
}

// (c) The semaphore limits concurrent HW-decode reservations. Beyond the cap,
// tryAcquire returns false (caller decodes in software). release frees a slot.
func TestGPUSemCapsConcurrency(t *testing.T) {
	g := newGPUSem(2)
	if !g.tryAcquire() || !g.tryAcquire() {
		t.Fatal("first two acquires must succeed under a cap of 2")
	}
	if g.tryAcquire() {
		t.Fatal("third acquire must fail at the cap (caller falls back to SW decode)")
	}
	if g.held() != 2 {
		t.Fatalf("held()=%d want 2", g.held())
	}
	g.release()
	if !g.tryAcquire() {
		t.Fatal("after release a slot must be available again")
	}
	// release must not under-count below zero.
	g.release()
	g.release()
	g.release()
	if g.held() != 0 {
		t.Fatalf("held()=%d want 0 after over-release", g.held())
	}
}

// A nil semaphore (limit 0/unset) is unlimited: every acquire succeeds, so the
// single-transcode common case is unchanged (always HW decode).
func TestGPUSemNilIsUnlimited(t *testing.T) {
	g := newGPUSem(0)
	if g != nil {
		t.Fatal("limit 0 must yield a nil (unlimited) semaphore")
	}
	for i := 0; i < 100; i++ {
		if !g.tryAcquire() {
			t.Fatalf("nil semaphore acquire #%d must succeed (unlimited)", i)
		}
	}
	g.release() // must not panic on nil
	if g.held() != 0 {
		t.Fatalf("nil held()=%d want 0", g.held())
	}
}

// The semaphore must stay correct under concurrent acquire/release.
func TestGPUSemConcurrentSafe(t *testing.T) {
	const (
		limit   = 4
		workers = 64
	)
	g := newGPUSem(limit)
	var wg sync.WaitGroup
	// acquired/release replace a "hold the slot" sleep with deterministic
	// overlap: with nobody releasing, exactly `limit` goroutines win a slot and
	// the rest fail tryAcquire. We block the winners until all `limit` slots are
	// simultaneously held (guaranteeing real overlap), then release. `acquired`
	// is sized to `workers` so no send can ever block: after release is closed
	// the losers re-acquire/release freely and their sends buffer harmlessly.
	acquired := make(chan struct{}, workers)
	release := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.tryAcquire() {
				acquired <- struct{}{}
				<-release // hold the slot so all limit slots overlap
				g.release()
			}
		}()
	}
	for i := 0; i < limit; i++ {
		<-acquired // all limit slots are now held simultaneously
	}
	if held := g.held(); held != limit {
		t.Fatalf("held()=%d want %d while all slots are held", held, limit)
	}
	close(release)
	wg.Wait()
	if g.held() != 0 {
		t.Fatalf("held()=%d want 0 after all releases", g.held())
	}
}

// reserveDecodeMode: under the cap, a HW-encoder session gets a real GPU slot
// (HW decode). At the cap with no idle session to reclaim, the next HW session
// is downgraded to software decode but still starts.
func TestReserveDecodeModeDowngradesAtCap(t *testing.T) {
	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	m.SetGPUTranscodeLimit(1)

	// First HW session takes the only slot.
	s1 := &HLSSession{Key: "a-0", mgr: m, spec: &encodeSpec{encoder: "h264_nvenc"}}
	m.reserveDecodeMode(s1)
	if s1.spec.swDecode {
		t.Fatal("first HW session under the cap must keep HW decode")
	}
	if !s1.holdsGPUSlot || m.gpuSem.held() != 1 {
		t.Fatalf("first session must hold the GPU slot; holds=%v held=%d", s1.holdsGPUSlot, m.gpuSem.held())
	}
	// Mark s1 as actively watched so it can't be reclaimed.
	s1.mu.Lock()
	s1.LastAccess = time.Now()
	s1.holdsGPUSlot = true
	s1.mu.Unlock()
	m.mu.Lock()
	m.sess["a-0"] = s1
	m.mu.Unlock()

	// Second HW session: cap reached, s1 is fresh (not idle) → downgrade to SW.
	s2 := &HLSSession{Key: "b-0", mgr: m, spec: &encodeSpec{encoder: "h264_nvenc"}}
	m.reserveDecodeMode(s2)
	if !s2.spec.swDecode {
		t.Error("session over the cap (no reclaimable idle session) must decode in software")
	}
	if s2.holdsGPUSlot {
		t.Error("software-decode session must not hold a GPU slot")
	}

	// A CPU-encoder session never touches the semaphore.
	s3 := &HLSSession{Key: "c-0", mgr: m, spec: &encodeSpec{encoder: "libx264"}}
	m.reserveDecodeMode(s3)
	if s3.holdsGPUSlot || s3.spec.swDecode {
		t.Errorf("CPU session must neither hold a slot nor be marked swDecode; holds=%v sw=%v", s3.holdsGPUSlot, s3.spec.swDecode)
	}
}

// (d-reclaim) When the cap is reached but an OLD idle HW session exists, a new
// play reclaims its slot (kills the old ffmpeg → frees VRAM) and decodes on the
// GPU itself, instead of being forced to software decode.
func TestReserveDecodeModeReclaimsIdleSlot(t *testing.T) {
	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	m.SetGPUTranscodeLimit(1)

	old := &HLSSession{Key: "old-0", Dir: t.TempDir(), mgr: m, spec: &encodeSpec{encoder: "h264_nvenc"}}
	m.reserveDecodeMode(old)
	if !old.holdsGPUSlot {
		t.Fatal("old session must hold the slot")
	}
	// Make it look idle (last access well past gpuReclaimIdleAfter).
	old.mu.Lock()
	old.LastAccess = time.Now().Add(-2 * gpuReclaimIdleAfter)
	old.mu.Unlock()
	m.mu.Lock()
	m.sess["old-0"] = old
	m.mu.Unlock()

	// New play: cap reached, but the old idle session is reclaimed → new one
	// gets the GPU slot (HW decode), old one is reaped.
	fresh := &HLSSession{Key: "new-0", mgr: m, spec: &encodeSpec{encoder: "h264_nvenc"}}
	m.reserveDecodeMode(fresh)
	if fresh.spec.swDecode {
		t.Error("new play should reclaim the idle slot and decode on the GPU, not fall back to SW")
	}
	if !fresh.holdsGPUSlot {
		t.Error("new play must hold the reclaimed GPU slot")
	}
	m.mu.Lock()
	_, oldStillTracked := m.sess["old-0"]
	m.mu.Unlock()
	if oldStillTracked {
		t.Error("the reclaimed idle session must be reaped (removed from the manager)")
	}
	if m.gpuSem.held() != 1 {
		t.Errorf("exactly one GPU slot must be held after reclaim; held=%d", m.gpuSem.held())
	}
}

// (d-supersede) Starting a NEW session for the SAME key while the old one is
// still tracked must end with exactly one session, the old encoder killed — the
// dedupe path closes the loser's source. (Same-key supersede is handled by
// GetOrStart's existing dedupe; this asserts the manager never keeps two.)
func TestGetOrStartSameKeyKeepsOneSession(t *testing.T) {
	stubCaps(t)
	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	src1 := &fakeSource{}
	s1, err := m.GetOrStart(context.Background(), HLSStartOpts{Key: "k", Source: src1, SourceSize: 1, KnownDurationSec: 10})
	if err != nil {
		t.Fatalf("GetOrStart#1: %v", err)
	}
	src2 := &fakeSource{}
	s2, err := m.GetOrStart(context.Background(), HLSStartOpts{Key: "k", Source: src2, SourceSize: 1, KnownDurationSec: 10})
	if err != nil {
		t.Fatalf("GetOrStart#2: %v", err)
	}
	if s1 != s2 {
		t.Error("same key must return the same session (no duplicate encoder/VRAM)")
	}
	if !src2.closed.Load() {
		t.Error("the second caller's source must be closed (its session was deduped away)")
	}
	m.mu.Lock()
	n := len(m.sess)
	m.mu.Unlock()
	if n != 1 {
		t.Errorf("manager must track exactly 1 session for the key, got %d", n)
	}
}

// stop() must return the held GPU slot to the semaphore so a reaped session
// doesn't leak VRAM accounting.
func TestStopReleasesGPUSlot(t *testing.T) {
	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	m.SetGPUTranscodeLimit(2)
	s := &HLSSession{Key: "k-0", Dir: t.TempDir(), mgr: m, spec: &encodeSpec{encoder: "h264_nvenc"}}
	m.reserveDecodeMode(s)
	if m.gpuSem.held() != 1 {
		t.Fatalf("held()=%d want 1 after reserve", m.gpuSem.held())
	}
	s.stop()
	if m.gpuSem.held() != 0 {
		t.Errorf("held()=%d want 0 after stop", m.gpuSem.held())
	}
	// Idempotent: a second stop must not under-count.
	s.stop()
	if m.gpuSem.held() != 0 {
		t.Errorf("held()=%d want 0 after double stop", m.gpuSem.held())
	}
}

// End-to-end CUDA-OOM recovery: a stub "ffmpeg" prints the OOM signature and
// exits non-zero on the FIRST (HW-decode) launch, then on the SECOND
// (software-decode) launch writes a playlist and a segment. The session must
// detect the OOM, flip to swDecode, relaunch, and produce output. The GPU slot
// it failed to use must be returned.
func TestSessionRecoversFromCUDAOOMWithSoftwareDecode(t *testing.T) {
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg")
	// The stub keys off whether `-hwaccel` is present in its args (HW launch) vs
	// absent (SW relaunch). On HW it emits the OOM and fails; on SW it writes the
	// expected HLS output relative to the -hls_segment_filename dir.
	script := `#!/bin/sh
for a in "$@"; do
  case "$a" in
    *seg_%05d.ts) segpat="$a" ;;
  esac
done
outdir=$(dirname "$segpat")
last=""
for a in "$@"; do last="$a"; done
if echo "$@" | grep -q -- "-hwaccel"; then
  echo "decoder->cvdl->cuvidCreateDecoder(...) failed -> CUDA_ERROR_OUT_OF_MEMORY: out of memory" 1>&2
  exit 1
fi
# software-decode launch: write a minimal playlist + one segment.
printf '#EXTM3U\n#EXT-X-TARGETDURATION:4\nseg_00000.ts\n#EXT-X-ENDLIST\n' > "$last"
printf 'tsdata' > "$outdir/seg_00000.ts"
exit 0
`
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	SetCachedForTesting(&Capabilities{FFmpegPath: ffmpeg, Preferred: "h264_nvenc"})
	t.Cleanup(ResetCachedForTesting)

	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	m.SetGPUTranscodeLimit(2)

	sess, err := m.GetOrStart(context.Background(), HLSStartOpts{
		Key: "oom-0", Source: &fakeSource{}, SourceSize: 1, KnownDurationSec: 10,
	})
	if err != nil {
		t.Fatalf("GetOrStart: %v", err)
	}

	// The session begins HW (slot held). After the OOM relaunch it must produce
	// the playlist via the software-decode path.
	if err := sess.WaitForMaster(5 * time.Second); err != nil {
		t.Fatalf("WaitForMaster after CUDA-OOM recovery: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sess.mu.Lock()
		sw := sess.spec.swDecode
		sess.mu.Unlock()
		if sw {
			break
		}
		<-time.After(2 * time.Millisecond) // cede a CPU à recuperação de OOM
	}
	sess.mu.Lock()
	sw := sess.spec.swDecode
	sess.mu.Unlock()
	if !sw {
		t.Error("session must have flipped to software decode after CUDA-OOM")
	}
	// The slot the HW decoder failed to allocate must be returned to the pool.
	if m.gpuSem.held() != 0 {
		t.Errorf("GPU slot must be released after CUDA-OOM fallback; held=%d", m.gpuSem.held())
	}
}
