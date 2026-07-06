package downloads

import (
	"log"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/contentid"
)

// tryLinkExisting checks whether file f of this torrent is byte-identical to a
// file the SAME user has already completed, and if so links the row to it
// (status=completed, linked=1) instead of fetching it from the swarm. The match
// is EXACT — the candidate is re-hashed against this torrent's own v1 piece
// hashes (contentid.VerifyInteriorPieces), so a size collision with different
// content never links. v2-only torrents (no v1 hashes) and any read/DB error
// fall through to a normal download. Returns true when it linked.
func (w *Worker) tryLinkExisting(d *Download, hash metainfo.Hash, fileIdx int, f *torrent.File) bool {
	if w.store == nil || w.streamer == nil {
		return false
	}
	pc, isV1, err := w.streamer.FilePieceCheck(hash, fileIdx)
	if err != nil || !isV1 {
		return false // v2-only torrent or error → fingerprint path (1c), not here
	}
	return w.linkMatch(d, f.Length(), pc)
}

// linkMatch links d to a completed file of the SAME user + exact size that is
// byte-identical to pc (the testable core of tryLinkExisting). The size is a
// free pre-filter; the match is verified against the torrent's own v1 piece
// hashes (every piece fully inside the file). Skips placeholders and this
// torrent's own files. Returns true when it linked.
//
// Certainty caveat: in a MULTI-FILE torrent the file's first/last pieces are
// shared with its neighbours, so the <2-piece-length region at each boundary is
// not hash-checked here (the neighbour bytes aren't this file). Exact byte-size
// + every interior piece matching means the file IS the content for real media;
// a hand-crafted file that matched the whole interior yet differed only at the
// borders is the only false-positive, and it's both implausible and harmless —
// the link is a logical pointer (no bytes touched, fully reversible), so a wrong
// guess just plays the wrong file, recoverable by removing the download.
func (w *Worker) linkMatch(d *Download, size int64, pc contentid.PieceCheck) bool {
	candidates, err := w.store.CompletedBySize(d.UserID, size)
	if err != nil || len(candidates) == 0 {
		return false
	}
	for _, cand := range candidates {
		if cand.FilePath == "" || cand.InfoHash == d.InfoHash {
			continue
		}
		if !certainFileMatch(cand.FilePath, pc) {
			continue
		}
		if _, err := w.store.CreateLinked(*d, cand.FilePath, size); err != nil {
			log.Printf("downloads: dedup link failed for #%d: %v", d.ID, err)
			return false
		}
		log.Printf("downloads: #%d adopted existing file %q (cross-torrent dedup, no re-download)", d.ID, cand.FilePath)
		return true
	}
	return false
}

// certainFileMatch reports whether the file at path is byte-identical to the
// torrent file described by pc (every interior piece re-hashed). Shared with the
// dedup-check handler via contentid.FileMatchesPieces.
func certainFileMatch(path string, pc contentid.PieceCheck) bool {
	return contentid.FileMatchesPieces(path, pc)
}
