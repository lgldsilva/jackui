package handlers

import (
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

func TestDownloadsBatchStopSeed_MissingIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	w := invokeCoverageHandler(t, DownloadsBatchStopSeed(&downloads.Store{}, s), http.MethodPost, "/api/downloads/batch/stop-seed", `{"ids":[]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsBatchStopSeed_TooMany(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	ids := make([]int, downloadsStopSeedBatchMax+1)
	body := `{"ids":[`
	for i, id := range ids {
		if i > 0 {
			body += ","
		}
		body += strconv.Itoa(id)
	}
	body += `]}`
	w := invokeCoverageHandler(t, DownloadsBatchStopSeed(&downloads.Store{}, s), http.MethodPost, "/api/downloads/batch/stop-seed", body)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}



func TestGetDownloadFileStat(t *testing.T) {
	if got := getDownloadFileStat(""); got.Exists || got.Apparent != 0 || got.OnDisk != 0 {
		t.Errorf("empty path = %+v", got)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	writeFile(t, p, []byte("hello"))
	got := getDownloadFileStat(p)
	if !got.Exists || got.Apparent != 5 {
		t.Errorf("existing file = %+v", got)
	}
}

func TestGetDownloadTorrentInfo_InvalidHash(t *testing.T) {
	s := streamer.NewForTesting()
	info := getDownloadTorrentInfo(s, "invalid", "")
	if info != nil {
		t.Errorf("invalid hash = %+v", info)
	}
}

func TestGetDownloadTorrentInfo_MagnetTrackers(t *testing.T) {
	s := streamer.NewForTesting()
	magnet := "magnet:?xt=urn:btih:dead&tr=http://tracker1&tr=http://tracker2"
	info := getDownloadTorrentInfo(s, "invalid", magnet)
	if info == nil || len(info.Trackers) != 2 {
		t.Errorf("magnet trackers = %+v", info)
	}
}

func TestDownloadsPeers_InvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	params := gin.Params{{Key: "id", Value: "not-an-id"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsPeers(&downloads.Store{}, s), http.MethodGet, "/api/downloads/not-an-id/peers", "", params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsDetails_InvalidID_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	params := gin.Params{{Key: "id", Value: "not-an-id"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsDetails(&downloads.Store{}, s), http.MethodGet, "/api/downloads/not-an-id/details", "", params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDownloadsRecheck_InvalidID_NewCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := streamer.NewForTesting()
	params := gin.Params{{Key: "id", Value: "not-an-id"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsRecheck(&downloads.Store{}, s), http.MethodPost, "/api/downloads/not-an-id/recheck", "", params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
