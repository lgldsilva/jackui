package transmissionrpc

import (
	"fmt"
	"strings"

	"github.com/lgldsilva/jackui/internal/downloads"
)

// ─── torrent-set ───────────────────────────────────────────────────────────

func (h *Handler) methodTorrentSet(args map[string]interface{}) rpcResponse {
	if h.store == nil {
		return successResp(nil)
	}
	ids := parseIDs(args["ids"])

	all, err := h.store.ListAll()
	if err != nil {
		return failResp(fmt.Sprintf(errListDownloads, err))
	}

	// Transmission RPC: omitted/empty "ids" means apply to ALL torrents
	// (nil → match every row), like forEachDownload and the other methods.
	for _, d := range all {
		if ids == nil || ids[d.ID] {
			h.applyAllTorrentSetArgs(d, args)
		}
	}

	return successResp(nil)
}

func (h *Handler) applyAllTorrentSetArgs(d downloads.Download, args map[string]interface{}) {
	h.applyPausedArg(d, args["paused"])
	h.applyLabelsArg(d, args["labels"])
	h.applyBandwidthPriority(d, args["bandwidthPriority"])
	h.applySequentialDownload(d, args["sequentialDownload"])
	h.applyPeerLimit(d, args["peerLimit"])
	h.applyTrackerList(d, args["trackerList"])
	h.applyTrackerAdd(d, args["trackerAdd"])
	h.applyTrackerRemove(d, args["trackerRemove"])
	h.applyTrackerReplace(d, args["trackerReplace"])
	h.applySpeedLimits(d, args["downloadLimit"], args["downloadLimited"], args["uploadLimit"], args["uploadLimited"])
	h.applySeedRatio(d, args["seedRatioLimit"])
	h.applySeedIdle(d, args["seedIdleLimit"])
	h.applyQueuePosition(d, args["queuePosition"])
	h.applyHonorsSessionLimits(d, args["honorsSessionLimits"])
}

func (h *Handler) applyPausedArg(d downloads.Download, raw interface{}) {
	b, ok := raw.(bool)
	if !ok {
		return
	}
	if b {
		_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusPaused)
	} else if d.Status == downloads.StatusPaused {
		_ = h.store.SetStatus(d.UserID, d.ID, downloads.StatusDownloading)
	}
}

func (h *Handler) applyLabelsArg(d downloads.Download, raw interface{}) {
	labels, ok := raw.([]interface{})
	if !ok || len(labels) == 0 {
		return
	}
	cat, ok := labels[0].(string)
	if !ok {
		return
	}
	_ = h.store.SetCategory(d.UserID, d.ID, cat)
}

func (h *Handler) applyBandwidthPriority(d downloads.Download, raw interface{}) {
	v, ok := raw.(float64)
	if !ok || h.streamer == nil {
		return
	}
	hh, err := hashFromDownload(d)
	if err != nil {
		return
	}
	// Map Transmission priority (-1 low, 0 normal, 1 high) to streamer labels.
	label := "normal"
	switch int(v) {
	case -1:
		label = "low"
	case 1:
		label = "high"
	}
	_ = h.streamer.SetPriority(hh, label)
}

func (h *Handler) applySequentialDownload(d downloads.Download, raw interface{}) {
	enable, ok := raw.(bool)
	if !ok || h.streamer == nil {
		return
	}
	hh, err := hashFromDownload(d)
	if err != nil {
		return
	}
	client := h.streamer.Client()
	if client == nil {
		return
	}
	t, ok := client.Torrent(hh)
	if !ok {
		return
	}
	if enable {
		// Sequential: download pieces in order from 0.
		// Cancel all current piece requests, then request from beginning.
		num := int(t.NumPieces())
		t.CancelPieces(0, int(num))
		t.DownloadPieces(0, int(num))
	} else {
		t.DownloadAll()
	}
}

func (h *Handler) applyPeerLimit(d downloads.Download, raw interface{}) {
	v, ok := raw.(float64)
	if !ok || h.streamer == nil {
		return
	}
	hh, err := hashFromDownload(d)
	if err != nil {
		return
	}
	client := h.streamer.Client()
	if client == nil {
		return
	}
	if t, ok := client.Torrent(hh); ok {
		t.SetMaxEstablishedConns(int(v))
	}
}

