package handlers

import (
	"context"
	"log"
	"net/http"
	"sort"
	"sync"
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
	Running   bool               `json:"running"`             // a benchmark run is in flight right now
	StartedAt string             `json:"startedAt,omitempty"` // RFC3339; set only when Running
}

type aiSlotView struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// BenchmarkRunTracker makes an in-flight benchmark run STOPPABLE and VISIBLE.
// Without it, each POST /api/ai/benchmark spawns a detached goroutine (see
// RunAIBenchmark) with no handle anyone can reach afterward: a run that's
// taking too long can't be cancelled, and a client that gave up (proxy/browser
// timeout — the run keeps going server-side by design) has no way to tell "is
// it still running?" short of polling and comparing timestamps by hand. One
// tracker is shared across GetAIBenchmark/RunAIBenchmark/RunAIBenchmarkIncomplete
// (injected from main.go) so cancel/status apply no matter which endpoint
// started the current run, and a second run can't be started (and thrash the
// same local Ollama GPU) while one is already active.
type BenchmarkRunTracker struct {
	mu        sync.Mutex
	cancel    context.CancelFunc
	startedAt time.Time
}

func NewBenchmarkRunTracker() *BenchmarkRunTracker { return &BenchmarkRunTracker{} }

// start claims the tracker for a new run, returning false if one is already
// active (the caller should reject the request rather than run two at once).
func (t *BenchmarkRunTracker) start(cancel context.CancelFunc) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel != nil {
		return false
	}
	t.cancel = cancel
	t.startedAt = time.Now()
	return true
}

// finish releases the tracker — call via defer right after start succeeds.
func (t *BenchmarkRunTracker) finish() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cancel = nil
}

// Stop cancels the active run's context, if any. Returns false when nothing is
// running — the caller can turn that into a 404/no-op response.
func (t *BenchmarkRunTracker) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cancel == nil {
		return false
	}
	t.cancel()
	return true
}

// Status reports whether a run is active and when it started (zero when not).
func (t *BenchmarkRunTracker) Status() (running bool, startedAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cancel != nil, t.startedAt
}

// CancelAIBenchmark — POST /api/ai/benchmark/cancel. Stops whichever benchmark
// run is currently in flight (started via RunAIBenchmark or
// RunAIBenchmarkIncomplete) — its context is cancelled, so it unwinds ASAP and
// persists whatever was already measured (the incremental save in
// saveProgress already covers every slot finished before the cancel).
func CancelAIBenchmark(tracker *BenchmarkRunTracker) gin.HandlerFunc {
	return func(c *gin.Context) {
		if tracker == nil || !tracker.Stop() {
			c.JSON(http.StatusNotFound, gin.H{"error": "nenhum benchmark em execução"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"cancelled": true})
	}
}

// GetAIBenchmark — GET /api/ai/benchmark. Returns the live chain order, the last
// benchmark results, and the editable case set.
func GetAIBenchmark(client *ai.Client, store *ai.BenchmarkStore, tracker *BenchmarkRunTracker) gin.HandlerFunc {
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
		if tracker != nil {
			if running, startedAt := tracker.Status(); running {
				resp.Running = true
				resp.StartedAt = startedAt.UTC().Format(time.RFC3339)
			}
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
		return ai.RankBefore(merged[i], merged[j])
	})
	return merged
}

