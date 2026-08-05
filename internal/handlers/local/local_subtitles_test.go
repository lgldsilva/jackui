package local

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	lb "github.com/lgldsilva/jackui/internal/local"
)

func TestDetectLangFromName(t *testing.T) {
	cases := map[string]string{
		"movie.pt-br.srt":     "pt-BR",
		"movie.pt_br.srt":     "pt-BR",
		"movie.pob.srt":       "pt-BR",
		"movie.pt-pt.srt":     "pt-PT",
		"movie.pt.srt":        "pt",
		"movie.por.srt":       "pt",
		"movie.portugues.srt": "pt",
		"movie.en.srt":        "en",
		"movie.eng.srt":       "en",
		"movie.english.srt":   "en",
		"movie.es.srt":        "es",
		"movie.spa.srt":       "es",
		"movie.fr.srt":        "fr",
		"movie.fra.srt":       "fr",
		"movie.unknown.srt":   "",
	}
	for name, want := range cases {
		if got := detectLangFromName(name); got != want {
			t.Errorf("detectLangFromName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestErrStrIfAny(t *testing.T) {
	if got := errStrIfAny(nil); got != "" {
		t.Errorf("nil = %q, want empty", got)
	}
	if got := errStrIfAny(os.ErrNotExist); got != os.ErrNotExist.Error() {
		t.Errorf("err = %q", got)
	}
}

func TestCollectDirSubs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "movie.en.srt"), []byte("1"))
	writeFile(t, filepath.Join(dir, "movie.pt-br.vtt"), []byte("2"))
	writeFile(t, filepath.Join(dir, "other.ass"), []byte("3"))
	writeFile(t, filepath.Join(dir, "readme.txt"), []byte("x"))

	subs, err := collectDirSubs(dir, "movie")
	if err != nil {
		t.Fatalf("collectDirSubs: %v", err)
	}
	if len(subs) != 3 {
		t.Fatalf("expected 3 subs, got %d: %+v", len(subs), subs)
	}
	// Basename matches come first (Match=2), then same-dir (Match=1).
	if subs[0].Match != 2 || subs[1].Match != 2 || subs[2].Match != 1 {
		t.Errorf("sort order = %+v", subs)
	}
	langs := map[string]bool{subs[0].Language: true, subs[1].Language: true}
	if !langs["pt-BR"] || !langs["en"] {
		t.Errorf("languages = %+v", subs)
	}
}

func TestLocalSidecars_MissingMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/local/sidecars?path=file", nil)
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	LocalSidecars(b)(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestLocalSidecarRead_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/local/sidecar?mount=M&path=file", nil)
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	LocalSidecarRead(b)(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestLocalSidecarRead_InvalidName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/local/sidecar?mount=M&path=file&name=../escape", nil)
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	LocalSidecarRead(b)(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestParseSubtrackReq_MissingParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/local/subtrack?mount=M&path=file", nil)
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	_, ok := parseSubtrackReq(b, c)
	if ok {
		t.Fatal("missing track must not be ok")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestParseSubtrackReq_InvalidTrack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	b := lb.NewBrowser([]config.ExternalMount{{Name: "M", Path: t.TempDir()}})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/local/subtrack?mount=M&path=file&track=abc", nil)
	c.Set("auth.claims", &auth.Claims{UserID: 1, Username: "admin", Role: auth.RoleAdmin})
	_, ok := parseSubtrackReq(b, c)
	if ok {
		t.Fatal("invalid track must not be ok")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestPersistVTT_NewCode(t *testing.T) {
	dir := t.TempDir()
	vttPath := filepath.Join(dir, "subs", "test.vtt")
	persistVTT(vttPath, []byte("WEBVTT\n"))
	data, err := os.ReadFile(vttPath)
	if err != nil || string(data) != "WEBVTT\n" {
		t.Fatalf("persistVTT: %v, %q", err, data)
	}
	// Empty path is a no-op.
	persistVTT("", []byte("x"))
}

func TestLocalSubVTTPath_NilCache(t *testing.T) {
	if got := localSubVTTPath(nil, "/a", nil, 0); got != "" {
		t.Errorf("nil cache = %q, want empty", got)
	}
}
