package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func decodeCategory(t *testing.T, body []byte) CategoryResult {
	t.Helper()
	var res CategoryResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("unmarshal %q: %v", body, err)
	}
	return res
}

func TestClassifyCategory_Direct(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		wantCat string
		wantSrc string
		minConf float64
	}{
		{"tv season-episode", "Some.Show.S01E02.1080p", "tv", "regex", 0.9},
		{"movie keyword", "Great Movie 2021", "movies", "regex", 0.8},
		{"music flac", "Artist - Album [FLAC]", "music", "regex", 0.7},
		{"games repack", "Some Game FitGirl Repack", "games", "regex", 0.5},
		{"adult", "XXX something", "adult", "regex", 0.9},
		{"software", "Windows 11 keygen", "software", "regex", 0.6},
		{"books pdf", "Programming Book.pdf", "books", "regex", 0.8},
		{"only quality tag", "Random.2160p.HDR", "movies", "regex", 0.4},
		{"no match", "qwerty zxcvb", "other", "fallback", 0},
		{"empty", "", "other", "fallback", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := classifyCategory(tc.title)
			if res.Category != tc.wantCat {
				t.Errorf("category = %q, want %q", res.Category, tc.wantCat)
			}
			if res.Source != tc.wantSrc {
				t.Errorf("source = %q, want %q", res.Source, tc.wantSrc)
			}
			if res.Confidence < tc.minConf {
				t.Errorf("confidence = %v, want >= %v", res.Confidence, tc.minConf)
			}
		})
	}
}

func TestJackettCategoryToCategory(t *testing.T) {
	tests := map[string]string{
		"2000": "movies",
		"2040": "movies", // 4-digit prefix bucket
		"5000": "tv",
		"3000": "music",
		"4000": "games",
		"4500": "software",
		"6000": "adult",
		"8000": "books",
		"7000": "other",
		"5":    "tv", // 1-digit → "5000"
		"50":   "tv", // <3 digits → 1-digit fallback "5000"
		"999":  "",   // 3-digit "9990" not mapped, 1-digit "9000" not mapped
		"":     "",
	}
	for in, want := range tests {
		if got := jackettCategoryToCategory(in); got != want {
			t.Errorf("jackettCategoryToCategory(%q) = %q, want %q", in, got, want)
		}
	}
}

func doClassify(t *testing.T, query string) (*httptest.ResponseRecorder, CategoryResult) {
	t.Helper()
	r := gin.New()
	r.GET("/classify", ClassifyCategory(nil)) // nil AI client → AI step skipped
	req := httptest.NewRequest(http.MethodGet, "/classify"+query, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		return w, decodeCategory(t, w.Body.Bytes())
	}
	return w, CategoryResult{}
}

func TestClassifyHandler_MissingParams(t *testing.T) {
	w, _ := doClassify(t, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestClassifyHandler_JackettWins(t *testing.T) {
	// Jackett category takes precedence even with a misleading title.
	_, res := doClassify(t, "?jackett_category=5000&title=Some.Movie.1080p")
	if res.Category != "tv" || res.Source != "jackett" {
		t.Fatalf("got %+v, want tv/jackett", res)
	}
	if res.Confidence != 0.95 {
		t.Errorf("confidence = %v, want 0.95", res.Confidence)
	}
}

func TestClassifyHandler_UnknownJackettFallsToRegex(t *testing.T) {
	// Unmapped Jackett id → falls through to regex on the title.
	_, res := doClassify(t, "?jackett_category=9999&title=Show.S03E04")
	if res.Category != "tv" || res.Source != "regex" {
		t.Fatalf("got %+v, want tv/regex", res)
	}
}

func TestClassifyHandler_HighConfidenceRegex(t *testing.T) {
	_, res := doClassify(t, "?title=Movie.Title.2020.1080p.BluRay")
	if res.Source != "regex" || res.Confidence < 0.8 {
		t.Fatalf("got %+v, want regex >=0.8", res)
	}
}

func TestClassifyHandler_LowConfidenceRegexNoAI(t *testing.T) {
	// Only a quality tag (weight 0.4) → no AI client → returned as low-conf regex.
	_, res := doClassify(t, "?title=Random.2160p.x265")
	if res.Source != "regex" || res.Category != "movies" {
		t.Fatalf("got %+v, want movies/regex", res)
	}
	if res.Confidence >= 0.8 {
		t.Errorf("confidence = %v, want < 0.8 (low-conf path)", res.Confidence)
	}
}

func TestClassifyHandler_FinalFallback(t *testing.T) {
	_, res := doClassify(t, "?title=qwertyzxcv")
	if res.Category != "other" || res.Source != "fallback" {
		t.Fatalf("got %+v, want other/fallback", res)
	}
}

func TestCategoryFromRegex_EmptyTitle(t *testing.T) {
	if _, ok := categoryFromRegex(""); ok {
		t.Error("empty title should not match")
	}
}

func TestCategoryFromAI_NilClient(t *testing.T) {
	if _, ok := categoryFromAI(nil, nil, "anything"); ok { //nolint:staticcheck // nil ctx ok: returns early
		t.Error("nil AI client should not match")
	}
}
