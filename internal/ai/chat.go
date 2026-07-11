package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ─── OpenAI-compatible /chat/completions ─────────────────────────────────────

type chatReq struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Temperature     float64       `json:"temperature"`
	MaxTokens       int           `json:"max_tokens"`
	ResponseFormat  *respFormat   `json:"response_format,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

// maxOutputTokens caps the reply. It has to be generous because reasoning models
// (gpt-oss, o-series) spend this same budget on chain-of-thought tokens BEFORE
// emitting the JSON — with only 200 they ran out mid-reasoning and Groq returned
// 400 "max completion tokens reached before generating a valid document". The cap
// doesn't slow models that finish early (the JSON we want is ~40 tokens); it just
// stops a reasoner from erroring. See https://console.groq.com/docs/reasoning.
const maxOutputTokens = 1024

// respFormat forces JSON output on providers that support it (Groq/OpenRouter/
// Ollama OpenAI-compat). Set only for title identification, not the plain-text
// music query.
type respFormat struct {
	Type string `json:"type"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResp struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

var errRateLimited = errors.New("ai: rate limited")
var errModelNotFound = errors.New("ai: model not found")
var errInsufficientBalance = errors.New("ai: saldo insuficiente")

// rateLimitError is a 429 carrying the vendor's Retry-After hint (0 if none). It
// unwraps to errRateLimited so existing errors.Is checks still match; the benchmark
// reads RetryAfter to back off the exact reset window and retry, so a transiently
// throttled model still gets a COMPLETE score instead of 100% over a few cases.
type rateLimitError struct {
	slotID     string
	RetryAfter time.Duration
}

func (e *rateLimitError) Error() string { return "ai: rate limited: " + e.slotID }
func (e *rateLimitError) Unwrap() error { return errRateLimited }

// parseRetryAfter reads a Retry-After header (delay in seconds, possibly
// fractional — Groq sends e.g. "2" or "1.5"). HTTP-date form and junk yield 0.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	return 0
}

// retryAfterOf pulls the vendor's Retry-After out of a rate-limit error (0 if the
// error isn't a rateLimitError or carried no hint).
func retryAfterOf(err error) time.Duration {
	var rl *rateLimitError
	if errors.As(err, &rl) {
		return rl.RetryAfter
	}
	return 0
}

// noteChainFailure routes a chain-walk error to the right recovery:
//   - model gone (vendor removed it) → self-heal the provider in the background.
//   - rate limit (429) → park the WHOLE provider (shared free quota) for the
//     vendor's Retry-After (capped), so we don't hammer every model on a dead key.
//   - anything else → trip just this model's breaker.
func (c *Client) noteChainFailure(s Slot, err error) {
	switch {
	case errors.Is(err, errModelNotFound):
		go c.healProvider(s.Provider)
	case errors.Is(err, errRateLimited):
		c.breaker.recordRateLimit(s.Provider, retryAfterOf(err))
	default:
		c.breaker.recordFailure(s.ID)
	}
}

// looksPaymentError checks if a failed response is due to insufficient balance
// (paid model with no credits) vs a genuine error. These should be recorded
// as "pago — sem saldo" rather than a hard failure, so the benchmark knows the
// model exists but couldn't be tested.
func looksPaymentError(status int, body string) bool {
	if status == http.StatusPaymentRequired || status == http.StatusForbidden {
		return true
	}
	b := strings.ToLower(body)
	for _, p := range []string{
		"insufficient", "insufficient_quota", "quota exceeded", "quota_exceeded",
		"payment required", "payment_required", "insufficient balance",
		"exceeded your current quota", "rate limit exceeded", "billing",
		"insufficient_credits", "not enough credits", "credit limit",
		"user_rate_limit_exceeded", "forbidden",
	} {
		if strings.Contains(b, p) {
			return true
		}
	}
	return false
}

