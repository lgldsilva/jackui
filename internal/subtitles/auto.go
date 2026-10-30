// AutoFetcher: automatically fetch an OpenSubtitles subtitle for a played
// item through a hash→imdb cascade and persist the decision per
// (hashKey, fileIdx) in the subtitle_links table (LinksStore). The cascade:
//
//  1. tier 1 — OpenSubtitles moviehash of the ACTIVE torrent (the seeking
//     reader pulls start/end pieces on demand) or of a complete local file;
//     frame-exact, strongest signal.
//  2. tier 2 — parser-extracted season/episode plus a TMDB-matched title:
//     IMDB-scoped when TMDB resolves, plain query otherwise. Any tier-1
//     failure (timeout, <64KiB file, unreadable file) degrades here; it is
//     never a terminal state by itself.
//
// Decisions are deduplicated (RF7): a ready row means zero OpenSubtitles
// traffic on the next Start; failed rows are never retried automatically.
package subtitles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/parser"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/tmdb"
)

// statusNone is the AutoStatus value when there is nothing to report: no row
// and auto-fetch disabled/absent.
const statusNone = "none"

const (
	// defaultHashBudget bounds the tier-1 hash compute on an active torrent;
	// past it the cascade degrades to tier 2. The streamer's seeking reader
	// usually finishes well within this window.
	defaultHashBudget = 15 * time.Second
	// defaultFetchBudget bounds a whole fetch cycle (hash + search + download)
	// so a cycle never outlives the session that started it, even without a
	// Cancel.
	defaultFetchBudget = 60 * time.Second
)

// AutoStatus is the player-facing state of one auto-fetch decision. Outcome
// fields are only populated while Status is "ready" (see AutoFetcher.Status).
type AutoStatus struct {
	Status          string // "none" | "pending" | "ready" | "failed"
	Reason          string // "" | "no_results" | "quota" | "no_api_key" | "error"
	FileID          string
	Language        string
	VTTPath         string
	HearingImpaired bool
	Forced          bool
}

// TorrentMeta is the slice of the streamer the AutoFetcher needs. Declared
// here (instead of importing the concrete *streamer.Streamer) so tests can
// fake it and the caller wires the adapter. The real streamer satisfies the
// OSHash signature through streamer.HashResult.
type TorrentMeta interface {
	// Name is the release/display file name of the file at fileIdx (used by
	// the tier-2 title search).
	Name(hash metainfo.Hash, fileIdx int) string
	// Complete reports whether fileIdx finished downloading.
	Complete(hash metainfo.Hash, fileIdx int) bool
	// OSHash computes the OpenSubtitles hash of an ACTIVE torrent's file via
	// the seeking reader (works mid-download).
	OSHash(ctx context.Context, hash metainfo.Hash, fileIdx int) (streamer.HashResult, error)
}

// MatchResolver resolves a raw release title to TMDB metadata. Satisfied by
// *tmdb.Client; faked in tests.
type MatchResolver interface {
	Match(ctx context.Context, rawTitle string) (*tmdb.Match, error)
}

// fetchKey identifies one item being fetched: hex infohash (torrents) or the
// web's synthetic local hash string, plus the file index.
type fetchKey struct {
	hashKey string
	fileIdx int
}

// AutoFetcher owns the automatic subtitle lifecycle for played items. All
// Start/Refine methods are async and idempotent per (hash, fileIdx); Cancel
// aborts in-flight work; Status exposes the persisted or in-flight state.
type AutoFetcher struct {
	client   *Client
	links    *LinksStore
	tmdb     MatchResolver
	settings func() (langs string, autoFetch bool)

	mu       sync.Mutex
	inflight map[fetchKey]context.CancelFunc

	refineMu sync.Mutex
	refined  map[string]bool // process-local guard so failed refinements never loop

	hashBudget  time.Duration
	fetchBudget time.Duration
}

// NewAutoFetcher wires the fetcher onto the OpenSubtitles client, the
// persistent LinksStore, the TMDB resolver and the settings snapshot closure
// (comma-separated language list, auto-fetch on/off).
func NewAutoFetcher(client *Client, links *LinksStore, tmdb MatchResolver,
	settings func() (langs string, autoFetch bool)) *AutoFetcher {
	return &AutoFetcher{
		client:      client,
		links:       links,
		tmdb:        tmdb,
		settings:    settings,
		inflight:    make(map[fetchKey]context.CancelFunc),
		refined:     make(map[string]bool),
		hashBudget:  defaultHashBudget,
		fetchBudget: defaultFetchBudget,
	}
}

