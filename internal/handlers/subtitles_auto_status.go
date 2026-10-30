package handlers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/subtitles"
)

// SubtitleAutoFetcher is the slice of subtitles.AutoFetcher the status
// endpoints and playback hooks consume. Declared here (consumer-side
// interface, Go convention) so tests inject a scriptable fake instead of a
// real AutoFetcher + LinksStore + OpenSubtitles client. *subtitles.AutoFetcher
// satisfies it.
type SubtitleAutoFetcher interface {
	StartTorrent(s subtitles.TorrentMeta, hash metainfo.Hash, fileIdx int)
	StartLocal(hashKey string, fileIdx int, name, path string)
	Status(ctx context.Context, hashKey string, fileIdx int) subtitles.AutoStatus
	Cancel(hashKey string)
	RefineOnComplete(s subtitles.TorrentMeta, hash metainfo.Hash, fileIdx int)
}

// StreamAutoHook carries the optional auto-subtitle hook for the stream-open
// path (StreamFile). The zero value (or the argument omitted entirely —
// variadic keeps every existing call site compiling) disables the hook: tests
// and boots where the auto pipeline is unavailable.
type StreamAutoHook struct {
	Meta    subtitles.TorrentMeta
	Fetcher SubtitleAutoFetcher
}

// subtitleAutoStatusSubtitle is the subtitle block of the frozen W1 wire shape.
// Present ONLY while status is ready — pending/failed rows may carry stale
// outcome values from a prior cycle and must never reach the UI (review N2).
type subtitleAutoStatusSubtitle struct {
	ID              string `json:"id"`
	Language        string `json:"language"`
	VTTURL          string `json:"vttUrl"`
	HearingImpaired bool   `json:"hearingImpaired"`
	Forced          bool   `json:"forced"`
}

// subtitleAutoStatusResponse is the frozen W1 wire shape both auto-status
// endpoints return (200 unless bad params):
//
//	{"status":"none|pending|ready|failed","reason":"","subtitle":{...}}
type subtitleAutoStatusResponse struct {
	Status   string                      `json:"status"`
	Reason   string                      `json:"reason"`
	Subtitle *subtitleAutoStatusSubtitle `json:"subtitle,omitempty"`
}

// respondSubtitleAutoStatus maps an AutoStatus onto the frozen wire shape.
// The subtitle block (and every outcome field) is emitted only for ready rows;
// vttUrl is RELATIVE ("/api/subtitles/download/<id>") — the web appends its
// auth token the same way subtitleDownloadURL does (W1 contract).
func respondSubtitleAutoStatus(c *gin.Context, st subtitles.AutoStatus) {
	resp := subtitleAutoStatusResponse{Status: st.Status, Reason: st.Reason}
	if st.Status == subtitles.StatusReady {
		resp.Subtitle = &subtitleAutoStatusSubtitle{
			ID:              st.FileID,
			Language:        st.Language,
			VTTURL:          "/api/subtitles/download/" + st.FileID,
			HearingImpaired: st.HearingImpaired,
			Forced:          st.Forced,
		}
	}
	c.JSON(http.StatusOK, resp)
}

// SubtitlesAutoStatus handles GET /api/stream/:hash/:file/subtitles/auto — the
// auto-fetch pipeline state for one torrent file:
//
//   - bad hash hex / non-numeric or out-of-range file → 400 (same validation
//     style as SubtitlesAuto); torrent not active → 404;
//   - no decision yet + auto-fetch on + OpenSubtitles configured → kick
//     StartTorrent (async — the fetch never blocks the response) and answer
//     pending immediately;
//   - a ready decision still on the query/imdb cascade for a COMPLETED file
//     kicks the frame-exact RefineOnComplete upgrade (async, self-guarded
//     against loops) and still answers the CURRENT status;
//   - everything else answers the persisted/in-flight state as-is; failed
//     rows are never re-kicked (RF7).
func SubtitlesAutoStatus(s *streamer.Streamer, meta subtitles.TorrentMeta, links *subtitles.LinksStore,
	client *subtitles.Client, fetcher SubtitleAutoFetcher, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		target, ok := resolveSubtitleAutoTarget(c, s)
		if !ok {
			return
		}
		if fetcher == nil {
			respondSubtitleAutoStatus(c, subtitles.AutoStatus{Status: statusAutoNone})
			return
		}
		hashKey := target.hash.HexString()
		st := fetcher.Status(c.Request.Context(), hashKey, target.fileIdx)
		if st.Status == statusAutoNone {
			maybeKickAutoFetch(c, fetcher, client, cfg, meta, target.hash, target.fileIdx, st)
			return
		}
		maybeKickRefine(c, fetcher, links, cfg, meta, target.hash, hashKey, target.fileIdx, st)
		respondSubtitleAutoStatus(c, st)
	}
}

