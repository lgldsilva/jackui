package ai

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Scoring/acurácia do benchmark (compositeScore/titleAccuracy/RankBefore…) — extraído de benchmark.go.
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

// compositeScore ranks a model by VALUE: quality² ÷ (latency^p × cost factor). Title
// identification is a BACKGROUND job (it runs once per item, off the request path), so
// accuracy should DOMINATE — a wrong title mis-files media, while a slower call is
// invisible. Two levers make accuracy dominate latency:
//   - accuracy is SQUARED, so a small accuracy gap outweighs a large speed gap. Measured
//     example that motivated this: an 88%@846ms model was out-ranking a 99%@1303ms one on
//     speed alone; squaring flips it (0.88²/∛0.846=0.82 < 0.99²/∛1.30=0.90), so the more
//     accurate model wins — which is what you want for correctness-critical extraction.
//   - the latency penalty is the CUBE ROOT (p = 1/3), gentler than sqrt: a 10× slower
//     model is penalized ~2.15× not ~3.16×. A 0.3s floor stops a sub-300ms call inflating.
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
	return accuracy * accuracy / math.Cbrt(seconds) / (1 + cost)
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
//
// The tier is COMPLETENESS-graded, not binary: a run that covered most cases
// (Completeness ≥ rankCompletenessFloor) is trustworthy and ranks on composite, so a
// free model that a rate limit cut at 80% but scored 99% isn't buried under mediocre
// full runs. Only a SPARSELY-measured run (few cases → its accuracy is noise) is
// demoted. Legacy rows have Completeness 0, so `!Incomplete` still gates them as before.
const rankCompletenessFloor = 0.7

func trustworthy(s SlotScore) bool {
	return !s.Incomplete || s.Completeness >= rankCompletenessFloor
}

func RankBefore(a, b SlotScore) bool {
	if ta, tb := trustworthy(a), trustworthy(b); ta != tb {
		return ta
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