// StartTorrent kicks off the auto-fetch for one file of a torrent. It never
// blocks the caller: it spawns a goroutine with its own bounded context
// (default 60s) and returns immediately. Repeated calls while a fetch is
// in-flight, or after a decision is persisted, issue zero OpenSubtitles
// requests.
func (a *AutoFetcher) StartTorrent(s TorrentMeta, hash metainfo.Hash, fileIdx int) {
	if s == nil {
		return
	}
	key := hash.HexString()
	a.start(fetchKey{key, fileIdx}, func(ctx context.Context, langs string) {
		a.run(ctx, key, fileIdx, langs, func(ctx context.Context) (SearchOpts, string) {
			if opts, ok := a.tier1Torrent(ctx, s, hash, fileIdx, langs); ok {
				return opts, SourceHash
			}
			return a.tier2(ctx, s.Name(hash, fileIdx), langs)
		})
	})
}

// StartLocal kicks off the auto-fetch for a local file that is COMPLETE on
// disk: tier 1 hashes the file directly, and only an unreadable (or too
// small) file falls back to the tier-2 title search.
func (a *AutoFetcher) StartLocal(hashKey string, fileIdx int, name, path string) {
	if hashKey == "" {
		return
	}
	a.start(fetchKey{hashKey, fileIdx}, func(ctx context.Context, langs string) {
		a.run(ctx, hashKey, fileIdx, langs, func(ctx context.Context) (SearchOpts, string) {
			if opts, ok := a.tier1Local(path, langs); ok {
				return opts, SourceHash
			}
			return a.tier2(ctx, name, langs)
		})
	})
}

// Status reports the current decision for (hashKey, fileIdx): the in-flight
// state while a fetch is running, otherwise the persisted row, otherwise
// "none". Outcome fields are only populated for ready rows — a pending/failed
// row may carry stale outcome values from a prior cycle and must not leak to
// the UI.
//
// A READY row is reported even when auto-fetch is currently OFF: previously
// fetched subtitles remain valid (RF7 persistence makes no calls), so the
// player keeps showing them. Only NEW fetches are gated by auto-fetch; a
// pending/failed row — lifecycle state of an active fetcher — reports "none"
// while the feature is off, as does a missing row.
func (a *AutoFetcher) Status(ctx context.Context, hashKey string, fileIdx int) AutoStatus {
	if a == nil || a.links == nil {
		return AutoStatus{Status: statusNone}
	}
	if a.busy(fetchKey{hashKey, fileIdx}) {
		return AutoStatus{Status: StatusPending}
	}
	row, err := a.links.Get(ctx, hashKey, fileIdx)
	if err != nil || row == nil {
		return AutoStatus{Status: statusNone}
	}
	if _, autoFetch := a.settingSnapshot(); !autoFetch && row.Status != StatusReady {
		return AutoStatus{Status: statusNone}
	}
	st := AutoStatus{Status: row.Status, Reason: row.Reason}
	if row.Status == StatusReady {
		st.FileID = row.FileID
		st.Language = row.Language
		st.VTTPath = row.VTTPath
		st.HearingImpaired = row.HearingImpaired
		st.Forced = row.Forced
	}
	return st
}

// Cancel aborts any in-flight fetch for the hash (viewer closed). The
// abandoned cycle exits WITHOUT persisting a failure — a pending row, if
// already written, is simply recovered by the next Start. The hash phase
// aborts immediately via context cancellation; OpenSubtitles HTTP calls that
// already started finish under the client's own request timeouts.
func (a *AutoFetcher) Cancel(hashKey string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, cancel := range a.inflight {
		if k.hashKey == hashKey {
			cancel()
		}
	}
}

// RefineOnComplete upgrades a query/imdb decision to the frame-exact hash
// signal once the file finished downloading: it recomputes the real hash and
// re-searches ONCE. On any failure the previous decision is kept untouched
// and the item is still marked refined so completion events never loop.
//
// Refinement only updates the persisted row — it NEVER swaps a subtitle that
// a player session already applied; the refined result takes effect on the
// NEXT session for this item.
func (a *AutoFetcher) RefineOnComplete(s TorrentMeta, hash metainfo.Hash, fileIdx int) {
	if s == nil || !s.Complete(hash, fileIdx) {
		return
	}
	if _, autoFetch := a.settingSnapshot(); !autoFetch {
		return
	}
	key := hash.HexString()
	a.start(fetchKey{key, fileIdx}, func(ctx context.Context, langs string) {
		a.refine(ctx, s, hash, fileIdx, key, langs)
	})
}

