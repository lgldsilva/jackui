package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/dbtest"
)

type deviceEnv struct {
	r     *gin.Engine
	store *auth.Store
	tm    *auth.TokenManager
	alice *auth.User
	bob   *auth.User
}

// newDeviceEnv mirrors cmd/server's wiring of the device routes.
func newDeviceEnv(t *testing.T) *deviceEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store, err := auth.New(dbtest.NewDB(t))
	if err != nil {
		t.Fatal(err)
	}
	tm := auth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Minute)
	flow := auth.NewDeviceFlow()
	lock := auth.NewLockout(3, time.Minute)
	r := gin.New()
	pub := r.Group("/api/auth")
	pub.POST("/device/start", DeviceStart(flow))
	pub.POST("/device/token", DeviceToken(flow, store, tm))
	pub.POST("/device/session", DeviceSession(store, tm))
	pub.GET("/device/profiles", DeviceProfiles(store))
	api := r.Group("/api", auth.Required(tm), auth.GuestRestrict(), auth.DeviceRestrict())
	api.GET("/auth/device/pending", DevicePending(flow, lock))
	api.POST("/auth/device/approve", DeviceApprove(flow, lock))
	api.GET("/auth/devices", ListDevices(store))
	api.DELETE("/auth/devices/:id", RevokeDevice(store))
	api.POST("/auth/devices/:id/pin", SetDevicePIN(store))
	api.GET("/auth/me", Me(store))
	env := &deviceEnv{r: r, store: store, tm: tm}
	env.alice = env.mkUser(t, "alice", auth.RoleUser)
	env.bob = env.mkUser(t, "bob", auth.RoleAdmin)
	return env
}

func (e *deviceEnv) mkUser(t *testing.T, name string, role auth.Role) *auth.User {
	t.Helper()
	id, err := e.store.CreateUser(name, "pw-"+name+"-123456", role)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := e.store.GetUserByID(id)
	return u
}

func (e *deviceEnv) bearer(t *testing.T, u *auth.User) string {
	t.Helper()
	tok, _, err := e.tm.SignAccess(u)
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + tok
}

func (e *deviceEnv) do(method, path, authz string, body any) (*httptest.ResponseRecorder, map[string]any) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	req.RemoteAddr = "192.0.2.10:1234"
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// pair runs the whole flow for user u on deviceID and returns the token response.
func (e *deviceEnv) pair(t *testing.T, u *auth.User, deviceID string) map[string]any {
	t.Helper()
	w, start := e.do(http.MethodPost, "/api/auth/device/start", "", gin.H{"deviceId": deviceID, "name": "Sala\x07", "model": "Shield"})
	if w.Code != http.StatusOK {
		t.Fatalf("start = %d %s", w.Code, w.Body)
	}
	code := start["userCode"].(string)
	if start["verificationPath"] != "/link?code="+code {
		t.Fatalf("verificationPath = %v", start["verificationPath"])
	}
	if _, body := e.do(http.MethodPost, "/api/auth/device/token", "", gin.H{"deviceCode": start["deviceCode"]}); body["error"] != auth.DeviceErrPending {
		t.Fatalf("pre-approval poll = %v", body)
	}
	w, pend := e.do(http.MethodGet, "/api/auth/device/pending?code="+strings.ToLower(code), e.bearer(t, u), nil)
	if w.Code != http.StatusOK || pend["name"] != "Sala" || pend["ip"] != "192.0.2.10" {
		t.Fatalf("pending = %d %v", w.Code, pend)
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/device/approve", e.bearer(t, u), gin.H{"userCode": code, "approve": true}); w.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", w.Code, w.Body)
	}
	w, tok := e.do(http.MethodPost, "/api/auth/device/token", "", gin.H{"deviceCode": start["deviceCode"]})
	if w.Code != http.StatusOK {
		t.Fatalf("token = %d %s", w.Code, w.Body)
	}
	return tok
}

