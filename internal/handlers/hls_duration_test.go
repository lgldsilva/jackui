package handlers

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestPickKnownDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ok   bool
		sec  float64
		want float64
	}{
		{"cache miss", false, 3600, 0},
		{"unknown duration", true, 0, 0},
		{"negative", true, -1, 0},
		{"known", true, 142.5, 142.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickKnownDuration(streamer.ProbeResult{DurationSec: tc.sec}, tc.ok)
			if got != tc.want {
				t.Fatalf("pickKnownDuration(%v, %v) = %v, want %v", tc.sec, tc.ok, got, tc.want)
			}
		})
	}
}
