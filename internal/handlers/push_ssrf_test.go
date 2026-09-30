package handlers

import (
	"net/http"
	"testing"
)

// A push endpoint is POSTed by the server later (with the VAPID Authorization
// header attached), so an attacker-supplied internal URL is an authenticated
// SSRF. Subscription must validate the endpoint: https + public host only.
func TestPushSubscribe_RejectsInternalEndpoints(t *testing.T) {
	store, sender := pushStores(t)
	r := pushRouter(store, sender)

	blocked := []string{
		"http://127.0.0.1:8080/push/xyz",      // loopback, insecure scheme
		"https://127.0.0.1/push/xyz",          // loopback over https
		"https://169.254.169.254/metadata/v1", // cloud metadata (link-local)
		"https://192.168.1.10:2268/x",         // RFC1918
		"https://[::1]/push/xyz",              // IPv6 loopback
		"http://push.example.com/e1",          // insecure scheme even when public
	}
	for _, ep := range blocked {
		w := pushDo(r, "POST", "/api/push/subscribe", map[string]any{
			"endpoint": ep,
			"keys":     map[string]string{"p256dh": "p", "auth": "a"},
		})
		if w.Code != http.StatusBadRequest {
			t.Errorf("subscribe %q: status = %d, want 400; body %s", ep, w.Code, w.Body.String())
		}
	}
	subs, _ := store.SubscriptionsFor(1)
	if len(subs) != 0 {
		t.Errorf("blocked endpoints must not be persisted, got %+v", subs)
	}
}

// A well-formed public https endpoint (IP literal, so no DNS is involved) is
// still accepted.
func TestPushSubscribe_AcceptsPublicHTTPS(t *testing.T) {
	store, sender := pushStores(t)
	r := pushRouter(store, sender)

	w := pushDo(r, "POST", "/api/push/subscribe", map[string]any{
		"endpoint": "https://203.0.113.7/fcm/send/abc",
		"keys":     map[string]string{"p256dh": "p", "auth": "a"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("subscribe public endpoint: status = %d, want 200; body %s", w.Code, w.Body.String())
	}
	subs, _ := store.SubscriptionsFor(1)
	if len(subs) != 1 {
		t.Fatalf("expected the public endpoint persisted, got %+v", subs)
	}
}
