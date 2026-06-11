package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseScheduleJSONClean(t *testing.T) {
	res, err := parseScheduleJSON(`{"kind":"weekly","minutes":0,"weekday":1,"hour":9,"minute":0}`)
	if err != nil {
		t.Fatalf("parseScheduleJSON: %v", err)
	}
	if res.Kind != "weekly" || res.Weekday != 1 || res.Hour != 9 || res.Minute != 0 {
		t.Fatalf("bad result: %+v", res)
	}
}

func TestParseScheduleJSONCodeFence(t *testing.T) {
	res, err := parseScheduleJSON("```json\n{\"kind\":\"interval\",\"minutes\":720,\"weekday\":0,\"hour\":0,\"minute\":0}\n```")
	if err != nil {
		t.Fatalf("parseScheduleJSON: %v", err)
	}
	if res.Kind != "interval" || res.Minutes != 720 {
		t.Fatalf("bad result: %+v", res)
	}
}

func TestParseScheduleJSONWrappedInProse(t *testing.T) {
	res, err := parseScheduleJSON(`Sure! Here is the schedule you asked for:
{"kind":"daily","minutes":0,"weekday":0,"hour":21,"minute":30}
Let me know if you need anything else.`)
	if err != nil {
		t.Fatalf("parseScheduleJSON: %v", err)
	}
	if res.Kind != "daily" || res.Hour != 21 || res.Minute != 30 {
		t.Fatalf("bad result: %+v", res)
	}
}

func TestParseScheduleJSONInvalidKind(t *testing.T) {
	for _, content := range []string{
		`{"kind":"invalid","minutes":0,"weekday":0,"hour":0,"minute":0}`,
		`{"kind":"monthly","minutes":0,"weekday":0,"hour":0,"minute":0}`, // unknown kind
		`{"minutes":30}`, // missing kind
	} {
		if _, err := parseScheduleJSON(content); !errors.Is(err, ErrInvalidSchedule) {
			t.Errorf("content %q: err = %v, want ErrInvalidSchedule", content, err)
		}
	}
}

func TestParseScheduleJSONGarbage(t *testing.T) {
	for _, content := range []string{"", "no json here", "{broken json}"} {
		_, err := parseScheduleJSON(content)
		if err == nil {
			t.Errorf("content %q: expected error", content)
			continue
		}
		if errors.Is(err, ErrInvalidSchedule) {
			t.Errorf("content %q: garbage is a parse failure, not the invalid verdict: %v", content, err)
		}
	}
}

func TestParseScheduleHappyPath(t *testing.T) {
	srv := httptest.NewServer(jsonChat(`{"kind":"weekly","minutes":0,"weekday":1,"hour":9,"minute":0}`, http.StatusOK))
	defer srv.Close()

	c := clientForURL(t, srv.URL)
	res, err := c.ParseSchedule(context.Background(), "toda segunda às 9h")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}
	if res.Kind != "weekly" || res.Weekday != 1 || res.Hour != 9 {
		t.Fatalf("bad result: %+v", res)
	}
}

func TestParseScheduleInvalidVerdictStopsChain(t *testing.T) {
	calls := 0
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		jsonChat(`{"kind":"invalid","minutes":0,"weekday":0,"hour":0,"minute":0}`, http.StatusOK)(w, r)
	}))
	defer invalid.Close()
	good := httptest.NewServer(jsonChat(`{"kind":"daily","minutes":0,"weekday":0,"hour":8,"minute":0}`, http.StatusOK))
	defer good.Close()

	c := clientForURL(t, invalid.URL, good.URL)
	_, err := c.ParseSchedule(context.Background(), "banana azul")
	if !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("err = %v, want ErrInvalidSchedule", err)
	}
	if calls != 1 {
		t.Fatalf("invalid verdict should stop the chain, slot called %d times", calls)
	}
}

func TestParseScheduleGarbageFallsThroughToNextSlot(t *testing.T) {
	garbage := httptest.NewServer(jsonChat("utter nonsense, no JSON", http.StatusOK))
	defer garbage.Close()
	good := httptest.NewServer(jsonChat(`{"kind":"interval","minutes":180,"weekday":0,"hour":0,"minute":0}`, http.StatusOK))
	defer good.Close()

	c := clientForURL(t, garbage.URL, good.URL)
	res, err := c.ParseSchedule(context.Background(), "a cada 3 horas")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}
	if res.Kind != "interval" || res.Minutes != 180 {
		t.Fatalf("bad result: %+v", res)
	}
}

