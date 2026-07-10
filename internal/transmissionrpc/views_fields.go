package transmissionrpc

import "github.com/lgldsilva/jackui/internal/downloads"

// ─── field mapping switches ────────────────────────────────────────────────

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
