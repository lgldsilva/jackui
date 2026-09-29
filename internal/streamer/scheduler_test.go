package streamer

import (
	"testing"
	"time"
)

func TestInTimeRange(t *testing.T) {
	tests := []struct {
		name     string
		now      time.Time
		rangeStr string
		expected bool
	}{
		{
			name:     "happy path inside the daytime range",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "08:00-18:00",
			expected: true,
		},
		{
			name:     "happy path outside the daytime range",
			now:      time.Date(2026, 6, 9, 19, 0, 0, 0, time.Local),
			rangeStr: "08:00-18:00",
			expected: false,
		},
		{
			name:     "inside the range that crosses midnight",
			now:      time.Date(2026, 6, 9, 23, 30, 0, 0, time.Local),
			rangeStr: "22:00-06:00",
			expected: true,
		},
		{
			name:     "inside the range that crosses midnight (early morning of the next day)",
			now:      time.Date(2026, 6, 9, 3, 15, 0, 0, time.Local),
			rangeStr: "22:00-06:00",
			expected: true,
		},
		{
			name:     "outside the range that crosses midnight",
			now:      time.Date(2026, 6, 9, 12, 0, 0, 0, time.Local),
			rangeStr: "22:00-06:00",
			expected: false,
		},
		{
			name:     "invalid range format",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "08:00_18:00",
			expected: false,
		},
		{
			name:     "invalid hours at the start",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "25:00-18:00",
			expected: false,
		},
		{
			name:     "invalid minutes at the end",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "08:00-18:65",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InTimeRange(tt.now, tt.rangeStr)
			if got != tt.expected {
				t.Errorf("InTimeRange(%v, %q) = %v; want %v", tt.now, tt.rangeStr, got, tt.expected)
			}
		})
	}
}
