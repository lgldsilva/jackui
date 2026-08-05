package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/downloads"
	"github.com/lgldsilva/jackui/internal/streamer"
)

// closedDownloadsStore returns a store whose pool has been closed, so every
// query fails — exercises the 500 error branches of the store-backed handlers.
func closedDownloadsStore(t *testing.T) *downloads.Store {
	t.Helper()
	pool := dbtest.NewDB(t)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}
	pool.Close()
	return store
}

func TestDownloadsListHandlersStoreErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := closedDownloadsStore(t)
	claims := &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin}

	for name, handler := range map[string]gin.HandlerFunc{
		"list":       DownloadsList(store, nil, nil, nil, ""),
		"filtered":   DownloadsListFiltered(store, nil, nil, nil),
		"list all":   DownloadsListAll(store, nil, nil, nil),
		"users":      DownloadsUsers(store, nil),
		"trackers":   DownloadsTrackers(store),
		"categories": DownloadsCategories(store),
		"pause all":  DownloadsPauseAll(store),
		"resume all": DownloadsResumeAll(store),
	} {
		t.Run(name, func(t *testing.T) {
			w := invokeCoverageHandlerWithClaims(t, handler, http.MethodGet, "/api/downloads", "", nil, claims)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
			}
		})
	}
}

func TestDownloadsBatchPauseResumeStoreErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := closedDownloadsStore(t)

	for name, handler := range map[string]gin.HandlerFunc{
		"batch pause":  DownloadsBatchPause(store),
		"batch resume": DownloadsBatchResume(store),
	} {
		t.Run(name, func(t *testing.T) {
			w := invokeCoverageHandler(t, handler, http.MethodPatch, "/api/downloads/batch", `{"ids":[1]}`)
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusInternalServerError, w.Body.String())
			}
		})
	}
}

func TestDownloadsCreateDestinationAndStoreErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claims := &auth.Claims{UserID: 1, Username: "user", Role: auth.RoleUser}
	dests := &DestinationService{SharedDir: t.TempDir()}

	// A destBase that is not one of the user's destinations is rejected (400).
	badDest := `{"infoHash":"h","magnet":"m","fileIndex":0,"destBase":"/not-configured"}`
	w := invokeCoverageHandlerWithClaims(t, DownloadsCreate(&downloads.Store{}, dests), http.MethodPost, "/api/downloads", badDest, nil, claims)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create bad dest status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	badDestBatch := `{"infoHash":"h","magnet":"m","files":[{"fileIndex":0}],"destBase":"/not-configured"}`
	w = invokeCoverageHandlerWithClaims(t, DownloadsBatchCreate(&downloads.Store{}, dests), http.MethodPost, "/api/downloads/batch", badDestBatch, nil, claims)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("batch bad dest status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	// Valid requests against a closed store surface the store error (500).
	store := closedDownloadsStore(t)
	valid := `{"infoHash":"h","magnet":"m","fileIndex":0}`
	w = invokeCoverageHandlerWithClaims(t, DownloadsCreate(store, dests), http.MethodPost, "/api/downloads", valid, nil, claims)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("create store error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	validBatch := `{"infoHash":"h","magnet":"m","files":[{"fileIndex":0}]}`
	w = invokeCoverageHandlerWithClaims(t, DownloadsBatchCreate(store, dests), http.MethodPost, "/api/downloads/batch", validBatch, nil, claims)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("batch store error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDownloadsSetPriorityMalformedJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	params := gin.Params{{Key: "id", Value: "1"}}
	w := invokeCoverageHandlerWithParams(t, DownloadsSetPriority(&downloads.Store{}), http.MethodPatch, "/api/downloads/1/priority", "{", params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDownloadsRecheckInvalidInfoHash(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	store, err := downloads.New(pool)
	if err != nil {
		t.Fatalf("downloads.New: %v", err)
	}
	d, err := store.Create(downloads.Download{UserID: 0, InfoHash: "not-a-hex-hash", FileIndex: 0, Name: "x", Magnet: "magnet:?xt=urn:btih:nothex"})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	params := gin.Params{{Key: "id", Value: strconv.Itoa(d.ID)}}
	handler := DownloadsRecheck(store, streamer.NewForTesting())
	w := invokeCoverageHandlerWithParams(t, handler, http.MethodPost, "/api/downloads/recheck", "", params)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestRefreshStoreAndOutcomeErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Closed pool: the rotation itself errors → 500.
	w := invokeCoverageHandler(t, Refresh(closedAuthStore(t), nil), http.MethodPost, "/api/auth/refresh", `{"refresh":"tok"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("closed store status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	// Live store, unknown token: outcome RefreshInvalid → 401.
	pool := dbtest.NewDB(t)
	store, err := auth.New(pool)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	w = invokeCoverageHandler(t, Refresh(store, nil), http.MethodPost, "/api/auth/refresh", `{"refresh":"bogus"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

type failCleaner struct{}

func (failCleaner) DeleteIncognito(int) error { return errors.New("db down") }

func (failCleaner) DeleteAllIncognito() error { return errors.New("db down") }

func TestClearIncognitoCleanerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claims := &auth.Claims{UserID: 1, Username: "user", Role: auth.RoleUser}
	w := invokeCoverageHandlerWithClaims(t, ClearIncognito(failCleaner{}), http.MethodDelete, "/api/user/incognito", "", nil, claims)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestRevokeOtherSessionsStoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claims := &auth.Claims{UserID: 1, Username: "user", Role: auth.RoleUser}
	w := invokeCoverageHandlerWithClaims(t, RevokeOtherSessions(closedAuthStore(t)), http.MethodPost, "/api/auth/sessions/revoke-others", `{"refresh":"tok"}`, nil, claims)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestRegisterAndInviteStoreErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := `{"username":"newuser","email":"new@example.com","password":"123456"}`
	w := invokeCoverageHandler(t, Register(closedAuthStore(t), nil, "https://example.com"), http.MethodPost, "/api/auth/register", body)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("register status = %d, want %d", w.Code, http.StatusInternalServerError)
	}

	w = invokeCoverageHandler(t, Invite(closedAuthStore(t), nil, "https://example.com"), http.MethodPost, "/api/auth/invite", `{}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("invite status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestResetInvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	store, err := auth.New(pool)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	w := invokeCoverageHandler(t, Reset(store), http.MethodPost, "/api/auth/reset", `{"token":"bogus","password":"123456"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestMFAEnrollStartStoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claims := &auth.Claims{UserID: 1, Username: "user", Role: auth.RoleUser}
	w := invokeCoverageHandlerWithClaims(t, MFAEnrollStart(closedAuthStore(t)), http.MethodPost, "/api/auth/mfa/enroll", "", nil, claims)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func newTestWAManager(t *testing.T) *auth.WAManager {
	t.Helper()
	wa, err := auth.NewWAManager("localhost", "JackUI", "http://localhost")
	if err != nil || wa == nil {
		t.Fatalf("NewWAManager: %v", err)
	}
	return wa
}

func TestPasskeyRegisterFinishSessionExpired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Closed store: Credentials error is ignored by the handler; the bogus
	// session id makes FinishRegister fail → 400.
	claims := &auth.Claims{UserID: 1, Username: "user", Role: auth.RoleUser}
	handler := PasskeyRegisterFinish(closedAuthStore(t), newTestWAManager(t))
	w := invokeCoverageHandlerWithClaims(t, handler, http.MethodPost, "/api/auth/passkey/register/finish?session=bogus", "", nil, claims)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestPasskeyLoginFinishSessionExpired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool := dbtest.NewDB(t)
	store, err := auth.New(pool)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	if _, err := store.CreateUserFull("pkuser", "pk@example.com", "secret1", auth.RoleUser, auth.StatusActive); err != nil {
		t.Fatalf("CreateUserFull: %v", err)
	}
	handler := PasskeyLoginFinish(store, nil, newTestWAManager(t))
	w := invokeCoverageHandler(t, handler, http.MethodPost, "/api/auth/passkey/login/finish?username=pkuser&session=bogus", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestPasskeyDeleteStoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claims := &auth.Claims{UserID: 1, Username: "user", Role: auth.RoleUser}
	params := gin.Params{{Key: "id", Value: "abc"}}
	w := invokeCoverageHandlerWithClaims(t, PasskeyDelete(closedAuthStore(t)), http.MethodDelete, "/api/auth/passkey/abc", "", params, claims)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
