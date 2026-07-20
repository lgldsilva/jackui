package ai

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lgldsilva/jackui/internal/config"
)

// BenchmarkCase is one labelled example: a raw torrent/release name and the
// canonical label we expect the model to extract. The set is user-editable
// (persisted in the benchmark store) — that's the "modifiable" part: tune it to
// the kind of releases you actually download and the chain re-ranks for them.
//
// Expect carries the STRUCTURE inline, in the same canonical form the rename
// feature produces, so examples and results are coherent with séries/temporadas/
// episódios (parsed by parseExpect at scoring time — no schema migration):
//   - Movie:           "Inception - 2010"        (Título - Ano)
//   - TV episode:      "Breaking Bad - S03E07"   (Série - Temporada/Episódio)
//   - TV (no season):  "Frieren - E01"           (Série - Episódio)
//   - Season pack:     "The Wire - S04"          (Série - Temporada, sem episódio)
//   - Plain title:     "Inception"               (sem estrutura → só o título conta)
type BenchmarkCase struct {
	Raw    string `json:"raw"`
	Expect string `json:"expect"`
	// Task selects which AI task this case measures: "rename" (default), "identify"
	// or "schedule" (see TaskRename/…). Empty normalizes to "rename" so legacy cases
	// (no Task column) and the UI's plain-title textarea keep scoring the rename task
	// exactly as before — the multi-task framework is additive, not a break.
	Task string `json:"task,omitempty"`
	// Origin is "default" for the built-in set (see OriginDefault) and empty for
	// user-added cases. Informational — scoring ignores it, and the UI's textarea
	// editor doesn't round-trip it, so any user-saved set simply becomes custom.
	Origin string `json:"origin,omitempty"`
}

// TaskScore is one model's accuracy on ONE task within a benchmark run. It's the
// per-task breakdown behind the global Accuracy/Composite — the UI can show it as
// extra columns, and the composite averages across tasks so the chain is ranked on
// ALL its jobs, not just rename.
type TaskScore struct {
	Accuracy float64 `json:"accuracy"` // 0..1 mean over this task's cases
	Samples  int     `json:"samples"`  // this task's cases that produced a usable reply
	Scored   int     `json:"scored"`   // usable + bad-output (the accuracy denominator)
}

// SlotScore is one model's aggregate result over the whole case set.
type SlotScore struct {
	SlotID        string  `json:"slotId"`
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	Accuracy      float64 `json:"accuracy"`     // 0..1 — mean of the per-task accuracies (so every task weighs equally)
	AvgLatencyMs  int64   `json:"avgLatencyMs"` // MEDIAN wall-clock per call (resilient to model-load residual)
	Composite     float64 `json:"composite"`    // accuracy / sqrt(latencySeconds) / (1+cost)
	Samples       int     `json:"samples"`      // cases that produced a usable reply
	Free          bool    `json:"free"`         // true when CostPer1M == 0
	CostPer1M     float64 `json:"costPer1M"`    // blended USD per 1M tokens (0 = free); drives the composite
	FailureReason string  `json:"failureReason,omitempty"`
	// Tasks is the per-task accuracy breakdown (keyed by task id: "rename",
	// "identify", "schedule"). Optional in the JSON — older persisted rows and the
	// single-task default leave it nil and the UI falls back to the global Accuracy.
	Tasks map[string]TaskScore `json:"tasks,omitempty"`
	// Incomplete is true when some cases were transiently SKIPPED (rate limit
	// after retries, network) so the model wasn't measured on the full set. These
	// are the ones the "Rodar faltantes" button re-runs later, outside the
	// rate-limit window. A model fully tested (even if some cases failed hard) is
	// NOT incomplete.
	Incomplete bool `json:"incomplete,omitempty"`
	// Completeness is the fraction of cases (0..1) that produced a scored result. 1.0 for a
	// full run; lower when cases were skipped (rate limit). RankBefore uses it to demote only
	// SPARSELY-measured runs, not a run that covered most cases at high accuracy. 0 on legacy
	// rows persisted before this field existed — RankBefore falls back to Incomplete there.
	Completeness float64 `json:"completeness,omitempty"`
	// History fields are OUTPUT-ONLY: they're populated by BenchmarkStore.Results
	// from the durable benchmark_history table (NOT by a live RunSlots measurement,
	// which has no past to look at). They answer "did this run succeed or error,
	// did the error persist, and when did it last succeed" without re-running.
	// Empty/zero on a fresh measurement and on legacy rows with no recorded history.
	LastOutcome         string `json:"lastOutcome,omitempty"`         // "ok" | "incomplete" | "error" of the last actual run
	LastError           string `json:"lastError,omitempty"`           // failure reason of the last failing run; "" once it succeeds again. Durable (survives the SaveResults re-baseline that wipes FailureReason)
	LastSuccessAt       string `json:"lastSuccessAt,omitempty"`       // RFC3339 of the last "ok" run; "" = never succeeded
	LastRunAt           string `json:"lastRunAt,omitempty"`           // RFC3339 of the last run (any outcome)
	FirstFailureAt      string `json:"firstFailureAt,omitempty"`      // RFC3339 the current error streak began; "" = not failing
	ConsecutiveFailures int    `json:"consecutiveFailures,omitempty"` // # of consecutive "error" runs (resets on a usable run)
}

