package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/luizg/jackui/internal/config"
)

func makeTestConfig(clients ...config.DownloadClient) *config.Config {
	cfg := &config.Config{Port: 8989}
	cfg.Jackett.URL = "http://localhost:9117"
	cfg.DownloadClients = clients
	return cfg
}

func postJSON(t *testing.T, router *gin.Engine, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// --- GetClients ---

func TestGetClients_NeverExposesPassword(t *testing.T) {
	cfg := makeTestConfig(
		config.DownloadClient{ID: "q1", Name: "qBit", Type: "qbittorrent", URL: "http://localhost:8080", Username: "admin", Password: "supersecret", Default: true},
		config.DownloadClient{ID: "t1", Name: "Transmission", Type: "transmission", URL: "http://localhost:9091", Default: false},
	)

	router := gin.New()
	router.GET("/api/clients", GetClients(cfg))

	req := httptest.NewRequest("GET", "/api/clients", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var clients []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &clients)

	if len(clients) != 2 {
		t.Fatalf("len(clients) = %d, want 2", len(clients))
	}

	for _, c := range clients {
		if _, hasPass := c["password"]; hasPass {
			t.Error("password must not be exposed in GET /api/clients")
		}
		if _, hasUser := c["username"]; hasUser {
			t.Error("username must not be exposed in GET /api/clients")
		}
	}
}

func TestGetClients_ReturnsExpectedFields(t *testing.T) {
	cfg := makeTestConfig(
		config.DownloadClient{ID: "q1", Name: "My qBit", Type: "qbittorrent", Default: true},
		config.DownloadClient{ID: "t1", Name: "My Trans", Type: "transmission", Default: false},
	)

	router := gin.New()
	router.GET("/api/clients", GetClients(cfg))

	req := httptest.NewRequest("GET", "/api/clients", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var clients []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &clients)

	if clients[0]["id"] != "q1" || clients[0]["name"] != "My qBit" {
		t.Errorf("client[0] = %v", clients[0])
	}
	if clients[0]["default"] != true {
		t.Error("expected client[0].default = true")
	}
	if clients[1]["default"] != false {
		t.Error("expected client[1].default = false")
	}
}

func TestGetClients_EmptyList(t *testing.T) {
	cfg := makeTestConfig()

	router := gin.New()
	router.GET("/api/clients", GetClients(cfg))

	req := httptest.NewRequest("GET", "/api/clients", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var clients []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &clients)
	if len(clients) != 0 {
		t.Errorf("expected empty array, got %d items", len(clients))
	}
}

// --- Download ---

func TestDownload_MissingBothURIs(t *testing.T) {
	cfg := makeTestConfig(
		config.DownloadClient{ID: "q1", Type: "qbittorrent", Default: true},
	)

	router := gin.New()
	router.POST("/api/download", Download(cfg))

	w := postJSON(t, router, "/api/download", map[string]string{"clientId": "q1"})

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when neither magnetUri nor torrentUrl provided", w.Code)
	}
}

func TestDownload_InvalidBody(t *testing.T) {
	cfg := makeTestConfig()
	router := gin.New()
	router.POST("/api/download", Download(cfg))

	req := httptest.NewRequest("POST", "/api/download", bytes.NewReader([]byte("not valid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for invalid JSON", w.Code)
	}
}

func TestDownload_ClientNotFound(t *testing.T) {
	cfg := makeTestConfig(
		config.DownloadClient{ID: "q1", Type: "qbittorrent", Default: true},
	)

	router := gin.New()
	router.POST("/api/download", Download(cfg))

	w := postJSON(t, router, "/api/download", map[string]string{
		"clientId":  "does-not-exist",
		"magnetUri": "magnet:?xt=urn:btih:abc",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for unknown clientId", w.Code)
	}
}

func TestDownload_NoClientsConfigured(t *testing.T) {
	cfg := makeTestConfig() // no clients

	router := gin.New()
	router.POST("/api/download", Download(cfg))

	w := postJSON(t, router, "/api/download", map[string]string{
		"magnetUri": "magnet:?xt=urn:btih:abc",
	})

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when no clients configured", w.Code)
	}
}

func TestDownload_UsesDefaultClientWhenNoIDProvided(t *testing.T) {
	// Start a real mock qBittorrent server so the handler can actually call it
	addCalled := false
	qbitSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/add":
			addCalled = true
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer qbitSrv.Close()

	cfg := makeTestConfig(
		config.DownloadClient{
			ID: "q1", Name: "qBit", Type: "qbittorrent",
			URL: qbitSrv.URL, Username: "admin", Password: "pass", Default: true,
		},
	)

	router := gin.New()
	router.POST("/api/download", Download(cfg))

	w := postJSON(t, router, "/api/download", map[string]string{
		// no clientId — should fall back to default
		"magnetUri": "magnet:?xt=urn:btih:abc",
	})

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — body: %s", w.Code, w.Body.String())
	}
	if !addCalled {
		t.Error("expected torrents/add to be called on the default client")
	}
}

func TestDownload_PicksClientByID(t *testing.T) {
	// Two clients — handler should call the one matching the given ID
	var calledServer string

	makeSrv := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/login":
				w.Write([]byte("Ok."))
			case "/api/v2/torrents/add":
				calledServer = name
				w.WriteHeader(http.StatusOK)
			}
		}))
	}

	srvA := makeSrv("A")
	defer srvA.Close()
	srvB := makeSrv("B")
	defer srvB.Close()

	cfg := makeTestConfig(
		config.DownloadClient{ID: "a", Type: "qbittorrent", URL: srvA.URL, Default: true},
		config.DownloadClient{ID: "b", Type: "qbittorrent", URL: srvB.URL, Default: false},
	)

	router := gin.New()
	router.POST("/api/download", Download(cfg))

	w := postJSON(t, router, "/api/download", map[string]string{
		"clientId":  "b",
		"magnetUri": "magnet:?xt=urn:btih:abc",
	})

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — body: %s", w.Code, w.Body.String())
	}
	if calledServer != "B" {
		t.Errorf("called server %q, want 'B'", calledServer)
	}
}

func TestDownload_UsesTorrentURLWhenNoMagnet(t *testing.T) {
	// Use a Transmission server since the URL/magnet distinction is cleaner to test
	var capturedFilename string

	transSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method    string                 `json:"method"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if fn, ok := req.Arguments["filename"].(string); ok {
			capturedFilename = fn
		}
		json.NewEncoder(w).Encode(map[string]string{"result": "success"})
	}))
	defer transSrv.Close()

	cfg := makeTestConfig(
		config.DownloadClient{ID: "t1", Type: "transmission", URL: transSrv.URL, Default: true},
	)

	router := gin.New()
	router.POST("/api/download", Download(cfg))

	w := postJSON(t, router, "/api/download", map[string]string{
		"torrentUrl": "http://tracker.example.com/file.torrent",
	})

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — body: %s", w.Code, w.Body.String())
	}
	if capturedFilename != "http://tracker.example.com/file.torrent" {
		t.Errorf("filename = %q, want torrent URL", capturedFilename)
	}
}
