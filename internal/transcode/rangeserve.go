package transcode

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

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
	if _, err := r.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	return io.ReadFull(r, p)
}

// size returns the total length via Seek(0, end) under the lock so a stray
// concurrent Range can't move the cursor out from under us. Restores the
// cursor to 0 on the way out — though the lock guarantees no caller observes
// the transient position.
func (r *readSeekerContent) size() (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	_, _ = r.Seek(0, io.SeekStart)
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
		serveWholeFile(w, r, src, totalSize)
		return
	}

	start, end, ok := parseRange(rangeHeader, totalSize)
	if !ok {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(totalSize, 10))
		http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	serveRangeFile(w, r, src, totalSize, start, end)
}

func serveWholeFile(w http.ResponseWriter, r *http.Request, src *readSeekerContent, totalSize int64) {
	w.Header().Set("Content-Length", strconv.FormatInt(totalSize, 10))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
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
}

func serveRangeFile(w http.ResponseWriter, r *http.Request, src *readSeekerContent, totalSize, start, end int64) {
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
