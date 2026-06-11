package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/ai"
	"github.com/lgldsilva/jackui/internal/config"
)

// aiChatStub serves an OpenAI-compatible /chat/completions whose single message
// content is the given string (or a bare error status when status != 200).
func aiChatStub(t *testing.T, content string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func aiClientFor(t *testing.T, baseURL string) *ai.Client {
	t.Helper()
	c := ai.New(config.AIConfig{
		Enabled:   true,
		Providers: map[string]config.AIProvider{"t": {BaseURL: baseURL, APIKey: "k"}},
		Chain:     []config.AIChainSlot{{ID: "t:m", Provider: "t", Model: "m"}},
	})
	if c == nil {
		t.Fatal("ai.New returned nil")
	}
	return c
}

func postScheduleParse(t *testing.T, client *ai.Client, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/watchlists/schedule/parse", WatchlistScheduleParse(client))
	req := httptest.NewRequest("POST", "/api/watchlists/schedule/parse", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestWatchlistScheduleParse_AIDisabled(t *testing.T) {
	w := postScheduleParse(t, nil, `{"text":"toda segunda às 9h"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestWatchlistScheduleParse_EmptyText(t *testing.T) {
	srv := aiChatStub(t, `{"kind":"daily"}`, http.StatusOK)
	client := aiClientFor(t, srv.URL)
	for _, body := range []string{`{"text":""}`, `{"text":"   "}`, `{}`, `not json`} {
		if w := postScheduleParse(t, client, body); w.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, w.Code)
		}
	}
}

func TestWatchlistScheduleParse_InvalidPhrase(t *testing.T) {
	srv := aiChatStub(t, `{"kind":"invalid","minutes":0,"weekday":0,"hour":0,"minute":0}`, http.StatusOK)
	w := postScheduleParse(t, aiClientFor(t, srv.URL), `{"text":"banana azul"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422; body: %s", w.Code, w.Body.String())
	}
}

func TestWatchlistScheduleParse_GarbageReply(t *testing.T) {
	srv := aiChatStub(t, "no json whatsoever", http.StatusOK)
	w := postScheduleParse(t, aiClientFor(t, srv.URL), `{"text":"a cada 3 horas"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422; body: %s", w.Code, w.Body.String())
	}
}

func TestWatchlistScheduleParse_ChainDown(t *testing.T) {
	srv := aiChatStub(t, "", http.StatusInternalServerError)
	w := postScheduleParse(t, aiClientFor(t, srv.URL), `{"text":"toda segunda às 9h"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}

func TestWatchlistScheduleParse_Success(t *testing.T) {
	srv := aiChatStub(t, `{"kind":"weekly","minutes":0,"weekday":1,"hour":9,"minute":0}`, http.StatusOK)
	w := postScheduleParse(t, aiClientFor(t, srv.URL), `{"text":"toda segunda às 9h"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["schedKind"] != "weekly" || out["schedWeekday"] != float64(1) || out["schedHour"] != float64(9) {
		t.Errorf("bad schedule shape: %v", out)
	}
}

func TestWatchlistScheduleParse_ClampsViaNormalized(t *testing.T) {
	// A hallucinated hour 30 / weekday 9 must come back clamped (the same
	// Normalized() path the store uses), never leak raw model output.
	srv := aiChatStub(t, `{"kind":"weekly","minutes":0,"weekday":9,"hour":30,"minute":99}`, http.StatusOK)
	w := postScheduleParse(t, aiClientFor(t, srv.URL), `{"text":"dia 32 às 30h"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["schedWeekday"] != float64(0) || out["schedHour"] != float64(0) || out["schedMinute"] != float64(0) {
		t.Errorf("expected clamped schedule, got: %v", out)
	}
}

func TestWatchlistScheduleParse_TextTooLong(t *testing.T) {
	// Bounded prompt: oversized free-text must be rejected before reaching the
	// AI provider (token cost), and the stub must never be hit.
	srv := aiChatStub(t, `{"kind":"interval","minutes":60}`, http.StatusOK)
	long := strings.Repeat("a", maxScheduleTextLen+1)
	w := postScheduleParse(t, aiClientFor(t, srv.URL), `{"text":"`+long+`"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
}

func TestWatchlistScheduleParse_DisabledCodeInBody(t *testing.T) {
	// The frontend hides the AI field only on code=ai_disabled; transient chain
	// failures keep it visible — the codes must stay distinguishable.
	w := postScheduleParse(t, nil, `{"text":"toda segunda"}`)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "ai_disabled") {
		t.Fatalf("want 503 with code ai_disabled, got %d: %s", w.Code, w.Body.String())
	}
}
