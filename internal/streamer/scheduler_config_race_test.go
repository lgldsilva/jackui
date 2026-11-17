package streamer

import (
	"sync"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/config"
)

// Run under -race: the scheduler goroutine resolves its targets from cfg.Stream
// while PUT /api/stream/settings rewrites that section.
func TestResolveBandwidthTargets_ConcurrentWithSettingsUpdate(t *testing.T) {
	cfg := &config.Config{}
	cfg.Stream.BandwidthSchedules = []config.BandwidthSchedule{{TimeRange: "not-a-range"}}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := int64(1); i <= 500; i++ {
			cfg.Update(func(c *config.Config) {
				c.Stream.MaxDownloadRate, c.Stream.MaxUploadRate = i, i
				c.Stream.SeedTrackers = []string{"t"}
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if down, up := resolveBandwidthTargets(cfg, time.Now()); down != up {
				t.Errorf("torn read: down=%d up=%d", down, up)
				return
			}
		}
	}()
	wg.Wait()

	if down, up := resolveBandwidthTargets(cfg, time.Now()); down != 500 || up != 500 {
		t.Errorf("targets = %d/%d, want 500/500", down, up)
	}
}