// Run outcome labels. A run is OK when it produced a complete, usable measurement;
// INCOMPLETE when transiently cut short (rate limit) — the "faltante" state; ERROR
// when it yielded no usable reply at all (hard failure). These drive the durable
// per-slot history so the UI can show success/error status, error persistence, and
// the date of the last success.
const (
	OutcomeOK         = "ok"
	OutcomeIncomplete = "incomplete"
	OutcomeError      = "error"
)

// Run benchmarks the configured chain. See RunSlots.
func (c *Client) Run(ctx context.Context, cases []BenchmarkCase) []SlotScore {
	return c.RunSlots(ctx, c.slots, cases)
}

// RunSlots benchmarks the given slots against the case set and returns scores
// sorted by composite (best first). Each slot is called directly (bypassing the
// breaker) so a parked model still gets measured. Used with the configured chain
// AND with discovered local Ollama models.
func (c *Client) RunSlots(ctx context.Context, slots []Slot, cases []BenchmarkCase) []SlotScore {
	return c.RunSlotsProgress(ctx, slots, cases, nil)
}

// RunSlotsProgress is RunSlots plus an optional onResult callback invoked as SOON
// as each slot finishes scoring (not just once the whole batch completes). The
// caller can use it to persist results incrementally — a long run with several
// local Ollama models (each with a warmup that can take minutes) would otherwise
// keep the store empty until every slot finishes, losing all progress if the run
// is aborted or times out partway through. onResult may be called concurrently
// from multiple goroutines (cloud slots run in parallel) — the caller must be
// safe for that.
// localSlotFloor is the minimum time a local slot's own sub-context gets,
// regardless of how the remaining run budget divides up: enough to cover a
// cold VRAM load (warmupTimeout) plus a handful of real cases. Below this, a
// slot deep in the queue would get a sliver too short to ever produce a usable
// sample even when the overall run still has time left.
const localSlotFloor = warmupTimeout + 2*time.Minute

// localSlotContext bounds ONE local model's turn in the sequential queue to a
// fair share of whatever time is left on the run — remainingSlots INCLUDES the
// one about to run. Without this, the first slow/stuck local model can consume
// the entire shared deadline and leave every slot still queued behind it with
// zero time, an instant "context deadline exceeded" before even attempting a
// call (the exact failure mode a large local fleet produced in production).
// context.WithTimeout already clamps to the parent's real deadline when it's
// sooner, so requesting more than what's actually left is harmless — this only
// ever shortens a slot's turn, never extends the overall run past ctx's own
// deadline.
func localSlotContext(ctx context.Context, remainingSlots int) (context.Context, context.CancelFunc) {
	if remainingSlots < 1 {
		remainingSlots = 1
	}
	share := localSlotFloor
	if dl, ok := ctx.Deadline(); ok {
		if fair := time.Until(dl) / time.Duration(remainingSlots); fair > share {
			share = fair
		}
	}
	return context.WithTimeout(ctx, share)
}