// start is the shared async entry: it enforces the auto-fetch gate, dedups
// concurrent work per (hashKey, fileIdx) and runs work on a bounded context.
func (a *AutoFetcher) start(k fetchKey, work func(ctx context.Context, langs string)) {
	if a == nil || work == nil {
		return
	}
	langs, autoFetch := a.settingSnapshot()
	if !autoFetch {
		return // RF: no rows, no API calls when auto-fetch is off
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.fetchBudget)
	if !a.begin(k, cancel) {
		cancel()
		return // a fetch for this item is already running
	}
	go func() {
		defer a.end(k)
		defer cancel()
		work(ctx, langs)
	}()
}

// run performs one full fetch cycle for an item: consult the persisted
// decision, resolve the search (cascade), search ONCE, download at most once
// and persist the outcome. Failure classification uses the client's typed
// sentinels — never string matching.
func (a *AutoFetcher) run(ctx context.Context, hashKey string, fileIdx int, langs string,
	resolve func(context.Context) (SearchOpts, string)) {
	if a.links == nil || a.client == nil || resolve == nil {
		return
	}
	row, err := a.links.Get(ctx, hashKey, fileIdx)
	if err != nil {
		return // persistence broken: never burn quota on undecidable state
	}
	// reSearch marks the documented settings-change re-search: a READY row whose
	// stored language list no longer matches the current settings gets a new
	// search. That search may replace the row ONLY on success — on any failure
	// (quota, no_results, error, disabled client, cancellation) the working
	// ready decision is left COMPLETELY UNTOUCHED. MarkFailed here would
	// permanently destroy the only working subtitle link (failed rows are never
	// retried), and no pending write is issued either: between the write and a
	// possibly-failing search the player would lose its subtitle for every
	// other reader of the store. Because the row keeps its OLD languages, a
	// revert of the settings dedups straight back to it (RF7), and a kept new
	// setting re-attempts once per explicit Start (a play event) — never from
	// status polls, which a ready row answers without kicking. Mirrors
	// refine(): a live decision is only ever replaced by a success.
	reSearch := false
	if row != nil {
		switch {
		case row.Status == StatusFailed:
			return // failed decisions are never retried automatically
		case row.Status == StatusReady && row.Languages == langs:
			return // RF7 dedup: decision persisted, zero OpenSubtitles traffic
		case row.Status == StatusReady:
			reSearch = true
		}
		// Falls through for:
		//   - ready with a DIFFERENT stored language list (reSearch): the
		//     settings-change path; only MarkReady below replaces the row.
		//   - pending: a crashed cycle from a previous process, recovered by
		//     this Start (the in-flight map already dedups this process).
	}
	if !a.client.Enabled() {
		if !reSearch {
			_ = a.links.MarkFailed(ctx, hashKey, fileIdx, ReasonNoAPIKey) // zero HTTP
		}
		return
	}
	if !reSearch {
		if err := a.links.UpsertPending(ctx, hashKey, fileIdx, langs); err != nil {
			return
		}
		if ctx.Err() != nil {
			return // cancelled between writes: leave the row pending for the next Start
		}
	}

	opts, source := resolve(ctx)
	if ctx.Err() != nil {
		return
	}
	results, err := a.client.SearchAuto(opts)
	if err != nil {
		if !reSearch {
			a.markFailure(ctx, hashKey, fileIdx, err)
		}
		return // a failed re-search keeps the prior ready row
	}
	pick := pickSubtitle(results, langs)
	if pick == nil {
		if !reSearch {
			_ = a.links.MarkFailed(ctx, hashKey, fileIdx, ReasonNoResults)
		}
		return // a failed re-search keeps the prior ready row
	}
	if ctx.Err() != nil {
		return
	}
	if _, err := a.client.Download(pick.ID); err != nil {
		if !reSearch {
			a.markFailure(ctx, hashKey, fileIdx, err)
		}
		return // a failed re-search keeps the prior ready row
	}
	_ = a.links.MarkReady(ctx, hashKey, fileIdx, pick.ID, pick.Language, langs,
		pick.HearingImp, pick.Forced, a.client.CachePath(pick.ID), source)
}

