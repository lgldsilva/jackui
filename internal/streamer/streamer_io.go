package streamer

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// Close shuts down the torrent client and releases storage.
func (s *Streamer) Close() {
	close(s.stop)
	s.client.Close()
	// Fecha o storage mmap (libera mapeamentos/handles). FileStorage default é
	// gerido pelo client, então storageImpl é nil nesse caso.
	if s.storageImpl != nil {
		_ = s.storageImpl.Close()
	}
	if s.dlPieceCompletion != nil {
		_ = s.dlPieceCompletion.Close()
	}
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
		return nil, nil, fmt.Errorf("índice de arquivo %d fora do intervalo (0..%d)", fileIdx, len(files)-1)
	}
	f := files[fileIdx]

	r := f.NewReader()
	// Readahead sized for the HLS transcode path. ffmpeg reads the source
	// sequentially and each 4s HLS segment of 4K video pulls ~15 MB; with only
	// 8 MiB of readahead the anacrolix Reader blocks waiting for the next piece
	// mid-segment, and WaitForMaster times out before the first segment lands
	// (confirmed on the GTX 1070 with 2160p sources). 32 MiB covers ~2 segments
	// of 4K lookahead so the encoder never starves on a healthy swarm. Configurável
	// via StreamConfig.ReadaheadMB (default 32) — ver streamReadahead().
	r.SetReadahead(s.streamReadahead())
	r.SetResponsive() // prioritize pieces around current read position

	// Reconcile THIS file's cache against the disk, once. anacrolix assumes an
	// empty store on add and would re-download pieces we already have (seen in
	// prod: 1.16 GB on disk, 0 reported). Scoped to the single file (not the
	// whole torrent) so a season pack doesn't trigger a multi-GB hash storm
	// that starves the encoder. Runs before warmTail so verified head pieces
	// are ready when ffmpeg starts reading.
	go s.verifyFilePieces(hash, fileIdx, f)

	// Warm the TAIL of the file in the background. Container indexes live at the
	// end: MP4 `moov` (non-faststart) and Matroska `Cues` both sit near EOF.
	// ffmpeg seeks there during demux init; if those pieces aren't downloaded,
	// the read blocks ~10s+ AND (since reads are serialized) head-of-lines the
	// sequential probe read, so the first segment never lands inside the wait
	// window. Kicking off the tail pieces NOW — on a separate reader, concurrent
	// with the head — means they're already arriving when ffmpeg asks.
	go s.warmTail(f)

	return &trackingReader{Reader: r, streamer: s, hash: hash}, f, nil
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
		return errors.New("torrent não ativo — chamar /stream/add primeiro")
	}
	files := e.t.Files()
	if fileIdx < 0 || fileIdx >= len(files) {
		return fmt.Errorf("file index %d fora do intervalo (0..%d)", fileIdx, len(files)-1)
	}
	f := files[fileIdx]
	r := f.NewReader()
	r.SetReadahead(8 << 20) // 8 MiB — enough to cover the first few seconds
	r.SetResponsive()
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
		r.Close()
		return fmt.Errorf("prefetch seek: %w", err)
	}
	// Tiny read just to commit the readahead hint and trigger piece priority.
	go func() {
		buf := make([]byte, 256<<10) // 256 KiB
		// Best-effort read with a soft deadline: even if it blocks waiting for
		// peers, the readahead is already registered so closing later still
		// leaves the pieces queued in anacrolix.
		done := make(chan struct{})
		go func() {
			_, _ = r.Read(buf)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
		r.Close()
	}()
	return nil
}

// activeReadGuard: a torrent read within this window is treated as still being
// watched, so an explicit Drop() (player close) is skipped. trackingReader bumps
// lastAccess on every read, including the HLS transcode's source reads.
const activeReadGuard = 60 * time.Second

// Drop forcibly removes a torrent (stops download, keeps files until GC).
func (s *Streamer) Drop(hash metainfo.Hash) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		// Do not drop while a player still holds a viewer lease — the lease is
		// the authoritative "someone is watching" signal. A forced Drop (manual
		// StreamDrop, health probe) must not kill a co-watcher's playback.
		if e.viewers > 0 {
			s.mu.Unlock()
			return
		}
		// Do not drop if it is registered as an active background download
		if _, protected := s.downloads[e.t.Name()]; protected {
			s.mu.Unlock()
			return
		}
		// Do not drop a torrent another reader is actively streaming. The player
		// calls Drop() on close, but with MULTIPLE sessions on the same torrent
		// (e.g. two browsers, or an HLS transcode still pulling segments for
		// another viewer), an eager drop killed the survivors' ffmpeg mid-playback
		// ("torrent closed" → demux I/O error → segment 404). A recent read means
		// someone is still watching — leave eviction to the idle reaper.
		if time.Since(e.lastAccess) < activeReadGuard {
			s.mu.Unlock()
			return
		}
		delete(s.active, hash)
	}
	s.mu.Unlock()
	if ok {
		e.t.Drop()
		s.purgeVerifiedFiles(hash)
	}
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

// trackingReader wraps a torrent.Reader so each read refreshes lastAccess.
type trackingReader struct {
	torrent.Reader
	streamer *Streamer
	hash     metainfo.Hash
}

func (r *trackingReader) Read(p []byte) (int, error) {
	r.streamer.mu.Lock()
	if e, ok := r.streamer.active[r.hash]; ok {
		e.lastAccess = time.Now()
	}
	r.streamer.mu.Unlock()
	return r.Reader.Read(p)
}