func TestParseScheduleAllSlotsGarbageIsInvalid(t *testing.T) {
	garbage := httptest.NewServer(jsonChat("still no JSON at all", http.StatusOK))
	defer garbage.Close()

	c := clientForURL(t, garbage.URL)
	if _, err := c.ParseSchedule(context.Background(), "x"); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("err = %v, want ErrInvalidSchedule (handler maps it to 422)", err)
	}
}

func TestParseScheduleChainDownIsNotInvalid(t *testing.T) {
	down := httptest.NewServer(jsonChat("", http.StatusInternalServerError))
	defer down.Close()

	c := clientForURL(t, down.URL)
	_, err := c.ParseSchedule(context.Background(), "toda segunda às 9h")
	if err == nil {
		t.Fatal("expected error from a dead chain")
	}
	if errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("infra failure must not look like an invalid phrase: %v", err)
	}
}

func TestParseScheduleNilClient(t *testing.T) {
	var c *Client
	if _, err := c.ParseSchedule(context.Background(), "x"); err == nil {
		t.Fatal("expected error on nil client")
	}
}

// TestDetectWeekday pins the PT/EN day→number map to the time.Weekday convention
// (0=Sunday … 6=Saturday) that watchlist.Schedule + nextCheckTime rely on. Both
// the long hyphenated PT form and the short form must resolve to the same number.
func TestDetectWeekday(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"domingo de manhã", 0},
		{"toda segunda às 7h", 1},
		{"Toda segunda-feira às 07h00", 1},
		{"toda terça-feira", 2},
		{"todas as quartas", 3},
		{"quinta-feira de tarde", 4},
		{"todas as sextas 22:30", 5},
		{"todo sábado de manhã", 6},
		{"every monday at 9am", 1},
		{"every Friday at 22:30", 5},
		{"sunday morning", 0},
	}
	for _, tc := range cases {
		got, ok := detectWeekday(tc.text)
		if !ok || got != tc.want {
			t.Errorf("detectWeekday(%q) = %d,%v; want %d,true", tc.text, got, ok, tc.want)
		}
	}
}

// TestDetectWeekdayNoneOrAmbiguous: no weekday named, or two DIFFERENT weekdays
// named, both report ok=false so the model's own answer stands.
func TestDetectWeekdayNoneOrAmbiguous(t *testing.T) {
	for _, text := range []string{
		"todo dia às 7h",          // no weekday
		"a cada 3 horas",          // interval, no weekday
		"duas vezes por dia",      // interval, no weekday
		"segunda e quinta às 10h", // two distinct days → ambiguous
	} {
		if _, ok := detectWeekday(text); ok {
			t.Errorf("detectWeekday(%q) should report ok=false", text)
		}
	}
	// The SAME day repeated is not ambiguous.
	if w, ok := detectWeekday("segunda, toda segunda-feira"); !ok || w != 1 {
		t.Errorf("repeated same day should resolve to 1,true; got %d,%v", w, ok)
	}
}

