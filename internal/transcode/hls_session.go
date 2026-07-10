package transcode

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// launch starts (or restarts) ffmpeg at segment `startSeg` and wires the exit
// watcher. Caller must hold no session lock. The watcher only marks the
// session closed if no newer launch superseded it (generation check), so a
// seek-restart doesn't look like the encoder dying for good.
func (s *HLSSession) launch(startSeg int) error {
	s.mu.Lock()
	if s.dead {
		s.mu.Unlock()
		return errSessionStopped
	}
	s.mu.Unlock()
	ffctx, cancel := context.WithCancel(context.Background())
	// #nosec G204 -- binario fixo/de config; valores de usuario sao operandos de -i ou inteiros; exec sem shell
	cmd := exec.CommandContext(ffctx, s.spec.ffmpegPath, s.spec.args(startSeg)...)
	log.Printf("hls: ffmpeg %s", strings.Join(s.spec.args(startSeg), " "))
	oom := newOOMWatcher("hls/" + s.Key + " ")
	cmd.Stderr = oom
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("ffmpeg start: %w", err)
	}
	s.mu.Lock()
	// Re-check after Start: a reap may have landed in between. stop() already
	// snapshotted the OLD Cmd, so this brand-new encoder is ours to kill.
	if s.dead {
		s.mu.Unlock()
		cancel()
		_ = cmd.Process.Kill()
		return errSessionStopped
	}
	s.Cmd = cmd
	s.Cancel = cancel
	s.oomDetector = oom
	s.startSeg = startSeg
	// Relançar limpa o flag de "encoder morto": um run anterior pode ter terminado
	// (closed=true) e este o ressuscita (ex: seek pra um buraco após o ffmpeg
	// completar perto do fim). Sem isso a sessão segue marcada closed e o GC a reapa.
	s.closed = false
	s.gen++
	myGen := s.gen
	s.mu.Unlock()

	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		// Only this launch's natural exit closes the session — a later restart
		// (higher gen) means this exit was an intentional kill, not the end.
		superseded := s.gen != myGen
		if !superseded {
			s.closed = true
		}
		s.mu.Unlock()
		// CUDA-OOM recovery: ffmpeg died trying to create a hardware decoder with
		// no VRAM. Relaunch the SAME session in software decode (NVENC still
		// encodes) so playback succeeds. Only for a non-superseded HW-decode run
		// that actually failed; tryRecoverFromCUDAOOM no-ops otherwise (and only
		// downgrades once).
		if err != nil && !superseded && s.tryRecoverFromCUDAOOM(ffctx, oom, startSeg) {
			return
		}
		if err != nil && !errors.Is(ffctx.Err(), context.Canceled) && !superseded {
			log.Printf("hls: ffmpeg exited for session %s: %v", s.Key, err)
		}
	}()
	return nil
}

// tryRecoverFromCUDAOOM relaunches the session in SOFTWARE decode after ffmpeg
// failed creating a hardware decoder (CUDA_ERROR_OUT_OF_MEMORY / hwaccel init
// error). It only fires when: the run was NOT cancelled by us, the stderr shows
// the recoverable signature, the spec was using HW decode, and we haven't
// already downgraded this session. On a match it flips spec.swDecode, returns
// the held GPU slot (the HW decoder it couldn't allocate), clears `closed` and
// relaunches at the same segment. Returns true when it took over the recovery
// (caller must not log the exit as a hard failure).
func (s *HLSSession) tryRecoverFromCUDAOOM(ffctx context.Context, oom *oomWatcher, startSeg int) bool {
	if errors.Is(ffctx.Err(), context.Canceled) || oom == nil || !oom.sawOOM() {
		return false
	}
	s.mu.Lock()
	if s.dead || s.swFallbackTried || s.spec == nil || s.spec.swDecode {
		s.mu.Unlock()
		return false
	}
	s.swFallbackTried = true
	s.spec.swDecode = true
	s.mu.Unlock()

	// The HW decoder failed to allocate — give the slot back so another session
	// can use the VRAM this one no longer holds.
	s.releaseGPUSlot()
	log.Printf("hls: CUDA-OOM on session %s — retrying with software decode (NVENC still encodes)", s.Key)
	if err := s.launch(startSeg); err != nil {
		log.Printf("hls: software-decode relaunch of %s failed: %v", s.Key, err)
	}
	return true
}

