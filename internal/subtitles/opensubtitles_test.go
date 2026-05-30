package subtitles

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type subTestTransport struct {
	serverURL string
}

func (t *subTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	testURL := t.serverURL + req.URL.Path
	if req.URL.RawQuery != "" {
		testURL += "?" + req.URL.RawQuery
	}
	newReq, _ := http.NewRequest(req.Method, testURL, req.Body)
	newReq.Header = req.Header
	return http.DefaultTransport.RoundTrip(newReq)
}

func testSubClient(t *testing.T, srv *httptest.Server, apiKey, username, password, cacheDir string) *Client {
	t.Helper()
	c := New(apiKey, username, password, cacheDir)
	c.http = &http.Client{Transport: &subTestTransport{serverURL: srv.URL}}
	return c
}

func TestSRTToVTTBasic(t *testing.T) {
	srt := `1
00:00:01,500 --> 00:00:04,200
Hello world

2
00:00:05,000 --> 00:00:07,000
Second line`
	vtt := string(SRTToVTT([]byte(srt)))
	if !strings.HasPrefix(vtt, "WEBVTT\n\n") {
		t.Errorf("missing WEBVTT header: %q", vtt[:20])
	}
	if !strings.Contains(vtt, "00:00:01.500 --> 00:00:04.200") {
		t.Errorf("comma not converted to dot in timing: %q", vtt)
	}
	if !strings.Contains(vtt, "Hello world") {
		t.Errorf("body lost")
	}
}

func TestSRTToVTTBOMStripped(t *testing.T) {
	srt := []byte("\xef\xbb\xbf1\n00:00:01,000 --> 00:00:02,000\nWith BOM")
	vtt := string(SRTToVTT(srt))
	if strings.Contains(vtt, "\xef\xbb\xbf") {
		t.Error("BOM not stripped")
	}
}

func TestSRTToVTTCRLFNormalized(t *testing.T) {
	srt := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nWindows line endings")
	vtt := string(SRTToVTT(srt))
	if strings.Contains(vtt, "\r\n") {
		t.Error("CRLF not normalized")
	}
}

func TestClientEnabledFalseWithoutKey(t *testing.T) {
	c := New("", "", "", "")
	if c.Enabled() {
		t.Error("expected disabled without API key")
	}
	c2 := New("some-key", "", "", "")
	if !c2.Enabled() {
		t.Error("expected enabled with API key")
	}
}

func TestSearchAutoErrWhenDisabled(t *testing.T) {
	c := New("", "", "", "")
	if _, err := c.SearchAuto(SearchOpts{Query: "x"}); err == nil {
		t.Error("expected error from disabled client")
	}
}

