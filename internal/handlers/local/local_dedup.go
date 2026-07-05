package local

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/downloads"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// Duplicate detection for the Local Files page: find files whose CONTENT is
// identical even when the names differ, scoped to the current folder
// (recursive). Two cheap, rclone-friendly stages avoid ever downloading a whole
// file (a full read on a Drive/rclone mount = a network fetch of the entire
// object):
//
//  1. Group by byte size — a content match is impossible across sizes, so this
//     free metadata pre-filter discards the vast majority of files.
//  2. Within a size group, fingerprint each file by hashing only its size + the
//     first and last dupSampleBytes (two small RANGED reads the FUSE layer turns
//     into partial fetches), never the middle.
//
// This is a fingerprint, not a full hash: distinct files that happen to share
// size + head + tail but differ only in the middle would be reported as
// duplicates. For real media (MKV/MP4 headers, trailing index/moov, container
// CRCs) that's astronomically unlikely, and the user always reviews + selects
// before anything is deleted — nothing is removed automatically.
const dupSampleBytes = 64 << 10 // 64 KiB sampled from each end

type dupFile struct {
	Path    string    `json:"path"` // mount-root-relative (the delete endpoint re-validates containment)
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

type dupGroup struct {
	Hash  string    `json:"hash"`
	Size  int64     `json:"size"`
	Files []dupFile `json:"files"`
}

// LocalDuplicates handles GET /api/local/duplicates?mount=&path= — read-only
// scan that returns groups of ≥2 byte-identical files under the folder.
func LocalDuplicates(b *lb.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount := c.Query("mount")
		if mount == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing mount parameter"})
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		scoped := ScopePath(b, c, mount, c.Query("path"))
		groups, err := findDuplicates(c.Request.Context(), b, mount, scoped)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "path not found"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"groups": groups, "total": len(groups)})
	}
}

func findDuplicates(ctx context.Context, b *lb.Browser, mount, scopedBase string) ([]dupGroup, error) {
	entries, err := b.Walk(mount, scopedBase, false)
	if err != nil {
		return nil, err
	}
	groups := []dupGroup{}
	// Pre-filter by size: a content match is impossible across different sizes,
	// so only sizes shared by ≥2 files are worth hashing.
	for size, list := range groupBySize(entries) {
		if len(list) < 2 {
			continue
		}
		for hash, dups := range fingerprintGroup(ctx, b, mount, list) {
			if len(dups) >= 2 {
				groups = append(groups, makeDupGroup(hash, size, dups))
			}
		}
	}
	sortDupGroups(groups)
	return groups, nil
}

// groupBySize buckets files by byte size, skipping empty files (which would all
// "match" trivially).
func groupBySize(entries []lb.Entry) map[int64][]lb.Entry {
	bySize := map[int64][]lb.Entry{}
	for _, e := range entries {
		if e.Size > 0 {
			bySize[e.Size] = append(bySize[e.Size], e)
		}
	}
	return bySize
}

// fingerprintGroup hashes each file in a same-size bucket and groups by
// fingerprint. Unreadable files are skipped; a cancelled context stops early
// (the partial result is still useful for the read-only scan).
func fingerprintGroup(ctx context.Context, b *lb.Browser, mount string, list []lb.Entry) map[string][]lb.Entry {
	byHash := map[string][]lb.Entry{}
	for _, e := range list {
		if ctx.Err() != nil {
			break
		}
		abs, rerr := b.ResolvePath(mount, e.Path)
		if rerr != nil {
			continue
		}
		h, herr := fingerprintFile(abs, e.Size)
		if herr != nil {
			continue
		}
		byHash[h] = append(byHash[h], e)
	}
	return byHash
}

func makeDupGroup(hash string, size int64, dups []lb.Entry) dupGroup {
	g := dupGroup{Hash: hash, Size: size, Files: []dupFile{}}
	for _, e := range dups {
		g.Files = append(g.Files, dupFile{Path: e.Path, Name: e.Name, Size: e.Size, ModTime: e.ModTime})
	}
	sort.Slice(g.Files, func(i, j int) bool { return g.Files[i].Path < g.Files[j].Path })
	return g
}

