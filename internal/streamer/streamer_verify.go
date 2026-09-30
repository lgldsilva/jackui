package streamer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// acquireVerify takes a piece-hash slot (disk-bound). Independent of max_active.
// Returns false if ctx is canceled or the limiter shut down (e.g. Streamer.Close)
// — caller should skip verify work.
func (s *Streamer) acquireVerify(ctx context.Context, label string) bool {
	if s.verifyLim == nil {
		return true
	}
	if err := s.verifyLim.AcquireContext(ctx); err != nil {
		if label != "" && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("streamer: piece-verify not acquired (%s): %v", label, err)
		}
		return false
	}
	if label != "" {
		log.Printf("streamer: piece-verify acquired (%s) limit=%d", label, s.verifyLim.Limit())
	}
	return true
}

// releaseVerify frees a piece-hash slot.
func (s *Streamer) releaseVerify(label string) {
	if s.verifyLim == nil {
		return
	}
	s.verifyLim.Release()
	if label != "" {
		log.Printf("streamer: piece-verify released (%s)", label)
	}
}

// Piece/file verify/recheck — extracted from streamer.go.
// VerifyFile is the exported entrypoint for the download worker to trigger
// on-disk piece reconciliation before requesting more data from the swarm. It
// reuses the same dedupe set (`verifiedFiles`) as the streaming path, so the
// verification happens AT MOST once per (hash, file) per process — regardless
// of whether streaming or download triggered it first.
//
// Background: anacrolix traditionally doesn't re-verify on startup; it trusts
// the bolt DB. If the previous shutdown was ungraceful (SIGKILL, container OOM),
// the bolt becomes stale and anacrolix "forgets" pieces that are on disk.
// Without this call, the worker requests bytes from the swarm that we already
// have.
func (s *Streamer) VerifyFile(ctx context.Context, hash metainfo.Hash, fileIdx int) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return ErrTorrentNotActive
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return fmt.Errorf(errFileIndexOutOfRange, fileIdx)
	}
	s.verifyFilePieces(ctx, hash, fileIdx, files[fileIdx])
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// VerifyTorrent reconciles on-disk pieces for EVERY file of a torrent — the
// whole-torrent download path. Same rationale and per-(hash,file) dedupe as
// VerifyFile, applied file by file (sequential: cost proportional to what's on
// disk; missing pieces fail the hash quickly via sparse reads).
func (s *Streamer) VerifyTorrent(ctx context.Context, hash metainfo.Hash) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return ErrTorrentNotActive
	}
	for i, f := range e.t.Files() {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.verifyFilePieces(ctx, hash, i, f)
	}
	return ctx.Err()
}

// RecheckAllFiles forces "Force Recheck" on ALL files of a torrent
// (whole-torrent download). Same contract as RecheckFile; files are re-hashed
// sequentially — a torrent with thousands of files doesn't fire thousands of
// concurrent hash loops.
func (s *Streamer) RecheckAllFiles(ctx context.Context, hash metainfo.Hash) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return ErrTorrentNotActive
	}
	for i, f := range e.t.Files() {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := fmt.Sprintf("%s-%d", hash.HexString(), i)
		s.verifiedMu.Lock()
		delete(s.verifiedFiles, key)
		s.verifiedMu.Unlock()
		if err := s.recheckFilePieces(ctx, key, f); err != nil {
			return err
		}
	}
	return nil
}

// RecheckFile forces a full re-verification of a file's pieces, IGNORING the
// verifiedFiles dedupe and re-hashing even pieces currently marked "complete".
// Use case: manual user action via the UI ("recheck") when they suspect the
// on-disk bytes are corrupted (BitErrors) or when the size/count in
// downloads.db doesn't match reality. Unlike VerifyFile, which skips
// already-complete pieces and dedupes per process, this validates everything
// again — semantics equivalent to qBittorrent's "Force Recheck". Blocks until
// done: the HTTP handler only re-enqueues afterwards.
func (s *Streamer) RecheckFile(ctx context.Context, hash metainfo.Hash, fileIdx int) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	s.mu.Unlock()
	if !ok {
		return ErrTorrentNotActive
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return fmt.Errorf(errFileIndexOutOfRange, fileIdx)
	}
	// Releases the dedup claim before re-hashing — that way the verification
	// actually runs. Keeps the guard: if another recheck is already in flight for
	// the same (hash,fileIdx), LoadOrStore returns loaded=true and the 2nd call
	// becomes a no-op.
	key := fmt.Sprintf("%s-%d", hash.HexString(), fileIdx)
	s.verifiedMu.Lock()
	delete(s.verifiedFiles, key)
	s.verifiedMu.Unlock()
	f := files[fileIdx]
	return s.recheckFilePieces(ctx, key, f)
}

