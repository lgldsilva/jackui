package imagesearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// privateImageServer is a loopback image host standing in for a LAN/metadata
// target. hits counts requests that actually reached it.
func privateImageServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(make([]byte, 2048))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func addrOf(srv *httptest.Server) string { return strings.TrimPrefix(srv.URL, "http://") }

// The client Default() hands to the sources must refuse a non-public address.
func TestDefaultClientRefusesLoopbackImage(t *testing.T) {
	img, hits := privateImageServer(t)

	_, _, err := downloadImage(context.Background(), Default().http, img.URL)
	if err == nil {
		t.Fatal("expected the download of a loopback image URL to be refused")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("expected an SSRF guard error, got %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("private server was reached %d time(s)", n)
	}
}

// A search result pointing at a private address is skipped, not fetched. The
// fake search engine is the only trusted address, standing in for a public host.
func TestSearchResultPointingAtPrivateAddressIsRefused(t *testing.T) {
	img, hits := privateImageServer(t)
	html := `<a class="iusc" m="{&quot;murl&quot;:&quot;` + img.URL + `&quot;}">`
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(html))
	}))
	defer engine.Close()

	b := NewBing(newHTTPClient(addrOf(engine)))
	b.baseURL = engine.URL
	data, _, err := b.Find(context.Background(), "some title")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if data != nil {
		t.Fatalf("expected no image from a private address, got %d bytes", len(data))
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("private server was reached %d time(s)", n)
	}
}

// A public-looking host that redirects to a private address must not be followed.
func TestRedirectToPrivateAddressIsRefused(t *testing.T) {
	img, hits := privateImageServer(t)
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, img.URL+"/secret.jpg", http.StatusFound)
	}))
	defer public.Close()

	_, _, err := downloadImage(context.Background(), newHTTPClient(addrOf(public)), public.URL)
	if err == nil {
		t.Fatal("expected the redirect to a private address to be refused")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("expected an SSRF guard error, got %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("private server was reached %d time(s)", n)
	}
}
