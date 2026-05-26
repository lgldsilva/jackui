package transcode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HLSSessionManager owns the lifecycle of ffmpeg-driven HLS transcoding
// sessions. One session per (info_hash, file_index, encoder options) tuple —
// concurrent viewers of the same content share a single ffmpeg + segments dir.
//
// Why HLS specifically: Safari (macOS + iOS) refuses progressive fragmented MP4
// via <video src> with chunked transfer encoding. Empirically every
// combination of -movflags, profile, level, GOP, B-frame count we tried
// produced bytes Safari rejects with MediaError.SRC_NOT_SUPPORTED before any
// frames decode. Apple's documented streaming path is HLS (.m3u8 + .ts
// segments) — `<video src="...m3u8">` is the only thing Safari treats as a
// first-class video source. Jellyfin / Plex / Emby all do this for browser
// clients. Stop trial-and-error, follow Apple's contract.
//
// Trade-off vs progressive MP4: needs disk space for ~2-segment-buffer per
// active session (~20-40 MB at 720p, ~80 MB at 1080p) and a small directory
// per session. ffmpeg writes segments to disk as it encodes; the handler
// serves them on request. Cleanup on idle keeps the footprint bounded.
type HLSSessionManager struct {
	baseDir string
	mu      sync.Mutex
	sess    map[string]*HLSSession
}

// HLSSession is a single ongoing HLS transcode. Same key = same session
// (deduped across concurrent requests for the same content).
type HLSSession struct {
	Key        string
	Dir        string
	Cmd        *exec.Cmd
	Cancel     context.CancelFunc
	StartedAt  time.Time
	LastAccess time.Time
	mu         sync.Mutex
	closed     bool
	// sourceSrv is an ephemeral HTTP loopback server that exposes the input
	// source (an io.ReadSeeker over the torrent file) so ffmpeg can fetch it
	// via Range requests. Without this, ffmpeg consumes stdin as a non-
	// seekable pipe — fatal for MP4 sources whose `moov` atom is at the END
	// of the file, since pipe input has no way to seek back to read it.
	sourceSrv *http.Server
}

// NewHLSManager constructs a manager rooted at baseDir/hls/. The directory
// is created on demand; existing contents (from previous server runs) are
// purged to avoid serving stale segments tied to old encoder options.
func NewHLSManager(baseDir string) (*HLSSessionManager, error) {
	root := filepath.Join(baseDir, "hls")
	if err := os.RemoveAll(root); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	m := &HLSSessionManager{
		baseDir: root,
		sess:    make(map[string]*HLSSession),
	}
	go m.gcLoop()
	return m, nil
}

// gcLoop reaps sessions idle for more than 60s. Real users keep the segment
// loop hot (every 4s a new segment fetched); 60s of silence means tab closed
// / network gone / user moved on. Ffmpeg keeps writing forever if we let it.
func (m *HLSSessionManager) gcLoop() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for range tick.C {
		now := time.Now()
		m.mu.Lock()
		for k, s := range m.sess {
			s.mu.Lock()
			idle := now.Sub(s.LastAccess)
			closed := s.closed
			s.mu.Unlock()
			if closed || idle > 60*time.Second {
				log.Printf("hls: reaping idle session %s (idle=%s)", k, idle)
				s.stop()
				delete(m.sess, k)
			}
		}
		m.mu.Unlock()
	}
}

// HLSStartOpts groups what's needed to spin up a session. The source is the
// torrent file source; the manager doesn't know about anacrolix specifically.
//
// IMPORTANT: Source MUST implement io.ReadSeeker. We expose it to ffmpeg via
// an ephemeral loopback HTTP server (one per session) so ffmpeg can issue
// Range requests and seek freely. Direct pipe-to-stdin (the previous design)
// fails on MP4 with `moov` at end of file because pipe input is non-seekable
// — ffmpeg can't walk past a multi-GB mdat box to read the metadata.
type HLSStartOpts struct {
	Key                 string        // unique session id, typically `${hash}-${fileIdx}-${codec}`
	Source              io.ReadSeeker // seekable input — wrapped by an internal HTTP server
	SourceSize          int64         // total size hint; required when the underlying reader lies about EOF
	VideoCodec          string        // "h264_nvenc" | "libx264" | etc.
	PreserveSourceAudio bool          // when true and source audio is AAC, -c:a copy; else transcode to AAC
}