// sortDupGroups gives a stable order (map iteration is random) so the UI and
// tests see deterministic output.
func sortDupGroups(groups []dupGroup) {
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Files) == 0 || len(groups[j].Files) == 0 {
			return groups[i].Hash < groups[j].Hash
		}
		return groups[i].Files[0].Path < groups[j].Files[0].Path
	})
}

// fingerprintFile hashes size + head + tail WITHOUT reading the middle, so on
// an rclone/Drive mount it costs two small ranged reads instead of downloading
// the whole object. Files at or under 2*dupSampleBytes are hashed whole (they're
// already tiny). The size is folded in so length alone always distinguishes.
func fingerprintFile(abs string, size int64) (string, error) {
	// #nosec G304 -- path validado por Browser.ResolvePath (guarda traversal/symlink) ou derivado de hash/config interna
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	fmt.Fprintf(h, "%d:", size)
	if size <= 2*dupSampleBytes {
		if _, err := io.Copy(h, f); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	head := make([]byte, dupSampleBytes)
	if _, err := io.ReadFull(f, head); err != nil {
		return "", err
	}
	h.Write(head)
	tail := make([]byte, dupSampleBytes)
	if _, err := f.ReadAt(tail, size-dupSampleBytes); err != nil && err != io.EOF {
		return "", err
	}
	h.Write(tail)
	return hex.EncodeToString(h.Sum(nil)), nil
}

type dedupDeleteReq struct {
	Mount string   `json:"mount"`
	Paths []string `json:"paths"` // mount-root-relative paths, as returned by LocalDuplicates
}

// LocalDuplicatesDelete handles POST /api/local/duplicates/delete — removes the
// selected duplicate files. Same write gate as delete (writable mount or admin)
// and every path is re-checked to live inside the caller's scoped root, so a
// non-admin can't delete another user's file by passing a crafted path.
func LocalDuplicatesDelete(b *lb.Browser, dls *downloads.Store, s *streamer.Streamer) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req dedupDeleteReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if req.Mount == "" || len(req.Paths) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "mount and paths are required"})
			return
		}
		if !CheckMountAccess(b, c, req.Mount) {
			return
		}
		if !canModifyMount(c, req.Mount) {
			return
		}
		baseAbs, err := b.ResolvePath(req.Mount, ScopePath(b, c, req.Mount, ""))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		deleted, errs := deleteDuplicates(b, dls, s, req.Mount, baseAbs, req.Paths)
		c.JSON(http.StatusOK, gin.H{"deleted": deleted, "errors": errs})
	}
}

func deleteDuplicates(b *lb.Browser, dls *downloads.Store, s *streamer.Streamer, mount, baseAbs string, paths []string) (int, []string) {
	deleted := 0
	errs := []string{}
	for _, p := range paths {
		abs, rerr := b.ResolvePath(mount, p)
		if rerr != nil || !withinBase(baseAbs, abs) || isMountRoot(b, abs) {
			errs = append(errs, p+": acesso negado")
			continue
		}
		st, serr := os.Stat(abs)
		if serr != nil || st.IsDir() {
			errs = append(errs, p+": não é um arquivo")
			continue
		}
		var linked []downloads.Download
		if dls != nil {
			linked, _ = dls.FindByPathPrefix(abs)
		}
		if rmErr := os.Remove(abs); rmErr != nil {
			errs = append(errs, p+": "+rmErr.Error())
			continue
		}
		purgeLinkedTorrents(dls, s, linked)
		deleted++
	}
	return deleted, errs
}

// withinBase reports whether abs sits strictly inside baseAbs (not the base
// itself, not an escape via ..). For non-UserSubpath mounts baseAbs is the
// mount root, so anything under the mount passes; for UserSubpath it's the
// caller's personal subdir, enforcing the per-user boundary.
func withinBase(baseAbs, abs string) bool {
	rel, err := filepath.Rel(baseAbs, abs)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
