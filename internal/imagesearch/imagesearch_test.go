package imagesearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// imageServer serves a 2 KB payload labelled as a JPEG — enough to pass
// downloadImage's content-type + size gates without a real encoder.
func imageServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(make([]byte, 2048))
	}))
}

func ddgServer(t *testing.T, imageURL string, withToken bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if withToken {
			w.Write([]byte(`<html><script>vqd="4-987654321";</script></html>`))
		} else {
			w.Write([]byte(`<html>no token here</html>`))
		}
	})
	mux.HandleFunc("/i.js", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("p") != "-1" {
			t.Errorf("expected safe-search off (p=-1), got %q", r.URL.Query().Get("p"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"image":"` + imageURL + `","width":600,"height":900}]}`))
	})
	return httptest.NewServer(mux)
}

func newDDG(srv *httptest.Server) *DuckDuckGo {
	d := NewDuckDuckGo(http.DefaultClient)
	d.htmlURL = srv.URL + "/"
	d.apiURL = srv.URL + "/i.js"
	return d
}

func TestDuckDuckGoFindsImage(t *testing.T) {
	img := imageServer(t)
	defer img.Close()
	srv := ddgServer(t, img.URL, true)
	defer srv.Close()

	d := newDDG(srv)
	data, ct, err := d.Find(context.Background(), "Some Movie 2020")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(data) == 0 || !strings.HasPrefix(ct, "image/") {
		t.Fatalf("expected image bytes, got %d bytes ct=%q", len(data), ct)
	}
}

func TestDuckDuckGoNoTokenReturnsNothing(t *testing.T) {
	img := imageServer(t)
	defer img.Close()
	srv := ddgServer(t, img.URL, false) // page has no vqd
	defer srv.Close()

	d := newDDG(srv)
	data, _, err := d.Find(context.Background(), "x")
	if err != nil || data != nil {
		t.Fatalf("expected (nil,nil) when no token, got data=%d err=%v", len(data), err)
	}
}

func TestBingScrapesMurl(t *testing.T) {
	img := imageServer(t)
	defer img.Close()
	html := `<a class="iusc" m="{&quot;cid&quot;:&quot;x&quot;,&quot;murl&quot;:&quot;` + img.URL + `&quot;}">`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("safeSearch") != "Off" {
			t.Errorf("expected safeSearch=Off, got %q", r.URL.Query().Get("safeSearch"))
		}
		w.Write([]byte(html))
	}))
	defer srv.Close()

	b := NewBing(http.DefaultClient)
	b.baseURL = srv.URL
	data, ct, err := b.Find(context.Background(), "adult title")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(data) == 0 || !strings.HasPrefix(ct, "image/") {
		t.Fatalf("expected image, got %d bytes ct=%q", len(data), ct)
	}
}

// When the first source finds nothing, the chain falls through to the next.
func TestChainFallsThrough(t *testing.T) {
	img := imageServer(t)
	defer img.Close()
	ddg := ddgServer(t, img.URL, false) // DDG yields nothing (no token)
	defer ddg.Close()
	bingHTML := `<a m="{&quot;murl&quot;:&quot;` + img.URL + `&quot;}">`
	bing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(bingHTML))
	}))
	defer bing.Close()

	d := newDDG(ddg)
	b := NewBing(http.DefaultClient)
	b.baseURL = bing.URL
	chain := NewChain(d, b)

	data, _, src, err := chain.Find(context.Background(), "thing")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(data) == 0 || src != "bing" {
		t.Fatalf("expected bing to win the fallback, got src=%q len=%d", src, len(data))
	}
}

func TestDownloadRejectsNonImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>not an image</html>"))
	}))
	defer srv.Close()
	_, _, err := downloadImage(context.Background(), http.DefaultClient, srv.URL)
	if err == nil {
		t.Fatal("expected non-image to be rejected")
	}
}