func TestSearchAuto_BuildsQueryParams(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()

	c := testSubClient(t, srv, "testkey", "", "", "")
	_, err := c.SearchAuto(SearchOpts{Query: "test", MovieHash: "hash123", MovieBytesize: 1024, IMDB: "tt1234567", Languages: "pt-BR", Season: 1, Episode: 2})
	if err != nil {
		t.Fatalf("SearchAuto: %v", err)
	}

	if !strings.Contains(capturedQuery, "query=test") {
		t.Errorf("missing query param: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "moviehash=hash123") {
		t.Errorf("missing moviehash param: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "moviebytesize=1024") {
		t.Errorf("missing moviebytesize: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "imdb_id=1234567") {
		t.Errorf("missing imdb_id: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "languages=pt-BR") {
		t.Errorf("missing languages: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "season_number=1") {
		t.Errorf("missing season_number: %s", capturedQuery)
	}
	if !strings.Contains(capturedQuery, "episode_number=2") {
		t.Errorf("missing episode_number: %s", capturedQuery)
	}
}

func TestSearch_Legacy(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	}))
	defer srv.Close()

	c := testSubClient(t, srv, "testkey", "", "", "")
	_, err := c.Search("test query", "en", 1, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.Contains(capturedQuery, "test+query") {
		t.Errorf("missing query: %s", capturedQuery)
	}
}

func TestSearch_ParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": []any{
				map[string]any{
					"attributes": map[string]any{
						"language":         "pt-BR",
						"release":          "Movie.2020.1080p",
						"url":              "http://subs.example.com/movie",
						"download_count":   1500,
						"hearing_impaired": false,
						"from_trusted":     true,
						"uploader":         map[string]any{"name": "UploaderName"},
						"files": []any{
							map[string]any{"file_id": 12345},
						},
					},
				},
				map[string]any{
					"attributes": map[string]any{
						"language": "en",
						"release":  "Another.Release",
						"uploader": map[string]any{"name": "Other"},
						"files": []any{
							map[string]any{"file_id": 67890},
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := testSubClient(t, srv, "testkey", "", "", "")
	results, err := c.SearchAuto(SearchOpts{Query: "movie"})
	if err != nil {
		t.Fatalf("SearchAuto: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Language != "pt-BR" || results[0].ID != "12345" || !results[0].Trusted {
		t.Fatalf("result[0] wrong: %+v", results[0])
	}
}

func TestSearch_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := testSubClient(t, srv, "testkey", "", "", "")
	_, err := c.SearchAuto(SearchOpts{Query: "test"})
	if err == nil {
		t.Fatal("expected error on non-200")
	}
}

func TestCachePath(t *testing.T) {
	c := New("key", "", "", "/tmp/subcache")
	path := c.cachePath("12345")
	if path == "" {
		t.Fatal("expected non-empty cache path")
	}
	if !strings.HasSuffix(path, ".vtt") {
		t.Fatal("expected .vtt suffix")
	}
}

func TestCachePath_EmptyDir(t *testing.T) {
	c := New("key", "", "", "")
	if p := c.cachePath("12345"); p != "" {
		t.Fatal("expected empty path without cache dir")
	}
}

func TestCachePath_EmptyFileID(t *testing.T) {
	c := New("key", "", "", "/tmp/subcache")
	if p := c.cachePath(""); p != "" {
		t.Fatal("expected empty path for empty file ID")
	}
}

func TestCachePath_Sanitizes(t *testing.T) {
	c := New("key", "", "", "/tmp/subcache")
	path := c.cachePath("123../foo")
	if !strings.Contains(path, "123foo") || strings.Contains(path, "../") {
		t.Fatalf("unsanitized path: %q", path)
	}
}

func TestCachePath_AllBadChars(t *testing.T) {
	c := New("key", "", "", "/tmp/subcache")
	if p := c.cachePath("..."); p != "" {
		t.Fatal("expected empty for all-bad chars")
	}
}

func TestNew_CreatesCacheDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subcache")
	c := New("key", "", "", dir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("cache dir should exist")
	}
	_ = c
}

func TestDownload_Disabled(t *testing.T) {
	c := New("", "", "", "")
	_, err := c.Download("12345")
	if err == nil {
		t.Fatal("expected error when disabled")
	}
}

func TestDownload_DiskCacheHit(t *testing.T) {
	dir := t.TempDir()
	c := New("key", "", "", dir)
	os.WriteFile(filepath.Join(dir, "12345.vtt"), []byte("WEBVTT\n\ncached"), 0644)
	data, err := c.Download("12345")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(data) != "WEBVTT\n\ncached" {
		t.Fatalf("expected cached data, got %q", data)
	}
}

func TestDownload_FullFlow(t *testing.T) {
	dir := t.TempDir()

	dlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nSubtitle text"))
	}))
	defer dlSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/download":
			json.NewEncoder(w).Encode(map[string]any{
				"link":       dlSrv.URL,
				"remaining":  100,
				"reset_time": "2025-01-01T00:00:00.000Z",
			})
		default:
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		}
	}))
	defer apiSrv.Close()

	c := testSubClient(t, apiSrv, "key", "", "", dir)
	data, err := c.Download("99999")
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !strings.HasPrefix(string(data), "WEBVTT") {
		t.Fatalf("expected WEBVTT, got %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(dir, "99999.vtt")); os.IsNotExist(err) {
		t.Fatal("expected cached file")
	}
}

func TestDownload_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := testSubClient(t, srv, "key", "", "", "")
	_, err := c.Download("123")
	if err == nil {
		t.Fatal("expected error on API failure")
	}
}

func TestDownload_EmptyLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"link": "", "remaining": 0})
	}))
	defer srv.Close()

	c := testSubClient(t, srv, "key", "", "", "")
	_, err := c.Download("123")
	if err == nil {
		t.Fatal("expected error on empty link")
	}
}

func TestEnsureToken_NoCredentials(t *testing.T) {
	c := New("key", "", "", "")
	tok, err := c.ensureToken()
	if err != nil {
		t.Fatalf("ensureToken: %v", err)
	}
	if tok != "" {
		t.Fatal("expected empty token without credentials")
	}
}

func TestEnsureToken_LoginFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/login" {
			json.NewEncoder(w).Encode(map[string]string{"token": "mytoken123", "status": "200"})
		}
	}))
	defer srv.Close()

	c := testSubClient(t, srv, "key", "user", "pass", "")
	tok, err := c.ensureToken()
	if err != nil {
		t.Fatalf("ensureToken: %v", err)
	}
	if tok != "mytoken123" {
		t.Fatalf("expected mytoken123, got %q", tok)
	}
	tok2, _ := c.ensureToken()
	if tok2 != "mytoken123" {
		t.Fatal("expected cached token")
	}
}

func TestEnsureToken_LoginError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := testSubClient(t, srv, "key", "user", "pass", "")
	_, err := c.ensureToken()
	if err == nil {
		t.Fatal("expected error on login failure")
	}
}

func TestApplyHeaders(t *testing.T) {
	c := New("apikey123", "", "", "")
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	c.applyHeaders(req)
	if req.Header.Get("Api-Key") != "apikey123" {
		t.Fatalf("Api-Key = %q", req.Header.Get("Api-Key"))
	}
	if req.Header.Get("User-Agent") != "JackUI v1.0" {
		t.Fatalf("User-Agent = %q", req.Header.Get("User-Agent"))
	}
}

func TestSearch_EmptyResultsOnNoFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"data": []any{
				map[string]any{
					"attributes": map[string]any{
						"language": "en",
						"release":  "Test",
						"uploader": map[string]any{"name": "U"},
						"files":    []any{},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := testSubClient(t, srv, "key", "", "", "")
	results, err := c.SearchAuto(SearchOpts{Query: "test"})
	if err != nil {
		t.Fatalf("SearchAuto: %v", err)
	}
	if len(results) != 0 {
		t.Fatal("expected 0 results when files list is empty")
	}
}