// readSeekerContent adapts a single-cursor io.ReadSeeker (e.g. anacrolix
// torrent.Reader) so the source server can answer concurrent Range requests
// CORRECTLY.
//
// CRITICAL: serialising Seek and Read independently (each with its own lock
// acquire/release) is NOT enough. Two concurrent handlers can interleave as:
//
//	A: Seek(1000)  [unlock]
//	B: Seek(50000) [unlock]
//	A: Read(buf)   → reads bytes from offset 50000, not 1000
//
// ffmpeg with -multiple_requests 1 fires concurrent Range GETs, and the
// production failure mode was exactly this — the MP4 demuxer parsed STSC
// (sample-to-chunk) entries that were "valid bytes from another atom" and
// died with "stream 1, contradictionary STSC and STCO". Single-byte counter
// example: under the bug, expected byte=10 at offset 512, got byte=20 (from
// some other offset's payload).
//
// Fix: expose readAt(p, off) that holds the mutex across Seek+Read so the
// pair is atomic per handler. The source server calls readAt per Range.
type readSeekerContent struct {
	mu sync.Mutex
	io.ReadSeeker
}

// readAt does an atomic Seek+Read under a single lock so concurrent handlers
// can't cross-pollinate the cursor. Returns io.EOF when the underlying reader
// signals it. n may be < len(p) on short reads — callers should loop.
func (r *readSeekerContent) readAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.ReadSeeker.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return io.ReadFull(r.ReadSeeker, p)
}

// size returns the total length via Seek(0, end) under the lock so a stray
// concurrent Range can't move the cursor out from under us. Restores the
// cursor to 0 on the way out — though the lock guarantees no caller observes
// the transient position.
func (r *readSeekerContent) size() (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	end, err := r.ReadSeeker.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	_, _ = r.ReadSeeker.Seek(0, io.SeekStart)
	return end, nil
}

// serveSource is the loopback HTTP handler ffmpeg fetches via. We do NOT use
// http.ServeContent because it calls Seek and Read separately on the reader
// — those separate calls open a race window where concurrent handlers swap
// the cursor between each other. We handle Range parsing ourselves and pass
// the (offset, length) pair to readAt which holds the lock end-to-end.
//
// We only implement the byte-range syntax ffmpeg actually emits: a single
// `bytes=start-end` or `bytes=start-`. Multipart ranges and suffix-length
// (`bytes=-N`) aren't used here so we keep the code path tight.
func serveSource(w http.ResponseWriter, r *http.Request, src *readSeekerContent, totalSize int64) {
	w.Header().Set("Accept-Ranges", "bytes")

	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		// Whole-file request. ffmpeg issues this for the initial probe.
		w.Header().Set("Content-Length", strconv.FormatInt(totalSize, 10))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		// Stream in chunks to avoid materialising the whole file in memory.
		// Each chunk is an atomic readAt — concurrent Range handlers stay
		// correct because we hold the lock per chunk, not per byte.
		buf := make([]byte, 256<<10)
		var off int64
		for off < totalSize {
			toRead := int64(len(buf))
			if remaining := totalSize - off; remaining < toRead {
				toRead = remaining
			}
			n, err := src.readAt(buf[:toRead], off)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				off += int64(n)
			}
			if err != nil {
				return
			}
		}
		return
	}

	start, end, ok := parseRange(rangeHeader, totalSize)
	if !ok {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(totalSize, 10))
		http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	length := end - start + 1
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, totalSize))
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusPartialContent)
	if r.Method == http.MethodHead {
		return
	}

	buf := make([]byte, 256<<10)
	off := start
	for off <= end {
		toRead := int64(len(buf))
		if remaining := end - off + 1; remaining < toRead {
			toRead = remaining
		}
		n, err := src.readAt(buf[:toRead], off)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			off += int64(n)
		}
		if err != nil {
			return
		}
	}
}

