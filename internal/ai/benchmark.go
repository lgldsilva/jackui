package ai

import (
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

	"github.com/luizg/jackui/internal/config"
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
//   - Plain title:     "Inception"               (sem estrutura → só o título conta)
type BenchmarkCase struct {
	Raw    string `json:"raw"`
	Expect string `json:"expect"`
}

// SlotScore is one model's aggregate result over the whole case set.
type SlotScore struct {
	SlotID        string  `json:"slotId"`
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	Accuracy      float64 `json:"accuracy"`     // 0..1 mean over cases
	AvgLatencyMs  int64   `json:"avgLatencyMs"` // MEDIAN wall-clock per call (resilient to model-load residual)
	Composite     float64 `json:"composite"`    // accuracy / sqrt(latencySeconds)
	Samples       int     `json:"samples"`      // cases that produced a usable reply
	Free          bool    `json:"free"`          // true when model is free (no billing cost)
	FailureReason string  `json:"failureReason,omitempty"`
}

// DefaultBenchmarkCases seeds a fresh store. Picked to exercise the hard parts:
// dotted names, scene tags, season/episode packs, non-English, and bracketed
// release-group noise. Expects use the canonical label (see BenchmarkCase) so
// the benchmark measures the full rename structure — título/ano for movies,
// série + temporada/episódio for TV — not just the title.
var DefaultBenchmarkCases = []BenchmarkCase{
	{Raw: "Inception.2010.1080p.BluRay.x264-SPARKS", Expect: "Inception - 2010"},
	{Raw: "The.Matrix.1999.2160p.UHD.BluRay.x265-TERMINAL", Expect: "The Matrix - 1999"},
	{Raw: "Breaking.Bad.S03E07.720p.HDTV.x264-CTU", Expect: "Breaking Bad - S03E07"},
	{Raw: "Game.of.Thrones.S01E09.Baelor.1080p.BluRay.x264-DEMAND", Expect: "Game of Thrones - S01E09"},
	{Raw: "Dune.Part.Two.2024.1080p.WEB-DL.DDP5.1.Atmos.H.264-FLUX", Expect: "Dune Part Two - 2024"},
	{Raw: "[Erai-raws] Frieren - 01 [1080p][Multiple Subtitle]", Expect: "Frieren - E01"},
	{Raw: "O.Auto.da.Compadecida.2000.DUBLADO.1080p", Expect: "O Auto da Compadecida - 2000"},
}

// compositeScore mirrors SelfAgent's ranking: quality divided by the square root
// of latency in seconds. The sqrt softens the latency penalty so a slightly
// slower but more accurate model can still win. A 0.3s floor stops a sub-300ms
// call from inflating the score to nonsense.
//
// Free models get a 1.3x multiplier on their composite score so they rank
// above paid models with similar accuracy/latency — the whole point is that
// free models are preferred when they work well enough.
func compositeScore(accuracy float64, avgLatencyMs int64, free bool) float64 {
	seconds := math.Max(0.3, float64(avgLatencyMs)/1000.0)
	score := accuracy / math.Sqrt(seconds)
	if free {
		score *= 1.3
	}
	return score
}

var alnumRe = regexp.MustCompile(`[^a-z0-9]+`)

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

func normalizeTitle(s string) string {
	return strings.Trim(alnumRe.ReplaceAllString(strings.ToLower(s), " "), " ")
}

// Canonical-label parsers. The structure of an expected label lives inside the
// Expect string (see BenchmarkCase); these pull it back out for scoring. Order
// matters: try the most specific (S..E..) first.
var (
	expectTVRe   = regexp.MustCompile(`(?i)^(.*\S)\s+-\s+S(\d{1,2})E(\d{1,3})\s*$`)
	expectEpRe   = regexp.MustCompile(`(?i)^(.*\S)\s+-\s+E(\d{1,3})\s*$`)
	expectYearRe = regexp.MustCompile(`^(.*\S)\s+-\s+(\d{4})\s*$`)
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
// title (no " - S..E.." / " - E.." / " - YYYY" tail) yields just the title, so
// title-only cases keep working exactly as before.
func parseExpect(expect string) expectFields {
	expect = strings.TrimSpace(expect)
	if m := expectTVRe.FindStringSubmatch(expect); m != nil {
		return expectFields{Title: strings.TrimSpace(m[1]), Season: atoiSafe(m[2]), Episode: atoiSafe(m[3])}
	}
	if m := expectEpRe.FindStringSubmatch(expect); m != nil {
		return expectFields{Title: strings.TrimSpace(m[1]), Episode: atoiSafe(m[2])}
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
	if len(cases) == 0 {
		cases = DefaultBenchmarkCases
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
		}(i, s)
	}
	// Local models: a single goroutine drains them sequentially (with warmup).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i, s := range slots {
			if s.Local {
				results[i] = c.scoreSlot(ctx, s, cases, true)
			}
		}
	}()
	wg.Wait()

	sort.SliceStable(results, func(i, j int) bool { return results[i].Composite > results[j].Composite })
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
	score := SlotScore{SlotID: s.ID, Provider: s.Provider, Model: s.Model, Free: s.Free}
	if warmup {
		warmCtx, warmCancel := context.WithTimeout(ctx, warmupTimeout)
		_, _, _ = c.metadataWithSlot(warmCtx, s, "warmup")
		warmCancel()
	}
	var accSum float64
	lats := make([]time.Duration, 0, len(cases))
	paymentFail := false
	for _, tc := range cases {
		if c.scoreSingleCase(ctx, s, tc, &score, &accSum, &lats) {
			paymentFail = true
			break
		}
	}
	if score.Samples > 0 {
		score.Accuracy = accSum / float64(score.Samples)
		score.AvgLatencyMs = medianDuration(lats).Milliseconds()
		score.Composite = compositeScore(score.Accuracy, score.AvgLatencyMs, score.Free)
	} else if paymentFail {
		score.Composite = -1
	}
	return score
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

func (c *Client) scoreSingleCase(ctx context.Context, s Slot, tc BenchmarkCase, score *SlotScore, accSum *float64, lats *[]time.Duration) bool {
	res, latency, err := c.metadataWithSlot(ctx, s, tc.Raw)
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
		return false
	}
	score.Samples++
	*lats = append(*lats, latency)
	*accSum += caseAccuracy(res, tc.Expect)
	return false
}

