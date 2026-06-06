package ai

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luizg/jackui/internal/config"
)

func TestMedianDuration(t *testing.T) {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	cases := []struct {
		name string
		in   []time.Duration
		want time.Duration
	}{
		{"empty", nil, 0},
		{"single", []time.Duration{ms(700)}, ms(700)},
		{"odd", []time.Duration{ms(900), ms(100), ms(500)}, ms(500)},
		{"even", []time.Duration{ms(400), ms(200), ms(800), ms(600)}, ms(500)},
		// The whole point: one huge model-load outlier must NOT move the median,
		// the way it would drag the mean. Mean here ≈ 2120ms; median stays 600ms.
		{"load_outlier", []time.Duration{ms(8000), ms(500), ms(600), ms(700), ms(800)}, ms(700)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := medianDuration(tc.in); got != tc.want {
				t.Fatalf("medianDuration(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseExpect(t *testing.T) {
	cases := []struct {
		expect string
		want   expectFields
	}{
		{"Breaking Bad - S03E07", expectFields{Title: "Breaking Bad", Season: 3, Episode: 7}},
		{"Game of Thrones - S01E09", expectFields{Title: "Game of Thrones", Season: 1, Episode: 9}},
		{"Frieren - E01", expectFields{Title: "Frieren", Episode: 1}},
		{"Inception - 2010", expectFields{Title: "Inception", Year: 2010}},
		{"Dune Part Two - 2024", expectFields{Title: "Dune Part Two", Year: 2024}},
		{"Inception", expectFields{Title: "Inception"}},
		// "Blade Runner 2049" has a 4-digit number but NO " - " tail, so it stays a
		// plain title and the year regex must not swallow the "2049".
		{"Blade Runner 2049", expectFields{Title: "Blade Runner 2049"}},
	}
	for _, tc := range cases {
		t.Run(tc.expect, func(t *testing.T) {
			if got := parseExpect(tc.expect); got != tc.want {
				t.Fatalf("parseExpect(%q) = %+v, want %+v", tc.expect, got, tc.want)
			}
		})
	}
}

func TestCaseAccuracy(t *testing.T) {
	tv := func(title string, s, e int) *RenameMetadata {
		return &RenameMetadata{Title: title, Kind: "tv", Season: s, Episode: e}
	}
	// Right title + right season/episode = perfect.
	if a := caseAccuracy(tv("Breaking Bad", 3, 7), "Breaking Bad - S03E07"); a != 1 {
		t.Fatalf("perfect TV extraction should be 1.0, got %v", a)
	}
	// Right title but WRONG episode must score below a title-only match — that's
	// the whole point of measuring série/temporada/episódio.
	wrongEp := caseAccuracy(tv("Breaking Bad", 3, 9), "Breaking Bad - S03E07")
	if !(wrongEp > 0.5 && wrongEp < 1) {
		t.Fatalf("right title + wrong episode should be in (0.5,1), got %v", wrongEp)
	}
	titleOnly := caseAccuracy(tv("Breaking Bad", 0, 0), "Breaking Bad")
	if wrongEp >= titleOnly {
		t.Fatalf("wrong episode (%v) should score below title-only (%v)", wrongEp, titleOnly)
	}
	// Year is not penalized: a movie with the right title scores 1.0 regardless.
	if a := caseAccuracy(&RenameMetadata{Title: "Inception", Kind: "movie", Year: 1999}, "Inception - 2010"); a != 1 {
		t.Fatalf("year mismatch should not penalize a movie, got %v", a)
	}
	if a := caseAccuracy(nil, "Inception - 2010"); a != 0 {
		t.Fatalf("nil result should be 0, got %v", a)
	}
}

func TestTitleAccuracy(t *testing.T) {
	if a := titleAccuracy("The Matrix", "the.matrix"); a != 1 {
		t.Fatalf("normalized exact match should be 1, got %v", a)
	}
	if a := titleAccuracy("Dune", "Dune Part Two"); !(a > 0 && a < 1) {
		t.Fatalf("partial overlap should be in (0,1), got %v", a)
	}
	if a := titleAccuracy("Totally Wrong", "Inception"); a != 0 {
		t.Fatalf("no overlap should be 0, got %v", a)
	}
}

func TestCompositeScoreFavorsFastAccurate(t *testing.T) {
	fastAccurate := compositeScore(0.9, 400, false)
	slowAccurate := compositeScore(0.9, 4000, false)
	fastSloppy := compositeScore(0.4, 400, false)
	if fastAccurate <= slowAccurate {
		t.Fatalf("faster should score higher at equal accuracy: %v vs %v", fastAccurate, slowAccurate)
	}
	if fastAccurate <= fastSloppy {
		t.Fatalf("more accurate should score higher at equal latency: %v vs %v", fastAccurate, fastSloppy)
	}
}

// ── Property-based tests ─────────────────────────────────────────────────────

func TestPropFreeBonusAlwaysHigher(t *testing.T) {
	for acc := 0.0; acc <= 1.0; acc += 0.1 {
		for lat := int64(100); lat <= 10000; lat += 500 {
			paid := compositeScore(acc, lat, false)
			free := compositeScore(acc, lat, true)
			if free < paid {
				t.Fatalf("free=%.4f < paid=%.4f at acc=%.1f lat=%d", free, paid, acc, lat)
			}
		}
	}
}

func TestPropScoreIncreasesWithAccuracy(t *testing.T) {
	for lat := int64(200); lat <= 5000; lat += 500 {
		prev := compositeScore(0.0, lat, false)
		for acc := 0.1; acc <= 1.0; acc += 0.1 {
			cur := compositeScore(acc, lat, false)
			if cur < prev {
				t.Fatalf("score decresceu acc=%.1f lat=%d: %.4f < %.4f", acc, lat, cur, prev)
			}
			prev = cur
		}
	}
}

func TestPropScoreDecreasesWithLatency(t *testing.T) {
	for acc := 0.1; acc <= 1.0; acc += 0.2 {
		prev := compositeScore(acc, 100, false)
		for lat := int64(200); lat <= 10000; lat += 500 {
			cur := compositeScore(acc, lat, false)
			if cur > prev {
				t.Fatalf("score subiu com latencia maior acc=%.1f lat=%d: %.4f > %.4f", acc, lat, cur, prev)
			}
			prev = cur
		}
	}
}

func TestPropScoreAlwaysFinite(t *testing.T) {
	for acc := 0.0; acc <= 1.0; acc += 0.1 {
		for lat := int64(0); lat <= 30000; lat += 1000 {
			s := compositeScore(acc, lat, false)
			if s < 0 || math.IsInf(s, 0) || math.IsNaN(s) {
				t.Fatalf("score invalido acc=%.1f lat=%d: %v", acc, lat, s)
			}
		}
	}
}

func TestPropTitleAccuracy(t *testing.T) {
	testPropTitleAccuracyRange(t)
	testPropTitleAccuracyExact(t)
	testPropTitleAccuracyNoOverlap(t)
	testPropTitleAccuracySymmetry(t)
}

func testPropTitleAccuracyRange(t *testing.T) {
	t.Helper()
	t.Run("resultado sempre em [0,1]", func(t *testing.T) {
		cases := []struct{ a, b string }{
			{"The Matrix", "The Matrix"},
			{"", "The Matrix"},
			{"The Matrix", ""},
			{"", ""},
			{"Dune.Part.Two.2024.1080p", "Dune Part Two"},
			{"a b c d e f", "a b c"},
			{"Inception", "Transformers"},
			{"Star Wars: The Empire Strikes Back", "Star Wars"},
			{"Star.Wars.Episode.V.1980", "Star Wars Episode V"},
			{"O.Auto.da.Compadecida.2000.DUBLADO", "O Auto da Compadecida"},
			{"Hello World! @#$%", "hello world"},
		}
		for _, tc := range cases {
			a := titleAccuracy(tc.a, tc.b)
			if a < 0 || a > 1 {
				t.Errorf("titleAccuracy(%q, %q) = %v, fora de [0,1]", tc.a, tc.b, a)
			}
		}
	})
}

func testPropTitleAccuracyExact(t *testing.T) {
	t.Helper()
	t.Run("exato apos normalizacao = 1", func(t *testing.T) {
		pairs := [][2]string{
			{"The.Matrix.1999", "the matrix 1999"},
			{"Dune.Part.Two.2024", "Dune Part Two 2024"},
			{"Breaking.Bad.S03E07", "Breaking Bad S03E07"},
			{"O Auto da Compadecida 2000", "O.Auto.da.Compadecida.2000"},
		}
		for _, p := range pairs {
			a := titleAccuracy(p[0], p[1])
			if a != 1.0 {
				t.Errorf("titleAccuracy(%q, %q) = %v, esperado 1.0", p[0], p[1], a)
			}
		}
	})
}

func testPropTitleAccuracyNoOverlap(t *testing.T) {
	t.Helper()
	t.Run("sem overlap = 0", func(t *testing.T) {
		if a := titleAccuracy("Matrix", "Inception"); a != 0 {
			t.Errorf("sem overlap deveria ser 0, got %v", a)
		}
		if a := titleAccuracy("The", "X Y Z"); a != 0 {
			t.Errorf("sem overlap deveria ser 0, got %v", a)
		}
	})
}

func testPropTitleAccuracySymmetry(t *testing.T) {
	t.Helper()
	t.Run("simetria aproximada", func(t *testing.T) {
		cases := [][2]string{
			{"The Matrix", "Matrix"},
			{"Dune Part Two", "Dune"},
			{"Star Wars A New Hope", "Star Wars"},
		}
		for _, tc := range cases {
			ab := titleAccuracy(tc[0], tc[1])
			ba := titleAccuracy(tc[1], tc[0])
			if ab != ba {
				t.Errorf("titleAccuracy nao simetrica: %q vs %q: %.4f != %.4f", tc[0], tc[1], ab, ba)
			}
		}
	})
}
func TestScoreSlotPaymentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"error":"insufficient balance"}`))
	}))
	defer srv.Close()

	cfg := config.AIConfig{Enabled: true, Providers: map[string]config.AIProvider{
		"p": {BaseURL: srv.URL, APIKey: "k"},
	}, Chain: []config.AIChainSlot{{ID: "p:m", Provider: "p", Model: "m"}}}
	c := New(cfg)
	if c == nil {
		t.Fatal("New nil")
	}
	scores := c.Run(context.Background(), []BenchmarkCase{{Raw: "Test", Expect: "Test"}})
	if len(scores) != 1 {
		t.Fatalf("expected 1 score, got %d", len(scores))
	}
	if scores[0].FailureReason != "pago — sem saldo" {
		t.Fatalf("expected 'pago — sem saldo', got %q", scores[0].FailureReason)
	}
	if scores[0].Composite != -1 {
		t.Fatalf("expected composite -1 for paid model, got %v", scores[0].Composite)
	}
	if scores[0].Free {
		t.Fatal("expected Free=false for paid model")
	}
}

func TestAdoptBenchmark(t *testing.T) {
	srv := httptest.NewServer(jsonChat(`{"title":"T","year":0,"kind":"movie"}`, http.StatusOK))
	defer srv.Close()
	c := clientForURL(t, srv.URL)

	scores := []SlotScore{
		{SlotID: "p0", Provider: "p0", Model: "m", Accuracy: 0.5, AvgLatencyMs: 1000, Composite: 1.5, Samples: 3},
		{SlotID: "p1", Provider: "p1", Model: "nope", Samples: 0}, // no samples → skipped
	}
	c.AdoptBenchmark(scores)
	if len(c.Slots()) != 1 || c.Slots()[0].ID != "p0" {
		t.Fatalf("expected 1 adopted slot, got %+v", c.Slots())
	}
}

// TestLocalModelClassification pins the rule that drives serialization: a bare
// local Ollama model is Local; the "-cloud" variant and any other provider aren't.
func TestLocalModelClassification(t *testing.T) {
	cases := []struct {
		provider, model string
		want            bool
	}{
		{"ollama", "llama3.1:8b", true},
		{"ollama", "gpt-oss:120b-cloud", false},
		{"groq", "llama-3.1-70b", false},
		{"openrouter", "some/model:free", false},
	}
	for _, tc := range cases {
		if got := localModel(tc.provider, tc.model); got != tc.want {
			t.Fatalf("localModel(%q,%q)=%v, want %v", tc.provider, tc.model, got, tc.want)
		}
	}
}

// concTracker is an httptest handler that records the peak number of requests
// in flight at once. Each request holds for a beat so genuinely-parallel calls
// overlap and bump the peak; sequential calls never exceed 1.
func concTracker(body string) (http.HandlerFunc, *int32) {
	var inFlight, peak int32
	h := func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if cur <= p || atomic.CompareAndSwapInt32(&peak, p, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + body + `}}]}`))
	}
	return h, &peak
}

// TestRunSlotsLocalSerialized locks in the core fix: several LOCAL Ollama models
// (sharing one GPU) are benchmarked strictly one-at-a-time — concurrent calls
// would exceed Ollama's connection slots and thrash VRAM.
func TestRunSlotsLocalSerialized(t *testing.T) {
	h, peak := concTracker(`"{\"title\":\"T\",\"year\":0,\"kind\":\"movie\"}"`)
	srv := httptest.NewServer(h)
	defer srv.Close()

	c := &Client{
		http:      &http.Client{},
		providers: map[string]config.AIProvider{"ollama": {BaseURL: srv.URL}},
	}

	var slots []Slot
	for i := 0; i < 4; i++ {
		slots = append(slots, Slot{ID: "ollama:m", Provider: "ollama", Model: "local-model", BaseURL: srv.URL, Local: true})
	}

	scores := c.RunSlots(context.Background(), slots, []BenchmarkCase{{Raw: "X", Expect: "X"}})
	if len(scores) != 4 {
		t.Fatalf("expected 4 scores, got %d", len(scores))
	}
	if p := atomic.LoadInt32(peak); p != 1 {
		t.Fatalf("local Ollama must be serialized (peak 1), got peak %d", p)
	}
}

// TestRunSlotsCloudAndExternalParallel proves the flip side: Ollama *cloud* models
// ("-cloud") and external vendors are NOT local, so they fan out and overlap.
func TestRunSlotsCloudAndExternalParallel(t *testing.T) {
	h, peak := concTracker(`"{\"title\":\"T\",\"year\":0,\"kind\":\"movie\"}"`)
	srv := httptest.NewServer(h)
	defer srv.Close()

	c := &Client{
		http: &http.Client{},
		providers: map[string]config.AIProvider{
			"ollama": {BaseURL: srv.URL},
			"groq":   {BaseURL: srv.URL},
		},
	}
	slots := []Slot{
		{ID: "ollama:c1", Provider: "ollama", Model: "gpt-oss:120b-cloud", BaseURL: srv.URL, Local: localModel("ollama", "gpt-oss:120b-cloud")},
		{ID: "ollama:c2", Provider: "ollama", Model: "qwen3:480b-cloud", BaseURL: srv.URL, Local: localModel("ollama", "qwen3:480b-cloud")},
		{ID: "groq:m", Provider: "groq", Model: "llama-3.1-70b", BaseURL: srv.URL, Local: localModel("groq", "llama-3.1-70b")},
	}
	c.RunSlots(context.Background(), slots, []BenchmarkCase{{Raw: "X", Expect: "X"}})

	if p := atomic.LoadInt32(peak); p < 2 {
		t.Fatalf("cloud Ollama + external should parallelize (peak >= 2), got peak %d", p)
	}
}

// TestRunSlotsBadOutputCountsAsZero pins Fix B: a case the model BOTCHED (Groq's
// HTTP 400 json_validate_failed) counts as a 0-accuracy case, so the model can't
// show a clean 100% next to a failure reason. Transient/infra errors (5xx, a
// crashed local llama-server) are NOT this — they stay skipped (covered elsewhere).
func TestRunSlotsBadOutputCountsAsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "BADCASE") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Failed to validate JSON","code":"json_validate_failed"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"Inception\",\"year\":2010,\"kind\":\"movie\"}"}}]}`))
	}))
	defer srv.Close()

	c := &Client{http: &http.Client{}, providers: map[string]config.AIProvider{"groq": {BaseURL: srv.URL}}}
	slot := Slot{ID: "groq:m", Provider: "groq", Model: "m", BaseURL: srv.URL}
	scores := c.RunSlots(context.Background(), []Slot{slot}, []BenchmarkCase{
		{Raw: "Inception.2010 GOODCASE", Expect: "Inception"},
		{Raw: "Whatever BADCASE", Expect: "Whatever"},
	})
	s := scores[0]
	if s.Samples != 1 {
		t.Fatalf("expected 1 usable sample (the good case), got %d", s.Samples)
	}
	// 1.0 (good) + 0 (botched) over 2 scored cases = 0.5.
	if !(s.Accuracy > 0.4 && s.Accuracy < 0.6) {
		t.Fatalf("botched case should drag accuracy to ~0.5, got %v", s.Accuracy)
	}
	if s.FailureReason == "" {
		t.Fatal("expected a failure reason recorded alongside the partial score")
	}
}