// hlsForwardSeekThreshold is how many segments PAST the highest one on disk a
// request must be before we treat it as a real forward seek (and restart the
// encoder) instead of the player's normal read-ahead buffering. The sequential
// encoder reaches anything within this window on its own, so restarting for it
// just thrashes ffmpeg — killing the run that was about to produce the segment.
// Generous (~2 min) so Safari's aggressive VOD buffering never trips it.
// (Was a tiny aheadWindow=2, which made every buffer-ahead request restart the
// encoder, producing a cascade where NO segment ever finished — playback froze
// and the player bailed.)
const hlsForwardSeekThreshold = 30

// hlsRestartCooldown debounces restarts so the burst of parallel segment
// requests a seek fires (around the target) doesn't spawn competing encoders.
const hlsRestartCooldown = 3 * time.Second

// EnsureSegment makes sure an encoder is (or will soon be) producing segment
// `idx`. The segment handler calls this when `idx` isn't on disk yet. It only
// restarts the encoder for a real seek: backward (idx < startSeg — the encoder
// already passed it and won't return) or a far-forward jump (beyond the
// read-ahead window). Everything in between is normal buffering — the running
// sequential encoder will reach it, so we let the caller wait.
func (s *HLSSession) EnsureSegment(idx int) {
	if s.spec == nil || !s.spec.vod {
		return
	}
	s.mu.Lock()
	start := s.startSeg
	closed := s.closed
	s.mu.Unlock()
	// Relança quando: o encoder morreu (closed — ex: terminou de transcodificar
	// após um seek perto do fim, deixando o miolo sem segmentos); seek pra trás
	// (idx < start, o encoder sequencial já passou e não volta); ou seek pra
	// frente além da janela de read-ahead. Sem o caso `closed`, um segmento num
	// buraco deixado por seeks dá 404 pra sempre — e o Safari, em VOD, não
	// refetcha a playlist estática pra respawnar a sessão → playback congela.
	if closed || idx < start || idx > s.highestSeg()+hlsForwardSeekThreshold {
		// Guard do prefetch do HLS nativo: Safari/iOS pede um segmento MUITO à
		// frente logo no início do play. Relançar pra servi-lo abandona o encode
		// sequencial do seg 0 que o player realmente precisa → thrash de restart
		// (frente/trás) + stall em t≈0 (o vídeo só destravava após ~minutos).
		// Enquanto ainda encodando DO INÍCIO (start==0) com pouca coisa produzida,
		// tratamos o salto grande como prefetch e deixamos o encode sequencial
		// seguir — a posição real (baixa) do player continua sendo servida. Seek
		// pra trás e seeks depois que o encode avançou ainda relançam normalmente.
		if !closed && start == 0 && idx > s.highestSeg()+hlsForwardSeekThreshold && s.highestSeg() < hlsForwardSeekThreshold {
			return
		}
		_ = s.RestartAt(idx)
	}
}

// highestSeg returns the largest seg_NNNNN.ts index currently on disk, or -1
// when none exist. Cheap readdir; only called when a requested segment is
// missing, not on the hot path.
func (s *HLSSession) highestSeg() int {
	entries, _ := os.ReadDir(s.Dir)
	hi := -1
	for _, e := range entries {
		if n, ok := parseSegName(e.Name()); ok && n > hi {
			hi = n
		}
	}
	return hi
}

// IsVOD reports whether this session serves a finite VOD playlist (full
// seekbar) vs the incremental EVENT/live playlist. Decided once at start from
// the VOD policy + known duration; handler and encoder read this single flag.
func (s *HLSSession) IsVOD() bool { return s.spec != nil && s.spec.vod }

// ParseSegIndex is the exported form of parseSegName for handlers that need to
// map a requested segment filename back to its index.
func ParseSegIndex(name string) (int, bool) { return parseSegName(name) }

// parseSegName extracts N from "seg_00042.ts". Returns ok=false for anything
// else (index.m3u8, temp files, etc.).
func parseSegName(name string) (int, bool) {
	if !strings.HasPrefix(name, "seg_") || !strings.HasSuffix(name, ".ts") {
		return 0, false
	}
	n, err := strconv.Atoi(name[len("seg_") : len(name)-len(".ts")])
	if err != nil {
		return 0, false
	}
	return n, true
}

