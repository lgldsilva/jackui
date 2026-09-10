package handlers

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

// totpCodeAt mirrors auth's TOTP derivation so handler tests can present a
// valid 6-digit code without reaching into the auth package internals.
// HMAC-SHA1 is the RFC 6238 TOTP algorithm, not a password hash (same
// justification as auth/totp.go).
func totpCodeAt(secret string, counter uint64) string {
	upper := strings.ToUpper(secret)
	if m := len(upper) % 8; m != 0 {
		upper += strings.Repeat("=", 8-m)
	}
	key, err := base32.StdEncoding.DecodeString(upper)
	if err != nil {
		return ""
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key) // #nosec G401 -- RFC 6238 requires HMAC-SHA1
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) | (uint32(sum[offset+1]) << 16) | (uint32(sum[offset+2]) << 8) | uint32(sum[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000)
}

// newOAuthTestStore wires an auth.Store onto the shared test Postgres (skips
// when JACKUI_TEST_DATABASE_URL is unset — same contract as store tests).
func newOAuthTestStore(t *testing.T) *auth.Store {
	t.Helper()
	s, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatalf("auth store: %v", err)
	}
	return s
}

// fakeGoogleForOAuth stands in for Google's token+userinfo endpoints.
func fakeGoogleForOAuth(t *testing.T, email, verified string) auth.GoogleEndpoints {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-1","token_type":"Bearer","expires_in":3599}`))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"email":"` + email + `","email_verified":` + verified + `,"name":"Luiz"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return auth.GoogleEndpoints{AuthURL: srv.URL, TokenURL: srv.URL + "/token", UserInfoURL: srv.URL + "/userinfo"}
}

func testOAuthHandlers(t *testing.T, store *auth.Store, mutate func(*config.AuthOAuth)) *GoogleOAuthHandlers {
	t.Helper()
	tm := auth.NewTokenManager([]byte("test-secret-0123456789abcdef-0123456789"), 15*time.Minute)
	cfg := config.AuthOAuth{
		Enabled:     true,
		ClientID:    "cid",
		ClientSecret: "csec",
		RedirectURL: "https://jackui.example.com/api/auth/oauth/google/callback",
	}
	if mutate != nil {
		mutate(&cfg)
	}
	eps := fakeGoogleForOAuth(t, "luiz@example.com", "true")
	return GoogleOAuth(store, tm, cfg, "https://jackui.example.com", eps)
}

func callOAuth(h gin.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	if body != "" {
		c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
	} else {
		c.Request = httptest.NewRequest(method, path, nil)
	}
	h(c)
	return w
}

func queryOf(rawURL string) url.Values {
	u, err := url.Parse(rawURL)
	if err != nil {
		return url.Values{}
	}
	return u.Query()
}

