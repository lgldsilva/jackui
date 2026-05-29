package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luizg/jackui/internal/config"
)

// jsonChat replies as an OpenAI-compatible endpoint with the given message content.
func jsonChat(content string, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func clientForURL(t *testing.T, urls ...string) *Client {
	t.Helper()
	cfg := config.AIConfig{Enabled: true, Providers: map[string]config.AIProvider{}}
	for i, u := range urls {
		name := "p" + string(rune('0'+i))
		cfg.Providers[name] = config.AIProvider{BaseURL: u, APIKey: "k"}
		cfg.Chain = append(cfg.Chain, config.AIChainSlot{ID: name, Provider: name, Model: "m"})
	}
	c := New(cfg)
	if c == nil {
		t.Fatal("New returned nil")
	}
	return c
}

func TestIdentifyTitleParsesJSON(t *testing.T) {
	srv := httptest.NewServer(jsonChat(`{"title":"Inception","year":2010,"kind":"movie"}`, http.StatusOK))
	defer srv.Close()

	c := clientForURL(t, srv.URL)
	res, slot, err := c.IdentifyTitle(context.Background(), "Inception.2010.1080p.BluRay.x264")
	if err != nil {
		t.Fatalf("IdentifyTitle: %v", err)
	}
	if res.Title != "Inception" || res.Year != 2010 || res.Kind != "movie" {
		t.Fatalf("bad result: %+v", res)
	}
	if res.Query() != "Inception 2010" {
		t.Fatalf("Query = %q", res.Query())
	}
	if slot != "p0" {
		t.Fatalf("slot = %q", slot)
	}
}

func TestChainFallsThroughOnError(t *testing.T) {
	bad := httptest.NewServer(jsonChat("", http.StatusInternalServerError))
	defer bad.Close()
	good := httptest.NewServer(jsonChat(`{"title":"The Matrix","year":1999,"kind":"movie"}`, http.StatusOK))
	defer good.Close()

	c := clientForURL(t, bad.URL, good.URL)
	res, slot, err := c.IdentifyTitle(context.Background(), "The.Matrix.1999")
	if err != nil {
		t.Fatalf("IdentifyTitle: %v", err)
	}
	if res.Title != "The Matrix" || slot != "p1" {
		t.Fatalf("expected fallback to p1/The Matrix, got slot=%q res=%+v", slot, res)
	}
}

func TestMusicQuery(t *testing.T) {
	srv := httptest.NewServer(jsonChat("\"Disturbed The Sickness\"\nextra line", http.StatusOK))
	defer srv.Close()
	c := clientForURL(t, srv.URL)
	q := c.MusicQuery(context.Background(), "Disturbed - The Sickness 2000 [FLAC]")
	// First line, quotes stripped.
	if q != "Disturbed The Sickness" {
		t.Fatalf("MusicQuery = %q, want \"Disturbed The Sickness\"", q)
	}
}

func TestParseTitleJSONStripsFences(t *testing.T) {
	res, err := parseTitleJSON("Here you go:\n```json\n{\"title\": \"Dune\", \"year\": 2021}\n```\nHope it helps!")
	if err != nil {
		t.Fatalf("parseTitleJSON: %v", err)
	}
	if res.Title != "Dune" || res.Year != 2021 || res.Kind != "unknown" {
		t.Fatalf("bad parse: %+v", res)
	}
}

func TestBreakerOpensAfterThreshold(t *testing.T) {
	b := newBreaker()
	if !b.available("x") {
		t.Fatal("fresh slot should be available")
	}
	for i := 0; i < breakerFailureThreshold; i++ {
		b.recordFailure("x", false)
	}
	if b.available("x") {
		t.Fatal("slot should be open after reaching failure threshold")
	}
	b.recordSuccess("x")
	if !b.available("x") {
		t.Fatal("recordSuccess should close the breaker")
	}
}

func TestLooksModelNotFound(t *testing.T) {
	// EMPIRICALLY VERIFIED responses (probed each vendor with a bogus model):
	//   Groq:       404 code "model_not_found" "...does not exist or you do not have access to it"
	//   OpenRouter: 400 "<id> is not a valid model ID"
	//   Ollama:     404 "model '<id>' not found"
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{404, `{"error":{"message":"The model ` + "`x`" + ` does not exist or you do not have access to it.","type":"invalid_request_error","code":"model_not_found"}}`, true}, // groq (verified)
		{400, `{"error":{"message":"vendor/x:free is not a valid model ID","code":400}}`, true},                                                                                  // openrouter (verified)
		{404, `{"error":{"message":"model 'x:99b' not found","type":"not_found_error","code":null}}`, true},                                                                      // ollama (verified)
		{404, `{"error":{"message":"No endpoints found for model x"}}`, true},                                                                                                    // openrouter (alt)
		{500, `{"error":"internal server error"}`, false},                                                                                                                       // transient, NOT model-not-found
		{200, `ok`, false},
	}
	for _, tc := range cases {
		if got := looksModelNotFound(tc.status, tc.body); got != tc.want {
			t.Errorf("looksModelNotFound(%d, %q) = %v, want %v", tc.status, tc.body, got, tc.want)
		}
	}
}

