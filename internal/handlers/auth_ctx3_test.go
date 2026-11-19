package handlers

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/lgldsilva/jackui/internal/auth"
)

// The token is spent and the flag flipped as one action: a client that drops in
// between must not leave the token consumed with the email still unverified.
func TestVerifyEmail_ClientGone_ConsumesAndVerifiesTogether(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "verif", "pass")
	tok, err := store.CreateToken(context.Background(), auth.TokenVerifyEmail, user.ID, "v@example.com", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w := callGone(VerifyEmail(store), http.MethodPost, "/", `{"token":"`+tok+`"}`, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	u, _ := store.GetUserByID(context.Background(), user.ID)
	if u == nil || !u.EmailVerified {
		t.Fatal("email must be verified once the token is consumed")
	}
}

func TestVerifyEmail_InvalidTokenStaysRejected(t *testing.T) {
	store := newAuthStore(t)
	w := callGone(VerifyEmail(store), http.MethodPost, "/", `{"token":"nope"}`, nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// Invite redemption, account creation and the verify token are one action.
func TestRegister_ClientGone_WithInvite_CreatesActiveUser(t *testing.T) {
	store := newAuthStore(t)
	inv, err := store.CreateToken(context.Background(), auth.TokenInvite, 0, "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"username":"newbie","email":"n@example.com","password":"secret1","invite":"` + inv + `"}`
	w := callGone(Register(store, nil, "https://example.com"), http.MethodPost, "/", body, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	u, _ := store.GetUserByUsername(context.Background(), "newbie")
	if u == nil || u.Status != auth.StatusActive {
		t.Fatalf("user = %+v, want active", u)
	}
	if _, err := store.ConsumeToken(context.Background(), inv, auth.TokenInvite); err == nil {
		t.Error("invite should be spent")
	}
}

func TestRegister_InvalidInviteIs400(t *testing.T) {
	store := newAuthStore(t)
	body := `{"username":"x1","email":"x@example.com","password":"secret1","invite":"bogus"}`
	w := callGone(Register(store, nil, "https://example.com"), http.MethodPost, "/", body, nil, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// A failed account creation must leave the invite usable.
func TestRegister_FailedCreateKeepsInvite(t *testing.T) {
	store := newAuthStore(t)
	createTestUser(t, store, "taken", "pass")
	inv, _ := store.CreateToken(context.Background(), auth.TokenInvite, 0, "", time.Hour)
	// Same username, different email: passes the Exists pre-check? No — Exists
	// matches username too, so go straight at the store to hit the unique key.
	if _, err := store.CreateUserWithInvite(context.Background(), inv, "taken", "t2@example.com", "secret1", auth.RoleUser); err == nil {
		t.Fatal("expected a unique violation")
	}
	if _, err := store.ConsumeToken(context.Background(), inv, auth.TokenInvite); err != nil {
		t.Fatalf("invite must survive a failed create: %v", err)
	}
}

// persistCredential advances the sign counter even when the client is gone.
func TestPersistCredential_ClientGone_AdvancesCounter(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "pk", "pass")
	cred := &webauthn.Credential{ID: []byte("cred-pk")}
	if err := store.AddCredential(context.Background(), user.ID, cred); err != nil {
		t.Fatal(err)
	}
	cred.Authenticator.SignCount = 7
	persistCredential(goneCtx(), store, cred)
	got, _ := store.Credentials(context.Background(), user.ID)
	if len(got) != 1 || got[0].Authenticator.SignCount != 7 {
		t.Fatalf("credentials = %+v, want sign count 7", got)
	}
}
