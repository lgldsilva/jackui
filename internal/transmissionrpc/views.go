package transmissionrpc

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

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

// coreTorrentField popula os campos mais comuns (defaultTorrentFields) —
// campos escalares simples sem iteração ou agregação.
func coreTorrentField(t map[string]interface{}, field string, v torrentView) bool {
	d := v.d
	switch field {
	case "id":
		t["id"] = d.ID
	case "hashString":
		t["hashString"] = d.InfoHash
	case "name":
		t["name"] = d.Name
	case "status":
		t["status"] = v.trStatus
	case "totalSize":
		t["totalSize"] = v.totalSize
	case "percentDone":
		t["percentDone"] = d.Progress
	case "rateDownload":
		t["rateDownload"] = v.downRate
	case "rateUpload":
		t["rateUpload"] = v.upRate
	case "downloadDir":
		t["downloadDir"] = v.downloadDir
	case "addedDate":
		t["addedDate"] = v.addTime
	case "doneDate":
		t["doneDate"] = v.doneTime
	case "error":
		errCode := 0
		if d.Status == downloads.StatusFailed && d.Error != "" {
			errCode = 1
		}
		t["error"] = errCode
	case "errorString":
		t["errorString"] = d.Error
	case "leftUntilDone":
		left := v.totalSize - d.BytesDownloaded
		if left < 0 {
			left = 0
		}
		t["leftUntilDone"] = left
	case "haveValid":
		t["haveValid"] = d.BytesDownloaded
	case "peersConnected":
		t["peersConnected"] = v.peers
	case "eta":
		t["eta"] = computeETA(v)
	case "etaIdle":
		t["etaIdle"] = computeETAIdle(v)
	case "isFinished":
		t["isFinished"] = d.Status == downloads.StatusCompleted
	case "isStalled":
		t["isStalled"] = isStalled(d, v)
	case "labels":
		t["labels"] = v.labels
	case "trackers":
		t["trackers"] = v.trackers
	case "uploadRatio":
		t["uploadRatio"] = computeRatio(v)
	case "uploadedEver":
		t["uploadedEver"] = v.uploadedBytes
	case "downloadedEver":
		t["downloadedEver"] = v.downloadedBytes
	case "queuePosition":
		t["queuePosition"] = queuePos(d)
	default:
		return false
	}
	return true
}

// aggTorrentField popula campos que exigem agregação ou chamada de build*().
// Separado de coreTorrentField para manter cada switch <30 branches (S1479).
func aggTorrentField(t map[string]interface{}, field string, v torrentView) bool {
	d := v.d
	switch field {
	case "magnetLink":
		t["magnetLink"] = v.magnetLink
	case "metadataPercentComplete":
		t["metadataPercentComplete"] = v.metadataComplete
	case "editDate":
		t["editDate"] = v.editTime
	case "fileCount":
		t["fileCount"] = len(v.files)
	case "percentComplete":
		t["percentComplete"] = d.Progress
	case "peers":
		t["peers"] = buildPeers(v)
	case "trackerList":
		t["trackerList"] = v.trackerList
	case "trackerStats":
		t["trackerStats"] = v.trackerStats
	case "files":
		t["files"] = buildFiles(v)
	case "fileStats":
		t["fileStats"] = buildFileStats(v)
	case "priorities":
		t["priorities"] = buildPriorities(v)
	case "wanted":
		t["wanted"] = buildWanted(v)
	case "pieces":
		t["pieces"] = buildPieces(v)
	case "availability":
		t["availability"] = []interface{}{}
	default:
		return false
	}
	return true
}

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

// extraTorrentField popula os campos opcionais/menos usados do protocolo.
// Os campos mais comuns são tratados em coreTorrentField.
func extraTorrentField(t map[string]interface{}, field string, v torrentView) {
	d := v.d
	switch field {
	case "activityDate":
		t["activityDate"] = v.startTime
	case "corruptEver":
		t["corruptEver"] = 0
	case "desiredAvailable":
		t["desiredAvailable"] = d.BytesDownloaded
	case "haveUnchecked":
		t["haveUnchecked"] = 0
	case "peersGettingFromUs":
		t["peersGettingFromUs"] = 0
	case "peersSendingToUs":
		t["peersSendingToUs"] = v.peers
	case "peersFrom":
		t["peersFrom"] = map[string]interface{}{
			"fromCache": 0, "fromDht": 0, "fromIncoming": 0,
			"fromLpd": 0, "fromLtep": 0, "fromPex": 0, "fromTracker": 0,
		}
	case "seedRatioLimit":
		t["seedRatioLimit"] = 2.0
	case "seedRatioMode":
		t["seedRatioMode"] = 0
	case "seedIdleLimit":
		t["seedIdleLimit"] = 0
	case "seedIdleMode":
		t["seedIdleMode"] = 0
	case "sizeWhenDone":
		t["sizeWhenDone"] = v.totalSize
	case "startDate":
		t["startDate"] = v.startTime
	case "torrentFile":
		t["torrentFile"] = ""
	case "maxConnectedPeers":
		t["maxConnectedPeers"] = v.peerLimit
	case "bandwidthPriority":
		t["bandwidthPriority"] = 0
	case "recheckProgress":
		t["recheckProgress"] = 0.0
	case "secondsDownloading":
		t["secondsDownloading"] = v.secondsDownloading
	case "secondsSeeding":
		t["secondsSeeding"] = v.secondsSeeding
	case "comment":
		t["comment"] = ""
	case "creator":
		t["creator"] = ""
	case "dateCreated":
		t["dateCreated"] = 0
	case "pieceCount":
		t["pieceCount"] = 0
	case "pieceSize":
		t["pieceSize"] = 0
	default:
		extraTorrentFieldSettings(t, field, v)
	}
}

// extraTorrentFieldSettings trata campos de configuração/limites — separado
// de extraTorrentField para manter cada switch <30 branches (S1479).
func extraTorrentFieldSettings(t map[string]interface{}, field string, v torrentView) {
	switch field {
	case "honorsSessionLimits":
		t["honorsSessionLimits"] = true
	case "isPrivate":
		t["isPrivate"] = v.isPrivate
	case "peerLimit":
		t["peerLimit"] = v.peerLimit
	case "primaryMimeType":
		t["primaryMimeType"] = v.primaryMimeType
	case "sequentialDownload":
		t["sequentialDownload"] = v.sequentialDownload
	case "downloadLimit":
		t["downloadLimit"] = 0
	case "downloadLimited":
		t["downloadLimited"] = false
	case "uploadLimit":
		t["uploadLimit"] = 0
	case "uploadLimited":
		t["uploadLimited"] = false
	case "bytesCompleted":
		bs := make([]int64, len(v.files))
		for i, f := range v.files {
			bs[i] = f.Downloaded
		}
		t["bytesCompleted"] = bs
	case "webseeds":
		t["webseeds"] = []string{}
	case "webseedsSendingToUs":
		t["webseedsSendingToUs"] = 0
	case "group":
		t["group"] = ""
	case "manualAnnounceTime":
		t["manualAnnounceTime"] = 0
	}
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
