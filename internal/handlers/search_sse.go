package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/auth"
	"github.com/luizg/jackui/internal/downloads"
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/jackett"
	"github.com/luizg/jackui/internal/middleware"
	"github.com/luizg/jackui/internal/streamer"
)

// dedupKey returns the canonical dedup key for a result. The Jackett client
// already canonicalizes infoHash at the source, but cached rows saved before
// that change (or any other path) may carry a raw/upper/base32 hash — so we
// re-canonicalize here. Falls back to tracker|title|size for hash-less entries
// (private trackers like amigos-share that expose no infoHash) so that the same
// result from the cache phase and the live phase doesn't appear twice in the
// stream. Returns "" only when no identifying information is present at all.
func dedupKey(r jackett.Result) string {
	if h := jackett.CanonicalInfoHash(r.InfoHash, r.MagnetURI); h != "" {
		return h
	}
	if r.InfoHash != "" {
		return r.InfoHash
	}
	if r.Tracker != "" && r.Title != "" {
		return strings.ToLower(r.Tracker) + "|" + strings.ToLower(r.Title) + "|" + fmt.Sprintf("%d", r.Size)
	}
	return ""
}

func writeSSE(c *gin.Context, event string, data any) {
	b, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, b)
	c.Writer.Flush()
}

type liveSearchState struct {
	c              *gin.Context
	enricher       *resultEnricher
	cachedSeen     map[string]bool
	liveSeen       map[string]bool
	mu             sync.Mutex
	liveResults    []jackett.Result
	liveCount      int
	indexersDone   int
	indexersFailed int
}

func (s *liveSearchState) handleHit(hit jackett.IndexerHit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.indexersDone++
	if hit.Err != nil {
		s.indexersFailed++
		writeSSE(s.c, "progress", gin.H{
			"phase":    "indexer",
			"indexer":  hit.IndexerName,
			"error":    hit.Err.Error(),
			"durMs":    hit.Duration.Milliseconds(),
			"done":     s.indexersDone,
		})
		return
	}
	emitted := 0
	for _, r := range hit.Results {
		if key := dedupKey(r); key != "" {
			if s.cachedSeen[key] || s.liveSeen[key] {
				continue
			}
			s.liveSeen[key] = true
		}
		s.liveResults = append(s.liveResults, r)
		writeSSE(s.c, "result", s.enricher.enrich(r, false))
		s.liveCount++
		emitted++
	}
	writeSSE(s.c, "progress", gin.H{
		"phase":   "indexer",
		"indexer": hit.IndexerName,
		"hits":    emitted,
		"durMs":   hit.Duration.Milliseconds(),
		"done":    s.indexersDone,
	})
}

func parseSearchParams(c *gin.Context) (query, category string, indexers []string) {
	query = c.Query("q")
	category = c.Query("category")
	if ip := c.Query("indexers"); ip != "" {
		for _, idx := range strings.Split(ip, ",") {
			if idx = strings.TrimSpace(idx); idx != "" {
				indexers = append(indexers, idx)
			}
		}
	}
	return
}

func setSSEHeaders(c *gin.Context) {
	c.Header(ContentType, "text/event-stream")
	c.Header(CacheControl, "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}

func emitCachedResults(c *gin.Context, store *history.Store, query string, userID int, includeAll bool, indexers []string, enricher *resultEnricher) (map[string]bool, int) {
	seen := make(map[string]bool)
	if store == nil {
		return seen, 0
	}
	// When the user scoped the search to specific indexers, skip the cache phase.
	// Cached rows only persist the tracker *display name*, not its indexer id, so
	// we can't reliably filter them to the selection — and emitting all cached
	// rows would leak results from OTHER providers (from a past broader search),
	// which is exactly the "I picked one indexer but got several" bug. The live
	// phase below already queries only the selected indexers, so a scoped search
	// still returns the right results (just without the instant-cache shortcut).
	if len(indexers) > 0 {
		return seen, 0
	}
	count := 0
	cached, _ := store.Search(query, userID, includeAll)
	for _, r := range cached {
		writeSSE(c, "result", enricher.enrich(r.Result, true))
		count++
		if key := dedupKey(r.Result); key != "" {
			seen[key] = true
		}
	}
	return seen, count
}

// SearchSSE handles GET /api/search/stream — streams results via Server-Sent Events.
//
// Flow:
//   1. Emit cached results from local DB (instant)
//   2. Fan out one HTTP request per configured Jackett indexer (parallel goroutines)
//   3. As each indexer responds, emit its results + progress event (live, ms-level)
//   4. When all done, emit `done`
func SearchSSE(client *jackett.Client, store *history.Store, favs *streamer.FavoritesStore, dls *downloads.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, category, indexers := parseSearchParams(c)
		if query == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": ErrQueryRequired})
			return
		}

		setSSEHeaders(c)

		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"
		enricher := buildEnricher(favs, dls, userID, includeAll)

		cachedSeen, cachedCount := emitCachedResults(c, store, query, userID, includeAll, indexers, enricher)
		writeSSE(c, "progress", gin.H{"phase": "live", "cached": cachedCount})

		state := &liveSearchState{
			c:          c,
			enricher:   enricher,
			cachedSeen: cachedSeen,
			liveSeen:   make(map[string]bool),
		}

		// Keep-alive: a slow indexer can leave a long gap with no bytes flowing,
		// and a reverse proxy (NPM) may cut the SSE stream on read-timeout before
		// the `done` event — the client then reports "Conexão perdida". A periodic
		// comment frame keeps the connection warm. Writes share state.mu with
		// handleHit so the ResponseWriter is never touched concurrently; the
		// WaitGroup guarantees the pinger has stopped before the final writes.
		stopPing := make(chan struct{})
		var pingWg sync.WaitGroup
		pingWg.Add(1)
		go func() {
			defer pingWg.Done()
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stopPing:
					return
				case <-c.Request.Context().Done():
					return
				case <-ticker.C:
					state.mu.Lock()
					_, _ = fmt.Fprint(c.Writer, ": ping\n\n")
					c.Writer.Flush()
					state.mu.Unlock()
				}
			}
		}()

		err := client.StreamSearch(c.Request.Context(), query, category, indexers, 30*time.Second, state.handleHit)
		close(stopPing)
		pingWg.Wait()

		if store != nil && len(state.liveResults) > 0 {
			incognito := middleware.IsIncognito(c)
			go func() { _ = store.Save(query, state.liveResults, userID, incognito) }()
		}

		if err != nil {
			msg := "Jackett indisponível, mostrando apenas cache"
			if cachedCount == 0 && state.liveCount == 0 {
				msg = err.Error()
			}
			writeSSE(c, "error", gin.H{"message": msg})
		}

		writeSSE(c, "done", gin.H{
			"total":          state.liveCount + cachedCount,
			"live":           state.liveCount,
			"cached":         cachedCount,
			"indexersDone":   state.indexersDone,
			"indexersFailed": state.indexersFailed,
		})
	}
}
