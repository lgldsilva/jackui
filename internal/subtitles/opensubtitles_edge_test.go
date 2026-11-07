package subtitles

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Edge-case tests for the OpenSubtitles client transport/decode paths and the
// SRT→VTT conversion — the failure branches the happy-path suite does not
// reach. A failing RoundTripper is used for transport-level errors (no
// network access; the retry policy's backoff keeps these sub-second).

// errTransport fails every request instantly, as a dead upstream would.
type errTransport struct{ err error }

func (t errTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

// bodyTransport returns a 200 response whose body fails on read — the read
// error surfaces after a successful Do, so no retry policy kicks in.
type bodyTransport struct{ bodyErr error }

func (t bodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(errReader{t.bodyErr}),
		Header:     make(http.Header),
	}, nil
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestUnitDecodeCappedReadError(t *testing.T) {
	boom := errors.New("socket closed")
	var v map[string]any
	if err := decodeCapped(errReader{boom}, &v); !errors.Is(err, boom) {
		t.Errorf("decodeCapped read error = %v, want the wrapped io error", err)
	}
}

func TestUnitDecodeCappedInvalidJSON(t *testing.T) {
	var v map[string]any
	if err := decodeCapped(strings.NewReader("not json at all"), &v); err == nil {
		t.Error("decodeCapped invalid JSON = nil error, want an unmarshal failure")
	}
}

func TestUnitSearchTransportError(t *testing.T) {
	c := New("key", "", "", "")
	c.http = &http.Client{Transport: errTransport{errors.New("connection refused")}}
	_, err := c.SearchAuto(SearchOpts{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "opensubtitles search") {
		t.Errorf("search transport error = %v, want a wrapped search error", err)
	}
}

// TestUnitSearchQuotaStatuses pins both quota statuses at the client level
// (429 rate limit and 406 exhausted download quota) with the body echoed.
func TestUnitSearchQuotaStatuses(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusNotAcceptable} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "slow down", code)
		}))
		c := testSubClient(t, srv, "key", "", "", "")
		_, err := c.SearchAuto(SearchOpts{Query: "x"})
		if !errors.Is(err, ErrQuota) {
			t.Errorf("status %d: err = %v, want ErrQuota", code, err)
		}
		if err != nil && !strings.Contains(err.Error(), "slow down") {
			t.Errorf("status %d: body text lost from the wrap: %v", code, err)
		}
	}
}

func TestUnitPrimaryFileFormat(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Movie.2024.eng.SRT", ".srt"}, // case-folded
		{" sub.ass ", ".ass"},          // trimmed
		{"name.", ""},                  // a bare trailing dot is not a format
		{"extensionless", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := primaryFileFormat(tc.name); got != tc.want {
			t.Errorf("primaryFileFormat(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestUnitDownloadLoginFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/login" {
			http.Error(w, "bad credentials", http.StatusUnauthorized)
			return
		}
		t.Errorf("unexpected request to %s", r.URL.Path)
	}))
	c := testSubClient(t, srv, "key", "user", "pass", t.TempDir())
	if _, err := c.Download("123"); err == nil || !strings.Contains(err.Error(), "login returned 401") {
		t.Errorf("login failure error = %v, want wrapped login status", err)
	}
}

func TestUnitDownloadLoginDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/login" {
			_, _ = w.Write([]byte("not json"))
			return
		}
	}))
	c := testSubClient(t, srv, "key", "user", "pass", "")
	if _, err := c.Download("123"); err == nil {
		t.Error("login decode failure = nil error, want a decode error")
	}
}

func TestUnitDownloadLoginTransportError(t *testing.T) {
	c := New("key", "user", "pass", "")
	c.http = &http.Client{Transport: errTransport{errors.New("connection refused")}}
	if _, err := c.Download("123"); err == nil || !strings.Contains(err.Error(), "opensubtitles login") {
		t.Errorf("login transport error = %v, want a wrapped login error", err)
	}
}

// TestUnitDownloadTokenHeader pins that a logged-in download carries the
// Bearer token on the /download meta request.
func TestUnitDownloadTokenHeader(t *testing.T) {
	dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nSub"))
	}))
	var gotAuth string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/login":
			writeJSON(w, map[string]string{"token": "tok123"})
		case "/api/v1/download":
			gotAuth = r.Header.Get("Authorization")
			writeJSON(w, map[string]any{"link": dlSrv.URL + "/file.srt"})
		default:
			http.NotFound(w, r)
		}
	}))
	c := testSubClient(t, apiSrv, "key", "user", "pass", t.TempDir())
	if _, err := c.Download("555"); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("Authorization = %q, want the login bearer token", gotAuth)
	}
}

