package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
)

// Regression: the settings handlers used to assign cfg.Stream / cfg.Jackett /
// cfg.DownloadClients / cfg.DownloadsQueue / cfg.External.Mounts in place with
// no lock while other requests (and the bandwidth scheduler goroutine) read the
// same fields. Run under -race: every writer and reader below must go through
// the config lock. (The scheduler and HLS readers have their own tests.)
func TestConfigHandlers_ConcurrentWritesAndReadsAreRaceFree(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, path := newTestConfig(t)

	r := gin.New()
	r.PUT("/stream", StreamUpdateSettings(cfg, path, nil))
	r.GET("/stream", StreamGetSettings(cfg, nil))
	r.PUT("/config", UpdateConfig(cfg, path, nil, nil))
	r.GET("/config", GetConfig(cfg, path))
	r.GET("/clients", GetClients(cfg))
	r.POST("/download", Download(cfg))
	r.PUT("/queue", DownloadsUpdateSettings(cfg, path, nil))
	r.GET("/queue", DownloadsGetSettings(cfg))
	r.PUT("/mounts", MountsUpdate(cfg, path, nil))
	r.GET("/mounts", MountsGet(cfg))
	r.GET("/mode", func(c *gin.Context) { c.String(http.StatusOK, transferMode(cfg)) })

	calls := []struct{ method, target, body string }{
		{"PUT", "/stream", `{"storageBackend":"file","maxDownloadRate":1024,"seedTrackers":["a","b"],"hlsMediaRenditions":true}`},
		{"GET", "/stream", ""},
		{"PUT", "/config", `{"port":8989,"jackett":{"url":"http://127.0.0.1:1","apiKey":"k"},"downloadClients":[{"id":"x","name":"x","type":"bogus","url":"http://127.0.0.1:1","default":true}]}`},
		{"GET", "/config", ""},
		{"GET", "/clients", ""},
		{"POST", "/download", `{"magnetUri":"magnet:?xt=urn:btih:aa"}`},
		{"PUT", "/queue", `{"maxActive":2,"maxConcurrentVerify":1,"stallThresholdMin":5,"maxStalls":2,"transferConcurrencyMode":"serial"}`},
		{"GET", "/queue", ""},
		{"PUT", "/mounts", `[{"name":"m","path":"/m"}]`},
		{"GET", "/mounts", ""},
		{"GET", "/mode", ""},
	}

	var wg sync.WaitGroup
	for _, call := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Every PUT fsyncs config.yaml; a handful is enough to overlap the reads.
			n := 40
			if call.method == "PUT" {
				n = 6
			}
			for i := 0; i < n; i++ {
				req := httptest.NewRequest(call.method, call.target, bytes.NewBufferString(call.body))
				req.Header.Set("Content-Type", "application/json")
				r.ServeHTTP(httptest.NewRecorder(), req)
			}
		}()
	}
	wg.Wait()

	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.yaml unreadable after concurrent saves: %v", err)
	}
	if reloaded.DownloadsQueue.MaxActive != 2 || len(reloaded.External.Mounts) != 1 {
		t.Errorf("concurrent saves lost an update: queue=%+v mounts=%+v",
			reloaded.DownloadsQueue, reloaded.External.Mounts)
	}
}

func TestResolveDownloadClient_NoDefaultFallsBackToFirst(t *testing.T) {
	cfg := &config.Config{DownloadClients: []config.DownloadClient{{ID: "a"}, {ID: "b"}}}
	if dc := resolveDownloadClient(cfg, ""); dc == nil || dc.ID != "a" {
		t.Errorf("got %+v, want the first client", dc)
	}
}

func TestDownloadsUpdateSettings_AutoTransferModePersistsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, path := newTestConfig(t)
	cfg.Stream.TransferConcurrencyMode = "serial"
	r := gin.New()
	r.PUT("/queue", DownloadsUpdateSettings(cfg, path, nil))

	body := `{"maxActive":2,"maxConcurrentVerify":1,"stallThresholdMin":5,"maxStalls":2,"transferConcurrencyMode":"auto"}`
	req := httptest.NewRequest("PUT", "/queue", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if got := cfg.StreamSnapshot().TransferConcurrencyMode; got != "" {
		t.Errorf("mode = %q, want empty (auto)", got)
	}
}
