package local

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/handlers/httpshared"
	lb "github.com/lgldsilva/jackui/internal/local"
	"github.com/lgldsilva/jackui/internal/subtitles"
)

// subtitleAutoFetcher is the slice of subtitles.AutoFetcher the local handlers
// consume. Mirrors handlers.SubtitleAutoFetcher: this package cannot import
// handlers (they import it — import cycle), so the small interface is
// duplicated here, same as the test helpers and localSubtitleExtensions.
type subtitleAutoFetcher interface {
	StartLocal(hashKey string, fileIdx int, name, path string)
	Status(ctx context.Context, hashKey string, fileIdx int) subtitles.AutoStatus
}

// errInvalidLocalPseudoHash is the 400 message for a :hash that is not a
// decodable local pseudo-hash.
const errInvalidLocalPseudoHash = "invalid local hash"

// decodeLocalPseudoHash decodes the web's `local-<base64url(json{mount,path})>`
// pseudo info-hash (buildLocalHash on the frontend; localInfoHash in
// local_play.go) into its mount and path. Named + tested guard (repo rule:
// every path/URL guard lives in a named, tested function). Duplicated logic
// from handlers.parseLocalInfoHash — same no-cross-import justification as
// above. Returns ok=false for torrent hashes, corrupt base64 or payloads
// missing mount/path; callers map that to 400.
func decodeLocalPseudoHash(hash string) (mount, path string, ok bool) {
	const prefix = "local-"
	if !strings.HasPrefix(hash, prefix) {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(hash, prefix))
	if err != nil {
		return "", "", false
	}
	var payload struct {
		Mount string `json:"mount"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Mount == "" || payload.Path == "" {
		return "", "", false
	}
	return payload.Mount, payload.Path, true
}

// subtitleAutoStatusSubtitle / subtitleAutoStatusResponse / respondSubtitleAutoStatus
// mirror the frozen W1 wire shape in handlers/subtitles_auto_status.go (the two
// packages cannot share the type — import cycle). Keep both copies in sync: the
// W1 client is written against it.
type subtitleAutoStatusSubtitle struct {
	ID              string `json:"id"`
	Language        string `json:"language"`
	VTTURL          string `json:"vttUrl"`
	HearingImpaired bool   `json:"hearingImpaired"`
	Forced          bool   `json:"forced"`
}

type subtitleAutoStatusResponse struct {
	Status   string                      `json:"status"`
	Reason   string                      `json:"reason"`
	Subtitle *subtitleAutoStatusSubtitle `json:"subtitle,omitempty"`
}

func respondLocalSubtitleAutoStatus(c *gin.Context, st subtitles.AutoStatus) {
	resp := subtitleAutoStatusResponse{Status: st.Status, Reason: st.Reason}
	// Outcome fields only for ready rows: pending/failed rows may carry stale
	// outcome values from a prior cycle and must never leak to the UI (review N2).
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

// LocalSubtitlesAutoStatus handles GET /api/local/subtitles/auto/:hash/:file —
// the auto-fetch pipeline state for a local file, same frozen wire shape as the
// torrent status endpoint. The route keeps the pseudo-hash IN THE PATH (frozen
// by spec, unlike every other local endpoint's mount/path query params): the
// pseudo-hash is decoded SERVER-SIDE to (mount, path) by decodeLocalPseudoHash
// and resolved through the SAME guards the query-param endpoints use:
// CheckMountAccess → ScopePath → ResolvePath.
//
// :file must be 0: local files are always addressed by file index 0 (the
// LocalPlay start hook hardcodes it), so any other index would only mint a
// bogus (localInfoHash, fileIdx) dedup row and a duplicate quota-charged
// fetch for the same file — rejected with 400.
//
// REVIEW NOTE (W1 N3) — the `?user=` view-as interaction: the override is
// ACCEPTED and HONORED. The pseudo-hash encodes the mount-relative path the
// client saw; per-user scoping is applied at RESOLUTION time through ScopePath
// (the single admin cross-user chokepoint, admin-gated), so an admin viewing
// another user's space resolves this route identically to the query-param
// endpoints when the client sends ?user= like it does for them. The hash
// itself does not encode any user scope, so nothing in the route can bypass
// or collide with the override.
//
// The dedup key is the CANONICAL re-encoding of the decoded (mount, path)
// (localInfoHash) — the exact string the LocalPlay start hook uses — immune to
// client-side encoding drift between the poll and the play.
func LocalSubtitlesAutoStatus(b *lb.Browser, links *subtitles.LinksStore, client *subtitles.Client,
	fetcher subtitleAutoFetcher, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		mount, path, ok := decodeLocalPseudoHash(c.Param("hash"))
		if !ok {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, errInvalidLocalPseudoHash)
			return
		}
		fileIdx, err := strconv.Atoi(c.Param("file"))
		if err != nil {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "invalid file index")
			return
		}
		// Local files always use index 0 (the LocalPlay hook hardcodes it): a
		// poller sending anything else would create a second
		// (localInfoHash, fileIdx) row and kick a second, quota-charged fetch
		// for the SAME file. Reject instead of silently pinning.
		if fileIdx != 0 {
			httpshared.RespondErrorMessage(c, http.StatusBadRequest, "local files always use file index 0")
			return
		}
		if !CheckMountAccess(b, c, mount) {
			return
		}
		abs, err := b.ResolvePath(mount, ScopePath(b, c, mount, path))
		if err != nil {
			httpshared.RespondError(c, http.StatusBadRequest, err)
			return
		}
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			httpshared.RespondErrorMessage(c, http.StatusNotFound, httpshared.ErrFileNotFound)
			return
		}
		if fetcher == nil {
			respondLocalSubtitleAutoStatus(c, subtitles.AutoStatus{Status: subtitleStatusNone})
			return
		}
		hashKey := localInfoHash(mount, path)
		status := fetcher.Status(c.Request.Context(), hashKey, fileIdx)
		if status.Status == subtitleStatusNone && cfg != nil && cfg.SubtitlesSnapshot().AutoFetch && client.Enabled() {
			// No decision yet + auto-fetch on + OpenSubtitles configured → kick
			// StartLocal (async, hashes the complete file directly) and answer
			// pending immediately — the fetch never blocks the response. fileIdx
			// is always 0 here (enforced above); the auto-fetch gate is read
			// through the synchronized config snapshot, never the raw field —
			// the settings POST swaps it concurrently (audit §1).
			fetcher.StartLocal(hashKey, fileIdx, filepath.Base(abs), abs)
			respondLocalSubtitleAutoStatus(c, subtitles.AutoStatus{Status: subtitles.StatusPending})
			return
		}
		respondLocalSubtitleAutoStatus(c, status)
	}
}

// subtitleStatusNone mirrors the unexported subtitles statusNone.
const subtitleStatusNone = "none"
