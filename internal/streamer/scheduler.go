package streamer

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/config"
)

// InTimeRange verifica se a hora atual cai dentro de um time_range como "08:00-18:00".
// Suporta faixas que viram a noite, ex: "22:00-06:00".
func InTimeRange(now time.Time, rangeStr string) bool {
	parts := strings.Split(rangeStr, "-")
	if len(parts) != 2 {
		return false
	}
	startStr, endStr := parts[0], parts[1]

	sh, sm, err1 := parseHourMin(startStr)
	eh, em, err2 := parseHourMin(endStr)
	if err1 != nil || err2 != nil {
		return false
	}

	// Hora do dia em minutos
	currentMinutes := now.Hour()*60 + now.Minute()
	startMinutes := sh*60 + sm
	endMinutes := eh*60 + em

	if startMinutes <= endMinutes {
		return currentMinutes >= startMinutes && currentMinutes <= endMinutes
	} else {
		// Intervalo cruza a meia-noite (ex: 22:00-06:00)
		return currentMinutes >= startMinutes || currentMinutes <= endMinutes
	}
}

func parseHourMin(s string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid time format")
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("invalid time digits")
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid time values")
	}
	return h, m, nil
}

// StartBandwidthScheduler inicia o loop periódico em background que aplica as regras de limites de banda baseadas no horário.
func StartBandwidthScheduler(ctx context.Context, s *Streamer, cfg *config.Config) {
	if s == nil || len(cfg.Stream.BandwidthSchedules) == 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		lastDown, lastUp := int64(-2), int64(-2) // Valores dummy iniciais

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				matched := false
				var targetDown, targetUp int64

				for _, sched := range cfg.Stream.BandwidthSchedules {
					if InTimeRange(now, sched.TimeRange) {
						targetDown = sched.MaxDownloadRate
						targetUp = sched.MaxUploadRate
						matched = true
						break
					}
				}

				if !matched {
					targetDown = cfg.Stream.MaxDownloadRate
					targetUp = cfg.Stream.MaxUploadRate
				}

				// Só atualiza os limites se eles mudaram de fato para evitar concorrência desnecessária no streamer.
				if targetDown != lastDown || targetUp != lastUp {
					s.SetRateLimits(targetDown, targetUp)
					lastDown = targetDown
					lastUp = targetUp
					log.Printf("[BandwidthScheduler] Limites de banda atualizados para download=%d B/s, upload=%d B/s (horário atual: %02d:%02d)", targetDown, targetUp, now.Hour(), now.Minute())
				}
			}
		}
	}()
}
