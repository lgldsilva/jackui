package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
