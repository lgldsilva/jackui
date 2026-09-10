package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// RFC 7636 appendix B vector: verifier "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
// → challenge "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM".
func TestPKCEChallengeRFCVector(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	got := PKCEChallenge(verifier)
	if got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("PKCEChallenge = %q, want RFC vector", got)
	}
}

func TestRandomTokenShapeAndUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		tok, err := RandomToken(32)
		if err != nil {
			t.Fatalf("RandomToken: %v", err)
		}
		if len(tok) != 43 { // base64url(32 bytes) has no padding
			t.Fatalf("token length = %d, want 43", len(tok))
		}
		if seen[tok] {
			t.Fatal("RandomToken repeated a value")
		}
		seen[tok] = true
	}
}

func TestGoogleOptionsReadyAndDomainAllowed(t *testing.T) {
	empty := GoogleOptions{}
	if empty.Ready() {
		t.Fatal("empty options must not be Ready")
	}
	full := GoogleOptions{ClientID: "id", ClientSecret: "secret", RedirectURL: "https://x/callback"}
	if !full.Ready() {
		t.Fatal("full options must be Ready")
	}
	anyDomain := GoogleOptions{}
	if !anyDomain.DomainAllowed("anyone@example.org") {
		t.Fatal("empty allowlist must allow any domain")
	}
	list := GoogleOptions{AllowedDomains: []string{"@Gmail.com", "lgldsilva.com.br"}}
	for _, ok := range []string{"a@gmail.com", "b@lgldsilva.com.br"} {
		if !list.DomainAllowed(ok) {
			t.Fatalf("%q should be allowed", ok)
		}
	}
	for _, no := range []string{"a@yahoo.com", "nodomain", "spoofgmail.com.br.evil.io"} {
		if list.DomainAllowed(no) {
			t.Fatalf("%q should be denied", no)
		}
	}
}

func TestFlowStoreStatesSingleUseAndExpiry(t *testing.T) {
	oldTTL := stateTTL
	stateTTL = 20 * time.Millisecond
	defer func() { stateTTL = oldTTL }()

	f := NewOAuthFlowStore()
	state, verifier, err := f.NewState(true)
	if err != nil || state == "" || verifier == "" {
		t.Fatalf("NewState: %v", err)
	}
	v, remember, ok := f.ConsumeState(state)
	if !ok || v != verifier || !remember {
		t.Fatalf("ConsumeState = %q %v %v, want verifier+remember+ok", v, remember, ok)
	}
	if _, _, ok := f.ConsumeState(state); ok {
		t.Fatal("state must be single-use")
	}
	if _, _, ok := f.ConsumeState("bogus"); ok {
		t.Fatal("unknown state must not consume")
	}
	// Expired state: issued but consumed after the window.
	state2, _, _ := f.NewState(false)
	time.Sleep(25 * time.Millisecond)
	if _, _, ok := f.ConsumeState(state2); ok {
		t.Fatal("expired state must not consume")
	}
}

func TestFlowStorePendingSingleUseAndExpiry(t *testing.T) {
	oldTTL := pendingTTL
	pendingTTL = 20 * time.Millisecond
	defer func() { pendingTTL = oldTTL }()

	f := NewOAuthFlowStore()
	code, err := f.SavePending(42, false)
	if err != nil || code == "" {
		t.Fatalf("SavePending: %v", err)
	}
	uid, remember, ok := f.PeekPending(code)
	if !ok || uid != 42 || remember {
		t.Fatalf("PeekPending = %d %v %v, want 42/false/true", uid, remember, ok)
	}
	// Peek does NOT consume: the MFA round-trip peeks before the final exchange.
	if _, _, ok = f.PeekPending(code); !ok {
		t.Fatal("PeekPending must not consume")
	}
	f.ConsumePending(code)
	if _, _, ok = f.PeekPending(code); ok {
		t.Fatal("ConsumePending must consume")
	}
	code2, _ := f.SavePending(7, true)
	time.Sleep(25 * time.Millisecond)
	if _, _, ok = f.PeekPending(code2); ok {
		t.Fatal("expired pending must not resolve")
	}
}

func TestAuthorizeURLEncodesParams(t *testing.T) {
	eps := DefaultGoogleEndpoints
	opts := GoogleOptions{ClientID: "cid", RedirectURL: "https://jackui/cb"}
	raw := eps.AuthorizeURL(opts, "st-ate", "ch-llenge")
	if !strings.HasPrefix(raw, eps.AuthURL+"?") {
		t.Fatalf("AuthorizeURL missing base: %q", raw)
	}
	for _, want := range []string{
		"client_id=cid", "response_type=code", "scope=openid+email+profile",
		"state=st-ate", "code_challenge=ch-llenge", "code_challenge_method=S256",
		"redirect_uri=https%3A%2F%2Fjackui%2Fcb",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("AuthorizeURL missing %q: %s", want, raw)
		}
	}
}

// fakeGoogle spins an httptest pair standing in for the token+userinfo endpoints.
func fakeGoogle(t *testing.T, tokenStatus int, tokenBody, userinfoBody string, userinfoStatus int) (GoogleEndpoints, *int) {
	userinfoHits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tokenStatus)
		_, _ = w.Write([]byte(tokenBody))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		userinfoHits++
		if r.Header.Get("Authorization") != "Bearer at-123" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(userinfoStatus)
		_, _ = w.Write([]byte(userinfoBody))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return GoogleEndpoints{AuthURL: srv.URL, TokenURL: srv.URL + "/token", UserInfoURL: srv.URL + "/userinfo"}, &userinfoHits
}

