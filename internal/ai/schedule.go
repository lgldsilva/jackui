package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// scheduleSystem turns a free-text scheduling phrase (the user writes in
// Portuguese, but English must work too) into the strict JSON shape the
// watchlist scheduler understands. Anything ambiguous must come back as
// kind "invalid" — the UI asks the user to rephrase instead of guessing.
const scheduleSystem = `You convert a natural-language scheduling phrase (Portuguese or English) into a STRICT JSON schedule.

Reply with ONLY one raw JSON object, no prose, no code fences:
{"kind":"interval","minutes":0,"weekday":0,"hour":0,"minute":0}

Field rules:
- "kind": "interval" (repeats every N minutes), "daily" (once a day at HH:MM), "weekly" (once a week on a weekday at HH:MM), or "invalid" when the text is ambiguous, nonsensical or not a schedule at all.
- "minutes": only for interval — the repeat period in minutes. "N times per day" means interval with 1440/N minutes.
- "weekday": only for weekly — 0=Sunday, 1=Monday … 6=Saturday.
- "hour"/"minute": only for daily/weekly, 24h clock. Vague times of day map to: morning/manhã=8:00, afternoon/tarde=14:00, evening|night/noite=20:00.
- Irrelevant fields stay 0. Never invent a schedule the text doesn't state.

Examples:
toda segunda às 9h → {"kind":"weekly","minutes":0,"weekday":1,"hour":9,"minute":0}
duas vezes por dia → {"kind":"interval","minutes":720,"weekday":0,"hour":0,"minute":0}
a cada 3 horas → {"kind":"interval","minutes":180,"weekday":0,"hour":0,"minute":0}
domingo de manhã → {"kind":"weekly","minutes":0,"weekday":0,"hour":8,"minute":0}
todo dia às 21:30 → {"kind":"daily","minutes":0,"weekday":0,"hour":21,"minute":30}
every monday at 9am → {"kind":"weekly","minutes":0,"weekday":1,"hour":9,"minute":0}
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
