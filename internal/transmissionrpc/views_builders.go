package transmissionrpc

import (
	"encoding/base64"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// ─── builder functions ─────────────────────────────────────────────────────

func buildPeers(v torrentView) []interface{} {
	if v.torrentObj != nil {
		swarm := v.torrentObj.KnownSwarm()
		peers := make([]interface{}, 0, len(swarm))
		for _, p := range swarm {
			addr := ""
			if p.Addr != nil {
				addr = p.Addr.String()
			}
			peers = append(peers, map[string]interface{}{
				"address":            addr,
				"clientName":         "",
				"clientIsChoked":     true,
				"clientIsInterested": false,
				"isDownloadingFrom":  false,
				"isEncrypted":        p.SupportsEncryption,
				"isIncoming":         false,
				"isUploadingTo":      false,
				"isUTP":              false,
				"peerId":             "",
				"peerIsChoked":       true,
				"peerIsInterested":   false,
				"port":               0,
				"progress":           0.0,
				"rateToClient":       0,
				"rateToPeer":         0,
			})
		}
		return peers
	}
	return []interface{}{}
}

func buildFiles(v torrentView) []interface{} {
	if len(v.files) == 0 {
		return []interface{}{
			map[string]interface{}{
				"begin_piece":    0,
				"bytesCompleted": v.d.BytesDownloaded,
				"end_piece":      1,
				"length":         v.totalSize,
				"name":           v.d.Name,
			},
		}
	}
	files := make([]interface{}, 0, len(v.files))
	for _, f := range v.files {
		files = append(files, map[string]interface{}{
			"begin_piece":    0,
			"bytesCompleted": f.Downloaded,
			"end_piece":      0,
			"length":         f.Size,
			"name":           f.Path,
		})
	}
	return files
}

func buildFileStats(v torrentView) []interface{} {
	if len(v.files) == 0 {
		return []interface{}{
			map[string]interface{}{
				"bytesCompleted": v.d.BytesDownloaded,
				"priority":       0,
				"wanted":         true,
			},
		}
	}
	stats := make([]interface{}, 0, len(v.files))
	for _, f := range v.files {
		priority := 0
		if f.Priority == "high" {
			priority = 1
		} else if f.Priority == "low" {
			priority = -1
		}
		stats = append(stats, map[string]interface{}{
			"bytesCompleted": f.Downloaded,
			"priority":       priority,
			"wanted":         f.Progress > 0 || f.Downloaded > 0,
		})
	}
	return stats
}

func buildPriorities(v torrentView) []interface{} {
	if len(v.files) == 0 {
		return []interface{}{0}
	}
	prios := make([]interface{}, 0, len(v.files))
	for _, f := range v.files {
		p := 0
		if f.Priority == "high" {
			p = 1
		} else if f.Priority == "low" {
			p = -1
		}
		prios = append(prios, p)
	}
	return prios
}

func buildWanted(v torrentView) []interface{} {
	if len(v.files) == 0 {
		return []interface{}{1}
	}
	wanted := make([]interface{}, 0, len(v.files))
	for _, f := range v.files {
		w := 1
		if f.Priority == "off" || (f.Size > 0 && f.Progress == 0 && f.Downloaded == 0) {
			w = 0
		}
		wanted = append(wanted, w)
	}
	return wanted
}

// buildPieces returns a base64-encoded bitfield of completed pieces.
func buildPieces(v torrentView) string {
	if v.torrentObj == nil {
		return ""
	}
	info := v.torrentObj.Info()
	if info == nil {
		return ""
	}
	numPieces := int(v.torrentObj.NumPieces())
	if numPieces == 0 {
		return ""
	}
	bits := make([]byte, (numPieces+7)/8)
	for i := 0; i < numPieces; i++ {
		ps := v.torrentObj.PieceState(i)
		if ps.Complete {
			bits[i/8] |= 1 << uint(i%8)
		}
	}
	return base64.StdEncoding.EncodeToString(bits)
}

// buildLabels constrói a lista de labels a partir da categoria e tracker.
func buildLabels(d downloads.Download) []string {
	labels := make([]string, 0)
	if d.Category != "" {
		labels = append(labels, d.Category)
	}
	if d.Tracker != "" && d.Tracker != d.Category {
		labels = append(labels, d.Tracker)
	}
	return labels
}

// buildTrackers constrói trackers, trackerStats e trackerList para um download.
func buildTrackers(d downloads.Download, si *streamer.TorrentInfo) (trackers, trackerStats []interface{}, trackerList string) {
	trackers = make([]interface{}, 0)
	trackerStats = make([]interface{}, 0)
	if d.Tracker == "" {
		return
	}
	trackerList = d.Tracker
	trackerURLs := []string{d.Tracker}
	if si != nil && len(si.Trackers) > 0 {
		trackerURLs = si.Trackers
	}
	trackerList = strings.Join(trackerURLs, "\n\n")
	for i, tr := range trackerURLs {
		trackers = append(trackers, map[string]interface{}{
			"announce": tr, "id": i, "scrape": "", "sitename": "", "tier": 0,
		})
		trackerStats = append(trackerStats, map[string]interface{}{
			"announce": tr, "announceState": 0, "downloadCount": 0,
			"downloaderCount": 0, "hasAnnounced": false, "hasScraped": false,
			"host": trackerHost(tr), "id": i, "isBackup": false,
			"lastAnnouncePeerCount": 0, "lastAnnounceResult": "",
			"lastAnnounceStartTime": 0, "lastAnnounceSucceeded": false,
			"lastAnnounceTime": 0, "lastAnnounceTimedOut": false,
			"lastScrapeResult": "", "lastScrapeStartTime": 0,
			"lastScrapeSucceeded": false, "lastScrapeTime": 0,
			"lastScrapeTimedOut": false, "leecherCount": 0,
			"nextAnnounceTime": 0, "nextScrapeTime": 0,
			"scrape": "", "scrapeState": 0, "seederCount": 0,
			"sitename": "", "tier": 0,
		})
	}
	return
}

func mapJackUIStatusToTR(d downloads.Download, si *streamer.TorrentInfo) int {
	if si != nil {
		switch si.Status {
		case "paused":
			return 0
		case "seeding":
			return 6
		case "downloading":
			if si.Progress > 0 {
				return 4
			}
			return 3
		}
	}

	switch d.Status {
	case downloads.StatusQueued:
		return 3
	case downloads.StatusDownloading:
		if d.Progress >= 1.0 {
			return 6
		}
		return 4
	case downloads.StatusCompleted:
		return 6
	case downloads.StatusPaused:
		return 0
	case downloads.StatusFailed:
		return 0
	default:
		return 0
	}
}

// trackerHost extracts the hostname from a tracker announce URL.
func trackerHost(announce string) string {
	if idx := strings.Index(announce, "://"); idx >= 0 {
		rest := announce[idx+3:]
		if idx2 := strings.IndexAny(rest, "/:"); idx2 >= 0 {
			return rest[:idx2]
		}
		return rest
	}
	return announce
}

// elapsedSeconds estima quanto tempo o download passou baixando e semeando.
func elapsedSeconds(d downloads.Download) (downloading, seeding int64) {
	now := time.Now().Unix()
	if d.Status == downloads.StatusCompleted && d.CompletedAt != nil {
		seeding = now - d.CompletedAt.Unix()
		if d.StartedAt != nil {
			downloading = int64(d.CompletedAt.Sub(*d.StartedAt).Seconds())
		}
	} else if d.StartedAt != nil {
		downloading = now - d.StartedAt.Unix()
	}
	if downloading < 0 {
		downloading = 0
	}
	if seeding < 0 {
		seeding = 0
	}
	return downloading, seeding
}

func computeETA(v torrentView) int {
	if v.downRate > 0 && v.totalSize > 0 {
		remaining := (v.totalSize - v.d.BytesDownloaded) / v.downRate
		return int(remaining)
	}
	return -1
}

func computeETAIdle(v torrentView) int {
	if v.upRate > 0 {
		return 0
	}
	return -1
}

func isStalled(d downloads.Download, v torrentView) bool {
	return d.Status == downloads.StatusDownloading && v.downRate == 0 && v.totalSize > 0 && v.d.BytesDownloaded < v.totalSize
}

func computeRatio(v torrentView) float64 {
	if v.downloadedBytes > 0 {
		return float64(v.uploadedBytes) / float64(v.downloadedBytes)
	}
	return 0.0
}

func queuePos(d downloads.Download) int {
	if d.Status == downloads.StatusCompleted || d.Status == downloads.StatusFailed {
		return 0
	}
	return d.ID
}
