package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/push"
)

func pushStores(t *testing.T) (*push.Store, *push.Sender) {
	t.Helper()
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1, 2, 3)
	s, err := push.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := push.NewSender(s)
	if err != nil {
		t.Fatal(err)
	}
	return s, sender
}

func pushRouter(store *push.Store, sender *push.Sender) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	authn := func(c *gin.Context) { setAuth(c, 1, false) }
	r.GET("/api/push/vapid", authn, PushVapidKey(sender))
	r.POST("/api/push/subscribe", authn, PushSubscribe(store))
	r.POST("/api/push/unsubscribe", authn, PushUnsubscribe(store))
	r.GET("/api/notifications", authn, NotificationsList(store))
	r.POST("/api/notifications/read", authn, NotificationsMarkRead(store))
	return r
}

func pushDo(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		req = httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestPush_NilStores(t *testing.T) {
	r := pushRouter(nil, nil)
	if w := pushDo(r, "GET", "/api/push/vapid", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("vapid status = %d", w.Code)
	}
	if w := pushDo(r, "POST", "/api/push/subscribe", map[string]any{"endpoint": "x"}); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("subscribe status = %d", w.Code)
	}
	// The feed degrades to empty, not error — the bell just shows nothing.
	if w := pushDo(r, "GET", "/api/notifications", nil); w.Code != http.StatusOK {
		t.Fatalf("notifications status = %d", w.Code)
	}
	if w := pushDo(r, "POST", "/api/notifications/read", nil); w.Code != http.StatusOK {
		t.Fatalf("read status = %d", w.Code)
	}
}

func TestPush_SubscribeListUnsubscribe(t *testing.T) {
	store, sender := pushStores(t)
	r := pushRouter(store, sender)

	w := pushDo(r, "GET", "/api/push/vapid", nil)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("key")) {
		t.Fatalf("vapid: %d %s", w.Code, w.Body.String())
	}

	sub := map[string]any{"endpoint": "https://push/e1", "keys": map[string]string{"p256dh": "p", "auth": "a"}}
	if w := pushDo(r, "POST", "/api/push/subscribe", sub); w.Code != http.StatusOK {
		t.Fatalf("subscribe: %d %s", w.Code, w.Body.String())
	}
	if w := pushDo(r, "POST", "/api/push/subscribe", map[string]any{"endpoint": ""}); w.Code != http.StatusBadRequest {
		t.Fatalf("subscribe missing keys should 400, got %d", w.Code)
	}

	if err := store.AddNotification(1, "Hi", "body", ""); err != nil {
		t.Fatal(err)
	}
	w = pushDo(r, "GET", "/api/notifications?limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("notifications: %d", w.Code)
	}
	var out struct {
		Items  []push.Notification `json:"items"`
		Unread int                 `json:"unread"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Unread != 1 {
		t.Fatalf("feed mismatch: %+v", out)
	}

	if w := pushDo(r, "POST", "/api/notifications/read", nil); w.Code != http.StatusOK {
		t.Fatalf("mark read: %d", w.Code)
	}
	if n, _ := store.UnreadCount(1); n != 0 {
		t.Fatalf("unread after read = %d", n)
	}

	if w := pushDo(r, "POST", "/api/push/unsubscribe", map[string]string{"endpoint": "https://push/e1"}); w.Code != http.StatusOK {
		t.Fatalf("unsubscribe: %d", w.Code)
	}
	if w := pushDo(r, "POST", "/api/push/unsubscribe", map[string]string{"endpoint": ""}); w.Code != http.StatusBadRequest {
		t.Fatalf("unsubscribe empty should 400, got %d", w.Code)
	}
}

func TestPush_ClosedStore500s(t *testing.T) {
	// Build store+sender on a live pool, then close the pool to force 500s
	// (Store.Close is a no-op now that the pool is shared/owned by main).
	pool := dbtest.NewDB(t)
	dbtest.SeedUsers(t, pool, 1)
	store, err := push.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := push.NewSender(store)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	r := pushRouter(store, sender)
	if w := pushDo(r, "POST", "/api/push/unsubscribe", map[string]string{"endpoint": "https://x"}); w.Code != http.StatusInternalServerError {
		t.Fatalf("unsubscribe on closed store = %d, want 500", w.Code)
	}
	if w := pushDo(r, "GET", "/api/notifications", nil); w.Code != http.StatusInternalServerError {
		t.Fatalf("notifications on closed store = %d, want 500", w.Code)
	}
	if w := pushDo(r, "POST", "/api/notifications/read", nil); w.Code != http.StatusInternalServerError {
		t.Fatalf("mark read on closed store = %d, want 500", w.Code)
	}
}
