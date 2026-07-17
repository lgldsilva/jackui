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

// Verify/recheck de pieces/arquivos — extraído de streamer.go.
// VerifyFile is the exported entrypoint para o worker de downloads disparar a
// reconciliação de pieces no disco antes de pedir mais dados ao swarm. Reusa
// o mesmo dedupe set (`verifiedFiles`) que o caminho de streaming, então a
// verificação acontece NO MÁXIMO uma vez por (hash, file) por processo —
// não importa se foi streaming ou download que disparou primeiro.
//
// Background: anacrolix tradicionalmente não re-verifica em startup; confia no
// bolt DB. Se o shutdown anterior foi ungraceful (SIGKILL, container OOM), o
// bolt fica desatualizado e anacrolix "esquece" pieces que estão no disco.
// Sem essa chamada, o worker pede ao swarm bytes que já temos.
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
// VerifyFile, applied file by file (sequencial: custo proporcional ao que está
// no disco; pieces ausentes falham o hash rápido via sparse reads).
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

// RecheckAllFiles força o "Force Recheck" em TODOS os arquivos de um torrent
// (download de torrent inteiro). Mesmo contrato do RecheckFile; os arquivos são
// re-hashados sequencialmente — um torrent de milhares de arquivos não dispara
// milhares de hash loops concorrentes.
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

// RecheckFile força uma re-verificação completa dos pieces de um arquivo,
// IGNORANDO o dedup do verifiedFiles e re-hashando até pieces marcados como
// "complete" no momento. Caso de uso: ação manual do user via UI ("recheck")
// quando ele suspeita que os bytes no disco estão corrompidos (BitErrors)
// ou quando o tamanho/contagem do downloads.db não bate com o real.
// Diferente do VerifyFile, que pula pieces já completos e dedupa por processo,
// aqui valida tudo de novo — semantics equivalente ao "Force Recheck" do
// qBittorrent. Bloqueia até terminar: o handler HTTP só reenfileira depois.
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
	// Libera o claim do dedup antes de re-hashar — assim a verificação roda
	// de verdade. Mantém a guarda: se outro recheck já está em voo no mesmo
	// (hash,fileIdx), LoadOrStore retorna loaded=true e a 2ª chamada vira no-op.
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
	// Marca como em-progresso antes da hashagem pra concorrent calls não
	// dispararem 2ª pass.
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
		// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
		r.Close()
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
	// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
	r.Close()
}