// statusAutoNone mirrors the unexported subtitles statusNone: nothing to
// report (no row, or auto-fetch disabled).
const statusAutoNone = "none"

// subtitleAutoTarget is the validated handler input: the torrent hash and the
// in-range file index. A false ok from resolveSubtitleAutoTarget means the
// error response was already written.
type subtitleAutoTarget struct {
	hash    metainfo.Hash
	fileIdx int
}

// resolveSubtitleAutoTarget validates the path params exactly as the handler
// always did, in the same order and with the same responses: numeric file,
// hex hash, active torrent (404) and file index in range.
func resolveSubtitleAutoTarget(c *gin.Context, s *streamer.Streamer) (subtitleAutoTarget, bool) {
	fileIdx, err := strconv.Atoi(c.Param("file"))
	if err != nil {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, errInvalidFileIndex)
		return subtitleAutoTarget{}, false
	}
	var h metainfo.Hash
	if err := h.FromHexString(c.Param("hash")); err != nil {
		httpshared.RespondError(c, http.StatusBadRequest, err)
		return subtitleAutoTarget{}, false
	}
	info, err := s.Get(h)
	if err != nil {
		// ErrTorrentNotActive and unknown hashes alike — the item has no
		// active stream, so there is no pipeline state to report.
		httpshared.RespondError(c, http.StatusNotFound, err)
		return subtitleAutoTarget{}, false
	}
	if fileIdx < 0 || fileIdx >= len(info.Files) {
		httpshared.RespondErrorMessage(c, http.StatusBadRequest, ErrFileIdxOutOfRange)
		return subtitleAutoTarget{}, false
	}
	return subtitleAutoTarget{hash: h, fileIdx: fileIdx}, true
}

// maybeKickAutoFetch resolves a "none" status with the fetcher wired: with
// the auto-fetch gate on and the OpenSubtitles client enabled it kicks
// StartTorrent (async — the fetch never blocks the response) and answers
// pending immediately; otherwise the state is reported as-is.
func maybeKickAutoFetch(c *gin.Context, fetcher SubtitleAutoFetcher, client *subtitles.Client,
	cfg *config.Config, meta subtitles.TorrentMeta, h metainfo.Hash, fileIdx int, st subtitles.AutoStatus) {
	if autoFetchEnabled(cfg) && client.Enabled() {
		fetcher.StartTorrent(meta, h, fileIdx) // async; returns immediately
		respondSubtitleAutoStatus(c, subtitles.AutoStatus{Status: subtitles.StatusPending})
		return
	}
	respondSubtitleAutoStatus(c, st)
}

// maybeKickRefine opportunistically upgrades a query/imdb decision to the
// frame-exact hash signal once the file finished downloading.
// RefineOnComplete self-guards (complete check, source==hash, hash_refined,
// in-process dedup), so a kicked poll is cheap.
func maybeKickRefine(c *gin.Context, fetcher SubtitleAutoFetcher, links *subtitles.LinksStore,
	cfg *config.Config, meta subtitles.TorrentMeta, h metainfo.Hash, hashKey string, fileIdx int, st subtitles.AutoStatus) {
	if subtitleAutoShouldRefine(st, cfg) && meta != nil && meta.Complete(h, fileIdx) {
		if row, gerr := links.Get(c.Request.Context(), hashKey, fileIdx); gerr == nil &&
			row != nil && row.Source != subtitles.SourceHash {
			fetcher.RefineOnComplete(meta, h, fileIdx)
		}
	}
}

// autoFetchEnabled reads the auto-fetch gate live through the synchronized
// snapshot so a settings save applies to the next request without a restart —
// and without racing the POST writer (the gate is polled every 1–2s per active
// player while the admin may be saving settings).
func autoFetchEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.SubtitlesSnapshot().AutoFetch
}

// subtitleAutoShouldRefine reports whether a status response may kick the
// frame-exact refinement: only a READY decision (a decision exists to
// upgrade), with auto-fetch on. Pending/failed rows are excluded — failed
// rows are never retried automatically (RF7) and pending rows still have
// their first cascade running. File completeness is the caller's check.
func subtitleAutoShouldRefine(st subtitles.AutoStatus, cfg *config.Config) bool {
	return st.Status == subtitles.StatusReady && autoFetchEnabled(cfg)
}