// ── Property-based tests for error detection ──────────────────────────────────

func TestLooksPaymentErrorProperties(t *testing.T) {
	t.Run("402 e 403 sempre sao pagamento", func(t *testing.T) {
		bodies := []string{"", "ok", "forbidden", "payment required", "{}", "random text"}
		for _, b := range bodies {
			if !looksPaymentError(http.StatusPaymentRequired, b) {
				t.Errorf("looksPaymentError(402, %q) should be true", b)
			}
			if !looksPaymentError(http.StatusForbidden, b) {
				t.Errorf("looksPaymentError(403, %q) should be true", b)
			}
		}
	})

	t.Run("2xx e 5xx (exceto 402/403) nunca sao pagamento", func(t *testing.T) {
		for code := 200; code < 600; code++ {
			if code == 402 || code == 403 {
				continue
			}
			if looksPaymentError(code, "") {
				t.Errorf("looksPaymentError(%d, '') should be false", code)
			}
		}
	})

	t.Run("mensagens de erro conhecidas", func(t *testing.T) {
		cases := []string{
			`{"error":"insufficient_quota"}`,
			`{"error":"quota exceeded"}`,
			`{"error":"insufficient balance"}`,
			`{"error":"you have exceeded your current quota"}`,
			`{"error":"payment_required"}`,
			`rate limit exceeded`,
			`insufficient_credits`,
			`not enough credits`,
			`billing problem`,
			`user_rate_limit_exceeded`,
		}
		for _, body := range cases {
			if !looksPaymentError(http.StatusOK, body) {
				t.Errorf("looksPaymentError(200, %q) should detect payment error in body", body)
			}
		}
	})

	t.Run("mensagens normais nao sao pagamento", func(t *testing.T) {
		cases := []string{
			`{"choices":[{"message":{"content":"ok"}}]}`,
			`The model does not exist`,
			`internal server error`,
			`timeout`,
			`bad gateway`,
			`model not found`,
			`{"error":{"code":"model_not_found"}}`,
		}
		for _, body := range cases {
			if looksPaymentError(http.StatusOK, body) {
				t.Errorf("looksPaymentError(200, %q) should be false for normal messages", body)
			}
		}
	})
}

func TestLooksModelNotFoundProperties(t *testing.T) {
	t.Run("404 sempre e model-not-found", func(t *testing.T) {
		bodies := []string{"", "not found", "{}", "anything", "model xyz not found"}
		for _, b := range bodies {
			if !looksModelNotFound(http.StatusNotFound, b) {
				t.Errorf("looksModelNotFound(404, %q) should be true", b)
			}
		}
	})

	t.Run("2xx nunca e model-not-found", func(t *testing.T) {
		bodies := []string{"", "ok", `{"choices":[{"message":{"content":"hello"}}]}`, "200 OK success"}
		for code := 200; code < 300; code++ {
			for _, b := range bodies {
				if looksModelNotFound(code, b) {
					t.Errorf("looksModelNotFound(%d, %q) should be false", code, b)
				}
			}
		}
	})

	t.Run("mensagens de modelo inexistente", func(t *testing.T) {
		cases := []string{
			`model_not_found`,
			`does not exist`,
			`is not a valid model`,
			`not a valid model id`,
			`no endpoints found for model`,
			`model not found`,
			`no such model`,
			`try pulling`,
			`decommissioned`,
			`has been deprecated`,
		}
		for _, body := range cases {
			if !looksModelNotFound(http.StatusOK, body) {
				t.Errorf("looksModelNotFound(200, %q) should detect model-not-found", body)
			}
		}
	})
}

