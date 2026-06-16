package transmissionrpc

import (
	"path/filepath"
	"testing"

	"github.com/lgldsilva/jackui/internal/downloads"
)

// The download-dir reported to the *arr must match where the worker actually
// writes promoted files (downloads.PromoteDir), so the *arr import from the right
// path — but only for *arr downloads while auto-promote is on.
func TestReportDir_AutoPromote(t *testing.T) {
	hOn := NewHandler(nil, nil, nil, "/dl", "/dl", "/shared", func() bool { return true })
	hOff := NewHandler(nil, nil, nil, "/dl", "/dl", "/shared", func() bool { return false })

	arr := downloads.Download{Source: downloads.SourceArr, Category: "tv-sonarr"}
	ui := downloads.Download{Source: "", Category: "tv-sonarr"}

	if got := hOn.reportDir(arr); got != filepath.Join("/shared", "tv-sonarr") {
		t.Errorf("arr+on reportDir = %q", got)
	}
	if got := hOn.reportDir(ui); got != "/dl" {
		t.Errorf("ui+on reportDir = %q (should stay downloadDir)", got)
	}
	if got := hOff.reportDir(arr); got != "/dl" {
		t.Errorf("arr+off reportDir = %q (should stay downloadDir)", got)
	}

	// session-get reports the shared base as download-dir when auto-promote is on.
	resp := hOn.methodSessionGet()
	if dir, _ := resp.Arguments["download-dir"].(string); dir != "/shared" {
		t.Errorf("session-get download-dir = %q, want /shared", dir)
	}
	resp = hOff.methodSessionGet()
	if dir, _ := resp.Arguments["download-dir"].(string); dir != "/dl" {
		t.Errorf("session-get (off) download-dir = %q, want /dl", dir)
	}
}