func (h *Handler) applyTrackerList(d downloads.Download, raw interface{}) {
	list, ok := raw.(string)
	if !ok || list == "" {
		return
	}
	tiers := parseTrackerTiers(list)
	if len(tiers) == 0 {
		return
	}
	h.applyTrackerTiers(d, tiers)
}

// parseTrackerTiers converte uma string multi-tier do Transmission
// (tier 1, linha vazia, tier 2, ...) em [][]string.
func parseTrackerTiers(list string) [][]string {
	var tiers [][]string
	for _, block := range strings.Split(list, "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var urls []string
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				urls = append(urls, line)
			}
		}
		if len(urls) > 0 {
			tiers = append(tiers, urls)
		}
	}
	return tiers
}

func (h *Handler) applyTrackerTiers(d downloads.Download, tiers [][]string) {
	if h.streamer != nil {
		if hh, err := hashFromDownload(d); err == nil {
			if t, ok := h.streamer.Client().Torrent(hh); ok {
				t.ModifyTrackers(tiers)
			}
		}
	}
	var all []string
	for _, tier := range tiers {
		all = append(all, tier...)
	}
	if len(all) > 0 {
		_ = h.store.SetCategory(d.UserID, d.ID, all[0])
	}
}

func (h *Handler) applyTrackerAdd(d downloads.Download, raw interface{}) {
	urls, ok := raw.([]interface{})
	if !ok || len(urls) == 0 {
		return
	}
	var announceList [][]string
	for _, u := range urls {
		if s, ok := u.(string); ok && s != "" {
			announceList = append(announceList, []string{s})
		}
	}
	if len(announceList) == 0 || h.streamer == nil {
		return
	}
	if hh, err := hashFromDownload(d); err == nil {
		client := h.streamer.Client()
		if t, ok := client.Torrent(hh); ok {
			t.AddTrackers(announceList)
		}
	}
}

func (h *Handler) applyTrackerRemove(d downloads.Download, raw interface{}) {
	ids, ok := raw.([]interface{})
	if !ok || len(ids) == 0 {
		return
	}
	// trackerRemove: array of tracker IDs to remove.
	// We can't remove individual trackers via anacrolix API directly,
	// but we can read the current list and rebuild without the removed IDs.
	_ = ids
}

func (h *Handler) applyTrackerReplace(d downloads.Download, raw interface{}) {
	pairs, ok := raw.([]interface{})
	if !ok || len(pairs) == 0 {
		return
	}
	// trackerReplace: array of [trackerId, newUrl] pairs.
	var announceList [][]string
	for _, pair := range pairs {
		p, ok := pair.([]interface{})
		if !ok || len(p) < 2 {
			continue
		}
		url, ok := p[1].(string)
		if !ok || url == "" {
			continue
		}
		announceList = append(announceList, []string{url})
	}
	if len(announceList) == 0 || h.streamer == nil {
		return
	}
	if hh, err := hashFromDownload(d); err == nil {
		client := h.streamer.Client()
		if t, ok := client.Torrent(hh); ok {
			t.AddTrackers(announceList)
		}
	}
}

func (h *Handler) applySeedRatio(d downloads.Download, raw interface{}) {
	// seedRatioLimit + seedRatioMode are accepted but not enforced.
	// The download worker doesn't stop seeding by ratio.
}

func (h *Handler) applySeedIdle(d downloads.Download, raw interface{}) {
	// seedIdleLimit + seedIdleMode are accepted but not enforced.
}

func (h *Handler) applyQueuePosition(d downloads.Download, raw interface{}) {
	// Queue position is best-effort: lower queuePosition = higher priority.
	// The store doesn't have a dedicated column, so we use ID as proxy.
}

func (h *Handler) applyHonorsSessionLimits(d downloads.Download, raw interface{}) {
	// When false, the torrent ignores global speed limits.
	// Not directly supported by anacrolix; we accept but don't enforce.
}

func (h *Handler) applySpeedLimits(d downloads.Download, dlRaw, dlLimited, ulRaw, ulLimited interface{}) {
	// Per-torrent speed limits. The anacrolix library v1.61 does not support
	// per-torrent bandwidth caps. Values are accepted for *arr compatibility
	// but not enforced at torrent level — global limits apply instead.
}
