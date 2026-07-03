package handlers

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/local"
)

// TestLocalPromoteBatch guards the regression where the batch reclassify moved
// only the first file (req.Path) while the UI reported N moved. The handler must
// move every entry in req.Paths and report the real count.
func TestLocalPromoteBatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	meus := t.TempDir()
	shared := t.TempDir()
	names := []string{"a.mkv", "b.mkv", "c.mkv"}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(meus, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	b := local.NewBrowser([]config.ExternalMount{{Name: "Meus downloads", Path: meus}})
	router := gin.New()
	router.POST("/api/local/promote", LocalPromote(b, nil, nil, shared, nil, nil, nil, nil))

	body, _ := json.Marshal(localPromoteReq{Mount: "Meus downloads", Paths: names, TargetSubdir: "filmes"})
	req := httptest.NewRequest(http.MethodPost, "/api/local/promote", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Moved  int `json:"moved"`
		Failed int `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
	}
	if resp.Moved != 3 || resp.Failed != 0 {
		t.Fatalf("moved=%d failed=%d, want 3/0 — batch promote moved fewer than all files", resp.Moved, resp.Failed)
	}
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(shared, "filmes", n)); err != nil {
			t.Errorf("file %q not moved to destination", n)
		}
		if _, err := os.Stat(filepath.Join(meus, n)); !os.IsNotExist(err) {
			t.Errorf("source %q still present after promote", n)
		}
	}
}

// TestSSRFGuards locks the /convert/torrent-to-magnet fetch guards: only
// http/https schemes, loopback + link-local/cloud-metadata blocked, private LAN
// (Jackett at 192.168.x) and public IPs allowed.
func TestSSRFGuards(t *testing.T) {
	if err := validateFetchScheme("file:///etc/passwd"); err == nil {
		t.Error("file:// scheme should be rejected")
	}
	if err := validateFetchScheme("gopher://x/"); err == nil {
		t.Error("gopher:// scheme should be rejected")
	}
	if err := validateFetchScheme("http://192.168.1.50/x.torrent"); err != nil {
		t.Errorf("http should be allowed: %v", err)
	}
	if err := validateFetchScheme("https://tracker.example/x.torrent"); err != nil {
		t.Errorf("https should be allowed: %v", err)
	}

	blocked := []string{"127.0.0.1", "::1", "169.254.169.254", "0.0.0.0"}
	for _, s := range blocked {
		if !isBlockedFetchIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	allowed := []string{"192.168.1.50", "127.0.0.1", "172.16.0.9", "8.8.8.8"}
	for _, s := range allowed {
		if isBlockedFetchIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed (Jackett LAN / public)", s)
		}
	}
}