// saveProgress builds an ai.RunSlotsProgress callback that persists each slot's
// score AS SOON as it finishes, instead of waiting for the whole run — a run
// with several local Ollama models can take many minutes (each with a warmup),
// and without this, a timeout or restart partway through would leave the store
// exactly as empty as before the run started. Safe to call concurrently: it may
// fire from multiple goroutines (cloud slots run in parallel). Best-effort — a
// write hiccup here doesn't abort the run; the final persistBenchmarkRun call
// still re-derives the authoritative, fully-ordered result set.
func saveProgress(store *ai.BenchmarkStore) func(ai.SlotScore) {
	if store == nil {
		return nil
	}
	return func(sc ai.SlotScore) {
		if err := store.UpsertResult(sc); err != nil {
			log.Printf("Warning: ai benchmark incremental save failed for %s: %v", sc.SlotID, err)
			return
		}
		if err := store.RecordOne(sc); err != nil {
			log.Printf("Warning: ai benchmark incremental history save failed for %s: %v", sc.SlotID, err)
		}
	}
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

// discoveryTimeout bounds only the model-listing calls (filterSlotsForBenchmark)
// that happen BEFORE we know the fleet size — listing a provider's /v1/models is
// fast, so this can stay flat regardless of how many local models end up tested.
const discoveryTimeout = 2 * time.Minute

// baseRunBudget/perLocalSlotBudget/maxRunBudget size the SCORING run's deadline
// to the fleet actually being tested — see scoringBudget.
const (
	baseRunBudget      = 5 * time.Minute
	perLocalSlotBudget = 4 * time.Minute
	maxRunBudget       = 45 * time.Minute
)

// scoringBudget grows the run deadline with the number of LOCAL Ollama slots
// (each up to a 5-minute VRAM warmup, see ai.warmupTimeout) because those are
// drained ONE AT A TIME (ai.RunSlotsProgress) — a flat ceiling that doesn't
// scale with the fleet starves whichever local models are queued last: their
// turn comes only after the deadline already expired, so they fail instantly
// with zero samples ("context deadline exceeded") without ever being tried.
// Non-local slots run in parallel and don't add to that serial queue, so they
// don't grow the budget. Capped so a huge/misconfigured fleet can't hang
// forever — see also ai.localSlotBudget, the per-slot fair-share guard inside
// the queue itself.
func scoringBudget(slots []ai.Slot) time.Duration {
	localCount := 0
	for _, s := range slots {
		if s.Local {
			localCount++
		}
	}
	budget := baseRunBudget + time.Duration(localCount)*perLocalSlotBudget
	if budget > maxRunBudget {
		return maxRunBudget
	}
	return budget
}

// RunAIBenchmark — POST /api/ai/benchmark. Benchmarks the chain PLUS every model
// installed on the local Ollama (auto-discovered) — each warmed up first — then
// persists the scores and re-orders the live chain best-first.
//
// Runs on a DETACHED context (not the request's) so a slow run with many local
// models isn't aborted if the browser/proxy times out the HTTP call; results are
// persisted regardless and show up on the next GET. tracker makes it STOPPABLE
// (POST /api/ai/benchmark/cancel) and guards against a second run starting
// while one is active — two runs at once would thrash the same local Ollama
// GPU (see ai.RunSlotsProgress's serialized local queue).
func RunAIBenchmark(client *ai.Client, store *ai.BenchmarkStore, tracker *BenchmarkRunTracker) gin.HandlerFunc {
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
		discoverCtx, discoverCancel := context.WithTimeout(context.Background(), discoveryTimeout)
		slots := filterSlotsForBenchmark(discoverCtx, client, provider, model)
		discoverCancel()

		ctx, cancel := context.WithTimeout(context.Background(), scoringBudget(slots))
		defer cancel()
		if tracker != nil {
			if !tracker.start(cancel) {
				c.JSON(http.StatusConflict, gin.H{"error": "já existe um benchmark em execução"})
				return
			}
			defer tracker.finish()
		}

		scores := client.RunSlotsProgress(ctx, slots, cases, saveProgress(store))

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
// window — without paying the cost of re-running the whole catalog. Shares
// tracker with RunAIBenchmark so it's stoppable and can't overlap a full run.
func RunAIBenchmarkIncomplete(client *ai.Client, store *ai.BenchmarkStore, tracker *BenchmarkRunTracker) gin.HandlerFunc {
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
		if tracker != nil {
			if !tracker.start(cancel) {
				c.JSON(http.StatusConflict, gin.H{"error": "já existe um benchmark em execução"})
				return
			}
			defer tracker.finish()
		}

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