// TestFixDailyWithNamedWeekday: the deterministic safety net upgrades a daily
// answer to weekly when the text names a weekday, preserving hour/minute — and
// leaves everything else untouched.
func TestFixDailyWithNamedWeekday(t *testing.T) {
	// The exact phrase from the bug report: model said daily 07:00 → must become
	// weekly on Monday (1) at 07:00.
	res := &ScheduleResult{Kind: "daily", Hour: 7, Minute: 0}
	fixDailyWithNamedWeekday(res, "Toda segunda-feira às 07h00")
	if res.Kind != "weekly" || res.Weekday != 1 || res.Hour != 7 || res.Minute != 0 {
		t.Fatalf("bug phrase not corrected: %+v", res)
	}
	// Daily with NO weekday named stays daily.
	daily := &ScheduleResult{Kind: "daily", Hour: 7}
	fixDailyWithNamedWeekday(daily, "todo dia às 7h")
	if daily.Kind != "daily" || daily.Weekday != 0 {
		t.Fatalf("plain daily must stay daily: %+v", daily)
	}
	// A correct weekly answer is never touched (no downgrade, no day rewrite).
	weekly := &ScheduleResult{Kind: "weekly", Weekday: 5, Hour: 18}
	fixDailyWithNamedWeekday(weekly, "toda sexta-feira às 18h")
	if weekly.Kind != "weekly" || weekly.Weekday != 5 {
		t.Fatalf("weekly answer must be preserved: %+v", weekly)
	}
	// interval/invalid are never touched.
	interval := &ScheduleResult{Kind: "interval", Minutes: 180}
	fixDailyWithNamedWeekday(interval, "a cada 3 horas")
	if interval.Kind != "interval" {
		t.Fatalf("interval must stay interval: %+v", interval)
	}
	fixDailyWithNamedWeekday(nil, "anything") // must not panic
}

// TestParseScheduleSafetyNetUpgradesDaily proves the END-TO-END fix: even when a
// (stubbed) weak model answers kind=daily for "Toda segunda-feira às 07h00",
// ParseSchedule returns weekly on Monday at 07:00. The parser is faithful (it
// passes the model's daily through); the safety net is what corrects it.
func TestParseScheduleSafetyNetUpgradesDaily(t *testing.T) {
	srv := httptest.NewServer(jsonChat(`{"kind":"daily","minutes":0,"weekday":0,"hour":7,"minute":0}`, http.StatusOK))
	defer srv.Close()

	c := clientForURL(t, srv.URL)
	res, err := c.ParseSchedule(context.Background(), "Toda segunda-feira às 07h00")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}
	if res.Kind != "weekly" || res.Weekday != 1 || res.Hour != 7 {
		t.Fatalf("safety net failed to upgrade daily→weekly: %+v", res)
	}
}

// TestScheduleBugFaithfulToModel documents WHY the fix is a prompt + safety net,
// not a parser change: the raw parser passes the model's verdict through verbatim
// when there's no weekday in the text to key the safety net off of.
func TestScheduleBugFaithfulToModel(t *testing.T) {
	res, err := parseScheduleJSON(`{"kind":"daily","minutes":0,"weekday":0,"hour":7,"minute":0}`)
	if err != nil {
		t.Fatalf("parseScheduleJSON: %v", err)
	}
	if res.Kind != "daily" {
		t.Fatalf("parser must pass the model's kind through verbatim, got %q", res.Kind)
	}
}

// TestSchedulePromptVocabularyGuard mirrors TestRenamePromptExamplesParse: it
// guards the few-shot prompt against drifting away from the cases that fixed the
// bug — the hyphenated long form, the HHhMM clock form, the PT day→number map,
// and the rule that a named day is always weekly.
func TestSchedulePromptVocabularyGuard(t *testing.T) {
	for _, want := range []string{
		"segunda-feira", "07h00", "domingo=0", "segunda=1", "sexta=5", "sábado=6",
		"NEVER", // "a NAMED day ... ALWAYS ... weekly, NEVER daily"
	} {
		if !strings.Contains(scheduleSystem, want) {
			t.Errorf("scheduleSystem must mention %q (regression guard for the weekday bug)", want)
		}
	}
	// Every weekly few-shot example output must parse with the real parser and
	// come back as kind=weekly — keeps the prompt examples honest.
	weekly := 0
	for _, line := range strings.Split(scheduleSystem, "\n") {
		_, after, found := strings.Cut(line, " → ")
		if !found || !strings.HasPrefix(after, "{") {
			continue
		}
		// The "invalid" few-shot is intentionally not a schedule (parser returns
		// ErrInvalidSchedule by design) — skip it; every OTHER example must parse.
		if strings.Contains(after, `"invalid"`) {
			continue
		}
		res, err := parseScheduleJSON(after)
		if err != nil {
			t.Fatalf("prompt example doesn't parse: %q (%v)", line, err)
		}
		if res.Kind == "weekly" {
			weekly++
		}
	}
	if weekly < 5 {
		t.Fatalf("expected >=5 weekly few-shot examples, found %d", weekly)
	}
}
