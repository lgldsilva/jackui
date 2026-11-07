//go:build e2e

// Package e2e runs HTTP integration specs against a live JackUI stack
// (no browser). See README.md in this directory for how to point the suite
// at a stack.
//
// Route coverage: one table entry per (method, path) pair from the sweep
// route inventory, asserting the handler's contract for a deterministic
// probe request (empty stores, no external providers). Routes that are
// feature-gated OFF on the reference stack are asserted as not registered
// in gated_off_test.go.
package e2e

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	// Bogus-but-well-formed placeholder values for parametrized routes.
	// Handlers must answer with their not-found / bad-request contract,
	// never with the gin NoRoute marker.
	bogusHash  = "0000000000000000000000000000000000000000"
	bogusFile  = "0"
	bogusName  = "e2e-missing"
	bogusID    = "999999"
	bogusKey   = "e2e-missing-key"
	bogusSeg   = "e2e0.ts"
	noRespBody = ""
)

var env struct {
	baseURL   string
	adminUser string
	adminPass string
}

var app = &apiClient{client: &http.Client{Timeout: 20 * time.Second}}

type apiClient struct {
	client *http.Client
	token  string
}

func TestMain(m *testing.M) {
	env.baseURL = strings.TrimRight(envOr("E2E_BASE_URL", "http://127.0.0.1:8989"), "/")
	env.adminUser = envOr("E2E_ADMIN_USER", "admin")
	env.adminPass = envOr("E2E_ADMIN_PASSWORD", "admin")

	if err := waitHealthy(60 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: stack not reachable at %s: %v\n", env.baseURL, err)
		os.Exit(1)
	}
	if err := app.loginAdmin(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: admin login failed: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func waitHealthy(limit time.Duration) error {
	deadline := time.Now().Add(limit)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := app.client.Get(env.baseURL + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("healthz=%d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(2 * time.Second)
	}
	return lastErr
}

func (a *apiClient) loginAdmin() error {
	var resp struct {
		Access string `json:"access"`
	}
	status, body, err := a.rawDo("POST", "/api/auth/login", "",
		fmt.Sprintf(`{"username":%q,"password":%q,"remember":false}`, env.adminUser, env.adminPass))
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("login status=%d body=%s", status, snippet(body))
	}
	if err := jsonUnmarshal(body, &resp); err != nil || resp.Access == "" {
		return fmt.Errorf("login response missing access token (err=%v)", err)
	}
	a.token = resp.Access
	return nil
}

// do sends an authenticated admin request and returns (status, body).
func (a *apiClient) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	status, respBody, err := a.rawDo(method, path, a.token, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return status, respBody
}

// rawDo is the transport used by both the helpers and the negative tests.
// tokenEmpty distinguishes "no Authorization header" from an empty bearer.
func (a *apiClient) rawDo(method, path, bearer, body string) (int, string, error) {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, env.baseURL+path, rdr)
	if err != nil {
		return 0, "", err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, string(b), nil
}

// routeCase is one table entry: a probe request plus the status the handler
// must answer with on the reference stack.
type routeCase struct {
	method string
	path   string
	body   string
	want   int
	// noAuth sends the request without the admin bearer (public routes).
	noAuth bool
	// rawCT overrides the JSON Content-Type (multipart probes).
	rawCT string
	// badJSON skips the table's JSON-validity check so the entry can probe a
	// malformed-body 400 branch without tripping stateful handlers (e.g. the
	// login lockout counters).
	badJSON bool
}

func (tc routeCase) name() string { return tc.method + " " + tc.path }

// runRoutes executes a table and asserts, per entry, the exact probe status
// and that the response never carries the gin NoRoute marker — a registered
// route must answer with its own handler contract.
func runRoutes(t *testing.T, cases []routeCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name(), func(t *testing.T) {
			bearer := app.token
			if tc.noAuth {
				bearer = ""
			}
			if tc.body != "" && tc.rawCT == "" && !tc.badJSON {
				tc.body = mustJSONBody(t, tc.body)
			}
			status, respBody, err := app.rawDo(tc.method, tc.path, bearer, tc.body)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if status != tc.want {
				t.Fatalf("want status %d, got %d (body: %s)", tc.want, status, snippet(respBody))
			}
			if strings.Contains(respBody, "endpoint not found") {
				t.Fatalf("route answered with the gin NoRoute marker — not registered on this build")
			}
		})
	}
}

func snippet(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