// refine upgrades one existing non-hash decision to a hash decision. Unlike
// run it never writes a pending row (the ready decision stays visible for the
// current session) and never marks failed — a refinement failure just keeps
// the old row.
func (a *AutoFetcher) refine(ctx context.Context, s TorrentMeta, hash metainfo.Hash,
	fileIdx int, hashKey, langs string) {
	if a.links == nil || a.client == nil || !a.client.Enabled() {
		return
	}
	row, err := a.links.Get(ctx, hashKey, fileIdx)
	if err != nil || row == nil {
		return
	}
	if row.Source == SourceHash || row.HashRefined {
		return // already frame-exact, or refinement already attempted
	}
	id := hashKey + "#" + strconv.Itoa(fileIdx)
	a.refineMu.Lock()
	if a.refined[id] {
		a.refineMu.Unlock()
		return
	}
	a.refined[id] = true
	a.refineMu.Unlock()
	// The in-memory guard above covers loops within this process; MarkRefined
	// persists hash_refined on the EXISTING row for every refinement failure
	// (hash unavailable, search error, no results, download error), so the
	// single attempt is consumed exactly once across restarts. The old
	// decision keeps every other column — the current session keeps playing
	// the previous subtitle; the outcome lands on the next one.

	// Tier 1 only: the real hash, now that the file is complete. If the hash
	// is unavailable the previous decision stands.
	opts, ok := a.tier1Torrent(ctx, s, hash, fileIdx, langs)
	if !ok || ctx.Err() != nil {
		a.markRefineFailure(ctx, hashKey, fileIdx)
		return
	}
	results, err := a.client.SearchAuto(opts)
	if err != nil {
		a.markRefineFailure(ctx, hashKey, fileIdx) // keep the old row; hash_refined blocks re-attempts
		return
	}
	pick := pickSubtitle(results, langs)
	if pick == nil || ctx.Err() != nil {
		a.markRefineFailure(ctx, hashKey, fileIdx)
		return
	}
	if _, err := a.client.Download(pick.ID); err != nil {
		a.markRefineFailure(ctx, hashKey, fileIdx)
		return
	}
	_ = a.links.MarkReady(ctx, hashKey, fileIdx, pick.ID, pick.Language, langs,
		pick.HearingImp, pick.Forced, a.client.CachePath(pick.ID), SourceHash)
}

// tier1Torrent computes the OpenSubtitles hash of the ACTIVE torrent file
// within the hash budget and builds the frame-exact hash search. A timeout or
// compute error (including files <64KiB, which cannot hash) is not fatal: the
// caller falls through to tier 2.
func (a *AutoFetcher) tier1Torrent(ctx context.Context, s TorrentMeta, hash metainfo.Hash,
	fileIdx int, langs string) (SearchOpts, bool) {
	if s == nil {
		return SearchOpts{}, false
	}
	hctx, cancel := context.WithTimeout(ctx, a.hashBudget)
	defer cancel()
	res, err := s.OSHash(hctx, hash, fileIdx)
	if err != nil || res.Hash == "" || res.Size <= 0 {
		return SearchOpts{}, false
	}
	return SearchOpts{MovieHash: res.Hash, MovieBytesize: res.Size, Languages: langs}, true
}

// tier1Local hashes a local file that is already complete on disk.
func (a *AutoFetcher) tier1Local(path, langs string) (SearchOpts, bool) {
	if path == "" {
		return SearchOpts{}, false
	}
	f, err := os.Open(path) // path is pre-resolved by the local stream/library caller
	if err != nil {
		return SearchOpts{}, false
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return SearchOpts{}, false
	}
	res, err := streamer.ComputeFileOSHash(f, fi.Size())
	if err != nil || res.Hash == "" {
		return SearchOpts{}, false
	}
	return SearchOpts{MovieHash: res.Hash, MovieBytesize: res.Size, Languages: langs}, true
}

// tier2 builds the title search: parser-extracted season/episode plus a
// cleaned query, upgraded to an IMDB-scoped search when TMDB resolves the
// title. TMDB failure is not fatal — the cleaned file name is searched
// query-only (source "query").
func (a *AutoFetcher) tier2(ctx context.Context, name, langs string) (SearchOpts, string) {
	q := parser.Parse(name)
	opts := SearchOpts{
		Query:     cleanVideoTitle(name),
		Season:    q.Season,
		Episode:   q.Episode,
		Languages: langs,
	}
	if a.tmdb == nil {
		return opts, SourceQuery
	}
	m, err := a.tmdb.Match(ctx, name)
	if err != nil || m == nil {
		return opts, SourceQuery
	}
	if m.Title != "" {
		opts.Query = m.Title // canonical title beats the raw file name
	}
	if m.ImdbID != "" {
		opts.IMDB = m.ImdbID
		return opts, SourceIMDB
	}
	return opts, SourceQuery
}