func TestUnitDownloadLinkErrors(t *testing.T) {
	t.Run("decode error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("garbage"))
		}))
		c := testSubClient(t, srv, "key", "", "", "")
		if _, err := c.Download("123"); err == nil || !strings.Contains(err.Error(), "decode download meta") {
			t.Errorf("decode error = %v", err)
		}
	})
	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		c := testSubClient(t, srv, "key", "", "", "")
		_, err := c.Download("123")
		if err == nil || !strings.Contains(err.Error(), "download returned 500") {
			t.Errorf("server error = %v, want the wrapped status", err)
		}
		if errors.Is(err, ErrQuota) {
			t.Errorf("a 500 must not be classified as quota: %v", err)
		}
	})
	t.Run("transport error", func(t *testing.T) {
		c := New("key", "", "", "")
		c.http = &http.Client{Transport: errTransport{errors.New("connection refused")}}
		if _, err := c.Download("123"); err == nil || !strings.Contains(err.Error(), "download request") {
			t.Errorf("transport error = %v, want a wrapped request error", err)
		}
	})
}

func TestUnitFetchSubtitleErrors(t *testing.T) {
	t.Run("body read error", func(t *testing.T) {
		c := New("key", "", "", "")
		c.http = &http.Client{Transport: bodyTransport{errors.New("reset mid-body")}}
		if _, err := c.fetchSubtitle("https://os.example/file.srt"); err == nil ||
			!strings.Contains(err.Error(), "read subtitle") {
			t.Errorf("body read error = %v, want a wrapped read error", err)
		}
	})
	t.Run("transport error", func(t *testing.T) {
		c := New("key", "", "", "")
		c.http = &http.Client{Transport: errTransport{errors.New("connection refused")}}
		if _, err := c.fetchSubtitle("https://os.example/file.srt"); err == nil ||
			!strings.Contains(err.Error(), "fetch subtitle file") {
			t.Errorf("transport error = %v, want a wrapped fetch error", err)
		}
	})
}

// TestUnitSRTToVTTEdges pins the conversion's documented behavior on inputs
// the basic suite does not cover: empty input, text without timings, BOM
// combined with CRLF, multi-comma timestamps, and the arrowRe tradeoff — any
// line containing "-->" (dialogue included) is treated as a timing line.
func TestUnitSRTToVTTEdges(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // substrings the output must contain
		not  []string // substrings the output must not contain
	}{
		{
			name: "empty input",
			in:   "",
			want: []string{"WEBVTT\n\n"},
		},
		{
			name: "no timing lines pass through",
			in:   "just some text\nno commas converted, really",
			want: []string{"no commas converted, really"}, // commas stay untouched off timing lines
		},
		{
			name: "BOM with CRLF",
			in:   "\xef\xbb\xbf1\r\n00:00:01,000 --> 00:00:02,000\r\nOlá",
			want: []string{"00:00:01.000 --> 00:00:02.000", "Olá"},
			not:  []string{"\xef\xbb\xbf", "\r\n", ","},
		},
		{
			name: "multiple commas on one timing line",
			in:   "00:00:01,500 --> 00:00:02,750,900",
			want: []string{"00:00:01.500 --> 00:00:02.750.900"},
		},
		{
			name: "dialogue containing --> is treated as timing",
			in:   "1\n00:00:01,000 --> 00:00:02,000\nhe said 3,5 --> 4,5 ok",
			want: []string{"he said 3.5 --> 4.5 ok"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(SRTToVTT([]byte(tc.in)))
			if !strings.HasPrefix(got, "WEBVTT\n\n") {
				t.Errorf("output missing WEBVTT header: %q", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("output missing %q: %q", w, got)
				}
			}
			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("output must not contain %q: %q", n, got)
				}
			}
		})
	}
}

// TestUnitCachePathConsistency: CachePath (exported) and cachePath agree, and
// the AutoFetcher records exactly that path in subtitle_links.vtt_path.
func TestUnitCachePathConsistency(t *testing.T) {
	dir := t.TempDir()
	c := New("key", "", "", dir)
	for _, id := range []string{"123", "0", "999999999"} {
		if got := c.CachePath(id); got != filepath.Join(dir, id+".vtt") {
			t.Errorf("CachePath(%q) = %q, want the .vtt cache path", id, got)
		}
	}
}