// TestRunSlotsRetriesRateLimitForCompleteScore pins Pendência 1: a case that gets
// a transient 429 is RETRIED (not skipped), so the model is scored on the full set
// — no misleading 100% over a partial sample count, no spurious "Falha".
func TestRunSlotsRetriesRateLimitForCompleteScore(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First call 429 (with a tiny Retry-After so the test is fast), then 200.
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"Inception\",\"year\":2010,\"kind\":\"movie\"}"}}]}`))
	}))
	defer srv.Close()

	c := &Client{http: &http.Client{}, providers: map[string]config.AIProvider{"groq": {BaseURL: srv.URL}}}
	slot := Slot{ID: "groq:m", Provider: "groq", Model: "m", BaseURL: srv.URL}
	scores := c.RunSlots(context.Background(), []Slot{slot}, []BenchmarkCase{{Raw: "Inception.2010", Expect: "Inception"}})
	s := scores[0]
	if s.Samples != 1 {
		t.Fatalf("the throttled case should be retried to a complete sample, got samples=%d", s.Samples)
	}
	if s.FailureReason != "" {
		t.Fatalf("a retried-then-OK case must not leave a failure reason, got %q", s.FailureReason)
	}
	if s.Accuracy != 1 {
		t.Fatalf("expected full accuracy after retry, got %v", s.Accuracy)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"2", 2 * time.Second},
		{"1.5", 1500 * time.Millisecond},
		{"0", 0},
		{"Wed, 21 Oct 2026 07:28:00 GMT", 0}, // HTTP-date form not handled → 0
		{"junk", 0},
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.in); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFreeOnly(t *testing.T) {
	in := []Slot{
		{Provider: "groq", Model: "openai/gpt-oss-20b"},          // free-tier provider → keep
		{Provider: "ollama", Model: "qwen2.5:7b"},                // local → keep
		{Provider: "ollama", Model: "gpt-oss:120b-cloud"},        // ollama free-tier → keep
		{Provider: "openrouter", Model: "deepseek/r1:free"},      // :free → keep
		{Provider: "opencode", Model: "deepseek-v4-flash-free"},  // -free → keep
		{Provider: "opencode", Model: "big-pickle"},              // paid Zen → DROP
		{Provider: "openrouter", Model: "anthropic/claude-opus"}, // paid → DROP
	}
	got := map[string]bool{}
	for _, s := range FreeOnly(in) {
		got[s.Model] = true
	}
	for _, m := range []string{"openai/gpt-oss-20b", "qwen2.5:7b", "gpt-oss:120b-cloud", "deepseek/r1:free", "deepseek-v4-flash-free"} {
		if !got[m] {
			t.Errorf("free model %q should be kept", m)
		}
	}
	for _, m := range []string{"big-pickle", "anthropic/claude-opus"} {
		if got[m] {
			t.Errorf("paid model %q must be dropped (burns credits)", m)
		}
	}
}

func TestRunSlotsFreeBonus(t *testing.T) {
	good := httptest.NewServer(jsonChat(`{"title":"Inception","year":2010,"kind":"movie"}`, http.StatusOK))
	defer good.Close()

	c := &Client{
		http:      &http.Client{},
		providers: map[string]config.AIProvider{"p": {BaseURL: good.URL, APIKey: "k"}},
	}

	paid := Slot{ID: "paid", Provider: "p", Model: "paid-model", BaseURL: good.URL, apiKey: "k", Free: false}
	free := Slot{ID: "free", Provider: "p", Model: "free-model", BaseURL: good.URL, apiKey: "k", Free: true}

	scores := c.RunSlots(context.Background(), []Slot{paid, free}, []BenchmarkCase{{Raw: "Inception.2010", Expect: "Inception"}})
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}

	var paidScore, freeScore float64
	for _, s := range scores {
		if s.Free {
			freeScore = s.Composite
		} else {
			paidScore = s.Composite
		}
	}
	if freeScore <= paidScore {
		t.Fatalf("free bonus not applied: free=%.4f <= paid=%.4f", freeScore, paidScore)
	}
}

func TestRunSortsByComposite(t *testing.T) {
	// Both slots are equally accurate; the only difference is the model id echoed
	// back, so accuracy ties and the sort is stable. We assert Run produces a
	// score per slot and they're ordered by composite descending.
	good := httptest.NewServer(jsonChat(`{"title":"Inception","year":2010,"kind":"movie"}`, http.StatusOK))
	defer good.Close()
	bad := httptest.NewServer(jsonChat("", http.StatusInternalServerError))
	defer bad.Close()

	c := clientForURL(t, good.URL, bad.URL) // p0 works, p1 always fails
	scores := c.Run(context.Background(), []BenchmarkCase{{Raw: "Inception.2010.1080p", Expect: "Inception"}})
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	if scores[0].SlotID != "p0" {
		t.Fatalf("working slot should rank first, got %q", scores[0].SlotID)
	}
	if scores[0].Composite <= scores[1].Composite {
		t.Fatalf("scores not sorted desc: %v", scores)
	}
	if scores[1].FailureReason == "" {
		t.Fatal("failing slot should record a failure reason")
	}
}

func TestApplyOrder(t *testing.T) {
	c := clientForURL(t, "http://a", "http://b", "http://c") // p0, p1, p2
	c.ApplyOrder([]string{"p2", "p0"})
	if c.slots[0].ID != "p2" || c.slots[1].ID != "p0" {
		t.Fatalf("order not applied: %s, %s, %s", c.slots[0].ID, c.slots[1].ID, c.slots[2].ID)
	}
	if c.slots[2].ID != "p1" {
		t.Fatalf("unranked slot should fall to the end, got %q", c.slots[2].ID)
	}
}

func TestHealProviderReplacesDeadModel(t *testing.T) {
	// Provider whose /models lists only "goodmodel" — the chain's "deadmodel"
	// must be dropped and replaced.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" || strings.HasSuffix(r.URL.Path, "/models") {
			w.Write([]byte(`{"data":[{"id":"goodmodel"}]}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	cfg := config.AIConfig{Enabled: true, Providers: map[string]config.AIProvider{
		"groq": {BaseURL: srv.URL, APIKey: "k"},
	}, Chain: []config.AIChainSlot{{ID: "groq:deadmodel", Provider: "groq", Model: "deadmodel"}}}
	c := New(cfg)
	if c == nil {
		t.Fatal("New nil")
	}

	c.healProvider("groq")

	slots := c.Slots()
	for _, s := range slots {
		if s.Model == "deadmodel" {
			t.Fatal("dead model should have been removed")
		}
	}
	if len(slots) != 1 || slots[0].Model != "goodmodel" {
		t.Fatalf("expected goodmodel replacement, got %+v", slots)
	}
}

