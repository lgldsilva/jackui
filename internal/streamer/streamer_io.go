package streamer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// Close shuts down the torrent client and releases storage. Idempotent: a
// second call is a no-op (close(s.stop) used to panic on the double close).
func (s *Streamer) Close() {
	s.closeOnce.Do(func() {
		if s.lifetimeCancel != nil {
			s.lifetimeCancel()
		}
		if s.verifyLim != nil {
			s.verifyLim.Shutdown()
		}
		if s.stop != nil {
			close(s.stop)
		}
		s.client.Close()
		// Closes the mmap storage (releases mappings/handles). The default FileStorage
		// is managed by the client, so storageImpl is nil in that case.
		if s.storageImpl != nil {
			_ = s.storageImpl.Close()
		}
		if s.dlPieceCompletion != nil {
			_ = s.dlPieceCompletion.Close()
		}
	})
}

// FileReader returns a ReadSeeker for one file, configured for streaming.
// The reader keeps the torrent alive (refreshes lastAccess on each read).
func (s *Streamer) FileReader(hash metainfo.Hash, fileIdx int) (io.ReadSeekCloser, *torrent.File, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		e.lastAccess = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return nil, nil, ErrTorrentNotActive
	}

	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return nil, nil, fmt.Errorf("file index %d out of range (0..%d)", fileIdx, len(files)-1)
	}
	f := files[fileIdx]

	r := f.NewReader()
	// Readahead sized for the HLS transcode path. ffmpeg reads the source
	// sequentially and each 4s HLS segment of 4K video pulls ~15 MB; with only
	// 8 MiB of readahead the anacrolix Reader blocks waiting for the next piece
	// mid-segment, and WaitForMaster times out before the first segment lands
	// (confirmed on the GTX 1070 with 2160p sources). 32 MiB covers ~2 segments
	// of 4K lookahead so the encoder never starves on a healthy swarm. Configurable
	// via StreamConfig.ReadaheadMB (default 32) — see streamReadahead().
	r.SetReadahead(s.streamReadahead())
	r.SetResponsive() // prioritize pieces around current read position

	// Reconcile THIS file's cache against the disk, once. anacrolix assumes an
	// empty store on add and would re-download pieces we already have (seen in
	// prod: 1.16 GB on disk, 0 reported). Scoped to the single file (not the
	// whole torrent) so a season pack doesn't trigger a multi-GB hash storm
	// that starves the encoder. Runs before warmTail so verified head pieces
	// are ready when ffmpeg starts reading.
	go s.verifyFilePieces(s.lifetimeCtx, hash, fileIdx, f)

	// Warm the TAIL of the file in the background. Container indexes live at the
	// end: MP4 `moov` (non-faststart) and Matroska `Cues` both sit near EOF.
	// ffmpeg seeks there during demux init; if those pieces aren't downloaded,
	// the read blocks ~10s+ AND (since reads are serialized) head-of-lines the
	// sequential probe read, so the first segment never lands inside the wait
	// window. Kicking off the tail pieces NOW — on a separate reader, concurrent
	// with the head — means they're already arriving when ffmpeg asks.
	go s.warmTail(f)

	tr := &trackingReader{
		Reader:   r,
		streamer: s,
		hash:     hash,
		file:     f,
		window:   s.streamReadahead(),
	}
	tr.applyWindow(0)
	return tr, f, nil
}

// Prefetch hints the anacrolix piece scheduler to start downloading the head of
// `fileIdx` on the already-active torrent, *without* serving any bytes back.
//
// Use case: while the user watches episode N of a series, we kick off pieces of
// N+1 in the background so the cut between episodes is near-instantaneous.
// Same idea for the next item in a playlist when it's the same torrent.
//
// Implementation: opens a Reader, seeks to 0, sets a generous readahead, reads
// a small head chunk, then closes after a short delay so the priority hint
// outlives the request lifecycle. The bytes already on disk stay there — only
// the in-memory priority hint goes away when the reader closes.
//
// Returns immediately; the actual download is asynchronous in anacrolix.
func (s *Streamer) Prefetch(hash metainfo.Hash, fileIdx int) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		e.lastAccess = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return errors.New("torrent not active — call /stream/add first")
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return fmt.Errorf("file index %d out of range (0..%d)", fileIdx, len(files)-1)
	}
	f := files[fileIdx]
	r := f.NewReader()
	r.SetReadahead(8 << 20) // 8 MiB — enough to cover the first few seconds
	r.SetResponsive()
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		_ = r.Close()
		return fmt.Errorf("prefetch seek: %w", err)
	}
	// Tiny read just to commit the readahead hint and trigger piece priority.
	// Bounded through the reader's OWN context: Reader.Close() does not unblock
	// a Read blocked in waitAvailable, so the old detached read goroutine
	// leaked past the soft deadline on a stalled swarm. The budget is
	// unchanged; the readahead is registered before the read, so the hint
	// survives even when it times out. The budget is read here (caller's
	// goroutine) and passed in, so the goroutine never touches the package var
	// (which tests shrink and restore).
	budget := prefetchReadBudget
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		r.SetContext(ctx)
		buf := make([]byte, 256<<10) // 256 KiB
		_, _ = r.Read(buf)
		_ = r.Close()
	}()
	return nil
}

// prefetchReadBudget bounds Prefetch's hint-commit read. Var (not const) so
// tests can shrink it; the default is the production budget.
var prefetchReadBudget = 5 * time.Second

