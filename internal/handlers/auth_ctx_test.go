package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

// callGone runs h as an authenticated-or-not request whose client dropped
// before the handler touched the database.
func callGone(h gin.HandlerFunc, method, path, body string, params gin.Params, claims *auth.Claims) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader([]byte(body))).WithContext(goneCtx())
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	if claims != nil {
		c.Set("auth.claims", claims)
	}
	h(c)
	return w
}

// A client that vanishes mid-refresh must not leave the family half-rotated:
// the old token is consumed, so a new pair has to exist, otherwise the retry
// past the grace window reads as theft and revokes every session.
func TestRefresh_ClientGone_StillRotatesAtomically(t *testing.T) {
	store := newAuthStore(t)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)
	user := createTestUser(t, store, "gone", "pass")
	refresh, _ := store.CreateRefreshToken(context.Background(), user.ID, time.Hour, false, "", "")

	w := callGone(Refresh(store, tm), http.MethodPost, "/api/auth/refresh", `{"refresh":"`+refresh+`"}`, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (rotation must not depend on the client); body=%s", w.Code, w.Body.String())
	}
	_, _, outcome, err := store.RotateRefreshToken(context.Background(), refresh, time.Minute)
	if err != nil || outcome != auth.RefreshGraceReissue {
		t.Fatalf("old token outcome = %v, %v; want grace reissue (it was consumed)", outcome, err)
	}
	sessions, _ := store.ListSessions(context.Background(), user.ID, "")
	if len(sessions) == 0 {
		t.Fatal("a rotated token must have a replacement session")
	}
}

// Reuse detection must revoke even when the thief's connection is already gone.
func TestRefresh_ClientGone_ReuseStillRevokesAll(t *testing.T) {
	pool := dbtest.NewDB(t)
	store, _ := auth.New(pool)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)
	user := createTestUser(t, store, "stolen", "pass")
	stale, _ := store.CreateRefreshToken(context.Background(), user.ID, time.Hour, false, "", "")
	if _, _, o, _ := store.RotateRefreshToken(context.Background(), stale, time.Minute); o != auth.RefreshRotated {
		t.Fatalf("setup outcome = %v", o)
	}
	live, _ := store.CreateRefreshToken(context.Background(), user.ID, time.Hour, false, "", "")
	// Age the consumption past the handler's grace window.
	if _, err := pool.Exec("UPDATE refresh_tokens SET consumed_at = now() - interval '10 minutes' WHERE consumed_at IS NOT NULL"); err != nil {
		t.Fatal(err)
	}

	w := callGone(Refresh(store, tm), http.MethodPost, "/api/auth/refresh", `{"refresh":"`+stale+`"}`, nil, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	if _, _, err := store.ValidateRefreshToken(context.Background(), live); err == nil {
		t.Fatal("reuse of a rotated token must revoke every session, even when the client is gone")
	}
}

// A password reset that lost its client after SetPassword must still cut the
// old sessions and paired devices off.
func TestReset_ClientGone_StillRevokesEverything(t *testing.T) {
	store := newAuthStore(t)
	user, _, deviceKey := compromisedAccount(t, store)
	tok, err := store.CreateToken(context.Background(), auth.TokenResetPassword, user.ID, "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w := callGone(Reset(store), http.MethodPost, "/api/auth/reset", `{"token":"`+tok+`","password":"newpass1"}`, nil, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	if _, err := store.VerifyPassword(context.Background(), user.Username, "newpass1"); err != nil {
		t.Errorf("new password not persisted: %v", err)
	}
	assertSessionCount(t, store, user.ID, 0)
	assertDeviceKeyRevoked(t, store, deviceKey)
}

func TestChangePassword_ClientGone_StillRevokesOthers(t *testing.T) {
	store := newAuthStore(t)
	user, _, deviceKey := compromisedAccount(t, store)
	claims := &auth.Claims{UserID: user.ID, Username: user.Username, Role: auth.RoleUser}
	w := callGone(ChangePassword(store), http.MethodPost, "/api/auth/password", `{"current":"oldpass1","new":"newpass1"}`, nil, claims)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	assertSessionCount(t, store, user.ID, 0)
	assertDeviceKeyRevoked(t, store, deviceKey)
}