// TestDiscoverViaModelsAPI_Metered covers a METERED provider (not in the free-tier
// allowlist) — like OpenRouter or OpenCode Zen. Only models we can POSITIVELY tell
// are free are discovered: an explicit 0 price, or a :free / -free id. Paid models
// AND models with NO pricing (the OpenCode Zen case — costly frontier models with
// no price field) are ignored, so the benchmark can't burn credits.
func TestDiscoverViaModelsAPI_Metered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
			{"id":"free-model","pricing":{"prompt":"0","completion":"0"}},
			{"id":"paid-model","pricing":{"prompt":"0.01","completion":"0.02"}},
			{"id":"vendor/x:free","pricing":{"prompt":"0","completion":"0"}},
			{"id":"deepseek-v4-flash-free"},
			{"id":"big-pickle"},
			{"id":"claude-opus-4-8"}
		]}`))
	}))
	defer srv.Close()

	cfg := config.AIConfig{Enabled: true, Providers: map[string]config.AIProvider{
		"zen": {BaseURL: srv.URL, APIKey: "k"},
	}, Chain: []config.AIChainSlot{{ID: "existing", Provider: "zen", Model: "existing-model"}}}
	c := New(cfg)
	if c == nil {
		t.Fatal("New nil")
	}

	got := map[string]bool{}
	for _, s := range c.DiscoverModels(context.Background()) {
		got[s.Model] = true
		if !s.Free {
			t.Errorf("discovered model %q on a metered provider must be marked Free", s.Model)
		}
	}
	wantKept := []string{"free-model", "vendor/x:free", "deepseek-v4-flash-free"}
	for _, m := range wantKept {
		if !got[m] {
			t.Errorf("free model %q should have been discovered", m)
		}
	}
	// Paid, and (crucially) the no-pricing frontier models, must be ignored.
	for _, m := range []string{"paid-model", "big-pickle", "claude-opus-4-8"} {
		if got[m] {
			t.Errorf("expensive model %q must NOT be auto-discovered (burns quota)", m)
		}
	}
}

// TestDiscoverViaModelsAPI_FreeTier covers a free-tier provider (Groq): no per-token
// billing, so its WHOLE catalog is discovered even without any price/suffix signal.
func TestDiscoverViaModelsAPI_FreeTier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Groq's /models has no pricing field at all.
		w.Write([]byte(`{"data":[{"id":"openai/gpt-oss-20b"},{"id":"llama-3.3-70b-versatile"}]}`))
	}))
	defer srv.Close()

	cfg := config.AIConfig{Enabled: true, Providers: map[string]config.AIProvider{
		"groq": {BaseURL: srv.URL, APIKey: "k"},
	}, Chain: []config.AIChainSlot{{ID: "existing", Provider: "groq", Model: "existing-model"}}}
	c := New(cfg)
	if c == nil {
		t.Fatal("New nil")
	}

	got := map[string]bool{}
	for _, s := range c.DiscoverModels(context.Background()) {
		got[s.Model] = true
		if !s.Free {
			t.Errorf("free-tier model %q should be marked Free", s.Model)
		}
	}
	for _, m := range []string{"openai/gpt-oss-20b", "llama-3.3-70b-versatile"} {
		if !got[m] {
			t.Errorf("free-tier provider should discover its whole catalog; missing %q", m)
		}
	}
}

func TestDiscoverOllamaModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/tags") {
			w.Write([]byte(`{"models":[{"name":"llama3.2:3b"},{"name":"qwen2.5:7b"},{"name":"mistral:7b"}]}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	cfg := config.AIConfig{Enabled: true, Providers: map[string]config.AIProvider{
		"ollama": {BaseURL: srv.URL + "/v1", APIKey: ""},
	}, Chain: []config.AIChainSlot{{ID: "ollama:existing", Provider: "ollama", Model: "existing-model"}}}
	c := New(cfg)
	if c == nil {
		t.Fatal("New nil")
	}

	slots := c.DiscoverModels(context.Background())
	// Should discover 3 new models (existing-model already in chain)
	if len(slots) < 3 {
		t.Fatalf("expected at least 3 discovered ollama models, got %d", len(slots))
	}
	for _, s := range slots {
		if s.Provider != "ollama" {
			t.Errorf("expected ollama provider, got %q", s.Provider)
		}
	}
}

func TestListModelsOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/tags") {
			w.Write([]byte(`{"models":[{"name":"model-a"},{"name":"model-b"}]}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c := &Client{
		http:      &http.Client{},
		providers: map[string]config.AIProvider{"ollama": {BaseURL: srv.URL + "/v1", APIKey: ""}},
		slots:     []Slot{{Provider: "ollama", BaseURL: srv.URL + "/v1"}},
	}
	slots := c.DiscoverOllamaModels(context.Background())
	if len(slots) < 2 {
		t.Fatalf("expected >= 2 ollama models, got %d", len(slots))
	}
}

func TestHealProviderOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/tags") {
			w.Write([]byte(`{"models":[{"name":"goodmodel"},{"name":"another"}]}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	cfg := config.AIConfig{Enabled: true, Providers: map[string]config.AIProvider{
		"ollama": {BaseURL: srv.URL + "/v1", APIKey: ""},
	}, Chain: []config.AIChainSlot{{ID: "ollama:deadmodel", Provider: "ollama", Model: "deadmodel"}}}
	c := New(cfg)
	if c == nil {
		t.Fatal("New nil")
	}

	c.healProvider("ollama")

	slots := c.Slots()
	for _, s := range slots {
		if s.Model == "deadmodel" {
			t.Fatal("dead model should have been removed")
		}
	}
	if len(slots) == 0 {
		t.Fatal("expected at least one replacement model")
	}
}

func TestDefaultBenchmarkStorePath(t *testing.T) {
	path := DefaultBenchmarkStorePath("/data")
	if path != "/data/.ai-benchmark.db" {
		t.Fatalf("expected /data/.ai-benchmark.db, got %q", path)
	}
}

func TestBenchmarkStoreEdgeCases(t *testing.T) {
	t.Run("nil store returns defaults", func(t *testing.T) {
		var s *BenchmarkStore
		if got := s.Results(); got != nil {
			t.Fatal("expected nil results")
		}
		if got := s.Order(); got != nil {
			t.Fatal("expected nil order")
		}
		if got := s.Cases(); len(got) == 0 {
			t.Fatal("expected default cases")
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})
}

func TestBenchmarkStoreRoundTrip(t *testing.T) {
	st, err := NewBenchmarkStore(filepath.Join(t.TempDir(), "bench.db"))
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	defer st.Close()

	// Cases seed defaults on first read.
	if got := st.Cases(); len(got) != len(DefaultBenchmarkCases) {
		t.Fatalf("expected default cases seeded, got %d", len(got))
	}
	// Editable: replace the set.
	custom := []BenchmarkCase{{Raw: "Foo.2020", Expect: "Foo"}}
	if err := st.SetCases(custom); err != nil {
		t.Fatalf("SetCases: %v", err)
	}
	if got := st.Cases(); len(got) != 1 || got[0].Expect != "Foo" {
		t.Fatalf("cases not replaced: %+v", got)
	}

	// Results + order persist best-first.
	scores := []SlotScore{
		{SlotID: "x", Composite: 0.9, Accuracy: 0.9, AvgLatencyMs: 500},
		{SlotID: "y", Composite: 0.2, Accuracy: 0.5, AvgLatencyMs: 6000},
	}
	if err := st.SaveResults(scores); err != nil {
		t.Fatalf("SaveResults: %v", err)
	}
	order := st.Order()
	if len(order) != 2 || order[0] != "x" || order[1] != "y" {
		t.Fatalf("order wrong: %v", order)
	}
	if res := st.Results(); len(res) != 2 || res[0].SlotID != "x" {
		t.Fatalf("results wrong: %+v", res)
	}
}