// activeReadGuard: a torrent read within this window is treated as still being
// watched, so an explicit Drop() (player close) is skipped. trackingReader bumps
// lastAccess on every read, including the HLS transcode's source reads.
const activeReadGuard = 60 * time.Second

// errRecentlyRead marks the non-explicit refusal when the torrent was read
// within activeReadGuard. Generic Drop discards it (silent protection); it
// must never leak into user-facing responses — explicit stops bypass this guard.
var errRecentlyRead = errors.New("torrent read within activeReadGuard")

// Drop forcibly removes a torrent (stops download, keeps files until GC).
// Lifecycle/cleanup path (idle reaper, health probe, move teardown): refusal
// is the intended protection and is intentionally silent.
func (s *Streamer) Drop(hash metainfo.Hash) {
	_ = s.drop(hash, false)
}

// drop removes a torrent honoring the protection guards. explicit=true marks a
// user-initiated stop/remove ("Stop", stop-seed, row delete): it bypasses ONLY
// the 60s activeReadGuard — that guard exists to protect co-watcher playback,
// but MONITORING reads (the *arr stack's torrent-get polls refresh lastAccess
// every ~60s) were keeping it permanently armed, so every explicit stop was
// silently refused (incident 2026-09-28: 6× DELETE /api/stream/:hash → 200,
// torrent never stopped). Viewer leases and background-download protection
// still win over an explicit stop; the caller surfaces the refusal.
func (s *Streamer) drop(hash metainfo.Hash, explicit bool) error {
	s.mu.Lock()
	e, ok := s.active[hash]
	if !ok {
		s.mu.Unlock()
		return ErrTorrentNotActive
	}
	// Do not drop while a player still holds a viewer lease — the lease is
	// the authoritative "someone is watching" signal. A forced Drop (manual
	// StreamDrop, health probe) must not kill a co-watcher's playback.
	if e.viewers > 0 {
		s.mu.Unlock()
		return ErrTorrentViewerActive
	}
	// Do not drop if it is registered as an active background download
	if _, protected := s.downloads[e.t.Name()]; protected {
		s.mu.Unlock()
		return ErrTorrentDownloadProtected
	}
	// Do not drop a torrent another reader is actively streaming. The player
	// calls Drop() on close, but with MULTIPLE sessions on the same torrent
	// (e.g. two browsers, or an HLS transcode still pulling segments for
	// another viewer), an eager drop killed the survivors' ffmpeg mid-playback
	// ("torrent closed" → demux I/O error → segment 404). A recent read means
	// someone is still watching — leave eviction to the idle reaper.
	if !explicit && time.Since(e.lastAccess) < activeReadGuard {
		s.mu.Unlock()
		return errRecentlyRead
	}
	delete(s.active, hash)
	s.mu.Unlock()
	e.t.Drop()
	s.purgeVerifiedFiles(hash)
	return nil
}

// purgeVerifiedFiles drops the hash-check dedup keys for a torrent when it
// leaves active memory. This per-lifecycle cleanup replaced a blunt
// wipe-the-whole-map-at-2000-entries, which could clear keys for files being
// actively read by another stream and force a needless full re-hash.
func (s *Streamer) purgeVerifiedFiles(hash metainfo.Hash) {
	prefix := hash.HexString() + "-"
	s.verifiedMu.Lock()
	for k := range s.verifiedFiles {
		if strings.HasPrefix(k, prefix) {
			delete(s.verifiedFiles, k)
		}
	}
	s.verifiedMu.Unlock()
}

// trackingReader wraps a torrent.Reader so each read refreshes lastAccess
// and so Seek/Read keep a playhead piece window (Now ahead, Normal behind,
// High on the container tail). anacrolix Reader.SetResponsive already marks
// the cursor; the extra window stops the *previous* 32 MiB from competing
// after a VOD seek.
type trackingReader struct {
	torrent.Reader
	streamer *Streamer
	hash     metainfo.Hash
	file     *torrent.File
	window   int64
	pos      int64
	last     pieceWindow
}

func (r *trackingReader) bumpAccess() {
	r.streamer.mu.Lock()
	if e, ok := r.streamer.active[r.hash]; ok {
		e.lastAccess = time.Now()
	}
	r.streamer.mu.Unlock()
}

func (r *trackingReader) Read(p []byte) (int, error) {
	r.bumpAccess()
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.pos += int64(n)
		r.applyWindow(r.pos)
	}
	return n, err
}

func (r *trackingReader) Seek(off int64, whence int) (int64, error) {
	pos, err := r.Reader.Seek(off, whence)
	if err != nil {
		return pos, err
	}
	r.bumpAccess()
	r.pos = pos
	r.applyWindow(pos)
	return pos, nil
}

func (r *trackingReader) applyWindow(filePos int64) {
	if r.file == nil {
		return
	}
	tor := r.file.Torrent()
	info := tor.Info()
	if info == nil || info.PieceLength <= 0 {
		return
	}
	win := r.window
	if win <= 0 {
		win = streamReadaheadDefault
	}
	next := computePieceWindow(pieceWindowInput{
		fileBegin:  r.file.BeginPieceIndex(),
		fileEnd:    r.file.EndPieceIndex(),
		fileOffset: r.file.Offset(),
		fileLength: r.file.Length(),
		pieceLen:   info.PieceLength,
		playhead:   filePos,
		window:     win,
		tail:       streamTailBytes,
	})
	if next == r.last {
		return
	}
	applyPieceWindow(tor, r.last, next)
	r.last = next
}
