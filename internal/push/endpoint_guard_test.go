package push

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// stubResolve swaps the DNS hook for a canned answer and restores it on cleanup.
func stubResolve(t *testing.T, ips []net.IPAddr, err error) {
	t.Helper()
	prev := resolveEndpointHost
	resolveEndpointHost = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return ips, err
	}
	t.Cleanup(func() { resolveEndpointHost = prev })
}

func TestValidateEndpoint_RejectsInternalOrInsecure(t *testing.T) {
	blocked := []string{
		"http://push.example.com/e1",          // insecure scheme
		"ftp://push.example.com/e1",           // not even http
		"https://127.0.0.1/e1",                // loopback literal
		"https://[::1]/e1",                    // IPv6 loopback
		"https://169.254.169.254/metadata/v1", // link-local metadata
		"https://192.168.1.10/e1",             // RFC1918
		"https://0.0.0.0/e1",                  // unspecified
		"not a url",                           // unparseable
		"",                                    // empty
	}
	for _, ep := range blocked {
		if err := ValidateEndpoint(ep); err == nil {
			t.Errorf("ValidateEndpoint(%q) accepted, want rejection", ep)
		}
	}
}

func TestValidateEndpoint_RejectsUnresolvableAndPrivateDNS(t *testing.T) {
	stubResolve(t, nil, context.DeadlineExceeded)
	if err := ValidateEndpoint("https://fcm.googleapis.com/fcm/send/abc"); err == nil {
		t.Error("unresolvable host must be rejected (fail closed)")
	}
	stubResolve(t, []net.IPAddr{{IP: net.ParseIP("10.0.0.5")}}, nil)
	if err := ValidateEndpoint("https://fcm.googleapis.com/fcm/send/abc"); err == nil {
		t.Error("DNS resolving to a private IP must be rejected")
	}
}

func TestValidateEndpoint_AcceptsPublicHTTPS(t *testing.T) {
	stubResolve(t, []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil)
	if err := ValidateEndpoint("https://fcm.googleapis.com/fcm/send/abc"); err != nil {
		t.Fatalf("public https endpoint rejected: %v", err)
	}
	// IP literals skip DNS entirely.
	if err := ValidateEndpoint("https://203.0.113.7/fcm/send/abc"); err != nil {
		t.Fatalf("public IP-literal endpoint rejected: %v", err)
	}
}

// The sender re-checks at send time: an internal endpoint (e.g. persisted by an
// older version, before subscribe-time validation) is skipped and kept — only
// the push service itself may declare it dead (404/410).
func TestSendOne_SkipsNonPublicEndpoint(t *testing.T) {
	pool := newTestStore(t)
	sender, err := NewSender(pool) // default guard — NOT the permissive test helper
	if err != nil {
		t.Fatal(err)
	}
	var delivered atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	if err := sender.store.Subscribe(1, srv.URL, "p", "a"); err != nil {
		t.Fatal(err)
	}
	if err := sender.NotifyUser(context.Background(), 1, "T", "b", ""); err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != 0 {
		t.Fatalf("loopback endpoint must NOT be delivered to, got %d posts", delivered.Load())
	}
	subs, _ := sender.store.SubscriptionsFor(1)
	if len(subs) != 1 {
		t.Fatalf("skip must keep the subscription row, got %+v", subs)
	}
}
