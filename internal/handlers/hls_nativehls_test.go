package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMediaSegQuery(t *testing.T) {
	cases := []struct {
		token     string
		nativeHLS bool
		want      string
	}{
		{"", false, ""},
		{"TOK", false, "?token=TOK"}, // unchanged from the pre-native_hls format
		{"", true, "?native_hls=1"},
		{"TOK", true, "?token=TOK&native_hls=1"},
	}
	for _, c := range cases {
		if got := mediaSegQuery(c.token, c.nativeHLS); got != c.want {
			t.Errorf("mediaSegQuery(%q,%v)=%q want %q", c.token, c.nativeHLS, got, c.want)
		}
	}
}

func TestNativeHLSParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mk := func(q string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/x?"+q, nil)
		return c
	}
	if !nativeHLSParam(mk("native_hls=1")) {
		t.Error("native_hls=1 should be true")
	}
	if nativeHLSParam(mk("native_hls=0")) {
		t.Error("native_hls=0 should be false")
	}
	if nativeHLSParam(mk("")) {
		t.Error("absent native_hls should be false")
	}
}

func TestBuildVODPlaylist_NativeHLSAddsFlagToSegments(t *testing.T) {
	pl := string(buildVODPlaylist(8, "TOK", true))
	if !strings.Contains(pl, "seg_00000.ts?token=TOK&native_hls=1") {
		t.Fatalf("expected native_hls on segment lines; got:\n%s", pl)
	}
	if !strings.Contains(pl, "#EXT-X-ENDLIST") {
		t.Fatal("VOD playlist must be finite (#EXT-X-ENDLIST)")
	}

	// Without the flag the segment URLs stay in the original token-only shape.
	plain := string(buildVODPlaylist(8, "TOK", false))
	if strings.Contains(plain, "native_hls") {
		t.Fatalf("non-native playlist must not carry native_hls; got:\n%s", plain)
	}
}

func TestSegURLBuilder_NativeHLS(t *testing.T) {
	withFlag := segURLBuilder("M", "v.mkv", "TOK", "", true, "")("seg_00001.ts")
	if !strings.Contains(withFlag, "native_hls=1") {
		t.Fatalf("expected native_hls=1 in seg URL, got %q", withFlag)
	}
	without := segURLBuilder("M", "v.mkv", "TOK", "", false, "")("seg_00001.ts")
	if strings.Contains(without, "native_hls") {
		t.Fatalf("did not expect native_hls when false, got %q", without)
	}
}
