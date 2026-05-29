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
	"github.com/luizg/jackui/internal/history"
	"github.com/luizg/jackui/internal/jackett"
	"github.com/luizg/jackui/internal/middleware"
)

func writeSSE(c *gin.Context, event string, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, b)
	c.Writer.Flush()
}

// SearchSSE handles GET /api/search/stream — streams results via Server-Sent Events.
//
// Flow:
//   1. Emit cached results from local DB (instant)
//   2. Fan out one HTTP request per configured Jackett indexer (parallel goroutines)
//   3. As each indexer responds, emit its results + progress event (live, ms-level)
//   4. When all done, emit `done`
//
// This replaces the previous "wait for Jackett /all then stream" which blocked ~100s
// before the first live result. Now first results appear in 1-5s.
func SearchSSE(client *jackett.Client, store *history.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := c.Query("q")
		if query == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' is required"})
			return
		}

		category := c.Query("category")
		var indexers []string
		if ip := c.Query("indexers"); ip != "" {
			for _, idx := range strings.Split(ip, ",") {
				if idx = strings.TrimSpace(idx); idx != "" {
					indexers = append(indexers, idx)
				}
			}
		}

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		userID, isAdmin, _ := auth.UserIDFromCtx(c)
		includeAll := isAdmin && c.Query("all") == "1"

		// Phase 1: send cached results immediately
		cachedSeen := make(map[string]bool)
		cachedCount := 0
		if store != nil {
			cached, _ := store.Search(query, userID, includeAll)
			for _, r := range cached {
				writeSSE(c, "result", enriched(r.Result, true))
				cachedCount++
				if r.InfoHash != "" {
					cachedSeen[r.InfoHash] = true
				}
			}
		}

		// Notify client that live search is starting
		writeSSE(c, "progress", gin.H{"phase": "live", "cached": cachedCount})

		// Phase 2: parallel per-indexer queries. As each indexer finishes,
		// its results stream immediately. Serialized via mutex on SSE write.
		liveResults := make([]jackett.Result, 0, 256)
		liveSeen := make(map[string]bool)
		var mu sync.Mutex
		var liveCount, indexersDone, indexersFailed int

		// Concurrent calls fan out — only the writes to ResponseWriter are serialized
		serr := client.StreamSearch(c.Request.Context(), query, category, indexers, 30*time.Second, func(hit jackett.IndexerHit) {
			mu.Lock()
			defer mu.Unlock()
			indexersDone++
			if hit.Err != nil {
				indexersFailed++
				writeSSE(c, "progress", gin.H{
					"phase":    "indexer",
					"indexer":  hit.IndexerName,
					"error":    hit.Err.Error(),
					"durMs":    hit.Duration.Milliseconds(),
					"done":     indexersDone,
				})
				return
			}
			// Emit each result that isn't already in cache or another indexer's results
			emitted := 0
			for _, r := range hit.Results {
				if r.InfoHash != "" {
					if cachedSeen[r.InfoHash] || liveSeen[r.InfoHash] {
						continue
					}
					liveSeen[r.InfoHash] = true
				}
				liveResults = append(liveResults, r)
				writeSSE(c, "result", enriched(r, false))
				liveCount++
				emitted++
			}
			writeSSE(c, "progress", gin.H{
				"phase":   "indexer",
				"indexer": hit.IndexerName,
				"hits":    emitted,
				"durMs":   hit.Duration.Milliseconds(),
				"done":    indexersDone,
			})
		})

		// Save what we got to history asynchronously (even partial)
		if store != nil && len(liveResults) > 0 && !middleware.IsIncognito(c) {
			go store.Save(query, liveResults, userID)
		}

		if serr != nil {
			msg := "Jackett indisponível, mostrando apenas cache"
			if cachedCount == 0 && liveCount == 0 {
				msg = serr.Error()
			}
			writeSSE(c, "error", gin.H{"message": msg})
		}

		writeSSE(c, "done", gin.H{
			"total":          liveCount + cachedCount,
			"live":           liveCount,
			"cached":         cachedCount,
			"indexersDone":   indexersDone,
			"indexersFailed": indexersFailed,
		})
	}
}