func TestRateLimitDetected(t *testing.T) {
	srv := httptest.NewServer(jsonChat("", http.StatusTooManyRequests))
	defer srv.Close()
	c := clientForURL(t, srv.URL)
	_, _, err := c.identifyWithSlot(context.Background(), c.slots[0], "whatever")
	if !isRateLimit(err) {
		t.Fatalf("expected rate-limit error, got %v", err)
	}
}

// ── Edge cases ──────────────────────────────────────────────────────────────

func TestNewEdgeCases(t *testing.T) {
	t.Run("nil when disabled", func(t *testing.T) {
		c := New(config.AIConfig{Enabled: false})
		if c != nil {
			t.Fatal("expected nil when disabled")
		}
	})

	t.Run("nil when empty chain", func(t *testing.T) {
		c := New(config.AIConfig{Enabled: true})
		if c != nil {
			t.Fatal("expected nil with empty chain")
		}
	})

	t.Run("nil when no slots resolve", func(t *testing.T) {
		c := New(config.AIConfig{Enabled: true, Chain: []config.AIChainSlot{
			{ID: "x", Provider: "nonexistent", Model: "m"},
		}})
		if c != nil {
			t.Fatal("expected nil when no provider resolves")
		}
	})

	t.Run("skips disabled slots", func(t *testing.T) {
		cfg := config.AIConfig{Enabled: true, Providers: map[string]config.AIProvider{
			"p": {BaseURL: "http://localhost", APIKey: "k"},
		}, Chain: []config.AIChainSlot{
			{ID: "a", Provider: "p", Model: "m1", Disabled: true},
			{ID: "b", Provider: "p", Model: "m2"},
		}}
		c := New(cfg)
		if c == nil {
			t.Fatal("expected non-nil")
		}
		if len(c.Slots()) != 1 || c.Slots()[0].ID != "b" {
			t.Fatalf("expected only slot b, got %+v", c.Slots())
		}
	})
}

func TestApplyChain(t *testing.T) {
	srv := httptest.NewServer(jsonChat(`{"title":"T","year":0,"kind":"movie"}`, http.StatusOK))
	defer srv.Close()
	c := clientForURL(t, srv.URL)
	if len(c.Slots()) != 1 {
		t.Fatalf("expected 1 slot, got %d", len(c.Slots()))
	}

	newDefs := []config.AIChainSlot{
		{ID: "new", Provider: "p0", Model: "newmodel"},
	}
	c.ApplyChain(newDefs)
	if len(c.Slots()) != 1 || c.Slots()[0].ID != "new" {
		t.Fatalf("ApplyChain failed: %+v", c.Slots())
	}

	t.Run("unresolvable defs keep chain unchanged", func(t *testing.T) {
		c.ApplyChain([]config.AIChainSlot{
			{ID: "gone", Provider: "nonexistent", Model: "x"},
		})
		if len(c.Slots()) != 1 || c.Slots()[0].ID != "new" {
			t.Fatalf("chain should be unchanged: %+v", c.Slots())
		}
	})
}

func TestIdentifyWithSlot(t *testing.T) {
	srv := httptest.NewServer(jsonChat(`{"title":"Inception","year":2010,"kind":"movie"}`, http.StatusOK))
	defer srv.Close()
	c := clientForURL(t, srv.URL)
	res, dur, err := c.IdentifyWithSlot(context.Background(), "p0", "Inception.2010")
	if err != nil {
		t.Fatalf("IdentifyWithSlot: %v", err)
	}
	if res.Title != "Inception" {
		t.Fatalf("title = %q", res.Title)
	}
	if dur <= 0 {
		t.Fatal("expected positive latency")
	}

	t.Run("unknown slot returns error", func(t *testing.T) {
		_, _, err := c.IdentifyWithSlot(context.Background(), "bogus", "x")
		if err == nil {
			t.Fatal("expected error for unknown slot")
		}
	})
}