// DiscoverOllamaModels queries the local Ollama (/api/tags) for installed models
// and returns a Slot per model that isn't already in the chain — so the benchmark
// can test EVERY local model, not just the one wired into the chain. Cloud models
// aren't listed by /api/tags (they're remote), so they stay explicit in config.
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
	var out []Slot
	for _, m := range tags.Models {
		if m.Name == "" || existing["ollama|"+m.Name] {
			continue
		}
		existing["ollama|"+m.Name] = true
		out = append(out, Slot{ID: "ollama:" + m.Name, Provider: "ollama", Model: m.Name, BaseURL: base, apiKey: key, Local: localModel("ollama", m.Name)})
	}
	return out
}

// DiscoverModels lists candidate models across ALL providers so the benchmark
// can test every available model, not just one per provider from the chain:
//   - ollama:  every locally-installed model (/api/tags)
//   - others:  ALL models from /v1/models (up to 100 per provider), with free
//     status tracked via pricing info or naming convention (:free suffix).
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

// discoverViaModelsAPI hits the OpenAI-compatible GET /models on a provider.
func (c *Client) discoverViaModelsAPI(ctx context.Context, name string, p config.AIProvider, capN int, existing map[string]bool) []Slot {
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
		isFree := strings.HasSuffix(m.ID, ":free") ||
			((m.Pricing.Prompt == "" || m.Pricing.Prompt == "0") && (m.Pricing.Completion == "" || m.Pricing.Completion == "0"))
		existing[name+"|"+m.ID] = true
		out = append(out, Slot{ID: name + ":" + m.ID, Provider: name, Model: m.ID, BaseURL: base, apiKey: p.APIKey, Free: isFree})
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
		kept = append(kept, pickReplacementSlot(provider, ids, p))
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

func pickReplacementSlot(provider string, ids []string, p config.AIProvider) Slot {
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
	log.Printf("ai: self-heal — added %s replacement %q (untested; run the benchmark to re-optimize)", provider, repl)
	return Slot{ID: provider + ":" + repl, Provider: provider, Model: repl, BaseURL: base, apiKey: p.APIKey, Local: localModel(provider, repl)}
}

// AdoptBenchmark rebuilds the live chain from benchmark scores: every model that
// produced a usable reply (Samples>0), ordered best-first by composite. This is
// what the user wants — "use the best benchmark" — while keeping the free local
// models in the chain as low-ranked fallbacks (the breaker skips a rate-limited
// vendor at runtime, falling through to the next, ultimately the free local).
func (c *Client) AdoptBenchmark(scores []SlotScore) {
	ranked := make([]SlotScore, 0, len(scores))
	for _, s := range scores {
		if s.Samples > 0 {
			ranked = append(ranked, s)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Composite > ranked[j].Composite })
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
