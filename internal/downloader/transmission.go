package downloader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lgldsilva/jackui/internal/config"
)

type Transmission struct {
	name      string
	rpcURL    string
	username  string
	password  string
	sessionID string
	client    *http.Client
}

type transmissionRequest struct {
	Method    string                 `json:"method"`
	Arguments map[string]interface{} `json:"arguments"`
}

type transmissionResponse struct {
	Result    string                 `json:"result"`
	Arguments map[string]interface{} `json:"arguments"`
}

func NewTransmission(dc config.DownloadClient) *Transmission {
	baseURL := strings.TrimRight(dc.URL, "/")
	rpcURL := baseURL + "/transmission/rpc"

	return &Transmission{
		name:     dc.Name,
		rpcURL:   rpcURL,
		username: dc.Username,
		password: dc.Password,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (t *Transmission) Name() string { return t.name }
func (t *Transmission) Type() string { return "transmission" }

// do issues an RPC call, transparently refreshing the session id on a single
// 409. The retry is bounded (doN) so a server that keeps returning 409 — e.g.
// a session id that rotates every request — can't recurse into a stack
// overflow.
func (t *Transmission) do(method string, args map[string]interface{}) (*transmissionResponse, error) {
	return t.doN(method, args, 1)
}

func (t *Transmission) doN(method string, args map[string]interface{}, retriesLeft int) (*transmissionResponse, error) {
	reqBody := transmissionRequest{
		Method:    method,
		Arguments: args,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", t.rpcURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if t.sessionID != "" {
		req.Header.Set("X-Transmission-Session-Id", t.sessionID)
	}
	if t.username != "" {
		req.SetBasicAuth(t.username, t.password)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle 409 - need to get session ID
	if resp.StatusCode == http.StatusConflict {
		t.sessionID = resp.Header.Get("X-Transmission-Session-Id")
		if t.sessionID == "" {
			return nil, fmt.Errorf("transmission returned 409 but no session ID")
		}
		if retriesLeft <= 0 {
			return nil, fmt.Errorf("transmission kept returning 409 after session refresh")
		}
		return t.doN(method, args, retriesLeft-1)
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("transmission returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result transmissionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Result != "success" {
		return nil, fmt.Errorf("transmission error: %s", result.Result)
	}

	return &result, nil
}

func (t *Transmission) AddMagnet(magnetURI string, savePath string) error {
	args := map[string]interface{}{
		"filename": magnetURI,
	}
	if savePath != "" {
		args["download-dir"] = savePath
	}

	_, err := t.do("torrent-add", args)
	if err != nil {
		return fmt.Errorf("failed to add magnet to Transmission: %w", err)
	}
	return nil
}

func (t *Transmission) AddTorrentURL(torrentURL string, savePath string) error {
	args := map[string]interface{}{
		"filename": torrentURL,
	}
	if savePath != "" {
		args["download-dir"] = savePath
	}

	_, err := t.do("torrent-add", args)
	if err != nil {
		return fmt.Errorf("failed to add torrent URL to Transmission: %w", err)
	}
	return nil
}
