// Package gluetun talks to a gluetun VPN container's control server to obtain
// the provider's forwarded port. Used to seed/leech behind a VPN: the
// BitTorrent peer port must be the port the VPN provider forwards, otherwise
// inbound peer connections never reach us.
package gluetun

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ForwardedPort queries gluetun's control server (default :8000) for the active
// forwarded port. controlURL is the base URL, e.g. "http://localhost:8000".
// Returns an error if the server is unreachable or no port is forwarded yet.
func ForwardedPort(ctx context.Context, controlURL string) (int, error) {
	endpoint := strings.TrimRight(controlURL, "/") + "/v1/openvpn/portforwarded"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("gluetun control returned %d", resp.StatusCode)
	}
	var body struct {
		Port int `json:"port"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decode gluetun response: %w", err)
	}
	if body.Port <= 0 {
		return 0, fmt.Errorf("no forwarded port available yet")
	}
	return body.Port, nil
}