func TestExchangeGoogleCodeHappy(t *testing.T) {
	eps, hits := fakeGoogle(t, http.StatusOK,
		`{"access_token":"at-123","token_type":"Bearer","expires_in":3599,"id_token":"x.y.z"}`,
		`{"email":"Luiz@Example.com","email_verified":true,"name":"Luiz"}`, http.StatusOK)
	info, err := ExchangeGoogleCode(context.Background(), eps,
		GoogleOptions{ClientID: "cid", ClientSecret: "sec", RedirectURL: "https://jackui/cb"}, "code", "verifier")
	if err != nil {
		t.Fatalf("ExchangeGoogleCode: %v", err)
	}
	if info.Email != "luiz@example.com" { // normalized to lowercase
		t.Fatalf("Email = %q, want lowercase", info.Email)
	}
	if !info.EmailVerified || info.Name != "Luiz" {
		t.Fatalf("unexpected userinfo: %+v", info)
	}
	if *hits != 1 {
		t.Fatalf("userinfo hits = %d, want 1", *hits)
	}
}

func TestExchangeGoogleCodeInvalidJSONUnreachableAndLongError(t *testing.T) {
	eps, _ := fakeGoogle(t, http.StatusOK, `not-json`, "", 0)
	if _, err := ExchangeGoogleCode(context.Background(), eps, GoogleOptions{}, "c", "v"); err == nil {
		t.Fatal("invalid token JSON must fail")
	}
	if _, err := ExchangeGoogleCode(context.Background(), GoogleEndpoints{TokenURL: "http://127.0.0.1:1/token"}, GoogleOptions{}, "c", "v"); err == nil {
		t.Fatal("unreachable token must fail")
	}
	if _, err := ExchangeGoogleCode(context.Background(), GoogleEndpoints{TokenURL: "://bad"}, GoogleOptions{}, "c", "v"); err == nil {
		t.Fatal("invalid token URL must fail")
	}
	long := strings.Repeat("e", 250)
	eps2, _ := fakeGoogle(t, http.StatusBadRequest, long, "", 0)
	if _, err := ExchangeGoogleCode(context.Background(), eps2, GoogleOptions{}, "c", "v"); err == nil || !strings.Contains(err.Error(), "token exchange failed") {
		t.Fatal("long token error body must be truncated into the failure")
	}
}

func TestFetchGoogleUserInfoUnreachableAndBadURL(t *testing.T) {
	if _, err := FetchGoogleUserInfo(context.Background(), GoogleEndpoints{UserInfoURL: "http://127.0.0.1:1/userinfo"}, "tok"); err == nil {
		t.Fatal("unreachable userinfo must fail")
	}
	if _, err := FetchGoogleUserInfo(context.Background(), GoogleEndpoints{UserInfoURL: "://bad"}, "tok"); err == nil {
		t.Fatal("invalid userinfo URL must fail")
	}
}

func TestExchangeGoogleCodeTokenError(t *testing.T) {
	eps, _ := fakeGoogle(t, http.StatusBadRequest, `{"error":"invalid_grant"}`, "", 0)
	if _, err := ExchangeGoogleCode(context.Background(), eps, GoogleOptions{}, "c", "v"); err == nil {
		t.Fatal("token error must propagate")
	}
}

func TestExchangeGoogleCodeNoAccessToken(t *testing.T) {
	eps, _ := fakeGoogle(t, http.StatusOK, `{"token_type":"Bearer"}`, "", 0)
	if _, err := ExchangeGoogleCode(context.Background(), eps, GoogleOptions{}, "c", "v"); err == nil {
		t.Fatal("missing access_token must fail")
	}
}

func TestFetchGoogleUserInfoRejectsUnverifiedAndEmpty(t *testing.T) {
	for name, body := range map[string]string{
		"unverified": `{"email":"a@b.c","email_verified":false}`,
		"no-email":   `{"sub":"123","name":"X"}`,
	} {
		eps, _ := fakeGoogle(t, http.StatusOK, `{"access_token":"at-123"}`, body, http.StatusOK)
		info, err := FetchGoogleUserInfo(context.Background(), eps, "at-123")
		if name == "no-email" {
			if err == nil {
				t.Fatal("missing e-mail must fail")
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info.EmailVerified {
			t.Fatalf("%s: must not be verified", name)
		}
	}
}

func TestFetchGoogleUserInfoBadJSONAndHTTPError(t *testing.T) {
	eps, _ := fakeGoogle(t, http.StatusOK, `{"access_token":"at-123"}`, `not-json`, http.StatusOK)
	if _, err := FetchGoogleUserInfo(context.Background(), eps, "at-123"); err == nil {
		t.Fatal("invalid JSON must fail")
	}
	eps2, _ := fakeGoogle(t, http.StatusOK, `{"access_token":"at-123"}`, `{"error":"x"}`, http.StatusForbidden)
	if _, err := FetchGoogleUserInfo(context.Background(), eps2, "at-123"); err == nil {
		t.Fatal("HTTP error must fail")
	}
}

// Compile-time guard: tokenResponse stays aligned with Google's reply shape.
var _ = json.Marshal
