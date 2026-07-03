package streamer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// artDirName is the cache subdir (under DataDir) holding resolved thumbnails as
// JPEG/PNG bytes for the "torrent" and "frame" sources. TMDB art is a remote
// URL and never stored here.
const artDirName = ".art"

// maxTorrentImageBytes caps how much we'll read for an embedded poster — real
// poster/cover images are well under this; the cap stops a maliciously huge or
// mislabeled image file from blocking the read indefinitely.
const maxTorrentImageBytes = 8 << 20 // 8 MiB

// minTorrentImageBytes filters out tiny images (favicons, UI sprites, sample
// thumbs) that masquerade as posters in some release folders.
const minTorrentImageBytes = 10 << 10 // 10 KiB

var artImageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

// preferredArtBasenames are the case-insensitive stems uploaders use for the
// "official" artwork in a release folder; matched first, before falling back to
// the largest image.
var preferredArtBasenames = []string{"poster", "cover", "folder", "fanart", "movie", "show", "thumb", "front", "art"}

func buildImageCandidates(files []*torrent.File) []imgCandidate {
	var cands []imgCandidate
	for i, f := range files {
		base := strings.ToLower(filepath.Base(f.Path()))
		ext := strings.ToLower(filepath.Ext(base))
		if !artImageExtensions[ext] {
			continue
		}
		if f.Length() < minTorrentImageBytes {
			continue
		}
		stem := strings.TrimSuffix(base, ext)
		rank, preferred := -1, false
		for r, name := range preferredArtBasenames {
			if strings.Contains(stem, name) {
				rank, preferred = len(preferredArtBasenames)-r, true
				break
			}
		}
		cands = append(cands, imgCandidate{idx: i, size: f.Length(), preferred: preferred, preferRank: rank})
	}
	return cands
}

func sortCandsByPreference(cands []imgCandidate) {
	sort.Slice(cands, func(a, b int) bool {
		if cands[a].preferred != cands[b].preferred {
			return cands[a].preferred
		}
		if cands[a].preferRank != cands[b].preferRank {
			return cands[a].preferRank > cands[b].preferRank
		}
		return cands[a].size > cands[b].size
	})
}

// TorrentImage finds the best embedded artwork file in an *active* torrent and
// returns its raw bytes plus the source filename. Prefers a file named like
// poster/cover/folder over the largest image. Returns an error if the torrent
// isn't active or has no suitable image — both are normal "no embedded art"
// cases the caller falls through on.
type imgCandidate struct {
	idx        int
	size       int64
	preferred  bool
	preferRank int
}

func (s *Streamer) TorrentImage(ctx context.Context, hash metainfo.Hash) ([]byte, string, error) {
	s.mu.Lock()
	e, ok := s.active[hash]
	if ok {
		e.lastAccess = time.Now()
	}
	s.mu.Unlock()
	if !ok {
		return nil, "", ErrTorrentNotActive
	}

	cands := buildImageCandidates(e.t.Files())
	if len(cands) == 0 {
		return nil, "", errors.New("no embedded image")
	}
	sortCandsByPreference(cands)

	f := e.t.Files()[cands[0].idx]
	r := f.NewReader()
	r.SetReadahead(maxTorrentImageBytes)
	r.SetResponsive()
	defer r.Close()

	// Reading torrent bytes can block on missing pieces; bound it on ctx so a
	// stalled swarm doesn't hang the request.
	type readResult struct {
		data []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(r, maxTorrentImageBytes))
		done <- readResult{data, err}
	}()
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case res := <-done:
		if res.err != nil && res.err != io.EOF {
			return nil, "", res.err
		}
		if len(res.data) < minTorrentImageBytes {
			return nil, "", errors.New("embedded image too small")
		}
		return res.data, f.Path(), nil
	}
}

// SaveArtBytes writes resolved art (torrent image or captured frame) under the
// .art cache dir and returns the DataDir-relative path to persist in CachedArt.
func (s *Streamer) SaveArtBytes(hash metainfo.Hash, data []byte) (string, error) {
	dir := filepath.Join(s.cfg.DataDir, artDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	rel := filepath.Join(artDirName, hash.HexString()+".jpg")
	if err := os.WriteFile(filepath.Join(s.cfg.DataDir, rel), data, 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// ReadArtBytes returns the cached art file for a DataDir-relative path produced
// by SaveArtBytes. The path is validated to stay within the .art dir so a
// crafted CachedArt.Path can't read arbitrary files.
func (s *Streamer) ReadArtBytes(rel string) ([]byte, error) {
	clean := filepath.Clean(rel)
	if !strings.HasPrefix(clean, artDirName+string(os.PathSeparator)) {
		return nil, fmt.Errorf("art path %q outside cache dir", rel)
	}
	return os.ReadFile(filepath.Join(s.cfg.DataDir, clean))
}
