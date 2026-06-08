package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/ai"
)

const errAIDisabled = "AI chain disabled"

// aiStatusResponse is the read model for the Settings AI card.
type aiStatusResponse struct {
	Enabled bool               `json:"enabled"`
	Chain   []aiSlotView       `json:"chain"`
	Results []ai.SlotScore     `json:"results"`
	Cases   []ai.BenchmarkCase `json:"cases"`
	Cost    ai.CostConfig      `json:"cost"`
}

type aiSlotView struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// GetAIBenchmark — GET /api/ai/benchmark. Returns the live chain order, the last
// benchmark results, and the editable case set.
func GetAIBenchmark(client *ai.Client, store *ai.BenchmarkStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp := aiStatusResponse{Enabled: client != nil}
		if client != nil {
			for _, s := range client.Slots() {
				resp.Chain = append(resp.Chain, aiSlotView{ID: s.ID, Provider: s.Provider, Model: s.Model})
			}
			resp.Cost = client.CostConfig()
		}
		if store != nil {
			resp.Results = store.Results()
			resp.Cases = store.Cases()
		}
		c.JSON(http.StatusOK, resp)
	}
}

// RunAIBenchmark — POST /api/ai/benchmark. Benchmarks the chain PLUS every model
// installed on the local Ollama (auto-discovered) — each warmed up first — then
// persists the scores and re-orders the live chain best-first.
//
// Runs on a DETACHED context (not the request's) so a slow run with many local
// models isn't aborted if the browser/proxy times out the HTTP call; results are
// persisted regardless and show up on the next GET.
func RunAIBenchmark(client *ai.Client, store *ai.BenchmarkStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": errAIDisabled})
			return
		}
		var cases []ai.BenchmarkCase
		if store != nil {
			cases = store.Cases()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		// Chain slots + discovered models across all providers (local Ollama +
		// Groq's models + OpenRouter free models), deduped against the chain.
		slots := client.Slots()
		slots = append(slots, client.DiscoverModels(ctx)...)
		// Drop models we're not allowed to pay for (cost > ceiling, or unknown cost)
		// before scoring — with the default ceiling of 0 this is free-only, so we
		// never spend; it also purges paid leftovers a pre-filter run adopted (e.g.
		// a Zen "big-pickle").
		slots = client.AffordableSlots(slots)

		scores := client.RunSlots(ctx, slots, cases)
		if store != nil {
			if err := store.SaveResults(scores); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		// Adopt the ranking as the live chain: best model first, every working
		// model (incl. discovered free locals) kept as fallback. The breaker then
		// skips a rate-limited vendor at runtime, falling through to the next.
		client.AdoptBenchmark(scores)

		c.JSON(http.StatusOK, gin.H{"results": scores})
	}
}

// RunAIBenchmarkIncomplete — POST /api/ai/benchmark/rerun-incomplete. Re-benchmarks
// ONLY the models left Incomplete by the last run (cases skipped by a rate limit)
// and merges the fresh scores in, persisting + re-adopting. Meant to be triggered
// LATER (e.g. a day after) so the retry lands outside the vendor's rate-limit
// window — without paying the cost of re-running the whole catalog.
func RunAIBenchmarkIncomplete(client *ai.Client, store *ai.BenchmarkStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": errAIDisabled})
			return
		}
		if store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "benchmark store unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		merged := client.RerunIncomplete(ctx, store.Results(), store.Cases())
		if err := store.SaveResults(merged); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		client.AdoptBenchmark(merged)

		c.JSON(http.StatusOK, gin.H{"results": merged})
	}
}

type aiCasesReq struct {
	Cases []ai.BenchmarkCase `json:"cases"`
}

// PutAICases — PUT /api/ai/benchmark/cases. Replaces the whole editable case set.
func PutAICases(store *ai.BenchmarkStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "benchmark store unavailable"})
			return
		}
		var req aiCasesReq
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := store.SetCases(req.Cases); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"cases": store.Cases()})
	}
}

// PutAICostConfig — PUT /api/ai/settings. Updates the cost knobs (ceiling, energy
// tariff, GPU watts) live and persists them so they survive a restart. Lets the
// admin tune the value-based score from the UI without editing env/yaml.
func PutAICostConfig(client *ai.Client, store *ai.BenchmarkStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if client == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": errAIDisabled})
			return
		}
		var cc ai.CostConfig
		if err := c.ShouldBindJSON(&cc); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if cc.MaxCostPer1M < 0 || cc.KWhPrice < 0 || cc.LocalWatts < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "valores não podem ser negativos"})
			return
		}
		client.SetCostConfig(cc)
		if store != nil {
			if err := store.SaveCostConfig(client.CostConfig()); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"cost": client.CostConfig()})
	}
}