// recheckFilePieces re-hashes every piece of a file (no skip-complete). Returns
// the first VerifyData error so callers can fail the recheck end-to-end.
func (s *Streamer) recheckFilePieces(ctx context.Context, key string, f *torrent.File) error {
	// Marks as in-progress before hashing so concurrent calls don't fire a
	// 2nd pass.
	s.verifiedMu.Lock()
	if s.verifiedFiles == nil {
		s.verifiedFiles = make(map[string]bool)
	}
	_, loaded := s.verifiedFiles[key]
	if !loaded {
		// No blunt wipe-at-2000 here: keys are purged per-torrent on Drop
		// (purgeVerifiedFiles), so this map tracks only currently-active torrents.
		s.verifiedFiles[key] = true
	}
	s.verifiedMu.Unlock()
	if loaded {
		return nil
	}

	completed := false
	defer func() {
		if !completed {
			s.verifiedMu.Lock()
			delete(s.verifiedFiles, key)
			s.verifiedMu.Unlock()
		}
	}()
	// One multi-GB recheck at a time — concurrent RecheckFile calls thrash disk.
	if !s.acquireVerify(ctx, key) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return ErrVerifyLimiterClosed
	}
	defer s.releaseVerify(key)
	for p := range f.Pieces() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.VerifyData(); err != nil {
			return err
		}
	}
	completed = true
	return nil
}

// verifyFilePieces hash-checks the on-disk pieces backing a single file so the
// scheduler reuses the cache instead of re-downloading. Runs once per
// (hash,fileIdx) per process. Verifying only this file's piece range keeps the
// cost proportional to what's being watched, not the whole (possibly huge)
// torrent. Pieces missing from disk fail their hash quickly (sparse reads).
func (s *Streamer) verifyFilePieces(ctx context.Context, hash metainfo.Hash, fileIdx int, f *torrent.File) {
	key := fmt.Sprintf("%s-%d", hash.HexString(), fileIdx)
	// Claim the file so two concurrent readers don't both hash it.
	s.verifiedMu.Lock()
	if s.verifiedFiles == nil {
		s.verifiedFiles = make(map[string]bool)
	}
	_, loaded := s.verifiedFiles[key]
	if !loaded {
		// No blunt wipe-at-2000 here: keys are purged per-torrent on Drop
		// (purgeVerifiedFiles), so this map tracks only currently-active torrents.
		s.verifiedFiles[key] = true
	}
	s.verifiedMu.Unlock()
	if loaded {
		return // already reconciled (or in progress) for this file
	}
	// If we bail before finishing (panic, or the torrent gets dropped mid-loop),
	// drop the claim so a later read can retry. Marking "verified" up front and
	// never clearing it meant an interrupted pass disabled reconciliation for
	// the whole process lifetime → re-downloading pieces already on disk.
	completed := false
	defer func() {
		if !completed {
			s.verifiedMu.Lock()
			delete(s.verifiedFiles, key)
			s.verifiedMu.Unlock()
		}
	}()
	// Serialize hashing across torrents: N parallel download inits each
	// VerifyFile a multi-GB pack after restart and starve the HDD.
	if !s.acquireVerify(ctx, key) {
		return
	}
	defer s.releaseVerify(key)
	for p := range f.Pieces() {
		if err := ctx.Err(); err != nil {
			return
		}
		// Only verify pieces that have bytes on disk; fully-missing pieces have
		// nothing to reconcile and verifying them just wastes a hash pass.
		if p.State().Complete {
			continue
		}
		if err := p.VerifyData(); err != nil {
			log.Printf("streamer: piece verify I/O error (key=%s): %v", key, err)
		}
	}
	completed = true
}

// warmTail prioritizes the last few MB of a file so the container index
// (moov/Cues) is downloading before ffmpeg seeks to it. Best-effort, bounded:
// opens its own reader (independent cursor, no contention with the main read),
// reads a small tail window, then closes after a short grace period.
func (s *Streamer) warmTail(f *torrent.File) {
	const tail = 8 << 20 // 8 MiB from the end
	length := f.Length()
	if length <= tail {
		return // small file — head readahead already covers it
	}
	r := f.NewReader()
	r.SetReadahead(tail)
	r.SetResponsive()
	if _, err := r.Seek(length-tail, io.SeekStart); err != nil {
		_ = r.Close()
		return
	}
	buf := make([]byte, 256<<10)
	done := make(chan struct{})
	go func() {
		_, _ = r.Read(buf) // commit the priority hint; bytes themselves discarded
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
	}
	_ = r.Close()
}