func TestDevicePairing_EndToEnd(t *testing.T) {
	e := newDeviceEnv(t)
	tok := e.pair(t, e.alice, "tv-1")
	key, _ := tok["deviceKey"].(string)
	if !strings.HasPrefix(key, auth.DeviceKeyPrefix) || tok["access"] == "" || tok["mediaToken"] == "" {
		t.Fatalf("token response = %v", tok)
	}
	claims, err := e.tm.ParseAccess(tok["access"].(string))
	if err != nil || claims.AMR != auth.AMRDevice || claims.UserID != e.alice.ID {
		t.Fatalf("access claims = %+v %v", claims, err)
	}

	w, sess := e.do(http.MethodPost, "/api/auth/device/session", auth.DevicePrefix+key, nil)
	if w.Code != http.StatusOK || sess["deviceKey"] != nil {
		t.Fatalf("session = %d %v (must not re-emit the key)", w.Code, sess)
	}
	devAuth := "Bearer " + sess["access"].(string)
	if w, _ := e.do(http.MethodGet, "/api/auth/me", devAuth, nil); w.Code != http.StatusOK {
		t.Fatalf("device token on /me = %d", w.Code)
	}
	if w, _ := e.do(http.MethodGet, "/api/auth/devices", devAuth, nil); w.Code != http.StatusForbidden {
		t.Fatalf("device token on /devices = %d, want 403", w.Code)
	}

	w, list := e.do(http.MethodGet, "/api/auth/devices", e.bearer(t, e.alice), nil)
	devs := list["devices"].([]any)
	if w.Code != http.StatusOK || len(devs) != 1 {
		t.Fatalf("list = %d %v", w.Code, list)
	}
	id := int(devs[0].(map[string]any)["id"].(float64))
	if w, _ := e.do(http.MethodDelete, "/api/auth/devices/"+itoa(id), e.bearer(t, e.alice), nil); w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d", w.Code)
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/device/session", auth.DevicePrefix+key, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key session = %d, want 401", w.Code)
	}
}

func TestDevicePairing_DeniedAndUnknownCodes(t *testing.T) {
	e := newDeviceEnv(t)
	_, start := e.do(http.MethodPost, "/api/auth/device/start", "", gin.H{"deviceId": "tv-9"})
	if w, _ := e.do(http.MethodPost, "/api/auth/device/approve", e.bearer(t, e.alice), gin.H{"userCode": start["userCode"], "approve": false}); w.Code != http.StatusOK {
		t.Fatalf("deny = %d", w.Code)
	}
	if _, body := e.do(http.MethodPost, "/api/auth/device/token", "", gin.H{"deviceCode": start["deviceCode"]}); body["error"] != auth.DeviceErrDenied {
		t.Fatalf("denied poll = %v", body)
	}
	for i := 0; i < 3; i++ {
		if w, _ := e.do(http.MethodGet, "/api/auth/device/pending?code=ZZZZ-ZZZZ", e.bearer(t, e.alice), nil); w.Code != http.StatusNotFound {
			t.Fatalf("unknown code = %d", w.Code)
		}
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/device/approve", e.bearer(t, e.alice), gin.H{"userCode": "ZZZZ-ZZZZ", "approve": true}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("after 3 misses = %d, want 429", w.Code)
	}
	for _, body := range []any{nil, gin.H{"deviceId": "  "}} {
		if w, _ := e.do(http.MethodPost, "/api/auth/device/start", "", body); w.Code != http.StatusBadRequest {
			t.Fatalf("bad start %v = %d", body, w.Code)
		}
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/device/token", "", gin.H{}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty token req = %d", w.Code)
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/device/approve", e.bearer(t, e.alice), gin.H{}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty approve = %d", w.Code)
	}
}

