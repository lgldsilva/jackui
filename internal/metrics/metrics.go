package metrics

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

var (
	ActiveTorrents = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "jackui_active_torrents",
		Help: "Number of currently active torrent downloads",
	})

	TotalPeers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "jackui_torrent_peers_total",
		Help: "Total number of peers across all active torrents",
	})

	GlobalDownloadRate = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "jackui_network_download_bytes_per_sec",
		Help: "Current global download rate in bytes per second",
	})

	GlobalUploadRate = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "jackui_network_upload_bytes_per_sec",
		Help: "Current global upload rate in bytes per second",
	})

	ActiveTranscodeSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "jackui_active_transcode_sessions",
		Help: "Number of active HLS transcoding sessions",
	})

	CacheBytesUsed = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "jackui_cache_bytes_used",
		Help: "Total disk space used by the torrent stream cache in bytes",
	})
)

// StartWorker inicia o coletor periódico em background que consulta o streamer e o transcode.HLSSessionManager
func StartWorker(ctx context.Context, s *streamer.Streamer, hls *transcode.HLSSessionManager) {
	if s == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 1. Torrents ativos e peers
				active := s.ActiveList()
				ActiveTorrents.Set(float64(len(active)))

				var peers float64 = 0
				for _, t := range active {
					peers += float64(t.Peers)
				}
				TotalPeers.Set(peers)

				// 2. Taxas globais de rede
				stats := s.GlobalStats()
				GlobalDownloadRate.Set(float64(stats.DownRate))
				GlobalUploadRate.Set(float64(stats.UpRate))

				// 3. Sessões de transcode
				if hls != nil {
					ActiveTranscodeSessions.Set(float64(len(hls.Sessions())))
				} else {
					ActiveTranscodeSessions.Set(0)
				}

				// 4. Cache bytes
				if cStats, err := s.Stats(); err == nil {
					CacheBytesUsed.Set(float64(cStats.TotalSize))
				}
			}
		}
	}()
}