func TestExtractRenameMetadata(t *testing.T) {
	successBody := `{"title":"Breaking Bad","year":2008,"kind":"tv","season":3,"episode":7,"episode_title":"One Minute"}`
	srv := httptest.NewServer(jsonChat(successBody, http.StatusOK))
	defer srv.Close()

	c := clientForURL(t, srv.URL)
	res, slot, err := c.ExtractRenameMetadata(context.Background(), "Breaking.Bad.S03E07.720p")
	if err != nil {
		t.Fatalf("ExtractRenameMetadata: %v", err)
	}
	if res.Title != "Breaking Bad" || res.Kind != "tv" || res.Season != 3 || res.Episode != 7 {
		t.Fatalf("bad result: %+v", res)
	}
	if slot != "p0" {
		t.Fatalf("slot = %q", slot)
	}

	t.Run("nil client returns error", func(t *testing.T) {
		_, _, err := (*Client)(nil).ExtractRenameMetadata(context.Background(), "x")
		if err == nil {
			t.Fatal("expected error from nil client")
		}
	})
}

func TestParseRenameJSON(t *testing.T) {
	t.Run("full movie metadata", func(t *testing.T) {
		res, err := parseRenameJSON(`{"title":"Inception","year":2010,"kind":"movie","season":0,"episode":0,"episode_title":""}`)
		if err != nil {
			t.Fatalf("parseRenameJSON: %v", err)
		}
		if res.Title != "Inception" || res.Year != 2010 || res.Kind != "movie" {
			t.Fatalf("bad result: %+v", res)
		}
	})

	t.Run("tv episode metadata", func(t *testing.T) {
		res, err := parseRenameJSON(`{"title":"Breaking Bad","year":2008,"kind":"tv","season":3,"episode":7}`)
		if err != nil {
			t.Fatalf("parseRenameJSON: %v", err)
		}
		if res.Title != "Breaking Bad" || res.Season != 3 || res.Episode != 7 {
			t.Fatalf("bad result: %+v", res)
		}
	})

	t.Run("code fences stripped", func(t *testing.T) {
		res, err := parseRenameJSON("```\n{\"title\":\"Dune\",\"year\":2021}\n```")
		if err != nil {
			t.Fatalf("parseRenameJSON fences: %v", err)
		}
		if res.Title != "Dune" {
			t.Fatalf("title = %q", res.Title)
		}
	})

	t.Run("unknown kind falls back to movie", func(t *testing.T) {
		res, err := parseRenameJSON(`{"title":"Test","year":0,"kind":"weird"}`)
		if err != nil {
			t.Fatalf("parseRenameJSON: %v", err)
		}
		if res.Kind != "movie" {
			t.Fatalf("expected movie fallback, got %q", res.Kind)
		}
	})

	t.Run("empty title falls back to generic parse", func(t *testing.T) {
		res, err := parseRenameJSON(`{"title":"","year":0}`)
		if err != nil {
			t.Fatalf("expected fallback, got error: %v", err)
		}
		if res.Title == "" {
			t.Fatal("expected non-empty title via fallback")
		}
	})

	t.Run("fallback to generic parse", func(t *testing.T) {
		res, err := parseRenameJSON(`The Matrix`)
		if err != nil {
			t.Fatalf("parseRenameJSON fallback: %v", err)
		}
		if res.Title != "The Matrix" {
			t.Fatalf("title = %q", res.Title)
		}
	})

	t.Run("garbage text returns error", func(t *testing.T) {
		_, err := parseRenameJSON("this is not a title at all and is far too long to be one and contains no useful information whatsoever for the parser to use as a fallback")
		if err == nil {
			t.Fatal("expected error for garbage")
		}
	})
}

