package transcode

import (
	"log"
	"strings"
	"sync"
)

// gpuSem caps how many ffmpeg sessions may hold a HARDWARE video decoder at
// once. The production failure: each `-hwaccel cuda` decode allocates a CUVID
// decoder in VRAM (cuvidCreateDecoder). With 7 concurrent 1080p/2160p HLS
// sessions the GPU ran out of memory (CUDA_ERROR_OUT_OF_MEMORY) and the NEXT
// play/seek failed — the player saw NotSupportedError. The encoder (NVENC)
// costs far less VRAM than the decoder, so we only gate the DECODE side: a
// session over the cap still runs, but with a SOFTWARE decoder (no -hwaccel)
// feeding the same NVENC encoder. Slower decode, but it always plays.
//
// A nil *gpuSem (limit 0 / unset) means "unlimited" — acquire always succeeds
// and the single-transcode common case behaves EXACTLY as before (HW decode).
type gpuSem struct {
	mu    sync.Mutex
	limit int
	inUse int
}

// newGPUSem builds a semaphore with the given limit. limit <= 0 returns nil
// (unlimited), so the hot single-transcode path takes no lock and never falls
// back to software decode.
func newGPUSem(limit int) *gpuSem {
	if limit <= 0 {
		return nil
	}
	return &gpuSem{limit: limit}
}

// tryAcquire takes a HW-decode slot without blocking. Returns true when a slot
// was reserved (caller must release it later), false when at the cap (caller
// must decode in software). A nil semaphore always grants (unlimited).
func (g *gpuSem) tryAcquire() bool {
	if g == nil {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inUse >= g.limit {
		return false
	}
	g.inUse++
	return true
}

// release returns a previously-acquired HW-decode slot. Safe (no-op) on a nil
// semaphore or when nothing is held — stop() may be called more than once, and
// software-decode sessions never acquired a slot, so release must be idempotent
// past zero.
func (g *gpuSem) release() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inUse > 0 {
		g.inUse--
	}
}

// held reports the current number of reserved HW-decode slots (0 for nil).
// Used by tests and the LRU reclaim decision.
func (g *gpuSem) held() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inUse
}

// cudaOOMSignatures are the ffmpeg stderr fragments that mean the HARDWARE
// decoder could not be created because the GPU is out of memory (or hwaccel
// init failed for an equivalent reason). Matched case-insensitively against the
// session's stderr. Any hit means "retry this session with software decode".
//
// Observed in production (GTX 1070, ffmpeg h264 cuvid):
//
//	cuvidCreateDecoder(...) failed -> CUDA_ERROR_OUT_OF_MEMORY: out of memory
//	Failed setup for format cuda: hwaccel initialisation returned error.
var cudaOOMSignatures = []string{
	"cuda_error_out_of_memory",
	"hwaccel initialisation returned error", // British spelling, as ffmpeg emits it
	"hwaccel initialization returned error", // American spelling, just in case
	"cuvidcreatedecoder",
}

// isCUDAOOM reports whether an ffmpeg stderr blob signals a hardware-decode
// failure we can recover from by switching to software decode. Case-insensitive
// so it matches regardless of ffmpeg's log casing.
func isCUDAOOM(stderr string) bool {
	low := strings.ToLower(stderr)
	for _, sig := range cudaOOMSignatures {
		if strings.Contains(low, sig) {
			return true
		}
	}
	return false
}

// oomWatcher is an io.Writer that ffmpeg's stderr is wired to. It forwards each
// complete line to the log (with the same prefix the plain logWriter used) AND
// raises a flag the moment a CUDA-OOM / hwaccel-init-failure signature appears,
// so the session's exit watcher can relaunch in software decode. Line-buffered
// like logWriter so the signature isn't split across two Write calls.
type oomWatcher struct {
	mu      sync.Mutex
	prefix  string
	buf     []byte
	oomSeen bool
}

func newOOMWatcher(prefix string) *oomWatcher { return &oomWatcher{prefix: prefix} }

func (w *oomWatcher) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if line == "" {
			continue
		}
		if isCUDAOOM(line) {
			w.oomSeen = true
		}
		log.Print(w.prefix, line)
	}
	w.mu.Unlock()
	return len(p), nil
}

// sawOOM reports whether a recoverable CUDA-OOM / hwaccel-init failure appeared
// in the stderr seen so far. Also scans any un-flushed tail (a final line with
// no trailing newline) so a crash that printed the signature last still counts.
func (w *oomWatcher) sawOOM() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.oomSeen || isCUDAOOM(string(w.buf))
}
