package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

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

// RunOutcome classifies a freshly-measured score into ok/incomplete/error. Order
// matters: a partially-measured run is "incomplete" (the re-runnable faltante state)
// even if it gathered some samples; only a run with zero usable replies and no
// transient cut is a hard "error".
func RunOutcome(s SlotScore) string {
	switch {
	case s.Incomplete:
		return OutcomeIncomplete
	case s.Samples > 0:
		return OutcomeOK
	default:
		return OutcomeError
	}
}

// compositeScore ranks a model by VALUE: quality ÷ (latency^p × cost factor). Title
// identification is a BACKGROUND job (it runs once per item, off the request path), so
// accuracy should dominate and latency is only a tiebreaker — a model that's a bit
// slower but more accurate should win. The latency penalty therefore uses the CUBE ROOT
// (p = 1/3), gentler than the old sqrt: a 10× slower model is penalized ~2.15× instead of
// ~3.16×, so a correct-but-slow model isn't buried by a fast-but-sloppy one. A 0.3s floor
// stops a sub-300ms call from inflating the score.
//
// Cost (USD per 1M tokens, blended) enters as a (1 + cost) divisor: free models
// (cost 0) divide by 1 — no penalty — and every dollar/1M pushes the score down.
// So ranking is value-based, not a binary free/paid flag: a cheap accurate model
// beats an expensive one, and free beats a same-quality paid model. (This replaced
// the old flat 1.3x free bonus — with cost 0 for every free model the relative
// order among them is unchanged.)
func compositeScore(accuracy float64, avgLatencyMs int64, costPer1M float64) float64 {
	seconds := math.Max(0.3, float64(avgLatencyMs)/1000.0)
	cost := math.Max(0, costPer1M)
	return accuracy / math.Cbrt(seconds) / (1 + cost)
}

// reliabilityPriorMean/Weight tune reliableAccuracy: a Bayesian shrinkage that
// pulls a low-sample accuracy toward a conservative prior, as if `reliabilityPriorWeight`
// extra "phantom" cases had scored `reliabilityPriorMean`. A model cut short by a
// rate limit after only 5 calls and a lucky 5/5 would otherwise rank as
// confidently as one that proved itself over the full ~80-case set — this
// discounts that noise without needing to know the full case count.
const (
	reliabilityPriorMean   = 0.5
	reliabilityPriorWeight = 15.0
)

// reliableAccuracy is the accuracy value RANKING should use — never the one
// shown to the user (SlotScore.Accuracy stays the raw measured value; only
// compositeScore's input is adjusted). More samples means less pull toward the
// prior; a full ~80-case run is barely affected, a 5-sample run is pulled hard.
func reliableAccuracy(accuracy float64, samples int) float64 {
	if samples <= 0 {
		return 0
	}
	n := float64(samples)
	return (accuracy*n + reliabilityPriorMean*reliabilityPriorWeight) / (n + reliabilityPriorWeight)
}

// RankBefore orders two results BEST FIRST for the live chain and the Settings
// table. A slot that ran the FULL case set always outranks one left Incomplete
// (cut short by a rate limit) regardless of raw composite — an incomplete run's
// accuracy comes from a small, cherry-picked-by-timing sample and isn't
// comparable to a model that was actually put through the whole set. Within the
// same completeness tier, composite (which already folds in reliableAccuracy)
// decides. This fixes a real production case: a model with 69% accuracy over 12
// rate-limited samples out-ranked one with 99% over a complete 84-sample run,
// purely because it happened to also be fast.
func RankBefore(a, b SlotScore) bool {
	if a.Incomplete != b.Incomplete {
		return !a.Incomplete
	}
	return a.Composite > b.Composite
}