func TestParseTitleJSONEdgeCases(t *testing.T) {
	t.Run("non-JSON fallback to text", func(t *testing.T) {
		res, err := parseTitleJSON("Inception")
		if err != nil {
			t.Fatalf("parseTitleJSON: %v", err)
		}
		if res.Title != "Inception" || res.Kind != "unknown" {
			t.Fatalf("bad result: %+v", res)
		}
	})

	t.Run("markdown fences stripped", func(t *testing.T) {
		res, err := parseTitleJSON("```json\n{\"title\":\"Dune\",\"year\":2021,\"kind\":\"movie\"}\n```")
		if err != nil {
			t.Fatalf("parseTitleJSON fences: %v", err)
		}
		if res.Title != "Dune" || res.Year != 2021 {
			t.Fatalf("bad result: %+v", res)
		}
	})

	t.Run("garbage returns error", func(t *testing.T) {
		_, err := parseTitleJSON("")
		if err == nil {
			t.Fatal("expected error for empty string")
		}
	})
}

func TestMusicQueryEdgeCases(t *testing.T) {
	t.Run("empty reply returns empty string", func(t *testing.T) {
		srv := httptest.NewServer(jsonChat("", http.StatusOK))
		defer srv.Close()
		c := clientForURL(t, srv.URL)
		q := c.MusicQuery(context.Background(), "Disturbed - The Sickness 2000 [FLAC]")
		if q != "" {
			t.Fatalf("expected empty, got %q", q)
		}
	})

	t.Run("multi-line with quoted text", func(t *testing.T) {
		srv := httptest.NewServer(jsonChat("\"Disturbed\"\nsome notes", http.StatusOK))
		defer srv.Close()
		c := clientForURL(t, srv.URL)
		q := c.MusicQuery(context.Background(), "Disturbed - The Sickness")
		if q != "Disturbed" {
			t.Fatalf("expected 'Disturbed', got %q", q)
		}
	})
}

func TestChatPaymentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"error":"insufficient balance"}`))
	}))
	defer srv.Close()

	c := clientForURL(t, srv.URL)
	_, _, err := c.identifyWithSlot(context.Background(), c.slots[0], "x")
	if !errors.Is(err, errInsufficientBalance) {
		t.Fatalf("expected errInsufficientBalance, got %v", err)
	}
}

func TestIdentifyWithSlotBadResult(t *testing.T) {
	srv := httptest.NewServer(jsonChat(`not json at all`, http.StatusOK))
	defer srv.Close()
	c := clientForURL(t, srv.URL)
	res, dur, err := c.IdentifyWithSlot(context.Background(), "p0", "x")
	if err != nil {
		t.Fatalf("should fall back to text extraction, got error: %v", err)
	}
	if res == nil || res.Title == "" {
		t.Fatal("expected fallback title from non-JSON response")
	}
	if dur <= 0 {
		t.Fatal("expected positive latency")
	}
}

func TestIdentifyTitleAllFail(t *testing.T) {
	fail := httptest.NewServer(jsonChat("", http.StatusInternalServerError))
	defer fail.Close()
	alsoFail := httptest.NewServer(jsonChat("", http.StatusInternalServerError))
	defer alsoFail.Close()

	c := clientForURL(t, fail.URL, alsoFail.URL) // two failing slots
	res, slot, err := c.IdentifyTitle(context.Background(), "test")
	if res != nil || slot != "" || err == nil {
		t.Fatalf("expected all fail: res=%v slot=%q err=%v", res, slot, err)
	}
}

func TestQueryEdgeCases(t *testing.T) {
	t.Run("nil result returns empty", func(t *testing.T) {
		var r *TitleResult
		if q := r.Query(); q != "" {
			t.Fatalf("expected empty, got %q", q)
		}
	})

	t.Run("empty title returns empty", func(t *testing.T) {
		r := &TitleResult{Title: "", Year: 0}
		if q := r.Query(); q != "" {
			t.Fatalf("expected empty, got %q", q)
		}
	})

	t.Run("year 0 omits year", func(t *testing.T) {
		r := &TitleResult{Title: "Inception", Year: 0}
		if q := r.Query(); q != "Inception" {
			t.Fatalf("expected 'Inception', got %q", q)
		}
	})

	t.Run("year included when >0", func(t *testing.T) {
		r := &TitleResult{Title: "Inception", Year: 2010}
		if q := r.Query(); q != "Inception 2010" {
			t.Fatalf("expected 'Inception 2010', got %q", q)
		}
	})
}

func TestChatErrorInResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":{"message":"provider error","code":"internal_error"}}`))
	}))
	defer srv.Close()

	c := clientForURL(t, srv.URL)
	_, _, err := c.identifyWithSlot(context.Background(), c.slots[0], "x")
	if err == nil || !strings.Contains(err.Error(), "provider error") {
		t.Fatalf("expected provider error, got %v", err)
	}
}

func TestChatModelNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"message":"model 'bogus' not found","type":"not_found_error"}}`))
	}))
	defer srv.Close()

	c := clientForURL(t, srv.URL)
	_, _, err := c.identifyWithSlot(context.Background(), c.slots[0], "x")
	if !errors.Is(err, errModelNotFound) {
		t.Fatalf("expected errModelNotFound, got %v", err)
	}
}

func TestExtractRenameFallsBack(t *testing.T) {
	bad := httptest.NewServer(jsonChat("", http.StatusInternalServerError))
	defer bad.Close()
	good := httptest.NewServer(jsonChat(`{"title":"Inception","year":2010,"kind":"movie"}`, http.StatusOK))
	defer good.Close()

	c := clientForURL(t, bad.URL, good.URL)
	res, slot, err := c.ExtractRenameMetadata(context.Background(), "Inception.2010")
	if err != nil {
		t.Fatalf("ExtractRenameMetadata: %v", err)
	}
	if res.Title != "Inception" {
		t.Fatalf("title = %q", res.Title)
	}
	if slot != "p1" {
		t.Fatalf("expected fallback to p1, got %q", slot)
	}
}

func TestMusicQuerySkipsBreaker(t *testing.T) {
	// First slot is rate-limited (breaker opens), second slot works
	rateLimited := httptest.NewServer(jsonChat("", http.StatusTooManyRequests))
	defer rateLimited.Close()
	works := httptest.NewServer(jsonChat(`"hello"`, http.StatusOK))
	defer works.Close()

	c := clientForURL(t, rateLimited.URL, works.URL)
	q := c.MusicQuery(context.Background(), "test")
	if q != "hello" {
		t.Fatalf("expected fallback to working slot, got %q", q)
	}
}

func TestRecordFailureRateLimit(t *testing.T) {
	b := newBreaker()
	// Rate-limit failure opens breaker for 3 min (shorter than 10 min normal)
	b.recordFailure("x", true)
	if b.available("x") {
		t.Fatal("rate-limit should open breaker immediately")
	}
	// Normal failure: first time still available (threshold=2)
	b.recordFailure("y", false)
	if !b.available("y") {
		t.Fatal("single normal failure should not open breaker")
	}
	b.recordFailure("y", false) // second failure opens
	if b.available("y") {
		t.Fatal("two failures should open breaker")
	}
	// Success closes breaker
	b.recordSuccess("y")
	if !b.available("y") {
		t.Fatal("success should close breaker")
	}
}

func TestResolveSlotEmptyID(t *testing.T) {
	srv := httptest.NewServer(jsonChat(`{"title":"T","year":0,"kind":"movie"}`, http.StatusOK))
	defer srv.Close()

	cfg := config.AIConfig{Enabled: true, Providers: map[string]config.AIProvider{
		"p": {BaseURL: srv.URL, APIKey: "k"},
	}, Chain: []config.AIChainSlot{{ID: "", Provider: "p", Model: "m"}}}
	c := New(cfg)
	if c == nil {
		t.Fatal("New nil")
	}
	if len(c.Slots()) != 1 || c.Slots()[0].ID != "p:m" {
		t.Fatalf("expected auto-id 'p:m', got %+v", c.Slots())
	}
}

func TestChatNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()
	c := clientForURL(t, srv.URL)
	_, _, err := c.identifyWithSlot(context.Background(), c.slots[0], "x")
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestChatGenericError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`{"error":"upstream failed"}`))
	}))
	defer srv.Close()

	c := clientForURL(t, srv.URL)
	_, _, err := c.identifyWithSlot(context.Background(), c.slots[0], "x")
	if err == nil {
		t.Fatal("expected error for 502")
	}
}

func TestChatBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{this is not json`))
	}))
	defer srv.Close()

	c := clientForURL(t, srv.URL)
	_, _, err := c.identifyWithSlot(context.Background(), c.slots[0], "x")
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
}
