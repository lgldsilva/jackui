package transmissionrpc

import (
	"fmt"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// ─── torrent-get ───────────────────────────────────────────────────────────

func (h *Handler) methodTorrentGet(args map[string]interface{}) rpcResponse {
	rawFields, _ := args["fields"].([]interface{})
	fieldSet := make(map[string]bool, len(rawFields))
	for _, f := range rawFields {
		if s, ok := f.(string); ok {
			fieldSet[s] = true
		}
	}
	if len(fieldSet) == 0 {
		for _, f := range defaultTorrentFields {
			fieldSet[f] = true
		}
	}

	idFilter := parseIDs(args["ids"])

	if h.store == nil {
		return successResp(map[string]interface{}{"torrents": []interface{}{}})
	}

	all, err := h.store.ListAll()
	if err != nil {
		return failResp(fmt.Sprintf(errListDownloads, err))
	}

	activeHashes := h.activeTorrentInfo(all)
	activeTorrentObjs := h.activeTorrentObjects(all)

	torrents := make([]interface{}, 0, len(all))
	for _, d := range all {
		if idFilter != nil && !idFilter[d.ID] {
			continue
		}
		si := activeHashes[d.InfoHash]
		to := activeTorrentObjs[d.InfoHash]
		t := h.buildTorrent(d, si, to, fieldSet)
		torrents = append(torrents, t)
	}

	return successResp(map[string]interface{}{"torrents": torrents})
}

// activeTorrentInfo resolve, por infoHash, os torrents ativos no streamer.
func (h *Handler) activeTorrentInfo(all []downloads.Download) map[string]*streamer.TorrentInfo {
	active := make(map[string]*streamer.TorrentInfo)
	if h.streamer == nil {
		return active
	}
	for _, d := range all {
		var hh metainfo.Hash
		if err := hh.FromHexString(d.InfoHash); err != nil {
			continue
		}
		if info, err := h.streamer.Get(hh); err == nil && info != nil {
			active[d.InfoHash] = info
		}
	}
	return active
}

var defaultTorrentFields = []string{
	"id", "hashString", "name", "status", "totalSize",
	"percentDone", "rateDownload", "rateUpload", "downloadDir",
	"addedDate", "doneDate", "error", "errorString",
	"leftUntilDone", "haveValid", "peersConnected",
	"eta", "isFinished", "isStalled", "labels", "trackers",
	"uploadRatio", "uploadedEver", "downloadedEver",
	"files", "fileStats", "fileCount",
	"magnetLink", "metadataPercentComplete", "trackerStats",
	"pieceCount", "pieceSize", "priorities", "wanted",
	"secondsDownloading", "secondsSeeding",
	"activityDate", "startDate", "sizeWhenDone",
	"recheckProgress", "bandwidthPriority",
	"comment", "creator", "dateCreated",
	"editDate", "honorsSessionLimits", "isPrivate",
	"peerLimit", "primaryMimeType", "sequentialDownload",
	"trackerList", "downloadLimit", "downloadLimited",
	"uploadLimit", "uploadLimited",
	"seedRatioLimit", "seedRatioMode",
	"seedIdleLimit", "seedIdleMode",
	"maxConnectedPeers", "peers",
}

// activeTorrentObjects resolve, por infoHash, os objetos *torrent.Torrent ativos.
func (h *Handler) activeTorrentObjects(all []downloads.Download) map[string]*torrent.Torrent {
	active := make(map[string]*torrent.Torrent)
	if h.streamer == nil {
		return active
	}
	client := h.streamer.Client()
	if client == nil {
		return active
	}
	for _, d := range all {
		var hh metainfo.Hash
		if err := hh.FromHexString(d.InfoHash); err != nil {
			continue
		}
		if t, ok := client.Torrent(hh); ok {
			active[d.InfoHash] = t
		}
	}
	return active
}

// torrentView agrega os valores derivados de um download (+ info do streamer)
// usados pra montar os campos do protocolo Transmission.
type torrentView struct {
	d                  downloads.Download
	trStatus           int
	downRate, upRate   int64
	peers              int
	seeders            int
	totalSize          int64
	labels             []string
	trackers           []interface{}
	trackerStats       []interface{}
	startTime          int64
	doneTime           int64
	addTime            int64
	editTime           int64
	downloadDir        string
	secondsDownloading int64
	secondsSeeding     int64
	files              []streamer.FileInfo
	trackerList        string
	magnetLink         string
	isPrivate          bool
	metadataComplete   float64
	primaryMimeType    string
	peerLimit          int
	sequentialDownload bool
	uploadedBytes      int64
	downloadedBytes    int64

	// Torrent object from anacrolix (may be nil when not active).
	// Used for pieces, peers, trackerStats that need deeper access.
	torrentObj *torrent.Torrent
}

func (h *Handler) newTorrentView(d downloads.Download, si *streamer.TorrentInfo, to *torrent.Torrent) torrentView {
	v := torrentView{
		d:           d,
		trStatus:    mapJackUIStatusToTR(d, si),
		addTime:     d.CreatedAt.Unix(),
		downloadDir: h.reportDir(d),
		isPrivate:   false,
		peerLimit:   50,
		torrentObj:  to,
	}
	if si != nil {
		v.downRate = si.DownRate
		v.upRate = si.UpRate
		v.peers = si.Peers
		v.seeders = si.Seeders
		v.totalSize = si.TotalSize
		v.files = si.Files
		v.metadataComplete = 1.0
		if si.Status == "paused" {
			v.sequentialDownload = true
		}
	}
	if v.totalSize <= 0 {
		v.totalSize = d.FileSize
	}
	if v.downloadDir == "" {
		v.downloadDir = h.dataDir
	}
	if v.files == nil {
		v.files = make([]streamer.FileInfo, 0)
	}

	if to != nil {
		stats := to.Stats()
		v.uploadedBytes = stats.BytesWrittenData.Int64()
		v.downloadedBytes = stats.BytesReadData.Int64()
	}

	// Build magnet link from existing magnet or infoHash.
	if d.Magnet != "" {
		v.magnetLink = d.Magnet
	} else if d.InfoHash != "" {
		v.magnetLink = magnetPrefix + d.InfoHash
	}

	v.labels = buildLabels(d)
	v.trackers, v.trackerStats, v.trackerList = buildTrackers(d, si)

	if d.StartedAt != nil {
		v.startTime = d.StartedAt.Unix()
	}
	if d.CompletedAt != nil {
		v.doneTime = d.CompletedAt.Unix()
	}
	v.editTime = v.doneTime
	if v.editTime == 0 {
		v.editTime = v.startTime
	}
	if v.editTime == 0 {
		v.editTime = v.addTime
	}

	v.secondsDownloading, v.secondsSeeding = elapsedSeconds(d)
	return v
}

func (h *Handler) buildTorrent(d downloads.Download, si *streamer.TorrentInfo, to *torrent.Torrent, fields map[string]bool) map[string]interface{} {
	v := h.newTorrentView(d, si, to)
	t := make(map[string]interface{})
	for field := range fields {
		if coreTorrentField(t, field, v) {
			continue
		}
		if aggTorrentField(t, field, v) {
			continue
		}
		extraTorrentField(t, field, v)
	}
	return t
}

// Builders and field-mapping functions are defined in views_builders.go
// and views_fields.go.
