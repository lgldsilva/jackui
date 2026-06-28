package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lgldsilva/jackui/internal/dbtest"

	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/watchlist"
)

// taskAwareChat is a stub OpenAI-compatible endpoint that answers DIFFERENTLY per
// task: it inspects the system prompt to tell which task is being benchmarked and
// returns the matching JSON. This lets a single run mix rename + schedule + identify
// cases through one server, exactly like a real multi-task benchmark.
func taskAwareChat(rename, identify, schedule string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role, Content string
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		system := ""
		if len(req.Messages) > 0 {
			system = req.Messages[0].Content
		}
		content := rename
		switch {
		case strings.Contains(system, "STRICT JSON schedule"):
			content = schedule
		case strings.Contains(system, "canonical movie or TV series title"):
			content = identify
		}
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// TestScheduleAccuracy pins the schedule scorer: kind mismatch is 0, weekday is the
// dominant weekly signal, interval/daily score on their defining fields.
func TestScheduleAccuracy(t *testing.T) {
	cases := []struct {
		res    *ScheduleResult
		expect string
		want   float64
	}{
		// Perfect weekly.
		{&ScheduleResult{Kind: "weekly", Weekday: 1, Hour: 7, Minute: 0}, "weekly:1:7:0", 1},
		// Right weekly but wrong day → loses the 0.6 weekday share.
		{&ScheduleResult{Kind: "weekly", Weekday: 2, Hour: 7, Minute: 0}, "weekly:1:7:0", 0.4},
		// The bug: should be weekly, model said daily → 0 (kind mismatch).
		{&ScheduleResult{Kind: "daily", Hour: 7, Minute: 0}, "weekly:1:7:0", 0},
		// Perfect daily / interval.
		{&ScheduleResult{Kind: "daily", Hour: 21, Minute: 30}, "daily:21:30", 1},
		{&ScheduleResult{Kind: "interval", Minutes: 180}, "interval:180", 1},
		// Right interval kind, wrong period → partial 0.5.
		{&ScheduleResult{Kind: "interval", Minutes: 60}, "interval:180", 0.5},
		// Malformed expect → can only fail.
		{&ScheduleResult{Kind: "weekly", Weekday: 1}, "garbage", 0},
		{nil, "weekly:1:7:0", 0},
	}
	for i, tc := range cases {
		if got := scheduleAccuracy(tc.res, tc.expect); got != tc.want {
			t.Errorf("case %d: scheduleAccuracy=%v, want %v", i, got, tc.want)
		}
	}
}

// TestParseScheduleExpect covers the compact label parser used by the dataset.
func TestParseScheduleExpect(t *testing.T) {
	cases := []struct {
		in   string
		want scheduleExpect
	}{
		{"weekly:1:7:0", scheduleExpect{Kind: "weekly", Weekday: 1, Hour: 7}},
		{"weekly:6:8:30", scheduleExpect{Kind: "weekly", Weekday: 6, Hour: 8, Minute: 30}},
		{"daily:21:30", scheduleExpect{Kind: "daily", Hour: 21, Minute: 30}},
		{"interval:180", scheduleExpect{Kind: "interval", Minutes: 180}},
		{"nonsense", scheduleExpect{}},
	}
	for _, tc := range cases {
		if got := parseScheduleExpect(tc.in); got != tc.want {
			t.Errorf("parseScheduleExpect(%q)=%+v, want %+v", tc.in, got, tc.want)
		}
	}
}

// TestScheduleExpectMatchesWatchlistKinds guards the duplicated schedule-kind
// constants in this package against drifting from watchlist.Sched*: a parsed
// schedule must drop straight into a watchlist.Schedule and survive Normalized().
func TestScheduleExpectMatchesWatchlistKinds(t *testing.T) {
	pairs := []struct {
		local, watchlist string
	}{
		{schedInterval, watchlist.SchedInterval},
		{schedDaily, watchlist.SchedDaily},
		{schedWeekly, watchlist.SchedWeekly},
	}
	for _, p := range pairs {
		if p.local != p.watchlist {
			t.Fatalf("schedule kind drift: ai %q != watchlist %q", p.local, p.watchlist)
		}
	}
	// A parsed weekly result must normalize to itself in a real watchlist.Schedule.
	we := parseScheduleExpect("weekly:1:7:0")
	s := watchlist.Schedule{Kind: we.Kind, Weekday: we.Weekday, Hour: we.Hour, Minute: we.Minute}.Normalized()
	if s.Kind != watchlist.SchedWeekly || s.Weekday != 1 || s.Hour != 7 {
		t.Fatalf("schedule label doesn't survive Normalized(): %+v", s)
	}
}

// TestMultiTaskScoreBreakdown runs a slot over a MIXED case set (rename + schedule +
// identify) and checks the per-task accuracy breakdown plus the global mean. The
// stub answers each task correctly, so every task scores 1.0 and so does the mean.
func TestMultiTaskScoreBreakdown(t *testing.T) {
	srv := httptest.NewServer(taskAwareChat(
		`{"title":"Inception","year":2010,"kind":"movie","season":0,"episode":0,"episode_title":""}`,
		`{"title":"Sicario","year":2015,"kind":"movie"}`,
		`{"kind":"weekly","minutes":0,"weekday":1,"hour":7,"minute":0}`,
	))
	defer srv.Close()
	c := clientForURL(t, srv.URL)

	cases := []BenchmarkCase{
		{Raw: "Inception.2010.1080p", Expect: "Inception - 2010", Task: TaskRename},
		{Raw: "Sicario.2015.1080p", Expect: "Sicario", Task: TaskIdentify},
		{Raw: "Toda segunda-feira às 07h00", Expect: "weekly:1:7:0", Task: TaskSchedule},
	}
	scores := c.Run(context.Background(), cases)
	if len(scores) != 1 {
		t.Fatalf("expected 1 score, got %d", len(scores))
	}
	sc := scores[0]
	if len(sc.Tasks) != 3 {
		t.Fatalf("expected 3 task breakdowns, got %d (%+v)", len(sc.Tasks), sc.Tasks)
	}
	for _, task := range []string{TaskRename, TaskIdentify, TaskSchedule} {
		ts, ok := sc.Tasks[task]
		if !ok {
			t.Fatalf("missing task %q in breakdown", task)
		}
		if ts.Accuracy != 1 || ts.Samples != 1 {
			t.Errorf("task %q: accuracy=%v samples=%d, want 1.0/1", task, ts.Accuracy, ts.Samples)
		}
	}
	if sc.Accuracy != 1 {
		t.Fatalf("global accuracy (mean of tasks) = %v, want 1", sc.Accuracy)
	}
}

// TestMultiTaskMeanWeighsTasksEqually: a model perfect at rename (many cases) but
// wrong at schedule must NOT get a near-100% just because rename dominates the case
// count — the mean-of-tasks gives each task equal weight.
func TestMultiTaskMeanWeighsTasksEqually(t *testing.T) {
	srv := httptest.NewServer(taskAwareChat(
		`{"title":"Inception","year":2010,"kind":"movie"}`,
		`{"title":"Sicario","year":2015,"kind":"movie"}`,
		`{"kind":"daily","minutes":0,"weekday":0,"hour":7,"minute":0}`, // WRONG: should be weekly
	))
	defer srv.Close()
	c := clientForURL(t, srv.URL)

	// 4 rename cases (all correct — the fixed stub returns "Inception", so every
	// rename Expect is "Inception") + 1 schedule case (wrong). Case-count would give
	// 4/5=80%; the mean-of-tasks gives (1.0 rename + 0.0 schedule)/2 = 50%.
	cases := []BenchmarkCase{
		{Raw: "Inception.2010.a", Expect: "Inception - 2010", Task: TaskRename},
		{Raw: "Inception.2010.b", Expect: "Inception - 2010", Task: TaskRename},
		{Raw: "Inception.2010.c", Expect: "Inception - 2010", Task: TaskRename},
		{Raw: "Inception.2010.d", Expect: "Inception - 2010", Task: TaskRename},
		// The schedule case has no weekday in the text the daily reply could be
		// upgraded from, so it stays daily → wrong kind → 0.
		{Raw: "manhã cedo todo dia", Expect: "weekly:1:7:0", Task: TaskSchedule},
	}
	sc := c.Run(context.Background(), cases)[0]
	if sc.Tasks[TaskRename].Accuracy != 1 {
		t.Fatalf("rename task should be 1.0, got %v", sc.Tasks[TaskRename].Accuracy)
	}
	if sc.Tasks[TaskSchedule].Accuracy != 0 {
		t.Fatalf("schedule task should be 0, got %v", sc.Tasks[TaskSchedule].Accuracy)
	}
	if sc.Accuracy != 0.5 {
		t.Fatalf("mean-of-tasks should be 0.5 (not 0.8 from case count), got %v", sc.Accuracy)
	}
}

// TestSingleTaskCollapsesToOldBehavior: a rename-only run produces the SAME global
// accuracy as before the multi-task change (the mean of one task is that task), and
// leaves a single-entry Tasks map.
func TestSingleTaskCollapsesToOldBehavior(t *testing.T) {
	srv := httptest.NewServer(jsonChat(`{"title":"Inception","year":2010,"kind":"movie"}`, http.StatusOK))
	defer srv.Close()
	c := clientForURL(t, srv.URL)

	// No Task field → defaults to rename (retrocompat with legacy datasets). The
	// fixed stub returns "Inception", so both Expects are "Inception" to score 1.0.
	cases := []BenchmarkCase{
		{Raw: "Inception.2010.a", Expect: "Inception - 2010"},
		{Raw: "Inception.2010.b", Expect: "Inception - 2010"},
	}
	sc := c.Run(context.Background(), cases)[0]
	if len(sc.Tasks) != 1 || sc.Tasks[TaskRename].Samples != 2 {
		t.Fatalf("single-task run should yield one rename task with 2 samples: %+v", sc.Tasks)
	}
	if sc.Accuracy != 1 {
		t.Fatalf("rename-only accuracy = %v, want 1", sc.Accuracy)
	}
}

// TestSlotScoreJSONRetrocompat: the new Tasks field is omitempty, so a score with
// no breakdown marshals to the SAME JSON the UI already consumes (no "tasks" key),
// and a legacy JSON without "tasks" unmarshals cleanly.
func TestSlotScoreJSONRetrocompat(t *testing.T) {
	legacy := `{"slotId":"groq:m","provider":"groq","model":"m","accuracy":0.9,"avgLatencyMs":500,"composite":1.2,"samples":10,"free":true,"costPer1M":0}`
	var sc SlotScore
	if err := json.Unmarshal([]byte(legacy), &sc); err != nil {
		t.Fatalf("legacy JSON must unmarshal: %v", err)
	}
	if sc.Tasks != nil {
		t.Fatalf("legacy JSON has no tasks, expected nil map")
	}
	// A score with no per-task breakdown must NOT emit a "tasks" key.
	out, _ := json.Marshal(SlotScore{SlotID: "x", Accuracy: 0.5})
	if strings.Contains(string(out), "tasks") {
		t.Fatalf("score without breakdown must omit tasks: %s", out)
	}
	// A score WITH a breakdown emits it.
	out, _ = json.Marshal(SlotScore{SlotID: "x", Tasks: map[string]TaskScore{TaskSchedule: {Accuracy: 1, Samples: 3, Scored: 3}}})
	if !strings.Contains(string(out), `"tasks"`) || !strings.Contains(string(out), TaskSchedule) {
		t.Fatalf("score with breakdown must emit tasks: %s", out)
	}
}

// TestScheduleCasesNotInPrompt + TestIdentifyCasesNotInPrompt: the few-shot leak
// guard, extended to the new tasks' default datasets.
func TestTaskCasesNotInPrompts(t *testing.T) {
	for _, tc := range DefaultScheduleCases {
		if strings.Contains(scheduleSystem, tc.Raw) {
			t.Fatalf("schedule case %q is a few-shot example in scheduleSystem (leak)", tc.Raw)
		}
	}
	for _, tc := range DefaultIdentifyCases {
		if strings.Contains(identifySystem, tc.Raw) || strings.Contains(renameSystem, tc.Raw) {
			t.Fatalf("identify case %q appears in a production prompt (leak)", tc.Raw)
		}
	}
}

// TestDefaultMultiTaskDatasetWellFormed: every default case has a known task and a
// parseable expect for that task; the schedule/identify tasks are actually present.
func TestDefaultMultiTaskDatasetWellFormed(t *testing.T) {
	var rename, schedule, identify int
	for _, tc := range AllDefaultBenchmarkCases() {
		if tc.Origin != OriginDefault {
			t.Fatalf("default case %q missing origin", tc.Raw)
		}
		switch normalizeTask(tc.Task) {
		case TaskRename:
			rename++
		case TaskSchedule:
			schedule++
			if parseScheduleExpect(tc.Expect).Kind == "" {
				t.Fatalf("schedule case %q has unparseable expect %q", tc.Raw, tc.Expect)
			}
		case TaskIdentify:
			identify++
			if strings.TrimSpace(tc.Expect) == "" {
				t.Fatalf("identify case %q has empty expect", tc.Raw)
			}
		default:
			t.Fatalf("default case %q has unknown task %q", tc.Raw, tc.Task)
		}
	}
	if rename < 50 || schedule < 10 || identify < 5 {
		t.Fatalf("multi-task dataset unbalanced: rename=%d schedule=%d identify=%d", rename, schedule, identify)
	}
}

// TestRunnerForDefaultsToRename: an unknown/empty task never drops a case — it falls
// back to the rename runner.
func TestRunnerForDefaultsToRename(t *testing.T) {
	if runnerFor("") == nil || runnerFor("bogus") == nil {
		t.Fatal("runnerFor must never return nil")
	}
}

// TestScheduleTaskUsesSafetyNet: the benchmark scores the SAME end-to-end behavior
// production gets — a model that returns daily for a named-weekday phrase is
// corrected by the safety net and scores as weekly.
func TestScheduleTaskUsesSafetyNet(t *testing.T) {
	srv := httptest.NewServer(jsonChat(`{"kind":"daily","minutes":0,"weekday":0,"hour":7,"minute":0}`, http.StatusOK))
	defer srv.Close()
	c := clientForURL(t, srv.URL)

	acc, _, _, err := runScheduleCase(context.Background(), c, c.Slots()[0], "weekly:1:7:0", "Toda segunda-feira às 07h00")
	if err != nil {
		t.Fatalf("runScheduleCase: %v", err)
	}
	if acc != 1 {
		t.Fatalf("safety net should make the daily reply score as the correct weekly (1.0), got %v", acc)
	}
}

// TestTaskRunnersPropagateTransientErrors: when the chat call fails (e.g. 5xx),
// each runner returns the error so scoreSingleCase can skip the case transiently
// (not score it 0). Covers the error branch of the identify/schedule runners.
func TestTaskRunnersPropagateTransientErrors(t *testing.T) {
	srv := httptest.NewServer(jsonChat("", http.StatusInternalServerError))
	defer srv.Close()
	c := clientForURL(t, srv.URL)
	s := c.Slots()[0]

	for _, run := range []taskRunner{runRenameCase, runIdentifyCase, runScheduleCase} {
		if _, _, _, err := run(context.Background(), c, s, "x", "Inception.2010"); err == nil {
			t.Fatalf("runner should propagate a 5xx as an error")
		}
	}
}

// TestRunScheduleCaseBadOutput: an unparseable schedule reply is a bad-output (a
// 0-accuracy scored case), not a transient skip.
func TestRunScheduleCaseBadOutput(t *testing.T) {
	srv := httptest.NewServer(jsonChat("not json at all", http.StatusOK))
	defer srv.Close()
	c := clientForURL(t, srv.URL)
	if _, _, _, err := runScheduleCase(context.Background(), c, c.Slots()[0], "daily:7:0", "x"); err != errBadOutput {
		t.Fatalf("unparseable schedule reply should be errBadOutput, got %v", err)
	}
}

// TestTaskBreakdownSkipsUnmeasuredTask: a task whose cases were all transiently
// skipped (scored==0) is left out of the mean so it doesn't dilute the accuracy.
func TestTaskBreakdownSkipsUnmeasuredTask(t *testing.T) {
	tl := &slotTally{tasks: map[string]*taskTally{}}
	tl.record(TaskRename, 1.0, true, false) // measured, perfect
	tl.tasks[TaskSchedule] = &taskTally{}   // present but never scored
	breakdown, mean := tl.taskBreakdown()
	if _, ok := breakdown[TaskSchedule]; ok {
		t.Fatal("an unmeasured task must be omitted from the breakdown")
	}
	if mean != 1.0 {
		t.Fatalf("mean must ignore the unmeasured task, got %v", mean)
	}
	// Empty tally → nil map, 0 mean.
	if b, m := (&slotTally{tasks: map[string]*taskTally{}}).taskBreakdown(); b != nil || m != 0 {
		t.Fatalf("empty tally should be nil,0; got %+v,%v", b, m)
	}
}

// TestIsRenameOnlySeed: a user-edited rename set, or one that already carries a
// task, is NOT the upgradeable rename-only seed.
func TestIsRenameOnlySeed(t *testing.T) {
	if !isRenameOnlySeed(DefaultBenchmarkCases) {
		t.Fatal("the untouched rename default IS the rename-only seed")
	}
	edited := append([]BenchmarkCase(nil), DefaultBenchmarkCases...)
	edited[0].Expect = "Changed"
	if isRenameOnlySeed(edited) {
		t.Fatal("an edited set is not the rename-only seed")
	}
	tasked := append([]BenchmarkCase(nil), DefaultBenchmarkCases...)
	tasked[0].Task = TaskSchedule
	if isRenameOnlySeed(tasked) {
		t.Fatal("a set already carrying a task is not the rename-only seed")
	}
	if isRenameOnlySeed(DefaultScheduleCases) {
		t.Fatal("a different-length set is not the rename-only seed")
	}
}

// TestStoreResultTaskBreakdownRoundtrip: the per-task breakdown survives a save +
// reload (so the UI still shows it after a restart), and a result with no breakdown
// (legacy) reloads with a nil Tasks map.
func TestStoreResultTaskBreakdownRoundtrip(t *testing.T) {
	st, err := NewBenchmarkStore(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("NewBenchmarkStore: %v", err)
	}
	defer st.Close()

	scores := []SlotScore{
		{SlotID: "a", Composite: 0.9, Accuracy: 0.8, Samples: 5, Tasks: map[string]TaskScore{
			TaskRename:   {Accuracy: 1, Samples: 3, Scored: 3},
			TaskSchedule: {Accuracy: 0.6, Samples: 2, Scored: 2},
		}},
		{SlotID: "b", Composite: 0.5, Accuracy: 0.5, Samples: 1}, // no breakdown
	}
	if err := st.SaveResults(scores); err != nil {
		t.Fatalf("SaveResults: %v", err)
	}
	got := st.Results()
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if len(got[0].Tasks) != 2 || got[0].Tasks[TaskSchedule].Accuracy != 0.6 {
		t.Fatalf("task breakdown didn't round-trip: %+v", got[0].Tasks)
	}
	if got[1].Tasks != nil {
		t.Fatalf("legacy result should reload with nil Tasks, got %+v", got[1].Tasks)
	}
}

// Ensure config import stays used even if the helpers above change.
var _ = config.AIConfig{}