// tokenRe collapses every run of non-letter/non-digit characters into a single
// space. Unicode-aware (\p{L}/\p{N}) so non-Latin titles still tokenize instead
// of being erased (the old [^a-z0-9] regex zeroed any CJK/Cyrillic title).
var tokenRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// titleAccuracy scores how well `got` matches `expect`: 1.0 for an exact match
// (after normalization), otherwise the Jaccard overlap of word tokens so a
// near-miss ("Dune" vs "Dune Part Two") still earns partial credit.
func titleAccuracy(got, expect string) float64 {
	ng, ne := normalizeTitle(got), normalizeTitle(expect)
	if ne == "" {
		return 0
	}
	if ng == ne {
		return 1
	}
	gt, et := tokenSet(ng), tokenSet(ne)
	if len(gt) == 0 {
		return 0
	}
	inter := 0
	for w := range gt {
		if et[w] {
			inter++
		}
	}
	union := len(gt) + len(et) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// normalizeTitle canonicalizes a title for comparison. The criterion is:
// case-insensitive, accent-insensitive ("Amélie" == "Amelie" — models often
// drop diacritics the release name never carried), punctuation/separator
// runs collapsed to one space, trimmed. Word ORDER is deliberately ignored
// downstream (token-set Jaccard in titleAccuracy), but the exact-match path
// here keeps full credit order-sensitive.
func normalizeTitle(s string) string {
	return strings.Trim(tokenRe.ReplaceAllString(strings.ToLower(foldAccents(s)), " "), " ")
}

// foldAccents strips combining marks after NFD decomposition ("Amélie" →
// "Amelie", "São" → "Sao"). Non-Latin scripts pass through untouched.
func foldAccents(s string) string {
	decomposed := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(decomposed))
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Canonical-label parsers. The structure of an expected label lives inside the
// Expect string (see BenchmarkCase); these pull it back out for scoring. Order
// matters: try the most specific (S..E..) first.
var (
	expectTVRe     = regexp.MustCompile(`(?i)^(.*\S)\s+-\s+S(\d{1,2})E(\d{1,3})\s*$`)
	expectEpRe     = regexp.MustCompile(`(?i)^(.*\S)\s+-\s+E(\d{1,3})\s*$`)
	expectSeasonRe = regexp.MustCompile(`(?i)^(.*\S)\s+-\s+S(\d{1,2})\s*$`)
	expectYearRe   = regexp.MustCompile(`^(.*\S)\s+-\s+(\d{4})\s*$`)
)

// expectFields is the structured form of an Expect label. Zero season/episode/
// year means "not pinned by this case" — those fields are simply not scored.
type expectFields struct {
	Title   string
	Season  int
	Episode int
	Year    int
}

// parseExpect splits a canonical Expect label into its structured fields. A bare
// title (no " - S..E.." / " - E.." / " - S.." / " - YYYY" tail) yields just the
// title, so title-only cases keep working exactly as before. The season-only
// form ("The Wire - S04", a season pack) pins the season but NOT the episode —
// a model that invents an episode number for a pack isn't penalized, only a
// wrong/missing season is.
func parseExpect(expect string) expectFields {
	expect = strings.TrimSpace(expect)
	if m := expectTVRe.FindStringSubmatch(expect); m != nil {
		return expectFields{Title: strings.TrimSpace(m[1]), Season: atoiSafe(m[2]), Episode: atoiSafe(m[3])}
	}
	if m := expectEpRe.FindStringSubmatch(expect); m != nil {
		return expectFields{Title: strings.TrimSpace(m[1]), Episode: atoiSafe(m[2])}
	}
	if m := expectSeasonRe.FindStringSubmatch(expect); m != nil {
		return expectFields{Title: strings.TrimSpace(m[1]), Season: atoiSafe(m[2])}
	}
	if m := expectYearRe.FindStringSubmatch(expect); m != nil {
		return expectFields{Title: strings.TrimSpace(m[1]), Year: atoiSafe(m[2])}
	}
	return expectFields{Title: expect}
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// caseAccuracy scores a model's structured extraction against the expected
// canonical label. The title carries 60% (the dominant signal); when the case
// pins a season and/or episode (TV) the remaining 40% is split between getting
// those numbers right. Year is intentionally NOT penalized — TMDB disambiguates
// by year downstream and a one-off year miss shouldn't tank an otherwise-correct
// extraction. Cases with no pinned structure score on title alone (unchanged).
func caseAccuracy(res *RenameMetadata, expect string) float64 {
	if res == nil {
		return 0
	}
	ef := parseExpect(expect)
	titleScore := titleAccuracy(res.Title, ef.Title)
	var checks []float64
	if ef.Season > 0 {
		checks = append(checks, boolScore(res.Season == ef.Season))
	}
	if ef.Episode > 0 {
		checks = append(checks, boolScore(res.Episode == ef.Episode))
	}
	if len(checks) == 0 {
		return titleScore
	}
	var sum float64
	for _, v := range checks {
		sum += v
	}
	return 0.6*titleScore + 0.4*(sum/float64(len(checks)))
}

func boolScore(ok bool) float64 {
	if ok {
		return 1
	}
	return 0
}

func tokenSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(s) {
		set[w] = true
	}
	return set
}

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

func (c *Client) RunSlotsProgress(ctx context.Context, slots []Slot, cases []BenchmarkCase, onResult func(SlotScore)) []SlotScore {
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

// DiscoverOllamaModels queries the local Ollama (/api/tags) for models it serves and
// returns a Slot per model that isn't already in the chain — so the benchmark can test
// EVERY available model, not just the one wired into the chain. This includes Ollama
// CLOUD models (the "-cloud" suffix): recent Ollama registers them locally and lists
// them in /api/tags, so they're discovered here too (ollamaCanComplete keeps them —
// /api/show for a remote model doesn't report capabilities, and on doubt we keep). They
// resolve as Free (the ollama provider is free-tier) but non-Local (see localModel), so
// the benchmark parallelizes them instead of serializing on the single GPU.
func (c *Client) DiscoverOllamaModels(ctx context.Context) []Slot {
	// Find the ollama provider's base URL from the chain.
	var base, key string
	for _, s := range c.slots {
		if s.Provider == "ollama" {
			base, key = s.BaseURL, s.apiKey
			break
		}
	}
	if base == "" {
		return nil
	}
	existing := map[string]bool{}
	for _, s := range c.slots {
		existing[s.Provider+"|"+s.Model] = true
	}
	// /api/tags lives at the server root, not under the OpenAI-compat /v1.
	tagsURL := strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + "/api/tags"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, tagsURL, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&tags) != nil {
		return nil
	}
	root := strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")
	var out []Slot
	for _, m := range tags.Models {
		if m.Name == "" || existing["ollama|"+m.Name] {
			continue
		}
		// Skip models that can't do text completion (e.g. embedding models like
		// nomic-embed-text) — they can never extract a title and would just clutter
		// the benchmark with permanent 0% failures. Decided by Ollama's OWN
		// capability metadata, not a name heuristic. On any doubt (old Ollama, error)
		// we keep the model rather than over-filter.
		if !ollamaCanComplete(ctx, c.http, root, m.Name) {
			continue
		}
		existing["ollama|"+m.Name] = true
		out = append(out, Slot{ID: "ollama:" + m.Name, Provider: "ollama", Model: m.Name, BaseURL: base, apiKey: key, Free: true, Local: localModel("ollama", m.Name)})
	}
	return out
}

// ollamaCanComplete asks Ollama (/api/show) whether a model supports text
// "completion". Returns true on any uncertainty (call/parse error, or capabilities
// not reported by an older Ollama) so we never drop a usable model — it returns
// false ONLY when Ollama EXPLICITLY lists capabilities without "completion" (e.g.
// an embedding-only model like nomic-embed-text → ["embedding"]).
func ollamaCanComplete(ctx context.Context, hc *http.Client, root, model string) bool {
	body, _ := json.Marshal(map[string]string{"model": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, root+"/api/show", bytes.NewReader(body))
	if err != nil {
		return true
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return true
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return true
	}
	var show struct {
		Capabilities []string `json:"capabilities"`
	}
	if json.NewDecoder(resp.Body).Decode(&show) != nil || len(show.Capabilities) == 0 {
		return true // old Ollama / no metadata → don't filter
	}
	for _, capability := range show.Capabilities {
		if capability == "completion" {
			return true
		}
	}
	return false
}

// freeTierProviders bill nothing per token — local Ollama, and Groq's
// rate-limited free tier. The benchmark may discover their WHOLE catalog. Every
// other provider is treated as metered/paid (OpenRouter, OpenCode Zen, and any
// unknown provider — paid by default): discovery there is limited to models we can
// positively tell are free, so benchmarking costly frontier models can't quietly
// burn credits/quota. See discoverViaModelsAPI.
var freeTierProviders = map[string]bool{"groq": true, "ollama": true}

// DiscoverModels lists candidate models across ALL providers so the benchmark
// can test every available model, not just one per provider from the chain:
//   - ollama:      every locally-installed model (/api/tags)
//   - free tier:   the whole /v1/models catalog (Groq) — nothing is billed
//   - metered:     ONLY models we can prove are free (a :free/-free id, or an
//     explicit 0 price). Paid frontier models on OpenRouter / OpenCode Zen are
//     ignored so the benchmark can't burn credits. (Zen returns no pricing at
//     all, so the suffix is the only signal there.)
//
// Skips models already in the chain. Caps at 100 per provider so the benchmark
// doesn't take hours on OpenRouter's 300+ model catalog.
func (c *Client) DiscoverModels(ctx context.Context) []Slot {
	existing := map[string]bool{}
	for _, s := range c.slots {
		existing[s.Provider+"|"+s.Model] = true
	}
	var out []Slot
	out = append(out, c.DiscoverOllamaModels(ctx)...)
	for _, s := range out {
		existing[s.Provider+"|"+s.Model] = true
	}
	for name, p := range c.providers {
		if name == "ollama" || p.BaseURL == "" {
			continue // ollama handled above; skip provider-less
		}
		out = append(out, c.discoverViaModelsAPI(ctx, name, p, 100, existing)...)
	}
	return out
}

// DiscoverModelsForProvider lists candidate models for a specific provider.
func (c *Client) DiscoverModelsForProvider(ctx context.Context, provider string) []Slot {
	existing := map[string]bool{}
	for _, s := range c.slots {
		existing[s.Provider+"|"+s.Model] = true
	}
	var out []Slot
	if provider == "ollama" {
		out = append(out, c.DiscoverOllamaModels(ctx)...)
	} else {
		p, ok := c.providers[provider]
		if ok && p.BaseURL != "" {
			out = append(out, c.discoverViaModelsAPI(ctx, provider, p, 100, existing)...)
		}
	}
	return out
}

// modelCostPer1M turns OpenAI-style per-TOKEN pricing (strings, USD) into a
// blended (prompt+completion)/2 price per 1M tokens. known=false when neither
// field is numeric — e.g. OpenCode Zen, which returns no pricing at all.
func modelCostPer1M(promptStr, completionStr string) (cost float64, known bool) {
	p, perr := strconv.ParseFloat(strings.TrimSpace(promptStr), 64)
	cmp, cerr := strconv.ParseFloat(strings.TrimSpace(completionStr), 64)
	if perr != nil && cerr != nil {
		return 0, false
	}
	return (p + cmp) / 2 * 1_000_000, true
}

// discoverViaModelsAPI hits the OpenAI-compatible GET /models on a provider.
func (c *Client) discoverViaModelsAPI(ctx context.Context, name string, p config.AIProvider, capN int, existing map[string]bool) []Slot {
	ceiling := c.CostConfig().MaxCostPer1M
	base := strings.TrimRight(p.BaseURL, "/")
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var body struct {
		Data []struct {
			ID      string `json:"id"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}
	var out []Slot
	for _, m := range body.Data {
		if m.ID == "" || existing[name+"|"+m.ID] || len(out) >= capN {
			continue
		}
		// Cost in USD per 1M tokens. Free-tier providers and :free/-free ids are 0;
		// a numeric price is blended; absent pricing (OpenCode Zen) is UNKNOWN.
		var cost float64
		known := true
		if c.isFreeModel(name, m.ID) {
			cost = 0
		} else {
			cost, known = modelCostPer1M(m.Pricing.Prompt, m.Pricing.Completion)
		}
		// Only discover models we can PAY for: known cost within the ceiling (0 =
		// free only, the default). Unknown-cost models (no pricing) are never
		// auto-tested — that's what keeps Zen's costly frontier models out.
		if !known || cost > ceiling {
			continue
		}
		existing[name+"|"+m.ID] = true
		out = append(out, Slot{ID: name + ":" + m.ID, Provider: name, Model: m.ID, BaseURL: base, apiKey: p.APIKey, Free: cost == 0, CostPer1M: cost})
	}
	return out
}

// listModels returns the model ids currently advertised by a provider (Ollama:
// /api/tags; others: OpenAI-compatible /models). Used by self-heal to verify a
// failing model really is gone and to pick a replacement. Empty on any error.
func (c *Client) listModels(ctx context.Context, provider string, p config.AIProvider) []string {
	base := strings.TrimRight(p.BaseURL, "/")
	if provider == "ollama" {
		return c.listOllamaModels(ctx, base)
	}
	return c.listOpenAIModels(ctx, base, p.APIKey)
}

func (c *Client) listOllamaModels(ctx context.Context, base string) []string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/v1")+"/api/tags", nil)
	resp, err := c.http.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&tags) != nil {
		return nil
	}
	var out []string
	for _, m := range tags.Models {
		if m.Name != "" {
			out = append(out, m.Name)
		}
	}
	return out
}

func (c *Client) listOpenAIModels(ctx context.Context, base, apiKey string) []string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}
	var out []string
	for _, m := range body.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out
}

// healProvider self-heals a provider whose chain model returned "doesn't exist":
// it re-lists the provider's models, drops any chain slot whose model is gone,
// and — if that left the provider unrepresented — adds a still-valid model
// (OpenRouter prefers a free one) so the chain keeps a working slot. Cheap
// (one /models or /api/tags call, no scoring), deduped per provider, runtime-only
// (a manual benchmark re-optimizes + persists).
func (c *Client) healProvider(provider string) {
	if _, busy := c.healing.LoadOrStore(provider, true); busy {
		return
	}
	defer c.healing.Delete(provider)
	p, ok := c.providers[provider]
	if !ok || p.BaseURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ids := c.listModels(ctx, provider, p)
	if len(ids) == 0 {
		return
	}
	avail := toSet(ids)

	c.mu.Lock()
	defer c.mu.Unlock()

	kept, dropped, providerHas := filterSlotsByAvail(c.slots, provider, avail)
	if !dropped {
		return
	}
	if !providerHas {
		kept = append(kept, c.pickReplacementSlot(provider, ids, p))
	}
	c.slots = kept
}

func toSet(ids []string) map[string]bool {
	avail := map[string]bool{}
	for _, id := range ids {
		avail[id] = true
	}
	return avail
}

func filterSlotsByAvail(slots []Slot, provider string, avail map[string]bool) ([]Slot, bool, bool) {
	var kept []Slot
	dropped := false
	providerHas := false
	for _, s := range slots {
		if s.Provider == provider && !avail[s.Model] {
			dropped = true
			log.Printf("ai: self-heal — %s model %q no longer exists; removing from chain", provider, s.Model)
			continue
		}
		kept = append(kept, s)
		if s.Provider == provider {
			providerHas = true
		}
	}
	return kept, dropped, providerHas
}

func (c *Client) pickReplacementSlot(provider string, ids []string, p config.AIProvider) Slot {
	repl := ids[0]
	if provider == "openrouter" {
		for _, id := range ids {
			if strings.HasSuffix(id, ":free") {
				repl = id
				break
			}
		}
	}
	base := strings.TrimRight(p.BaseURL, "/")
	cost := -1.0
	if c.isFreeModel(provider, repl) {
		cost = 0
	}
	log.Printf("ai: self-heal — added %s replacement %q (untested; run the benchmark to re-optimize)", provider, repl)
	return Slot{ID: provider + ":" + repl, Provider: provider, Model: repl, BaseURL: base, apiKey: p.APIKey, Free: cost == 0, Local: localModel(provider, repl), CostPer1M: cost}
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
