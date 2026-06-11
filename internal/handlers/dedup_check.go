package handlers

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/contentid"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// dedupCheckTimeout bounds the metadata fetch + catalog scan for one check.
const dedupCheckTimeout = 60 * time.Second

var (
	errInvalidLinkIndex = errors.New("invalid fileIndex")
	errLinkAccessDenied = errors.New("access denied to mount")
)

// dedupMatch is a per-file "you already have this" hit surfaced to the UI.
type dedupMatch struct {
	FileIndex  int    `json:"fileIndex"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	IsVideo    bool   `json:"isVideo"`
	Source     string `json:"source"`            // download | library | cloud
	Mount      string `json:"mount,omitempty"`   // for library/cloud (link target)
	RelPath    string `json:"relPath,omitempty"` // mount-relative (link target)
	Confidence string `json:"confidence"`        // certain (piece-verified) | probable (fingerprint)
}

// catalogCand is a file already on disk that could match a torrent file.
type catalogCand struct {
	source, mount, relPath, absPath string
	size                            int64
	remote                          bool // gdrive/FUSE → fingerprint only, never a full piece read
}

type dedupCheckReq struct {
	Magnet string `json:"magnet"`
}

// DedupCheck (POST /api/downloads/dedup-check) reports which of a torrent's files
// the user ALREADY has on disk — a completed download, the local library, or a
// cloud mount — so the UI can offer to link instead of re-download. Read-only:
// it activates the torrent only to read its file list and the ends of files.
func DedupCheck(s *streamer.Streamer, dls *downloads.Store, b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req dedupCheckReq
		if err := c.ShouldBindJSON(&req); err != nil || req.Magnet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "magnet is required"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), dedupCheckTimeout)
		defer cancel()
		info, err := s.Add(ctx, req.Magnet)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		var hash metainfo.Hash
		if err := hash.FromHexString(info.InfoHash); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad info hash"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		matches := findDedupMatches(ctx, s, dls, b, hash, info, userFromCtx(c), userID)
		c.JSON(http.StatusOK, gin.H{"matches": matches, "totalFiles": len(info.Files)})
	}
}

func findDedupMatches(ctx context.Context, s *streamer.Streamer, dls *downloads.Store, b *local.Browser, hash metainfo.Hash, info *streamer.TorrentInfo, username string, userID int) []dedupMatch {
	sizes := map[int64]bool{}
	for _, f := range info.Files {
		if f.Size > 0 {
			sizes[f.Size] = true
		}
	}
	matches := []dedupMatch{}
	if len(sizes) == 0 {
		return matches
	}
	idx := map[int64][]catalogCand{}
	addDownloadCandidates(idx, dls, userID, sizes)
	addMountCandidates(ctx, idx, b, username, sizes)
	for _, f := range info.Files {
		if ctx.Err() != nil {
			break
		}
		if cands := idx[f.Size]; len(cands) > 0 {
			if m := verifyBestCandidate(s, hash, f, cands); m != nil {
				matches = append(matches, *m)
			}
		}
	}
	return matches
}

// addDownloadCandidates adds the user's completed downloads of the wanted sizes.
func addDownloadCandidates(idx map[int64][]catalogCand, dls *downloads.Store, userID int, sizes map[int64]bool) {
	if dls == nil {
		return
	}
	for size := range sizes {
		rows, err := dls.CompletedBySize(userID, size)
		if err != nil {
			continue
		}
		for _, d := range rows {
			if d.FilePath != "" {
				idx[size] = append(idx[size], catalogCand{source: "download", absPath: d.FilePath, size: size, remote: detectRemoteFS(d.FilePath)})
			}
		}
	}
}

// addMountCandidates walks each mount the user can access once, keeping only
// media files whose size is wanted. Cloud (FUSE) mounts are flagged remote so
// they're matched by cheap fingerprint, never a full piece read. Honours ctx so
// a slow/large mount can't run past the request deadline.
func addMountCandidates(ctx context.Context, idx map[int64][]catalogCand, b *local.Browser, username string, sizes map[int64]bool) {
	if b == nil {
		return
	}
	for _, m := range b.MountsFor(username) {
		if ctx.Err() != nil {
			return
		}
		addOneMountCandidates(idx, b, username, m, sizes)
	}
}

// addOneMountCandidates walks a single mount, adding its same-size media files.
func addOneMountCandidates(idx map[int64][]catalogCand, b *local.Browser, username string, m local.Mount, sizes map[int64]bool) {
	entries, err := b.Walk(m.Name, b.UserScopedPath(m.Name, "", username), true)
	if err != nil {
		return
	}
	// Walk returns mount-root-relative paths (with the {username}/ prefix on a
	// UserSubpath mount); strip it so e.Path round-trips through ResolvePathFor
	// (which re-adds it) instead of doubling to {username}/{username}/...
	entries = b.StripUserScope(m.Name, username, entries)
	remote := detectRemoteFS(m.Path)
	src := "library"
	if remote {
		src = "cloud"
	}
	for _, e := range entries {
		if e.IsDir || !sizes[e.Size] {
			continue
		}
		if abs, err := b.ResolvePathFor(m.Name, e.Path, username); err == nil {
			idx[e.Size] = append(idx[e.Size], catalogCand{source: src, mount: m.Name, relPath: e.Path, absPath: abs, size: e.Size, remote: remote})
		}
	}
}

// verifyBestCandidate confirms the strongest match for file f: a CERTAIN match
// (local candidate re-hashed against the torrent's v1 piece hashes) wins; failing
// that, a PROBABLE match (same size + head/tail fingerprint) — the only option
// for cloud files, which can't be piece-verified without a full download.
func verifyBestCandidate(s *streamer.Streamer, hash metainfo.Hash, f streamer.FileInfo, cands []catalogCand) *dedupMatch {
	pc, isV1, _ := s.FilePieceCheck(hash, f.Index)
	return pickMatch(f, cands, pc, isV1, func() string {
		fp, _ := s.FingerprintFile(hash, f.Index)
		return fp
	})
}

// pickMatch is the pure matching core: a CERTAIN match (local candidate
// re-hashed against the torrent's v1 piece hashes) wins; failing that, a
// PROBABLE match (same size + head/tail fingerprint), the only option for cloud
// files. torrentFP is evaluated lazily — only when a fingerprint comparison is
// actually needed (it costs a small swarm read).
func pickMatch(f streamer.FileInfo, cands []catalogCand, pc contentid.PieceCheck, isV1 bool, torrentFP func() string) *dedupMatch {
	var fp string
	var fpDone bool
	var probable *dedupMatch
	for _, cand := range cands {
		if !cand.remote && isV1 {
			if contentid.FileMatchesPieces(cand.absPath, pc) {
				return makeMatch(f, cand, "certain")
			}
			continue
		}
		if !fpDone {
			fp = torrentFP()
			fpDone = true
		}
		if fp == "" || probable != nil {
			continue
		}
		if candFP, err := contentid.Fingerprint(cand.absPath, cand.size); err == nil && candFP == fp {
			probable = makeMatch(f, cand, "probable")
		}
	}
	return probable
}

func makeMatch(f streamer.FileInfo, cand catalogCand, confidence string) *dedupMatch {
	name := path.Base(f.Path)
	if name == "." || name == "/" || name == "" {
		name = f.Path
	}
	return &dedupMatch{
		FileIndex: f.Index, Name: name, Size: f.Size, IsVideo: f.IsVideo,
		Source: cand.source, Mount: cand.mount, RelPath: cand.relPath, Confidence: confidence,
	}
}

type dedupLinkItem struct {
	FileIndex int    `json:"fileIndex"`
	Mount     string `json:"mount"`
	RelPath   string `json:"relPath"`
}

type dedupLinkReq struct {
	Magnet   string          `json:"magnet"`
	InfoHash string          `json:"infoHash"`
	Name     string          `json:"name"`
	Items    []dedupLinkItem `json:"items"`
}

// DedupLink (POST /api/downloads/link) creates completed+linked download rows
// for files the user confirmed they already have (the "você já tem" flow). Each
// item's path is resolved through the browser's per-user access control, so a
// user can only link to files they're allowed to see.
func DedupLink(dls *downloads.Store, b *local.Browser) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req dedupLinkReq
		if err := c.ShouldBindJSON(&req); err != nil || req.InfoHash == "" || len(req.Items) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "infoHash and items are required"})
			return
		}
		userID, _, _ := auth.UserIDFromCtx(c)
		username := userFromCtx(c)
		linked := 0
		errs := []string{}
		for _, it := range req.Items {
			abs, err := resolveLinkTarget(b, username, it)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			st, statErr := os.Stat(abs)
			if statErr != nil || st.IsDir() {
				errs = append(errs, it.RelPath+": not a file")
				continue
			}
			if _, err := dls.CreateLinked(downloads.Download{
				UserID: userID, InfoHash: req.InfoHash, FileIndex: it.FileIndex,
				Magnet: req.Magnet, Name: req.Name,
			}, abs, st.Size()); err != nil {
				errs = append(errs, err.Error())
				continue
			}
			linked++
		}
		c.JSON(http.StatusOK, gin.H{"linked": linked, "errors": errs})
	}
}

// resolveLinkTarget validates one link item against the browser's per-user access
// control and returns the resolved absolute path.
func resolveLinkTarget(b *local.Browser, username string, it dedupLinkItem) (string, error) {
	if it.FileIndex < 0 {
		return "", errInvalidLinkIndex
	}
	if b == nil || !b.UserCanAccess(username, it.Mount) {
		return "", errLinkAccessDenied
	}
	return b.ResolvePathFor(it.Mount, it.RelPath, username)
}
