package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ─── Google OAuth (OIDC) — login by e-mail ──────────────────────────────────
//
// Authorization-code flow with PKCE against Google's fixed endpoints. The
// linking rule matches the Gitea setup in this homelab: the Google account is
// matched to an existing JackUI account by E-MAIL; unknown e-mails are refused
// unless auto-provision is explicitly enabled (set at construction time).

// GoogleEndpoints holds the IdP URLs. Package-level var so tests can point the
// whole flow at an httptest server; production uses DefaultGoogleEndpoints.
type GoogleEndpoints struct {
	AuthURL     string // authorization endpoint (browser redirect)
	TokenURL    string // token exchange (code → access token)
	UserInfoURL string // userinfo (access token → verified e-mail)
}

// DefaultGoogleEndpoints are Google's production OIDC endpoints.
var DefaultGoogleEndpoints = GoogleEndpoints{
	AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:    "https://oauth2.googleapis.com/token",
	UserInfoURL: "https://openidconnect.googleapis.com/v1/userinfo",
}

// GoogleOptions are the runtime options an operator configures via
// config.yaml / JACKUI_OAUTH_* env (mirrors config.AuthOAuth).
type GoogleOptions struct {
	ClientID       string
	ClientSecret   string
	RedirectURL    string   // must match the URI registered in Google Console
	AutoProvision  bool     // create an account for unknown (verified) e-mails
	AllowedDomains []string // empty = any domain (Google consent screen gates users)
}

// Ready reports whether the flow can start (all required credentials set).
func (o GoogleOptions) Ready() bool {
	return o.ClientID != "" && o.ClientSecret != "" && o.RedirectURL != ""
}

// DomainAllowed reports whether the e-mail's domain passes the allowlist.
func (o GoogleOptions) DomainAllowed(email string) bool {
	if len(o.AllowedDomains) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, d := range o.AllowedDomains {
		if domain == strings.ToLower(strings.TrimPrefix(d, "@")) {
			return true
		}
	}
	return false
}

// GoogleUserInfo is the subset of the OIDC userinfo response the login uses.
type GoogleUserInfo struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

// tokenResponse is the subset of the OAuth token endpoint reply we need.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	IDToken     string `json:"id_token"`
}

// PKCEChallenge derives the S256 code_challenge from a verifier
// (RFC 7636 §4.2): BASE64URL(SHA256(ASCII(verifier))).
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// RandomToken returns a URL-safe random string (~n*5.3 bits of entropy).
// Used for OAuth states, PKCE verifiers and one-time exchange codes.
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewState creates a fresh state+verifier pair (both single-use) and remembers
// it for stateTTL. Returns the opaque state (goes in the authorize URL) and the
// PKCE verifier (kept server-side; sent at token exchange).
func (f *OAuthFlowStore) NewState(remember bool) (state, verifier string, err error) {
	verifier, err = RandomToken(48)
	if err != nil {
		return "", "", err
	}
	state, err = RandomToken(32)
	if err != nil {
		return "", "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[state] = oauthState{Verifier: verifier, Remember: remember, Expires: time.Now().Add(stateTTL)}
	return state, verifier, nil
}

// ConsumeState validates and removes a state (single-use), returning the PKCE
// verifier and the remember flag carried from /start.
func (f *OAuthFlowStore) ConsumeState(state string) (verifier string, remember bool, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, found := f.states[state]
	if !found {
		return "", false, false
	}
	delete(f.states, state)
	if time.Now().After(s.Expires) {
		return "", false, false
	}
	return s.Verifier, s.Remember, true
}

// SavePending registers a short-lived opaque code that the SPA exchanges
// (POST /api/auth/oauth/exchange) for a real token pair. Keeping tokens out of
// the redirect URL keeps them out of browser history.
func (f *OAuthFlowStore) SavePending(userID int, remember bool) (code string, err error) {
	code, err = RandomToken(32)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending[code] = oauthPending{UserID: userID, Remember: remember, Expires: time.Now().Add(pendingTTL)}
	return code, nil
}

// PeekPending returns the pending login for a code WITHOUT consuming it — the
// MFA round-trip needs a second call before the code is spent.
func (f *OAuthFlowStore) PeekPending(code string) (userID int, remember bool, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, found := f.pending[code]
	if !found {
		return 0, false, false
	}
	if time.Now().After(p.Expires) {
		delete(f.pending, code)
		return 0, false, false
	}
	return p.UserID, p.Remember, true
}

// ConsumePending removes a pending code (single-use). The handler only calls
// this after tokens were issued successfully, so a failed MFA retry still works.
func (f *OAuthFlowStore) ConsumePending(code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.pending, code)
}

// ExchangeGoogleCode swaps the authorization code for an access token using the
// stored PKCE verifier (confidential client: client_secret also authenticates us).
func ExchangeGoogleCode(ctx context.Context, endpoints GoogleEndpoints, opts GoogleOptions, code, verifier string) (*GoogleUserInfo, error) {
	// The userinfo payload is what the login actually uses; the token response is
	// an intermediate step, so both share this helper's parse path.
	form := url.Values{
		"code":          {code},
		"client_id":     {opts.ClientID},
		"client_secret": {opts.ClientSecret},
		"redirect_uri":  {opts.RedirectURL},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoints.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token endpoint unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, truncateForLog(body))
	}
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("token endpoint returned invalid JSON: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, errors.New("token endpoint returned no access_token")
	}
	return FetchGoogleUserInfo(ctx, endpoints, tok.AccessToken)
}

// FetchGoogleUserInfo resolves the verified e-mail behind an access token.
func FetchGoogleUserInfo(ctx context.Context, endpoints GoogleEndpoints, accessToken string) (*GoogleUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoints.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo endpoint unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo failed (%d): %s", resp.StatusCode, truncateForLog(body))
	}
	var info GoogleUserInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("userinfo returned invalid JSON: %w", err)
	}
	if info.Email == "" {
		return nil, errors.New("userinfo returned no e-mail")
	}
	info.Email = strings.ToLower(strings.TrimSpace(info.Email))
	return &info, nil
}

// AuthorizeURL builds the Google authorization redirect (PKCE, S256).
func (e GoogleEndpoints) AuthorizeURL(opts GoogleOptions, state, codeChallenge string) string {
	q := url.Values{
		"client_id":             {opts.ClientID},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"redirect_uri":          {opts.RedirectURL},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return e.AuthURL + "?" + q.Encode()
}

// ─── flow store (states + pending exchanges) ────────────────────────────────

// Vars (not consts) so tests can shorten the windows; do not mutate at runtime.
var (
	stateTTL   = 10 * time.Minute // round-trip to Google and back
	pendingTTL = 5 * time.Minute  // SPA must exchange within this window
)

type oauthState struct {
	Verifier string
	Remember bool
	Expires  time.Time
}

type oauthPending struct {
	UserID   int
	Remember bool
	Expires  time.Time
}

// OAuthFlowStore holds the in-flight OAuth round-trips: authorize states and
// pending SPA exchanges. In-memory by design — a restart merely forces the
// user to click the button again; no secrets rest on disk.
type OAuthFlowStore struct {
	mu      sync.Mutex
	states  map[string]oauthState
	pending map[string]oauthPending
}

// NewOAuthFlowStore wires a fresh flow store.
func NewOAuthFlowStore() *OAuthFlowStore {
	return &OAuthFlowStore{
		states:  make(map[string]oauthState),
		pending: make(map[string]oauthPending),
	}
}

func truncateForLog(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

var oauthHTTPClient = &http.Client{Timeout: 15 * time.Second}
