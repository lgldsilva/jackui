package handlers

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/ai"
)

const errAIDisabled = "AI chain disabled"

// aiStatusResponse is the read model for the Settings AI card.
type aiStatusResponse struct {
	Enabled   bool               `json:"enabled"`
	Chain     []aiSlotView       `json:"chain"`
	Results   []ai.SlotScore     `json:"results"`
	Cases     []ai.BenchmarkCase `json:"cases"`
	Cost      ai.CostConfig      `json:"cost"`
	Providers []string           `json:"providers"`
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
			resp.Providers = client.Providers()
		}
		if store != nil {
			resp.Results = store.Results()
			resp.Cases = store.Cases()
		}
		c.JSON(http.StatusOK, resp)
	}
}

// filterExistingSlots filters candidate slots currently configured.
func filterExistingSlots(client *ai.Client, provider, model string) []ai.Slot {
	var slots []ai.Slot
	for _, s := range client.Slots() {
		if (provider == "" || s.Provider == provider) && (model == "" || s.Model == model) {
			slots = append(slots, s)
		}
	}
	return slots
}

// discoverSlotsForBenchmark lists and filters candidate models from providers.
func discoverSlotsForBenchmark(ctx context.Context, client *ai.Client, provider, model string) []ai.Slot {
	var slots []ai.Slot
	if model != "" {
		var discovered []ai.Slot
		if provider == "" {
			discovered = client.DiscoverModels(ctx)
		} else {
			discovered = client.DiscoverModelsForProvider(ctx, provider)
		}
		for _, s := range discovered {
			if s.Model == model {
				slots = append(slots, s)
			}
		}
	} else {
		if provider == "" {
			slots = append(slots, client.DiscoverModels(ctx)...)
		} else {
			slots = append(slots, client.DiscoverModelsForProvider(ctx, provider)...)
		}
	}
	return slots
}

// filterSlotsForBenchmark filters and dedupes candidate slots for benchmarking.
func filterSlotsForBenchmark(ctx context.Context, client *ai.Client, provider, model string) []ai.Slot {
	slots := filterExistingSlots(client, provider, model)
	discovered := discoverSlotsForBenchmark(ctx, client, provider, model)
	slots = append(slots, discovered...)
	slots = client.AffordableSlots(slots)
	return dedupeSlots(slots)
}

// mergeBenchmarkScores overwrites/inserts new scores into the existing benchmark results.
func mergeBenchmarkScores(existing []ai.SlotScore, newScores []ai.SlotScore) []ai.SlotScore {
	existingMap := make(map[string]ai.SlotScore)
	for _, s := range existing {
		existingMap[s.SlotID] = s
	}
	for _, s := range newScores {
		existingMap[s.SlotID] = s
	}
	var merged []ai.SlotScore
	for _, s := range existingMap {
		merged = append(merged, s)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].Composite > merged[j].Composite
	})
	return merged
}

// persistBenchmarkRun records the run history for the freshly-measured scores,
// then persists results — merging over the stored set for a single provider/model
// run, or replacing wholesale for a full run — and returns the re-read results so
// the response carries the joined history (LastSuccessAt etc.) for every row. With
// no store it's a pass-through. Extracted from RunAIBenchmark to keep that handler
// under the S3776 cognitive-complexity gate (the project already does this with
// filterSlotsForBenchmark).
func persistBenchmarkRun(store *ai.BenchmarkStore, scores []ai.SlotScore, provider, model string) ([]ai.SlotScore, error) {
	if store == nil {
		return scores, nil
	}
	if err := store.RecordRun(scores); err != nil {
		return nil, err
	}
	merged := scores
	if provider != "" || model != "" {
		merged = mergeBenchmarkScores(store.Results(), scores)
	}
	if err := store.SaveResults(merged); err != nil {
		return nil, err
	}
	return store.Results(), nil
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
		provider := c.Query("provider")
		model := c.Query("model")

		var cases []ai.BenchmarkCase
		if store != nil {
			cases = store.Cases()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		slots := filterSlotsForBenchmark(ctx, client, provider, model)
		scores := client.RunSlots(ctx, slots, cases)

		merged, err := persistBenchmarkRun(store, scores, provider, model)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		client.AdoptBenchmark(merged)

		c.JSON(http.StatusOK, gin.H{"results": merged})
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

		merged, fresh := client.RerunIncomplete(ctx, store.Results(), store.Cases())
		// Record history only for the slots actually re-measured (fresh); carried-over
		// rows keep their timeline untouched.
		if err := store.RecordRun(fresh); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if err := store.SaveResults(merged); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		merged = store.Results() // re-read with the joined history
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

func dedupeSlots(slots []ai.Slot) []ai.Slot {
	seen := make(map[string]bool)
	var out []ai.Slot
	for _, s := range slots {
		if !seen[s.ID] {
			seen[s.ID] = true
			out = append(out, s)
		}
	}
	return out
}
