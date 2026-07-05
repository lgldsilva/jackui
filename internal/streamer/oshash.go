package streamer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/anacrolix/torrent/metainfo"
)

// HashResult is the cached OpenSubtitles file hash for one (torrent, file).
type HashResult struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// computeOSHash implements the OpenSubtitles file hash algorithm:
// sum of file size + first 64KB (as uint64s) + last 64KB (as uint64s), little-endian.
// https://trac.opensubtitles.org/projects/opensubtitles/wiki/HashSourceCodes
func computeOSHash(r io.ReadSeeker, size int64) (string, error) {
	const chunkSize = 64 * 1024
	if size < chunkSize {
		return "", errors.New("file smaller than 64KB; OS hash not applicable")
	}

	hash := uint64(size)
	buf := make([]byte, chunkSize)

	// First 64KB from start
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek start: %w", err)
	}
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("read head: %w", err)
	}
	for i := 0; i < chunkSize; i += 8 {
		hash += binary.LittleEndian.Uint64(buf[i:])
	}

	// Last 64KB
	if _, err := r.Seek(-chunkSize, io.SeekEnd); err != nil {
		return "", fmt.Errorf("seek end: %w", err)
	}
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("read tail: %w", err)
	}
	for i := 0; i < chunkSize; i += 8 {
		hash += binary.LittleEndian.Uint64(buf[i:])
	}

	return fmt.Sprintf("%016x", hash), nil
}

// ComputeFileOSHash opens a file on disk and returns its OpenSubtitles hash.
// Used by the /local subtitle endpoints — bypasses the torrent-only OSHash()
// path so legendas funcionam pra arquivos fora do streamer.
func ComputeFileOSHash(f io.ReadSeeker, size int64) (HashResult, error) {
	h, err := computeOSHash(f, size)
	if err != nil {
		return HashResult{}, err
	}
	return HashResult{Hash: h, Size: size}, nil
}

// hashKey identifies a (torrent, file) pair in the hash cache.
type hashKey struct {
	hash    metainfo.Hash
	fileIdx int
}

var (
	hashCacheMu sync.Mutex
	hashCache   = make(map[hashKey]HashResult)
	hashWait    = make(map[hashKey]chan struct{}) // in-flight computations
)

// OSHash returns the OpenSubtitles hash for one file in an active torrent.
// Blocks until the hash is computed or ctx expires. Caches the result.
// Returns ("", 0, error) if the file isn't streamable as hash (too small, not active, timeout).
func (s *Streamer) OSHash(ctx context.Context, hash metainfo.Hash, fileIdx int) (HashResult, error) {
	key := hashKey{hash, fileIdx}

	// Fast path: cached
	hashCacheMu.Lock()
	if cached, ok := hashCache[key]; ok {
		hashCacheMu.Unlock()
		return cached, nil
	}
	// Already computing? wait
	if waitCh, ok := hashWait[key]; ok {
		hashCacheMu.Unlock()
		select {
		case <-waitCh:
			hashCacheMu.Lock()
			if cached, ok := hashCache[key]; ok {
				hashCacheMu.Unlock()
				return cached, nil
			}
			hashCacheMu.Unlock()
			return HashResult{}, errors.New("hash computation failed")
		case <-ctx.Done():
			return HashResult{}, ctx.Err()
		}
	}
	// Start computing
	waitCh := make(chan struct{})
	hashWait[key] = waitCh
	hashCacheMu.Unlock()

	defer func() {
		hashCacheMu.Lock()
		delete(hashWait, key)
		close(waitCh)
		hashCacheMu.Unlock()
	}()

	// Verify torrent is active and grab the file
	s.mu.Lock()
	e, ok := s.active[hash]
	if !ok {
		s.mu.Unlock()
		return HashResult{}, ErrTorrentNotActive
	}
	files := e.t.Files()
	s.mu.Unlock()

	if fileIdx < 0 || fileIdx >= len(files) {
		return HashResult{}, fmt.Errorf(errFileIndexOutOfRange, fileIdx)
	}
	f := files[fileIdx]
	size := f.Length()
	if size < 64*1024 {
		return HashResult{}, errors.New("file too small for OS hash")
	}

	// Open a reader independent of the player; it will prioritize the head/tail pieces we touch.
	r := f.NewReader()
	r.SetReadahead(64 * 1024)
	r.SetResponsive()
	defer r.Close()

	// Honour ctx via a side goroutine that closes the reader on cancel
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// #nosec G104 -- Close best-effort no cleanup; erro no teardown irrelevante
			r.Close()
		case <-done:
		}
	}()
	defer close(done)

	hashStr, err := computeOSHash(r, size)
	if err != nil {
		return HashResult{}, err
	}

	result := HashResult{Hash: hashStr, Size: size}
	hashCacheMu.Lock()
	if len(hashCache) >= 2000 {
		hashCache = make(map[hashKey]HashResult)
	}
	hashCache[key] = result
	hashCacheMu.Unlock()
	return result, nil
}
