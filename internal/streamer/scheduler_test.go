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
			name:     "caminho feliz dentro do intervalo diurno",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "08:00-18:00",
			expected: true,
		},
		{
			name:     "caminho feliz fora do intervalo diurno",
			now:      time.Date(2026, 6, 9, 19, 0, 0, 0, time.Local),
			rangeStr: "08:00-18:00",
			expected: false,
		},
		{
			name:     "dentro do intervalo que cruza meia-noite",
			now:      time.Date(2026, 6, 9, 23, 30, 0, 0, time.Local),
			rangeStr: "22:00-06:00",
			expected: true,
		},
		{
			name:     "dentro do intervalo que cruza meia-noite (madrugada do dia seguinte)",
			now:      time.Date(2026, 6, 9, 3, 15, 0, 0, time.Local),
			rangeStr: "22:00-06:00",
			expected: true,
		},
		{
			name:     "fora do intervalo que cruza meia-noite",
			now:      time.Date(2026, 6, 9, 12, 0, 0, 0, time.Local),
			rangeStr: "22:00-06:00",
			expected: false,
		},
		{
			name:     "formato de faixa invalido",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "08:00_18:00",
			expected: false,
		},
		{
			name:     "horas invalidas no inicio",
			now:      time.Date(2026, 6, 9, 10, 30, 0, 0, time.Local),
			rangeStr: "25:00-18:00",
			expected: false,
		},
		{
			name:     "minutos invalidos no fim",
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