// parseRange handles the two forms ffmpeg emits: "bytes=start-end" and
// "bytes=start-". Returns the resolved inclusive [start,end] in absolute
// bytes plus an ok flag; clamps end to totalSize-1.
func parseRange(header string, totalSize int64) (int64, int64, bool) {
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(header, "bytes=")
	if strings.Contains(spec, ",") {
		return 0, 0, false
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, false
	}
	startStr := spec[:dash]
	endStr := spec[dash+1:]
	if startStr == "" {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 || start >= totalSize {
		return 0, 0, false
	}
	end := totalSize - 1
	if endStr != "" {
		parsed, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || parsed < start {
			return 0, 0, false
		}
		if parsed < end {
			end = parsed
		}
	}
	return start, end, true
}

// GetOrStart returns an existing session keyed by opts.Key or starts a new one.
// On new-session, ffmpeg begins encoding immediately; the caller should poll
// for index.m3u8 to appear via WaitForMaster.
func (m *HLSSessionManager) GetOrStart(ctx context.Context, opts HLSStartOpts) (*HLSSession, error) {
	m.mu.Lock()
	if s, ok := m.sess[opts.Key]; ok {
		s.mu.Lock()
		s.LastAccess = time.Now()
		s.mu.Unlock()
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	caps := Cached()
	if caps == nil {
		return nil, errors.New("transcode caps not probed yet")
	}

	dir := filepath.Join(m.baseDir, opts.Key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir hls dir: %w", err)
	}

	// ffmpeg arguments — H.264 Main 4.0 + AAC 2ch, HLS event-stream output.
	// Notes per flag:
	//   -re                — don't read input faster than realtime (matches
	//                        encoder pace, avoids buffer overrun on the pipe)
	//                        DISABLED for torrent input since anacrolix throttles naturally.
	//   -hls_time 4        — 4-second segments. Smaller = lower start latency
	//                        but more files; 4s is the sweet spot most CDNs use.
	//   -hls_list_size 0   — keep all segments in the playlist (we serve VOD)
	//   -hls_flags delete_segments+temp_file
	//                      — delete stale segments only when EXPLICITLY rotated
	//                        (we don't rotate); temp_file means write `.ts.tmp`
	//                        then rename to `.ts` so the handler never serves
	//                        a half-written file.
	//   -hls_playlist_type vod — Safari/iOS prefer VOD over EVENT for finite content
	//   -c:v h264_*        — NVENC/VAAPI/QSV/libx264, picked by caps
	//   -pix_fmt yuv420p   — force 8-bit (NVENC h264 can't do 10-bit)
	//   -profile:v main -level:v 5.2 — Safari-friendly codec string. Level 5.2
	//                        covers up to 4K@60fps. Previously we hardcoded 4.0
	//                        which capped at 1080p@30 and caused NVENC to error
	//                        "Invalid Level" on any 2160p source. Safari iOS 11+,
	//                        macOS Safari and modern Edge all accept up to L5.2.
	//                        The cost of overshoot on a 720p source is zero —
	//                        the level field is metadata only, not encode work.
	//   -g 60 -bf 0        — keyframe every 2s, no B-frames (HLS demands keyframe
	//                        at segment boundary; aligning -g with segment length
	//                        avoids "key frame may not be reached" warnings)
	//   -sn -dn -map_chapters -1 -map_metadata -1 — strip extra tracks Safari rejects
	encoder := caps.Preferred
	if opts.VideoCodec != "" {
		encoder = opts.VideoCodec
	}

	// Stand up an ephemeral loopback HTTP server that serves the source via
	// http.ServeContent — gives ffmpeg full Range support so it can seek to
	// `moov` atoms at end of file (the production failure mode with MP4
	// torrents that aren't faststart-encoded).
	if opts.Source == nil {
		return nil, errors.New("HLSStartOpts.Source is required (seekable input)")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("loopback listen: %w", err)
	}
	srcSize := opts.SourceSize
	sourceReader := &readSeekerContent{ReadSeeker: opts.Source}
	// If the caller didn't pass a size we discover it once at startup. This
	// path is exercised mainly by tests; production always supplies SourceSize
	// from torrent.File.Length().
	if srcSize <= 0 {
		if sz, err := sourceReader.size(); err == nil {
			srcSize = sz
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/source", func(w http.ResponseWriter, r *http.Request) {
		// Custom Range handler — see serveSource for the race condition that
		// makes http.ServeContent unsafe with a single-cursor underlying reader.
		serveSource(w, r, sourceReader, srcSize)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()
	inputURL := fmt.Sprintf("http://%s/source", listener.Addr().String())

	args := []string{
		"-hide_banner", "-loglevel", "warning",
		// `-seekable 1` + `-multiple_requests 1` let ffmpeg issue multiple
		// Range GETs against the loopback URL — needed for the MP4 demuxer
		// to walk past a giant `mdat` and seek to `moov` at end of file.
		// `-probesize 50M` + `-analyzeduration 5M` give ffmpeg enough room
		// to read the moov even when it's deep into the file.
		"-seekable", "1",
		"-multiple_requests", "1",
		"-probesize", "50M",
		"-analyzeduration", "5M",
		"-i", inputURL,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-sn", "-dn", "-map_chapters", "-1", "-map_metadata", "-1",
		"-c:v", encoder,
	}
	args = append(args, encoderPresetArgs(encoder)...)
	args = append(args,
		"-pix_fmt", "yuv420p",
		"-profile:v", "main", "-level:v", "5.2",
		"-g", "60", "-bf", "0",
		"-c:a", "aac", "-b:a", "192k", "-ac", "2",
		"-f", "hls",
		"-hls_time", "4",
		"-hls_list_size", "0",
		// `temp_file` makes ffmpeg write to `.ts.tmp` then atomic-rename to
		// `.ts` so the segment handler never serves a half-written file.
		// `independent_segments` declares every segment starts with a
		// keyframe — required for HLS over fragmented MP4 transcode and
		// for Safari's "can decode this segment standalone" check.
		// `append_list` keeps adding entries as segments are produced
		// (default behaviour during live encode; explicit for clarity).
		"-hls_flags", "temp_file+independent_segments+append_list",
		// Note: we INTENTIONALLY don't set `-hls_playlist_type vod` here.
		// vod means "all segments declared up-front, writer waits till end".
		// We want incremental playlist writes so Safari can start fetching
		// segments while encoding is still running. Live-style playlist
		// grows until ffmpeg finishes, then gets an `#EXT-X-ENDLIST` tag.
		// `-hls_playlist_type vod` declares the stream as finite Video-on-Demand
		// in the playlist header (`#EXT-X-PLAYLIST-TYPE:VOD`). Without this flag,
		// Safari and other HLS players assume LIVE: the seekbar is hidden, and
		// clicking it jumps to the "live edge" (the start of the playlist while
		// encoding is in progress) — both reported by users.
		// CRITICAL: VOD does NOT block incremental writes. ffmpeg keeps
		// appending segments as it encodes; the EXT-X-ENDLIST marker is
		// written when encode finishes. Clients see growing playlist + VOD
		// type + correct seek-to-timestamp behavior.
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(dir, "seg_%05d.ts"),
		"-y",
		filepath.Join(dir, "index.m3u8"),
	)

	ffctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ffctx, caps.FFmpegPath, args...)
	cmd.Stderr = newLogWriter("hls/" + opts.Key + " ")

	log.Printf("hls: starting session %s\nffmpeg %s", opts.Key, strings.Join(args, " "))

	if err := cmd.Start(); err != nil {
		cancel()
		_ = srv.Close()
		os.RemoveAll(dir)
		return nil, fmt.Errorf("ffmpeg start: %w", err)
	}

	s := &HLSSession{
		Key:        opts.Key,
		Dir:        dir,
		Cmd:        cmd,
		Cancel:     cancel,
		StartedAt:  time.Now(),
		LastAccess: time.Now(),
		sourceSrv:  srv,
	}

	// Watch ffmpeg exit to mark session closed and tear down the loopback
	// HTTP server (otherwise it leaks a listening socket per finished session).
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		cancelShutdown()
		if err != nil && !errors.Is(ffctx.Err(), context.Canceled) {
			log.Printf("hls: ffmpeg exited for session %s: %v", opts.Key, err)
		}
	}()

	m.mu.Lock()
	m.sess[opts.Key] = s
	m.mu.Unlock()

	return s, nil
}

// WaitForMaster blocks up to `timeout` waiting for the master `index.m3u8`
// to appear. ffmpeg only writes the playlist after the first segment is
// completely encoded, so the wait is bounded by `-hls_time 4` plus encoder
// startup. We bail if the session ends without writing one.
func (s *HLSSession) WaitForMaster(timeout time.Duration) error {
	path := filepath.Join(s.Dir, "index.m3u8")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check the file FIRST so a fast encode (whole input <4s) that
		// closes between our last check and now still gets a positive
		// result. The previous order returned "session ended" for files
		// short enough that ffmpeg exited inside the 150ms sleep window.
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			return nil
		}
		s.mu.Lock()
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
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("hls master not ready within %s", timeout)
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
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
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
		time.Sleep(150 * time.Millisecond)
	}
	return "", fmt.Errorf("segment %s not ready within %s", name, timeout)
}

