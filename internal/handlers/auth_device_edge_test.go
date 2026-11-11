package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/auth"
)

func noClaimsCtx(method, body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func TestDeviceHandlers_RequireClaims(t *testing.T) {
	for name, h := range map[string]gin.HandlerFunc{
		"list":    ListDevices(nil),
		"revoke":  RevokeDevice(nil),
		"pin":     SetDevicePIN(nil),
		"approve": DeviceApprove(auth.NewDeviceFlow(), auth.NewLockout(3, 0)),
	} {
		c, w := noClaimsCtx(http.MethodPost, `{}`)
		h(c)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s without claims = %d, want 401", name, w.Code)
		}
	}
}

func TestSetDevicePIN_BadBody(t *testing.T) {
	c, w := noClaimsCtx(http.MethodPost, `not json`)
	setAuth(c, 1, false)
	c.Params = gin.Params{{Key: "id", Value: "4"}}
	SetDevicePIN(nil)(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d", w.Code)
	}
}

func TestDeviceStart_FlowFull(t *testing.T) {
	flow := auth.NewDeviceFlow()
	h := DeviceStart(flow)
	codes := []int{}
	for i := 0; i < 6; i++ {
		c, w := noClaimsCtx(http.MethodPost, `{"deviceId":"tv"}`)
		h(c)
		codes = append(codes, w.Code)
	}
	if codes[0] != http.StatusOK || codes[5] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v (6th start from one IP must be 429)", codes)
	}
}

func TestDeviceSession_PINLockoutIs429(t *testing.T) {
	e := newDeviceEnv(t)
	tok := e.pair(t, e.alice, "tv-1")
	key := auth.DevicePrefix + tok["deviceKey"].(string)
	id := itoa(int(tok["deviceKeyId"].(float64)))
	if w, _ := e.do(http.MethodPost, "/api/auth/devices/"+id+"/pin", e.bearer(t, e.alice), gin.H{"pin": "2468"}); w.Code != http.StatusOK {
		t.Fatalf("set pin = %d", w.Code)
	}
	last := 0
	for i := 0; i < 5; i++ {
		w, _ := e.do(http.MethodPost, "/api/auth/device/session", key, gin.H{"pin": "0000"})
		last = w.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("5th wrong PIN = %d, want 429", last)
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/device/session", key, gin.H{"pin": "2468"}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked profile = %d, want 429", w.Code)
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/devices/"+id+"/pin", e.bearer(t, e.alice), gin.H{"pin": ""}); w.Code != http.StatusOK {
		t.Fatalf("clear pin = %d", w.Code)
	}
	if w, _ := e.do(http.MethodPost, "/api/auth/device/session", key, nil); w.Code != http.StatusOK {
		t.Fatalf("after clearing PIN = %d", w.Code)
	}
}

func TestDeviceToken_SlowDownCarriesInterval(t *testing.T) {
	e := newDeviceEnv(t)
	_, start := e.do(http.MethodPost, "/api/auth/device/start", "", gin.H{"deviceId": "tv-3"})
	e.do(http.MethodPost, "/api/auth/device/token", "", gin.H{"deviceCode": start["deviceCode"]})
	_, body := e.do(http.MethodPost, "/api/auth/device/token", "", gin.H{"deviceCode": start["deviceCode"]})
	if body["error"] != auth.DeviceErrSlowDown || body["interval"] == nil {
		t.Fatalf("fast poll = %v", body)
	}
}