// markFailure classifies a search/download error through the client's typed
// sentinels and persists the failed decision. Failed rows are never retried.
func (a *AutoFetcher) markFailure(ctx context.Context, hashKey string, fileIdx int, err error) {
	reason := ReasonError
	switch {
	case errors.Is(err, ErrQuota):
		reason = ReasonQuota
	case errors.Is(err, ErrDisabled):
		reason = ReasonNoAPIKey
	}
	_ = a.links.MarkFailed(ctx, hashKey, fileIdx, reason)
}

// markRefineFailure records a failed refinement attempt on the item's EXISTING
// row (hash_refined) without altering the decision itself: the ready row stays
// untouched for the running session, and the persisted flag stops a restart
// from looping the same failing search. Best-effort, like the other
// persistence calls.
func (a *AutoFetcher) markRefineFailure(ctx context.Context, hashKey string, fileIdx int) {
	_ = a.links.MarkRefined(ctx, hashKey, fileIdx)
}

// begin registers the item as in-flight, storing its cancel func. Returns
// false when work is already in flight — the caller must not spawn a
// duplicate goroutine.
func (a *AutoFetcher) begin(k fetchKey, cancel context.CancelFunc) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, busy := a.inflight[k]; busy {
		return false
	}
	a.inflight[k] = cancel
	return true
}

// end clears the in-flight registration once the goroutine settles.
func (a *AutoFetcher) end(k fetchKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.inflight, k)
}

// busy reports whether a fetch for the item is currently in flight.
func (a *AutoFetcher) busy(k fetchKey) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.inflight[k]
	return ok
}

// settingSnapshot returns the current settings through the injected closure,
// defensively treating a missing closure as "disabled with no languages".
func (a *AutoFetcher) settingSnapshot() (string, bool) {
	if a == nil || a.settings == nil {
		return "", false
	}
	langs, autoFetch := a.settings()
	return strings.TrimSpace(langs), autoFetch
}

// pickSubtitle selects the best auto-appliable result: ASS/SSA-only entries
// are skipped (srtToVTT cannot produce a valid track from them), then results
// are ranked by the settings' language priority, preferring non-hearing-
// impaired and non-forced. Equal candidates keep the client's download-count
// order (the search sorts by download_count; SliceStable preserves it).
// Returns nil when nothing appliable remains.
func pickSubtitle(results []Subtitle, langs string) *Subtitle {
	appliable := make([]Subtitle, 0, len(results))
	for _, s := range results {
		if subtitleAutoAppliable(s) {
			appliable = append(appliable, s)
		}
	}
	if len(appliable) == 0 {
		return nil
	}
	priority := languagePriority(langs)
	sort.SliceStable(appliable, func(i, j int) bool {
		a, b := appliable[i], appliable[j]
		if ra, rb := languageRank(priority, a.Language), languageRank(priority, b.Language); ra != rb {
			return ra < rb
		}
		if a.HearingImp != b.HearingImp {
			return !a.HearingImp
		}
		if a.Forced != b.Forced {
			return !a.Forced
		}
		return false
	})
	return &appliable[0]
}

// subtitleAutoAppliable reports whether a result can be auto-applied as a VTT
// track: only SRT/VTT convert cleanly. The PRIMARY format (the file the
// download actually delivers — Subtitle.ID is always Files[0].FileID) decides;
// a files[0]=.ass + files[1]=.srt result must not pass just because .srt
// appears somewhere in the Formats union. An unknown primary format (the API
// carried no extension for it) falls back to the Formats-union rule, matching
// the manual flow.
func subtitleAutoAppliable(s Subtitle) bool {
	switch s.PrimaryFormat {
	case "", ".srt", ".vtt": // "" = unknown primary: judged by the union below
	default:
		return false
	}
	if len(s.Formats) == 0 {
		return true
	}
	for _, f := range s.Formats {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case ".srt", ".vtt":
			return true
		}
	}
	return false
}

// languagePriority splits the settings language list ("pt-BR,pt") into the
// ranked preference order used by pickSubtitle.
func languagePriority(langs string) []string {
	parts := strings.Split(langs, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// languageRank positions a subtitle language inside the priority order;
// unknown languages rank after every configured one.
func languageRank(priority []string, language string) int {
	lang := strings.ToLower(strings.TrimSpace(language))
	for i, p := range priority {
		if p == lang {
			return i
		}
	}
	return len(priority)
}

// cleanVideoTitle reduces a media file name to a usable OpenSubtitles query:
// base name with the extension stripped (separators kept; OS tokenizes them).
// TMDB.Match performs its own deeper cleaning for the resolved-title path.
func cleanVideoTitle(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	return strings.TrimSpace(strings.TrimSuffix(base, filepath.Ext(base)))
}