// stop kills ffmpeg, shuts down the loopback source server, and removes the
// segment dir. Idempotent.
func (s *HLSSession) stop() {
	s.mu.Lock()
	already := s.closed
	s.mu.Unlock()
	if s.Cancel != nil {
		s.Cancel()
	}
	if !already && s.Cmd != nil && s.Cmd.Process != nil {
		_ = s.Cmd.Process.Kill()
	}
	if s.sourceSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.sourceSrv.Shutdown(shutdownCtx)
		cancel()
	}
	_ = os.RemoveAll(s.Dir)
}

// Peek returns an existing session without starting one. Used by the
// segment handler which must NOT race the playlist handler into creating
// a duplicate ffmpeg. Returns an error when the session isn't tracked.
func (m *HLSSessionManager) Peek(key string) (*HLSSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sess[key]
	if !ok {
		return nil, errors.New("session not found")
	}
	s.mu.Lock()
	s.LastAccess = time.Now()
	s.mu.Unlock()
	return s, nil
}

// Close terminates the session immediately. Called by handlers when the
// underlying torrent is dropped or the user explicitly cancels.
func (m *HLSSessionManager) Close(key string) {
	m.mu.Lock()
	s, ok := m.sess[key]
	if ok {
		delete(m.sess, key)
	}
	m.mu.Unlock()
	if ok {
		s.stop()
	}
}

// logWriter routes ffmpeg stderr lines to log.Printf with a stable prefix.
type logWriter struct {
	prefix string
	buf    []byte
}

func newLogWriter(prefix string) *logWriter { return &logWriter{prefix: prefix} }

func (w *logWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if line != "" {
			log.Print(w.prefix, line)
		}
	}
	return len(p), nil
}