func TestAdminSetPassword_ClientGone_StillRevokesEverything(t *testing.T) {
	store := newAuthStore(t)
	user, _, deviceKey := compromisedAccount(t, store)
	if err := adminSetPassword(goneCtx(), store, user.ID, "newpass1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifyPassword(context.Background(), user.Username, "newpass1"); err != nil {
		t.Errorf("new password not persisted: %v", err)
	}
	assertSessionCount(t, store, user.ID, 0)
	assertDeviceKeyRevoked(t, store, deviceKey)
}

func TestSetUserStatus_ClientGone_DisableStillRevokesSessions(t *testing.T) {
	store := newAuthStore(t)
	user, _, _ := compromisedAccount(t, store)
	admin := &auth.Claims{UserID: user.ID + 1000, Username: "root", Role: auth.RoleAdmin}
	w := callGone(SetUserStatus(store), http.MethodPatch, "/", `{"status":"disabled"}`,
		gin.Params{{Key: "id", Value: strconv.Itoa(user.ID)}}, admin)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	assertSessionCount(t, store, user.ID, 0)
}

// Brute force: a client that sends a bad password and disconnects must still
// be counted, otherwise dropping the connection defeats the lockout.
func TestLogin_ClientGone_FailureStillCountsTowardLockout(t *testing.T) {
	store := newAuthStore(t)
	tm := auth.NewTokenManager([]byte("test-secret-key-32-bytes-long!!"), 15*time.Minute)
	lockout := auth.NewLockout(3, time.Minute)
	createTestUser(t, store, "target", "rightpass")
	for i := 0; i < 3; i++ {
		callGone(Login(store, tm, lockout), http.MethodPost, "/api/auth/login", `{"username":"target","password":"wrong"}`, nil, nil)
	}
	if locked, _ := lockout.Locked("target"); !locked {
		t.Fatal("three bad passwords from vanished clients must lock the account")
	}
}

// Device PIN: guesses from clients that vanish are charged in the database, so
// the profile locks after the limit and even the right PIN is then refused.
func TestCheckDevicePIN_ClientGone_GuessesStillCharged(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "tvuser", "pass")
	key, id, err := store.CreateDeviceKey(context.Background(), user.ID, auth.DeviceKeyMeta{DeviceID: "tv", Name: "TV"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDevicePIN(context.Background(), id, user.ID, "4321"); err != nil {
		t.Fatal(err)
	}
	k, err := store.LookupDeviceKey(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	check := func(ctx context.Context, pin string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
		checkDevicePIN(c, store, k, pin)
		return w.Code
	}
	for i := 0; i < 5; i++ {
		check(goneCtx(), "0000")
	}
	if got := check(context.Background(), "4321"); got != http.StatusTooManyRequests {
		t.Fatalf("right PIN after 5 vanished guesses = %d, want 429 (locked)", got)
	}
}

// Device pairing is single-use in memory: the key must be persisted even when
// the polling TV disconnected, otherwise the pairing is burnt for nothing.
func TestPersistPairedDeviceKey_ClientGone_StillIssuesKey(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "pair", "pass")
	key, _, err := persistPairedDeviceKey(goneCtx(), store, user.ID, auth.DeviceKeyMeta{DeviceID: "tv9", Name: "TV"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupDeviceKey(context.Background(), key); err != nil {
		t.Fatalf("key not persisted: %v", err)
	}
}

// A cancelled DisableTOTP must not leave MFA off while the backup codes live on
// (or the reverse): both statements are one transaction.
func TestDisableTOTP_ClientGone_IsAllOrNothing(t *testing.T) {
	store := newAuthStore(t)
	user := createTestUser(t, store, "mfaoff", "pass")
	_ = store.SetTOTPSecret(context.Background(), user.ID, "SECRET")
	_ = store.EnableTOTP(context.Background(), user.ID)
	if _, err := store.GenerateBackupCodes(context.Background(), user.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err := store.DisableTOTP(goneCtx(), user.ID); err == nil {
		t.Fatal("DisableTOTP on a cancelled ctx should fail")
	}
	if _, enabled, _ := store.GetTOTPSecret(context.Background(), user.ID); !enabled {
		t.Error("MFA must still be enabled after a cancelled disable")
	}
	if n := store.CountBackupCodes(context.Background(), user.ID); n != 3 {
		t.Errorf("backup codes = %d, want 3 untouched", n)
	}
}