// RestartAt relaunches ffmpeg to begin producing at segment `seg`. Only
// meaningful in VOD mode. The decision of WHETHER to restart lives in
// EnsureSegment; this just performs it, serialised so concurrent segment
// requests can't spawn duplicate encoders. No-op when already encoding from
// `seg`. Older segments on disk are kept so backward seeks reuse them.
func (s *HLSSession) RestartAt(seg int) error {
	if s.spec == nil || !s.spec.vod {
		return nil
	}
	s.restartMu.Lock()
	defer s.restartMu.Unlock()

	s.mu.Lock()
	cur := s.startSeg
	cancel := s.Cancel
	since := time.Since(s.lastRestart)
	closed := s.closed
	dead := s.dead
	s.mu.Unlock()
	if dead {
		// Reaped/closed by the manager — the segment dir is gone. The client's
		// next master request recreates a fresh session via GetOrStart.
		return errSessionStopped
	}
	// Encoder VIVO já produzindo daqui: nada a fazer. Mas se está closed (morto),
	// precisa ressuscitar mesmo que seg == cur — os segmentos podem não existir.
	if seg == cur && !closed {
		return nil // already encoding from here
	}
	// Debounce: a single seek fires several segment requests near the target.
	// The first restart wins; the rest (different seg numbers) are absorbed so
	// they don't kill the just-launched encoder. A genuine later seek (after the
	// cooldown) still restarts. Um encoder MORTO (closed) ignora o cooldown —
	// senão o playback fica 404 até o cooldown vencer.
	if since < hlsRestartCooldown && !closed {
		return nil
	}

	log.Printf("hls: seek-restart session %s from seg %d → %d (closed=%v)", s.Key, cur, seg, closed)
	if cancel != nil {
		cancel() // kill the current ffmpeg; gen bump in launch() guards the watcher
	}
	s.mu.Lock()
	s.lastRestart = time.Now()
	s.mu.Unlock()
	return s.launch(seg)
}

// WaitForMaster blocks up to `timeout` waiting for the master `index.m3u8`
// to appear. ffmpeg only writes the playlist after the first segment is
// completely encoded, so the wait is bounded by `-hls_time 4` plus encoder
// startup. We bail if the session ends without writing one.
func (s *HLSSession) WaitForMaster(timeout time.Duration) error {
	path := filepath.Join(s.Dir, hlsPlaylistFile)
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		// Check the file FIRST so a fast encode (whole input <4s) that
		// closes between our last check and now still gets a positive
		// result.
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			return nil
		}
		// Keep the session alive while a client is actively waiting.
		s.mu.Lock()
		s.LastAccess = time.Now()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			// One last check — fsync race: ffmpeg may have written the
			// playlist just before exiting.
			if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
				return nil
			}
			return errors.New("hls session ended before producing playlist")
		}

		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("hls master not ready within %s", timeout)
			}
		}
	}
}

// WaitForSegment blocks for the named segment file (basename only) to exist
// and be fully written. ffmpeg's `temp_file` flag means segments appear
// atomically — once `seg_NNN.ts` is visible, it's complete.
func (s *HLSSession) WaitForSegment(name string, timeout time.Duration) (string, error) {
	// Defensively reject anything with a slash to prevent path traversal.
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return "", errors.New("invalid segment name")
	}
	path := filepath.Join(s.Dir, name)
	// Qualquer requisição de segmento conta como atividade — mesmo se ainda não
	// existe (404) — senão o GC reapa a sessão durante a janela de buracos
	// pós-seek em que o player insiste pedindo segmentos ainda não gerados.
	s.mu.Lock()
	s.LastAccess = time.Now()
	s.mu.Unlock()
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	for {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			s.mu.Lock()
			s.LastAccess = time.Now()
			s.mu.Unlock()
			return path, nil
		}
		if closed {
			// Last chance: ffmpeg exited; check one more time, otherwise 404.
			if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
				return path, nil
			}
			return "", errors.New("segment not found and encoder ended")
		}

		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return "", fmt.Errorf("segment %s not ready within %s", name, timeout)
			}
		}
	}
}

// stop kills ffmpeg, shuts down the loopback source server, closes the input
// source, and removes the segment dir. Idempotent. Cmd/Cancel are snapshotted
// UNDER s.mu (launch writes them under the same lock — reading them bare was a
// data race), and `dead` is set first so a concurrent launch/RestartAt holding
// a stale session pointer can't resurrect an encoder into the removed dir.
func (s *HLSSession) stop() {
	s.mu.Lock()
	already := s.closed
	s.dead = true
	cancel, cmd, srv, src := s.Cancel, s.Cmd, s.sourceSrv, s.source
	retryCancel := s.retryCancel
	s.mu.Unlock()
	if retryCancel != nil {
		retryCancel() // halt the background duration re-probe, if any
	}
	if cancel != nil {
		cancel()
	}
	if !already && cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if srv != nil {
		shutdownCtx, cancelSrv := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		cancelSrv()
	}
	closeIfCloser(src)
	s.releaseGPUSlot() // return the GPU-decode slot (if held) to the semaphore
	_ = os.RemoveAll(s.Dir)
}
