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
		if r.InfoHash != "" {
			if s.cachedSeen[r.InfoHash] || s.liveSeen[r.InfoHash] {
				continue
			}
			s.liveSeen[r.InfoHash] = true
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
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}

func emitCachedResults(c *gin.Context, store *history.Store, query string, userID int, includeAll bool, enricher *resultEnricher) (map[string]bool, int) {
	seen := make(map[string]bool)
	if store == nil {
		return seen, 0
	}
	count := 0
	cached, _ := store.Search(query, userID, includeAll)
	for _, r := range cached {
		writeSSE(c, "result", enricher.enrich(r.Result, true))
		count++
		if r.InfoHash != "" {
			seen[r.InfoHash] = true
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' is required"})
			return
		}

		setSSEHeaders(c)

		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"
		enricher := buildEnricher(favs, dls, userID, includeAll)

		cachedSeen, cachedCount := emitCachedResults(c, store, query, userID, includeAll, enricher)
		writeSSE(c, "progress", gin.H{"phase": "live", "cached": cachedCount})

		state := &liveSearchState{
			c:          c,
			enricher:   enricher,
			cachedSeen: cachedSeen,
			liveSeen:   make(map[string]bool),
		}
		err := client.StreamSearch(c.Request.Context(), query, category, indexers, 30*time.Second, state.handleHit)

		if store != nil && len(state.liveResults) > 0 && !middleware.IsIncognito(c) {
			go func() { _ = store.Save(query, state.liveResults, userID) }()
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
