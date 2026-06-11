package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// scheduleSystem turns a free-text scheduling phrase (the user writes in
// Portuguese, but English must work too) into the strict JSON shape the
// watchlist scheduler understands. Anything ambiguous must come back as
// kind "invalid" — the UI asks the user to rephrase instead of guessing.
//
// The few-shot set is deliberately wide on weekdays: a weak free model used to
// read "Toda segunda-feira às 07h00" and answer kind=daily 07:00 because it had
// no example of the hyphenated long form ("segunda-feira") nor the "HHhMM" clock
// form ("07h00"), and only one short-form weekly example. So it now (a) spells
// out the PT day→number map covering long and short forms, (b) states that any
// "HHhMM"/"HHh"/"HH:MM" is a clock time, (c) gives a weekly example for several
// distinct weekdays in BOTH the hyphenated long form and the HHhMM time form, and
// (d) states that a NAMED weekday is ALWAYS weekly, never daily. (The post-parse
// safety net in ParseSchedule also catches a model that ignores all of this.)
const scheduleSystem = `You convert a natural-language scheduling phrase (Portuguese or English) into a STRICT JSON schedule.

Reply with ONLY one raw JSON object, no prose, no code fences:
{"kind":"interval","minutes":0,"weekday":0,"hour":0,"minute":0}

Field rules:
- "kind": "interval" (repeats every N minutes), "daily" (once a day at HH:MM), "weekly" (once a week on a weekday at HH:MM), or "invalid" when the text is ambiguous, nonsensical or not a schedule at all.
- "minutes": only for interval — the repeat period in minutes. "N times per day" / "N vezes por dia" means interval with 1440/N minutes.
- "weekday": only for weekly — 0=Sunday, 1=Monday … 6=Saturday. Portuguese day names map as: domingo=0, segunda=1, terça=2, quarta=3, quinta=4, sexta=5, sábado=6. Accept the LONG form ("segunda-feira", "terça-feira" …) and the SHORT form ("segunda", "terça" …) — both name the same day. English: sunday=0, monday=1 … saturday=6.
- A NAMED day of the week (in any form) ALWAYS means kind "weekly", NEVER "daily". "daily" is only for "todo dia"/"every day" with NO weekday named.
- "hour"/"minute": only for daily/weekly, 24h clock. The forms "HHhMM", "HHh" and "HH:MM" are all clock times: "07h00"=07:00, "18h"=18:00, "9h"=09:00, "21:30"=21:30. Vague times of day map to: morning/manhã=8:00, afternoon/tarde=14:00, evening|night/noite=20:00.
- Irrelevant fields stay 0. Never invent a schedule the text doesn't state.

Examples:
toda segunda às 9h → {"kind":"weekly","minutes":0,"weekday":1,"hour":9,"minute":0}
toda segunda-feira às 07h00 → {"kind":"weekly","minutes":0,"weekday":1,"hour":7,"minute":0}
toda terça-feira às 21:30 → {"kind":"weekly","minutes":0,"weekday":2,"hour":21,"minute":30}
toda sexta-feira às 18h → {"kind":"weekly","minutes":0,"weekday":5,"hour":18,"minute":0}
todo sábado de manhã → {"kind":"weekly","minutes":0,"weekday":6,"hour":8,"minute":0}
domingo de manhã → {"kind":"weekly","minutes":0,"weekday":0,"hour":8,"minute":0}
duas vezes por dia → {"kind":"interval","minutes":720,"weekday":0,"hour":0,"minute":0}
a cada 3 horas → {"kind":"interval","minutes":180,"weekday":0,"hour":0,"minute":0}
todo dia às 21:30 → {"kind":"daily","minutes":0,"weekday":0,"hour":21,"minute":30}
every monday at 9am → {"kind":"weekly","minutes":0,"weekday":1,"hour":9,"minute":0}
every friday at 22:30 → {"kind":"weekly","minutes":0,"weekday":5,"hour":22,"minute":30}
every 45 minutes → {"kind":"interval","minutes":45,"weekday":0,"hour":0,"minute":0}
banana azul → {"kind":"invalid","minutes":0,"weekday":0,"hour":0,"minute":0}`

// ErrInvalidSchedule means the model understood the request but the text isn't
// a recognizable schedule (or no model produced usable JSON). Handlers map it
// to 422 so the UI can ask the user to rephrase.
var ErrInvalidSchedule = errors.New("ai: text is not a recognizable schedule")

// ScheduleResult is the model's strict-JSON answer for a scheduling phrase.
// Mirrors watchlist.Schedule but lives here so the ai package stays decoupled.
type ScheduleResult struct {
	Kind    string `json:"kind"` // "interval" | "daily" | "weekly"
	Minutes int    `json:"minutes"`
	Weekday int    `json:"weekday"` // 0=Sunday … 6=Saturday
	Hour    int    `json:"hour"`
	Minute  int    `json:"minute"`
}