// looksModelNotFound maps the "this model doesn't exist" responses across the
// vendors we use — each phrases it differently:
//   - Groq:       404, code "model_not_found", "... does not exist"
//   - OpenRouter: 400/404, "is not a valid model", "No endpoints found for model"
//   - Ollama:     404, "model '...' not found" / "try pulling it"
func looksModelNotFound(status int, body string) bool {
	if status == http.StatusNotFound {
		return true
	}
	b := strings.ToLower(body)
	for _, p := range []string{
		"model_not_found", "does not exist", "is not a valid model",
		"not a valid model id", "no endpoints found for model", "model not found",
		"no such model", "try pulling", "decommissioned", "has been deprecated",
	} {
		if strings.Contains(b, p) {
			return true
		}
	}
	return false
}

// httpResponseError classifies a /chat/completions response status into one of
// the error sentinels (rate limit, model-not-found, no-balance, bad-output) or a
// generic error. Returns nil for a 2xx status. Kept out of chat() so the request
// path stays simple (and under the cognitive-complexity gate).
func httpResponseError(s Slot, status int, raw string, retryAfter time.Duration) error {
	if status >= 200 && status < 300 {
		return nil
	}
	if status == http.StatusTooManyRequests {
		return &rateLimitError{slotID: s.ID, RetryAfter: retryAfter}
	}
	if looksModelNotFound(status, raw) {
		return fmt.Errorf("%w: %s/%s", errModelNotFound, s.Provider, s.Model)
	}
	if looksPaymentError(status, raw) {
		return fmt.Errorf("%w: %s/%s — sem saldo", errInsufficientBalance, s.Provider, s.Model)
	}
	// A 400 is the vendor rejecting the model's own output (e.g. Groq's
	// json_validate_failed) — a quality failure of this model, not a transient
	// infra error like a 5xx. Mark it so the benchmark scores it as a 0.
	if status == http.StatusBadRequest {
		return fmt.Errorf("%w: %s returned 400: %s", errBadOutput, s.ID, strings.TrimSpace(raw))
	}
	return fmt.Errorf("ai: %s returned %d: %s", s.ID, status, strings.TrimSpace(raw))
}

func (c *Client) chat(ctx context.Context, s Slot, system, user string, jsonMode bool) (content string, latency time.Duration, tokens int, err error) {
	reqBody := chatReq{
		Model:       s.Model,
		Temperature: 0, // deterministic — we want the same title every time
		MaxTokens:   maxOutputTokens,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	if jsonMode {
		reqBody.ResponseFormat = &respFormat{Type: "json_object"}
	}
	// gpt-oss are reasoning models: cap reasoning to "low" so the tiny JSON output
	// is reached fast (keeps latency down) and the token budget isn't burned on
	// chain-of-thought. Scoped to gpt-oss by name — Groq/OpenRouter honor it, and
	// we deliberately don't send it to other families (e.g. Qwen3 uses a different
	// reasoning knob, so a blanket value would be wrong there).
	if strings.Contains(s.Model, "gpt-oss") {
		reqBody.ReasoningEffort = "low"
	}
	body, _ := json.Marshal(reqBody)
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/chat/completions", bytes.NewReader(body))
	if reqErr != nil {
		return "", 0, 0, reqErr
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	// Respect the provider's requests/min cap BEFORE starting the clock, so the throttle
	// wait isn't charged to the model's measured latency. No-op for unthrottled providers.
	if e := c.limiter.reserve(ctx, s.Provider); e != nil {
		return "", 0, 0, e
	}
	start := time.Now()
	resp, doErr := c.http.Do(req)
	latency = time.Since(start)
	if doErr != nil {
		return "", latency, 0, doErr
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if e := httpResponseError(s, resp.StatusCode, string(raw), parseRetryAfter(resp.Header.Get("Retry-After"))); e != nil {
		return "", latency, 0, e
	}

	var cr chatResp
	if e := json.Unmarshal(raw, &cr); e != nil {
		return "", latency, 0, fmt.Errorf("ai: %s bad json: %w", s.ID, e)
	}
	if cr.Error != nil && cr.Error.Message != "" {
		return "", latency, 0, fmt.Errorf("ai: %s error: %s", s.ID, cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", latency, 0, fmt.Errorf("ai: %s returned no choices", s.ID)
	}
	return cr.Choices[0].Message.Content, latency, cr.Usage.TotalTokens, nil
}