func TestDevicePairing_ProfilesPINAndIsolation(t *testing.T) {
	e := newDeviceEnv(t)
	aKey := e.pair(t, e.alice, "tv-1")["deviceKey"].(string)
	bTok := e.pair(t, e.bob, "tv-1")
	bKey := bTok["deviceKey"].(string)

	w, prof := e.do(http.MethodGet, "/api/auth/device/profiles", auth.DevicePrefix+aKey, nil)
	if w.Code != http.StatusOK || len(prof["profiles"].([]any)) != 2 {
		t.Fatalf("profiles = %d %v", w.Code, prof)
	}
	if w, _ := e.do(http.MethodGet, "/api/auth/device/profiles", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("profiles w/o key = %d", w.Code)
	}

	bID := itoa(int(bTok["deviceKeyId"].(float64)))
	// Alice can't touch Bob's key; Bob (admin) sets his own PIN.
	if w, _ := e.do(http.MethodPost, "/api/auth/devices/"+bID+"/pin", e.bearer(t, e.alice), gin.H{"pin": "1234"}); w.Code != http.StatusNotFound {
		t.Fatalf("foreign pin = %d", w.Code)
	}
	if w, _ := e.do(http.MethodDelete, "/api/auth/devices/"+bID, e.bearer(t, e.alice), nil); w.Code != http.StatusNotFound {
		t.Fatalf("foreign revoke = %d", w.Code)
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/devices/"+bID+"/pin", e.bearer(t, e.bob), gin.H{"pin": "12"}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad pin = %d", w.Code)
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/devices/"+bID+"/pin", e.bearer(t, e.bob), gin.H{"pin": "4321"}); w.Code != http.StatusOK {
		t.Fatalf("set pin = %d", w.Code)
	}
	w, body := e.do(http.MethodPost, "/api/auth/device/session", auth.DevicePrefix+bKey, nil)
	if w.Code != http.StatusUnauthorized || body["pinRequired"] != true {
		t.Fatalf("no pin = %d %v", w.Code, body)
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/device/session", auth.DevicePrefix+bKey, gin.H{"pin": "4321"}); w.Code != http.StatusOK {
		t.Fatalf("right pin = %d", w.Code)
	}
	// Admin's device token is still denied the admin surface.
	w, sess := e.do(http.MethodPost, "/api/auth/device/session", auth.DevicePrefix+aKey, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("alice session = %d", w.Code)
	}
	if w, l := e.do(http.MethodGet, "/api/auth/devices?all=1", e.bearer(t, e.bob), nil); w.Code != http.StatusOK || len(l["devices"].([]any)) != 2 {
		t.Fatalf("admin all = %d %v", w.Code, l)
	}
	if w, l := e.do(http.MethodGet, "/api/auth/devices?all=1", e.bearer(t, e.alice), nil); w.Code != http.StatusOK || len(l["devices"].([]any)) != 1 {
		t.Fatalf("non-admin all must be own only = %d %v", w.Code, l)
	}
	_ = sess
	if err := e.store.SetStatus(e.alice.ID, auth.StatusDisabled); err != nil {
		t.Fatal(err)
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/device/session", auth.DevicePrefix+aKey, nil); w.Code != http.StatusForbidden {
		t.Fatalf("disabled user session = %d, want 403", w.Code)
	}
	if w, _ := e.do(http.MethodDelete, "/api/auth/devices/abc", e.bearer(t, e.bob), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d", w.Code)
	}
}

func TestDeviceToken_DisabledApproverGetsDenied(t *testing.T) {
	e := newDeviceEnv(t)
	_, start := e.do(http.MethodPost, "/api/auth/device/start", "", gin.H{"deviceId": "tv-2"})
	e.do(http.MethodPost, "/api/auth/device/approve", e.bearer(t, e.alice), gin.H{"userCode": start["userCode"], "approve": true})
	_ = e.store.SetStatus(e.alice.ID, auth.StatusDisabled)
	if _, body := e.do(http.MethodPost, "/api/auth/device/token", "", gin.H{"deviceCode": start["deviceCode"]}); body["error"] != auth.DeviceErrDenied {
		t.Fatalf("disabled approver = %v", body)
	}
}

func TestCleanDeviceField(t *testing.T) {
	long := strings.Repeat("x", 100)
	if got := cleanDeviceField(" a\nb\x7f "); got != "ab" {
		t.Fatalf("got %q", got)
	}
	if got := cleanDeviceField(long); len(got) != deviceFieldMax {
		t.Fatalf("len = %d", len(got))
	}
}