func (c *Client) RunSlotsProgress(ctx context.Context, slots []Slot, cases []BenchmarkCase, onResult func(SlotScore)) []SlotScore { //nolint:gocognit // complexidade cognitiva rastreada no refactor de god-files (auditoria #416)
	if len(cases) == 0 {
		cases = AllDefaultBenchmarkCases()
	}
	// Only LOCAL Ollama models (Slot.Local — see localModel) must be serialized:
	// they share one GPU and Ollama serves a single model at a time, so concurrent
	// calls exceed its connection slots and thrash models in/out of VRAM. They run
	// one at a time in a single goroutine, each with a VRAM warmup (one untimed
	// priming call) so the latency reflects a resident model.
	//
	// Everything else — external vendors AND Ollama *cloud* models ("-cloud", which
	// run on remote infra) — tolerates parallelism, so we fan those out one
	// goroutine per slot to cut wall-clock. Both groups run concurrently: the local
	// queue overlaps the parallel cloud calls.
	results := make([]SlotScore, len(slots))
	var wg sync.WaitGroup
	for i, s := range slots {
		if s.Local {
			continue
		}
		wg.Add(1)
		go func(i int, s Slot) {
			defer wg.Done()
			results[i] = c.scoreSlot(ctx, s, cases, false)
			if onResult != nil {
				onResult(results[i])
			}
		}(i, s)
	}
	// Local models: a single goroutine drains them sequentially (with warmup),
	// each capped to a FAIR SHARE of the run's remaining time (see
	// localSlotContext) so one slow/stuck model can't starve every local model
	// still queued behind it.
	localTotal := 0
	for _, s := range slots {
		if s.Local {
			localTotal++
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		done := 0
		for i, s := range slots {
			if !s.Local {
				continue
			}
			slotCtx, slotCancel := localSlotContext(ctx, localTotal-done)
			results[i] = c.scoreSlot(slotCtx, s, cases, true)
			slotCancel()
			done++
			if onResult != nil {
				onResult(results[i])
			}
		}
	}()
	wg.Wait()

	sort.SliceStable(results, func(i, j int) bool { return RankBefore(results[i], results[j]) })
	return results
}

// warmupTimeout bounds the untimed priming call for a local Ollama model. It has
// to cover the cold-load of a large model into VRAM (e.g. an 8B model off a cold
// cache can take well over two minutes), otherwise the warmup is cut short, the
// model isn't resident, and the FIRST timed case eats the load cost — skewing the
// measurement against bigger-but-better local models.
const warmupTimeout = 300 * time.Second

// scoreSlot runs the full case set against one slot and aggregates the result.
// When warmup is true (local Ollama) it issues one untimed priming call first so
// the model is resident in VRAM before the timed cases run.
//
// Latency is reported as the MEDIAN of the per-case wall-clock, not the mean. A
// mean is dragged up by a single slow call — and the most common slow call is
// model-load residual leaking into the first timed case when the warmup didn't
// fully load the model. The median ignores that lone outlier, so the score
// reflects steady-state inference latency, which is what we're ranking on.
func (c *Client) scoreSlot(ctx context.Context, s Slot, cases []BenchmarkCase, warmup bool) SlotScore {
	score := SlotScore{SlotID: s.ID, Provider: s.Provider, Model: s.Model, Free: s.Free, CostPer1M: s.CostPer1M}
	if warmup {
		warmCtx, warmCancel := context.WithTimeout(ctx, warmupTimeout)
		_, _, _, _ = c.metadataWithSlot(warmCtx, s, "warmup")
		warmCancel()
	}
	t := &slotTally{lats: make([]time.Duration, 0, len(cases)), tasks: map[string]*taskTally{}}
	paymentFail := false
	for _, tc := range cases {
		if c.scoreSingleCase(ctx, s, tc, &score, t) {
			paymentFail = true
			break
		}
	}
	// Incomplete = some cases were transiently skipped (didn't reach the full set).
	// Not for a paid no-balance abort (re-running won't help).
	score.Incomplete = !paymentFail && t.scored < len(cases)
	if len(cases) > 0 {
		score.Completeness = float64(t.scored) / float64(len(cases))
	}
	// Local models aren't free — price their energy (latency × tokens × power ×
	// tariff) so a slow/power-hungry local ranks below a fast cloud-free one. No-op
	// unless a tariff is configured.
	if s.Local {
		if e := c.localEnergyCostPer1M(t.totalLatency, t.tokens); e > 0 {
			score.CostPer1M = e
			score.Free = false
		}
	}
	if score.Samples > 0 {
		// Accuracy is the MEAN of the per-task accuracies (each task weighs equally,
		// so a model great at rename but terrible at schedule can't top the ranking).
		// A single-task run collapses to that one task's accuracy — identical to the
		// old behavior. Each task's own denominator is its `scored` (bad output = 0).
		// Latency is the median over the USABLE replies across all tasks.
		score.Tasks, score.Accuracy = t.taskBreakdown()
		score.AvgLatencyMs = medianDuration(t.lats).Milliseconds()
		score.Composite = compositeScore(reliableAccuracy(score.Accuracy, score.Samples), score.AvgLatencyMs, score.CostPer1M)
	} else if paymentFail {
		score.Composite = -1
	}
	return score
}

// slotTally accumulates a slot's per-case outcomes across the run: count of scored
// cases (usable + bad-output), total tokens + latency (for the local-energy
// estimate), the per-call latencies (for the median), and a per-task sub-tally.
type slotTally struct {
	scored       int
	tokens       int
	lats         []time.Duration
	totalLatency time.Duration
	tasks        map[string]*taskTally
}

// taskTally is the per-task accumulator inside a slotTally: accuracy sum and the
// usable/scored counts for that one task.
type taskTally struct {
	accSum  float64
	samples int
	scored  int
}

// record folds one case's outcome into the right task's sub-tally, creating it on
// first use. badOutput=true marks a 0-accuracy scored case (the model replied but
// botched it) so the task's accuracy denominator still grows.
func (t *slotTally) record(task string, accuracy float64, usable, badOutput bool) {
	task = normalizeTask(task)
	tt := t.tasks[task]
	if tt == nil {
		tt = &taskTally{}
		t.tasks[task] = tt
	}
	if usable {
		tt.samples++
		tt.scored++
		tt.accSum += accuracy
	} else if badOutput {
		tt.scored++ // counts as a 0-accuracy case
	}
}

// taskBreakdown turns the per-task sub-tallies into the SlotScore.Tasks map and
// the overall accuracy — the MEAN of the per-task accuracies (each task weighs
// equally regardless of how many cases it has). A single task collapses to that
// task's accuracy, matching the historical single-task number exactly.
func (t *slotTally) taskBreakdown() (map[string]TaskScore, float64) {
	if len(t.tasks) == 0 {
		return nil, 0
	}
	out := make(map[string]TaskScore, len(t.tasks))
	var sum float64
	counted := 0
	for name, tt := range t.tasks {
		if tt.scored == 0 {
			continue // task had only transient skips → not measured, don't dilute the mean
		}
		acc := tt.accSum / float64(tt.scored)
		out[name] = TaskScore{Accuracy: acc, Samples: tt.samples, Scored: tt.scored}
		sum += acc
		counted++
	}
	if counted == 0 {
		return out, 0
	}
	return out, sum / float64(counted)
}

// medianDuration returns the median of the samples (the mean of the two middle
// values for an even count). Empty input is 0. Sorts a copy — the caller's slice
// stays in call order.
func medianDuration(lats []time.Duration) time.Duration {
	n := len(lats)
	if n == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), lats...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// Rate-limit retry budget. A throttle is transient, so we wait the vendor's reset
// window and retry — that's how a model gets a COMPLETE score (all cases) instead
// of a misleading 100% over the few that slipped past the limit. Capped so a
// per-DAY quota (Retry-After of minutes/hours) doesn't stall the benchmark: past
// the cap we give up and the case is skipped.
const (
	maxRateLimitRetries = 3
	maxRateLimitWait    = 20 * time.Second
)

// metadataWithRetry runs one rename case, retrying transient rate limits with
// backoff so the model is measured on the whole case set. Kept as a thin wrapper
// over taskCaseWithRetry for the existing rename-only callers/tests.
func (c *Client) metadataWithRetry(ctx context.Context, s Slot, raw string) (*RenameMetadata, time.Duration, int, error) {
	res, latency, tokens, err := c.metadataWithSlot(ctx, s, raw)
	if err == nil || !errors.Is(err, errRateLimited) {
		return res, latency, tokens, err
	}
	// Reuse the generic backoff loop by retrying the rename call directly.
	for attempt := 1; attempt <= maxRateLimitRetries; attempt++ {
		wait := rateLimitBackoff(err, attempt-1)
		if wait <= 0 || wait > maxRateLimitWait || ctx.Err() != nil {
			return res, latency, tokens, err
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return res, latency, tokens, err
		case <-timer.C:
		}
		res, latency, tokens, err = c.metadataWithSlot(ctx, s, raw)
		if err == nil || !errors.Is(err, errRateLimited) {
			return res, latency, tokens, err
		}
	}
	return res, latency, tokens, err
}

// taskCaseWithRetry runs ONE case for the case's task (rename/identify/schedule)
// through the right runner, retrying transient rate limits with the same backoff
// budget so the model is measured on the whole set. Returns the accuracy the runner
// computed, the call latency, tokens (for the local-energy estimate) and the error
// class (nil=usable, errBadOutput=scored 0, anything else=transient skip).
func (c *Client) taskCaseWithRetry(ctx context.Context, s Slot, tc BenchmarkCase) (accuracy float64, latency time.Duration, tokens int, err error) {
	run := runnerFor(tc.Task)
	for attempt := 0; ; attempt++ {
		accuracy, latency, tokens, err = run(ctx, c, s, tc.Expect, tc.Raw)
		if err == nil || !errors.Is(err, errRateLimited) || attempt >= maxRateLimitRetries {
			return accuracy, latency, tokens, err
		}
		wait := rateLimitBackoff(err, attempt)
		if wait <= 0 || wait > maxRateLimitWait || ctx.Err() != nil {
			return accuracy, latency, tokens, err // per-day quota or no time left → give up, skip the case
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return accuracy, latency, tokens, err
		case <-timer.C:
		}
	}
}

// rateLimitBackoff picks the wait before retrying a throttled call: the vendor's
// Retry-After (plus a small cushion) when present, else exponential (2s, 4s, 8s).
func rateLimitBackoff(err error, attempt int) time.Duration {
	var rl *rateLimitError
	if errors.As(err, &rl) && rl.RetryAfter > 0 {
		return rl.RetryAfter + 500*time.Millisecond
	}
	return time.Duration(1<<uint(attempt)) * 2 * time.Second
}

// scoreSingleCase runs one case and folds it into the running score. Returns true
// only to ABORT the whole slot (no point continuing) — i.e. the model is paid with
// no balance. Error handling has three tiers:
//   - insufficient balance → abort the slot ("pago — sem saldo").
//   - bad output (errBadOutput: HTTP 400 / unparseable JSON) → a quality failure of
//     this model on this input: counts as a 0-accuracy case (scored++, accSum += 0).
//   - anything else (rate limit after retries, 5xx, network, a crashed local
//     llama-server) → transient/infra: skip silently, don't penalize accuracy. If
//     every case is transient the slot ends with Samples==0 → 0% / — / —.
func (c *Client) scoreSingleCase(ctx context.Context, s Slot, tc BenchmarkCase, score *SlotScore, t *slotTally) bool {
	accuracy, latency, tk, err := c.taskCaseWithRetry(ctx, s, tc)
	if err != nil {
		if errors.Is(err, errInsufficientBalance) {
			if score.FailureReason == "" {
				score.FailureReason = "pago — sem saldo"
			}
			return true
		}
		if score.FailureReason == "" {
			score.FailureReason = err.Error()
		}
		if errors.Is(err, errBadOutput) {
			t.scored++                        // model replied but botched it → a 0-accuracy case
			t.record(tc.Task, 0, false, true) // recorded as a 0 against this task
		}
		return false
	}
	score.Samples++
	t.scored++
	t.tokens += tk
	t.totalLatency += latency
	t.lats = append(t.lats, latency)
	t.record(tc.Task, accuracy, true, false)
	return false
}

// AdoptBenchmark rebuilds the live chain from benchmark scores: every model that
// produced a usable reply (Samples>0), ordered best-first by composite. This is
// what the user wants — "use the best benchmark" — while keeping the free local
// models in the chain as low-ranked fallbacks (the breaker skips a rate-limited
// vendor at runtime, falling through to the next, ultimately the free local).
// RerunIncomplete re-benchmarks ONLY the models flagged Incomplete in prev (cases
// transiently skipped, typically rate-limited) and merges the fresh scores over
// prev. This backs the "Rodar faltantes" button: run it later — a day after, even
// — so the retry lands OUTSIDE the rate-limit window and the model finally gets a
// complete score. Paid models are filtered out (never spend on them); if nothing
// is incomplete, prev is returned unchanged.
// NeedsRerun reports whether a result is worth re-running via "Rodar faltantes":
// either the model was left Incomplete (some cases transiently skipped) OR it
// failed with a rate limit. The rate-limit check also catches results persisted
// BEFORE the Incomplete flag existed, so the button works on pre-existing data
// without forcing a full re-run first. A hard failure (bad output) is NOT
// re-runnable — re-trying won't change a model that genuinely can't comply.
func NeedsRerun(s SlotScore) bool {
	return s.Incomplete || strings.Contains(strings.ToLower(s.FailureReason), "rate limit")
}

// RerunIncomplete returns (merged, fresh): merged is prev with the re-run scores
// folded in (sorted best-first); fresh is ONLY the slots actually re-measured this
// call. The caller records history for `fresh` alone — recording the carried-over
// slots would spuriously bump their failure streak for a run that never happened.
func (c *Client) RerunIncomplete(ctx context.Context, prev []SlotScore, cases []BenchmarkCase) (merged, fresh []SlotScore) {
	if len(cases) == 0 {
		cases = AllDefaultBenchmarkCases()
	}
	var slots []Slot
	for _, r := range prev {
		if NeedsRerun(r) {
			if slot, ok := c.resolveSlot(r.SlotID, r.Provider, r.Model); ok {
				slots = append(slots, slot)
			}
		}
	}
	slots = c.AffordableSlots(slots)
	if len(slots) == 0 {
		return prev, nil
	}
	fresh = c.RunSlots(ctx, slots, cases)

	byID := make(map[string]SlotScore, len(prev))
	order := make([]string, 0, len(prev))
	for _, s := range prev {
		byID[s.SlotID] = s
		order = append(order, s.SlotID)
	}
	for _, s := range fresh {
		if _, seen := byID[s.SlotID]; !seen {
			order = append(order, s.SlotID)
		}
		byID[s.SlotID] = s // fresh score wins
	}
	out := make([]SlotScore, 0, len(byID))
	for _, id := range order {
		out = append(out, byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return RankBefore(out[i], out[j]) })
	return out, fresh
}

func (c *Client) AdoptBenchmark(scores []SlotScore) {
	ranked := make([]SlotScore, 0, len(scores))
	for _, s := range scores {
		if s.Samples > 0 {
			ranked = append(ranked, s)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return RankBefore(ranked[i], ranked[j]) })
	defs := make([]config.AIChainSlot, 0, len(ranked))
	for _, s := range ranked {
		defs = append(defs, config.AIChainSlot{ID: s.SlotID, Provider: s.Provider, Model: s.Model})
	}
	c.ApplyChain(defs)
}

// ApplyOrder re-sorts the live chain to the given slot-id order (best first).
// Unknown ids are ignored; slots not named keep their relative order at the end.
func (c *Client) ApplyOrder(order []string) {
	rank := map[string]int{}
	for i, id := range order {
		rank[id] = i
	}
	sort.SliceStable(c.slots, func(i, j int) bool {
		ri, oki := rank[c.slots[i].ID]
		rj, okj := rank[c.slots[j].ID]
		if oki && okj {
			return ri < rj
		}
		return oki && !okj // ranked slots before unranked
	})
}