// ParseSchedule walks the chain until a slot converts the free-text phrase into
// a schedule. ErrInvalidSchedule (checked with errors.Is) covers both "the model
// says this isn't a schedule" and "no model produced parseable JSON"; any other
// error means the chain itself failed (network/rate limit — AI unavailable).
func (c *Client) ParseSchedule(ctx context.Context, text string) (*ScheduleResult, error) {
	if c == nil {
		return nil, errors.New("ai client not initialized")
	}
	lastErr := errors.New("ai: no provider available")
	for _, s := range c.slotList() {
		if !c.breaker.available(s.Provider, s.ID) {
			continue
		}
		content, _, _, err := c.chat(ctx, s, scheduleSystem, text, true)
		if err != nil {
			lastErr = err
			c.noteChainFailure(s, err)
			continue
		}
		c.breaker.recordSuccess(s.Provider, s.ID)
		res, perr := parseScheduleJSON(content)
		if perr == nil {
			fixDailyWithNamedWeekday(res, text)
			return res, nil
		}
		if errors.Is(perr, ErrInvalidSchedule) {
			// The model answered: this text is not a schedule. That's a verdict,
			// not a failure — don't burn the rest of the chain on it.
			return nil, perr
		}
		// Unparseable reply — a quality failure of THIS model; try the next one,
		// but keep the typed error so the handler still answers 422 if all fail.
		lastErr = fmt.Errorf("%w: %v", ErrInvalidSchedule, perr)
	}
	return nil, lastErr
}

// parseScheduleJSON pulls the JSON object out of a model reply (possibly wrapped
// in prose or ```json fences — same tolerance as parseTitleJSON) and validates
// the kind. kind "invalid" (or anything unknown) → ErrInvalidSchedule.
func parseScheduleJSON(content string) (*ScheduleResult, error) {
	start := strings.IndexByte(content, '{')
	end := strings.LastIndexByte(content, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("ai: no JSON object in schedule reply")
	}
	var res ScheduleResult
	if err := json.Unmarshal([]byte(content[start:end+1]), &res); err != nil {
		return nil, fmt.Errorf("ai: bad schedule json: %w", err)
	}
	switch res.Kind {
	case "interval", "daily", "weekly":
		return &res, nil
	default:
		return nil, ErrInvalidSchedule
	}
}

// weekdayWords maps each Portuguese/English weekday name to its time.Weekday
// number (0=Sunday … 6=Saturday — the SAME convention as watchlist.Schedule and
// nextCheckTime). Long PT forms ("segunda-feira") are matched by the bare day
// regex below; the "-feira" suffix is just noise on the same day.
var weekdayWords = map[string]int{
	"domingo": 0, "sunday": 0,
	"segunda": 1, "monday": 1,
	"terca": 2, "terça": 2, "tuesday": 2,
	"quarta": 3, "wednesday": 3,
	"quinta": 4, "thursday": 4,
	"sexta": 5, "friday": 5,
	"sabado": 6, "sábado": 6, "saturday": 6,
}

// weekdayRe finds a named weekday in the original phrase as a WHOLE word, so it
// won't fire on a substring (e.g. it must not match "segunda" inside an unrelated
// token). The PT "-feira" suffix is consumed when present so "segunda-feira"
// still resolves to "segunda"; an optional trailing "s" covers the PT plural form
// ("todas as quartas", "as sextas"). Word boundaries are explicit because Go's
// regexp \b is byte-oriented and would mis-handle the accented forms.
var weekdayRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(domingo|segunda|terça|terca|quarta|quinta|sexta|sábado|sabado|sunday|monday|tuesday|wednesday|thursday|friday|saturday)(?:-feira)?s?(?:$|[^\p{L}])`)

// detectWeekday returns the time.Weekday number named in the text and ok=true
// when exactly one weekday word appears. Multiple distinct weekdays (e.g. "segunda
// e quinta") are ambiguous for a single weekly slot, so it reports ok=false and
// the model's own answer stands.
func detectWeekday(text string) (int, bool) {
	matches := weekdayRe.FindAllStringSubmatch(text, -1)
	day := -1
	for _, m := range matches {
		w, known := weekdayWords[strings.ToLower(m[1])]
		if !known {
			continue
		}
		if day >= 0 && day != w {
			return 0, false // two different days named → ambiguous, leave it to the model
		}
		day = w
	}
	if day < 0 {
		return 0, false
	}
	return day, true
}

// fixDailyWithNamedWeekday is the deterministic safety net for the schedule bug:
// a weak model that reads "Toda segunda-feira às 07h00" and answers kind="daily"
// (dropping the weekday) is corrected to "weekly" on the day the phrase actually
// names, keeping the hour/minute the model already extracted. It ONLY upgrades
// daily→weekly when the ORIGINAL text names exactly one weekday — it never
// downgrades, never touches interval/invalid, and never overrides a weekly answer
// the model got right. This complements the strengthened prompt; the prompt is
// the primary fix, this just stops a regression from reaching the user.
func fixDailyWithNamedWeekday(res *ScheduleResult, text string) {
	if res == nil || res.Kind != "daily" {
		return
	}
	if w, ok := detectWeekday(text); ok {
		res.Kind = "weekly"
		res.Weekday = w
	}
}
