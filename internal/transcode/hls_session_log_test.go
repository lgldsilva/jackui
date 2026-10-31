package transcode

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

// A session key derives from the request (hash + file path), so a line break in
// it must not be able to forge an extra log entry when ffmpeg exits with an error.
func TestLaunchExitLogQuotesSessionKey(t *testing.T) {
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	s := &HLSSession{
		Key:  "abc\nhls: forged entry",
		spec: &encodeSpec{vod: true, ffmpegPath: "/bin/false"},
	}
	if err := s.launch(0); err != nil {
		t.Fatalf("launch: %v", err)
	}
	// exitErr is set right after the exit is logged, so seeing it means the
	// goroutine has finished writing to buf.
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		exited := s.exitErr != nil
		s.mu.Unlock()
		if exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ffmpeg stand-in did not exit")
		}
		time.Sleep(5 * time.Millisecond)
	}

	out := buf.String()
	if !strings.Contains(out, `session "abc\nhls: forged entry"`) {
		t.Fatalf("exit log does not quote the key:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "hls: forged entry") {
			t.Fatalf("key forged a log line:\n%s", out)
		}
	}
}
