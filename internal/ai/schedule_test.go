package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
