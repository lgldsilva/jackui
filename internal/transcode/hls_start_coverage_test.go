package transcode

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

type coverageReadSeeker struct {
	*bytes.Reader
	closed bool
}

func (s *coverageReadSeeker) Close() error {
	s.closed = true
	return nil
}

func TestHLSStartHelperErrorAndCachePaths(t *testing.T) {
	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	defer m.Close("unused")

	ResetCachedForTesting()
	if _, err := m.buildSession(context.Background(), "no-caps", HLSStartOpts{Source: bytes.NewReader(nil)}); err == nil {
		t.Fatal("buildSession should reject an unprobed capability cache")
	}

	known := HLSStartOpts{Key: "known", KnownDurationSec: 12.5}
	if got := m.resolveDuration(context.Background(), "known", known, "unused", "unused"); got != 12.5 {
		t.Fatalf("known duration = %v, want 12.5", got)
	}
	m.cacheDuration("cached", 7.25)
	if got := m.resolveDuration(context.Background(), "cached", HLSStartOpts{Key: "cached"}, "unused", "unused"); got != 7.25 {
		t.Fatalf("cached duration = %v, want 7.25", got)
	}
}

func TestStartSourceServerSizeDiscoveryAndRange(t *testing.T) {
	if _, _, err := startSourceServer(HLSStartOpts{}); err == nil {
		t.Fatal("startSourceServer should reject a nil source")
	}

	const body = "seekable source body"
	src := bytes.NewReader([]byte(body))
	srv, url, err := startSourceServer(HLSStartOpts{Source: src})
	if err != nil {
		t.Fatalf("startSourceServer: %v", err)
	}
	defer srv.Close()

	var resp *http.Response
	for attempt := 0; attempt < 20; attempt++ {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET source: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(got) != body {
		t.Fatalf("source body = %q, want %q", got, body)
	}
}

func TestGetOrStartExistingAndWaitingCancellation(t *testing.T) {
	m, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager: %v", err)
	}
	defer m.Close("existing")

	existingKey := m.EffectiveKey("existing", false)
	existing := &HLSSession{Key: existingKey, LastAccess: time.Now(), mgr: m}
	m.sess[existingKey] = existing
	callerSource := &coverageReadSeeker{Reader: bytes.NewReader([]byte("caller"))}
	got, err := m.GetOrStart(context.Background(), HLSStartOpts{Key: "existing", Source: callerSource})
	if err != nil || got != existing {
		t.Fatalf("existing session = %p, %v; want %p, nil", got, err, existing)
	}
	if !callerSource.closed {
		t.Fatal("source passed to an existing session must be closed")
	}

	waiting, err := NewHLSManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewHLSManager waiting: %v", err)
	}
	defer waiting.Close("waiting")
	key := waiting.EffectiveKey("waiting", false)
	waiting.starting[key] = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waitSource := &coverageReadSeeker{Reader: bytes.NewReader(nil)}
	_, err = waiting.GetOrStart(ctx, HLSStartOpts{Key: "waiting", Source: waitSource})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting GetOrStart error = %v, want context.Canceled", err)
	}
	if !waitSource.closed {
		t.Fatal("source must be closed when waiting context is canceled")
	}
}
