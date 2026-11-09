package handlers

import (
	"errors"
	"net/http"
	"testing"

	"github.com/lgldsilva/jackui/internal/streamer"
	"github.com/lgldsilva/jackui/internal/transcode"
)

// TestTranscodeStream_RejectsNonWhitelistedOptions pins the early 400 for
// client-chosen ffmpeg muxers/encoders: the request must die before any
// source is opened (the streamer here has no torrent, so reaching it = 404).
func TestTranscodeStream_RejectsNonWhitelistedOptions(t *testing.T) {
	for _, q := range []string{"?container=tee", "?container=image2", "?video=rawvideo", "?acodec=flac"} {
		w := burnSubServe(t, TranscodeStream(streamer.NewForTesting(), nil, ""), q)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body: %s", q, w.Code, w.Body.String())
		}
	}
	// Whitelisted values keep the unchanged flow (404: no such torrent).
	w := burnSubServe(t, TranscodeStream(streamer.NewForTesting(), nil, ""), "?video=h264&acodec=aac&container=matroska")
	if w.Code != http.StatusNotFound {
		t.Errorf("whitelisted: status = %d, want 404; body: %s", w.Code, w.Body.String())
	}
}

func TestTranscodeRunStatus_InvalidOptionMaps400(t *testing.T) {
	err := transcode.ValidateOptions(nil, transcode.Options{Container: "tee"})
	if !errors.Is(err, transcode.ErrInvalidOption) {
		t.Fatalf("ValidateOptions err = %v", err)
	}
	if got := transcodeRunStatus(err); got != http.StatusBadRequest {
		t.Errorf("transcodeRunStatus = %d, want 400", got)
	}
}
