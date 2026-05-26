package subtitles

import (
	"strings"
	"testing"
)

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
	// UTF-8 BOM = 0xEF 0xBB 0xBF
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