func TestOAuthProvidersEndpoint(t *testing.T) {
	h := testOAuthHandlers(t, nil, nil)
	w := callOAuth(h.Providers(), http.MethodGet, "/api/auth/oauth/providers", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"google":true`) {
		t.Fatalf("providers ready = %d %s", w.Code, w.Body.String())
	}
	off := testOAuthHandlers(t, nil, func(c *config.AuthOAuth) { c.ClientID = "" })
	w2 := callOAuth(off.Providers(), http.MethodGet, "/api/auth/oauth/providers", "")
	if !strings.Contains(w2.Body.String(), `"google":false`) {
		t.Fatalf("providers unconfigured = %s", w2.Body.String())
	}
}

func TestOAuthStartNotReadyRedirectsToLogin(t *testing.T) {
	h := testOAuthHandlers(t, nil, func(c *config.AuthOAuth) { c.ClientID = "" })
	w := callOAuth(h.Start(), http.MethodGet, "/api/auth/oauth/google/start", "")
	if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "oauthError=disabled") {
		t.Fatalf("start not ready = %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestOAuthStartBuildsAuthorizeRedirect(t *testing.T) {
	h := testOAuthHandlers(t, nil, nil)
	w := callOAuth(h.Start(), http.MethodGet, "/api/auth/oauth/google/start", "")
	if w.Code != http.StatusFound {
		t.Fatalf("start = %d", w.Code)
	}
	loc := w.Header().Get("Location")
	q := queryOf(loc)
	if !strings.HasPrefix(loc, "http://127.0.0.1") && !strings.Contains(loc, "/token") && !strings.Contains(loc, "127.0.0.1") {
		t.Fatalf("start must redirect at the (fake) IdP: %s", loc)
	}
	if q.Get("client_id") != "cid" || q.Get("response_type") != "code" || q.Get("state") == "" {
		t.Fatalf("authorize params missing: %v", q)
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("PKCE missing: %v", q)
	}
	if q.Get("redirect_uri") != "https://jackui.example.com/api/auth/oauth/google/callback" {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
}

func TestOAuthCallbackRejectsBadStateAndMissingParams(t *testing.T) {
	h := testOAuthHandlers(t, nil, nil)
	for name, path := range map[string]string{
		"no-code":  "/cb?state=x",
		"no-state": "/cb?code=y",
		"bogus":    "/cb?code=y&state=bogus",
	} {
		w := callOAuth(h.Callback(), http.MethodGet, path, "")
		if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "oauthError=state") {
			t.Fatalf("%s: %d %s", name, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestOAuthCallbackProviderError(t *testing.T) {
	h := testOAuthHandlers(t, nil, nil)
	w := callOAuth(h.Callback(), http.MethodGet, "/cb?error=access_denied", "")
	if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "oauthError=login_failed") {
		t.Fatalf("provider error = %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestOAuthFullFlowLinksExistingAccountByEmail(t *testing.T) {
	store := newOAuthTestStore(t)
	if _, err := store.CreateUserFull("luiz", "luiz@example.com", "password1", auth.RoleUser, auth.StatusActive); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	h := testOAuthHandlers(t, store, nil)

	// start → capture state
	w := callOAuth(h.Start(), http.MethodGet, "/api/auth/oauth/google/start", "")
	state := queryOf(w.Header().Get("Location")).Get("state")
	// callback with the fake Google exchange
	w = callOAuth(h.Callback(), http.MethodGet, "/api/auth/oauth/google/callback?code=g-code&state="+url.QueryEscape(state), "")
	if w.Code != http.StatusFound {
		t.Fatalf("callback = %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "/auth/google/callback?code=") {
		t.Fatalf("callback must bounce to the SPA: %s", loc)
	}
	code := queryOf(loc).Get("code")

	// exchange → token pair
	w = callOAuth(h.Exchange(), http.MethodPost, "/api/auth/oauth/exchange", `{"code":"`+code+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("exchange = %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Access  string `json:"access"`
		Refresh string `json:"refresh"`
		User    struct {
			ID       int    `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("exchange body: %v", err)
	}
	if resp.Access == "" || resp.Refresh == "" || resp.User.Username != "luiz" {
		t.Fatalf("exchange tokens/user incomplete: %+v", resp)
	}

	// exchange code is single-use
	w = callOAuth(h.Exchange(), http.MethodPost, "/api/auth/oauth/exchange", `{"code":"`+code+`"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("replayed exchange = %d, want 401", w.Code)
	}
}

func TestOAuthCallbackUnknownAccountRefusedWithoutProvision(t *testing.T) {
	store := newOAuthTestStore(t)
	h := testOAuthHandlers(t, store, nil)
	w := callOAuth(h.Start(), http.MethodGet, "/start", "")
	state := queryOf(w.Header().Get("Location")).Get("state")
	w = callOAuth(h.Callback(), http.MethodGet, "/cb?code=g&state="+url.QueryEscape(state), "")
	loc := w.Header().Get("Location")
	if w.Code != http.StatusFound || !strings.Contains(loc, "oauthError=unknown_account") {
		t.Fatalf("unknown account = %d %s", w.Code, loc)
	}
}

func TestOAuthCallbackAutoProvisionCreatesActiveUser(t *testing.T) {
	store := newOAuthTestStore(t)
	h := testOAuthHandlers(t, store, func(c *config.AuthOAuth) { c.AutoProvision = true })
	w := callOAuth(h.Start(), http.MethodGet, "/start", "")
	state := queryOf(w.Header().Get("Location")).Get("state")
	w = callOAuth(h.Callback(), http.MethodGet, "/cb?code=g&state="+url.QueryEscape(state), "")
	if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "/auth/google/callback?code=") {
		t.Fatalf("autoprovision callback = %d %s", w.Code, w.Header().Get("Location"))
	}
	user, err := store.GetUserByEmail("luiz@example.com")
	if err != nil || user == nil {
		t.Fatalf("provisioned user missing: %v", err)
	}
	if user.Status != auth.StatusActive || user.Role != auth.RoleUser || user.Username != "luiz" {
		t.Fatalf("provisioned user wrong: %+v", user)
	}
}

func TestOAuthCallbackDomainNotAllowed(t *testing.T) {
	store := newOAuthTestStore(t)
	_ = store
	h := testOAuthHandlers(t, store, func(c *config.AuthOAuth) { c.AllowedDomains = []string{"corporate.example"} })
	w := callOAuth(h.Start(), http.MethodGet, "/start", "")
	state := queryOf(w.Header().Get("Location")).Get("state")
	w = callOAuth(h.Callback(), http.MethodGet, "/cb?code=g&state="+url.QueryEscape(state), "")
	if !strings.Contains(w.Header().Get("Location"), "oauthError=domain_not_allowed") {
		t.Fatalf("domain filter = %s", w.Header().Get("Location"))
	}
}

func TestOAuthCallbackUnverifiedEmailRefused(t *testing.T) {
	store := newOAuthTestStore(t)
	h := GoogleOAuth(store, auth.NewTokenManager([]byte("test-secret-0123456789abcdef-0123456789"), 15*time.Minute),
		config.AuthOAuth{Enabled: true, ClientID: "cid", ClientSecret: "csec", RedirectURL: "https://x.example.com/api/auth/oauth/google/callback"},
		"https://x.example.com",
		fakeGoogleForOAuth(t, "luiz@example.com", "false"))
	w := callOAuth(h.Start(), http.MethodGet, "/start", "")
	state := queryOf(w.Header().Get("Location")).Get("state")
	w = callOAuth(h.Callback(), http.MethodGet, "/cb?code=g&state="+url.QueryEscape(state), "")
	if !strings.Contains(w.Header().Get("Location"), "oauthError=email_unverified") {
		t.Fatalf("unverified e-mail = %s", w.Header().Get("Location"))
	}
}

func TestOAuthCallbackAccountStatusGates(t *testing.T) {
	for name, status := range map[string]auth.Status{
		"pending":  auth.StatusPending,
		"disabled": auth.StatusDisabled,
	} {
		t.Run(name, func(t *testing.T) {
			store := newOAuthTestStore(t)
			if _, err := store.CreateUserFull("luiz", "luiz@example.com", "password1", auth.RoleUser, status); err != nil {
				t.Fatalf("seed: %v", err)
			}
			h := testOAuthHandlers(t, store, nil)
			w := callOAuth(h.Start(), http.MethodGet, "/start", "")
			state := queryOf(w.Header().Get("Location")).Get("state")
			w = callOAuth(h.Callback(), http.MethodGet, "/cb?code=g&state="+url.QueryEscape(state), "")
			want := "oauthError=account_pending"
			if status == auth.StatusDisabled {
				want = "oauthError=account_disabled"
			}
			if !strings.Contains(w.Header().Get("Location"), want) {
				t.Fatalf("%s: %s", name, w.Header().Get("Location"))
			}
		})
	}
}

func TestOAuthExchangeMFARoundTrip(t *testing.T) {
	store := newOAuthTestStore(t)
	if _, err := store.CreateUserFull("luiz", "luiz@example.com", "password1", auth.RoleUser, auth.StatusActive); err != nil {
		t.Fatalf("seed: %v", err)
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("totp secret: %v", err)
	}
	if err := store.SetTOTPSecret(1, secret); err != nil {
		t.Fatalf("set totp: %v", err)
	}
	if err := store.EnableTOTP(1); err != nil {
		t.Fatalf("enable totp: %v", err)
	}
	h := testOAuthHandlers(t, store, nil)

	w := callOAuth(h.Start(), http.MethodGet, "/start", "")
	state := queryOf(w.Header().Get("Location")).Get("state")
	w = callOAuth(h.Callback(), http.MethodGet, "/cb?code=g&state="+url.QueryEscape(state), "")
	code := queryOf(w.Header().Get("Location")).Get("code")

	// No TOTP yet → mfaRequired, code stays alive.
	w = callOAuth(h.Exchange(), http.MethodPost, "/exchange", `{"code":"`+code+`"}`)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"mfaRequired":true`) {
		t.Fatalf("mfa challenge = %d %s", w.Code, w.Body.String())
	}
	// Wrong code → still challenged.
	w = callOAuth(h.Exchange(), http.MethodPost, "/exchange", `{"code":"`+code+`","totp":"000000"}`)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"mfaRequired":true`) {
		t.Fatalf("wrong totp = %d %s", w.Code, w.Body.String())
	}
	// Valid TOTP (current 30s window) → tokens.
	valid := totpCodeAt(secret, uint64(time.Now().Unix()/30))
	w = callOAuth(h.Exchange(), http.MethodPost, "/exchange", `{"code":"`+code+`","totp":"`+valid+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("valid totp exchange = %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"access":`) {
		t.Fatalf("expected tokens, got %s", w.Body.String())
	}
}

func TestOAuthExchangeInvalidCode(t *testing.T) {
	h := testOAuthHandlers(t, nil, nil)
	w := callOAuth(h.Exchange(), http.MethodPost, "/exchange", `{"code":"dead"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid exchange code = %d", w.Code)
	}
	w = callOAuth(h.Exchange(), http.MethodPost, "/exchange", ``)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty body = %d", w.Code)
	}
}

func TestOAuthProvisionUsernameCollision(t *testing.T) {
	store := newOAuthTestStore(t)
	// "luiz" já existe → auto-provision precisa derivar "luiz-2".
	if _, err := store.CreateUserFull("luiz", "old@example.com", "password1", auth.RoleUser, auth.StatusActive); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := testOAuthHandlers(t, store, func(c *config.AuthOAuth) { c.AutoProvision = true })
	w := callOAuth(h.Start(), http.MethodGet, "/start", "")
	state := queryOf(w.Header().Get("Location")).Get("state")
	w = callOAuth(h.Callback(), http.MethodGet, "/cb?code=g&state="+url.QueryEscape(state), "")
	if !strings.Contains(w.Header().Get("Location"), "/auth/google/callback?code=") {
		t.Fatalf("collision callback = %d %s", w.Code, w.Header().Get("Location"))
	}
	user, err := store.GetUserByEmail("luiz@example.com")
	if err != nil || user == nil {
		t.Fatalf("provisioned user missing: %v", err)
	}
	if user.Username != "luiz-2" {
		t.Fatalf("username = %q, want luiz-2", user.Username)
	}
}

func TestOAuthEmptyBaseURLFallsBackToRelativeSPAPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := GoogleOAuth(nil, nil, config.AuthOAuth{ClientID: "", ClientSecret: "", RedirectURL: ""}, "", auth.DefaultGoogleEndpoints)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/start", nil)
	h.Start()(c)
	loc := w.Header().Get("Location")
	if w.Code != http.StatusFound || !strings.HasPrefix(loc, "/login?oauthError=disabled") {
		t.Fatalf("relative fallback = %d %s", w.Code, loc)
	}
}

func TestDeriveUsernameEdges(t *testing.T) {
	cases := map[string]string{
		"First.Last@Example.com": "first.last",
		"weird+$tag!@x.io":       "weirdtag",
		"---@x.io":               "user",
		"@x.io":                  "user",
	}
	for in, want := range cases {
		if got := deriveUsername(in); got != want {
			t.Fatalf("deriveUsername(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("a", 40) + "@x.io"
	if got := deriveUsername(long); len(got) != 32 {
		t.Fatalf("deriveUsername long = %q (%d chars)", got, len(got))
	}
}
