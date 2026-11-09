package transcode

import (
	"errors"
	"testing"
)

// TestValidateOptionsRejectsUnknownCodecsAndContainers pins the whitelist on
// the client-controlled codec/container query params: anything outside it
// would otherwise be handed to ffmpeg verbatim as -c:v / -c:a / -f.
func TestValidateOptionsRejectsUnknownCodecsAndContainers(t *testing.T) {
	bad := []Options{
		{VideoCodec: "libx265"},
		{VideoCodec: "rawvideo"},
		{AudioCodec: "flac"},
		{Container: "tee"},
		{Container: "image2"},
		{Container: "segment"},
	}
	for _, o := range bad {
		if err := ValidateOptions(&Capabilities{}, o); !errors.Is(err, ErrInvalidOption) {
			t.Errorf("ValidateOptions(%+v) = %v, want ErrInvalidOption", o, err)
		}
		// The gate must not depend on the capability probe having run.
		if err := ValidateOptions(nil, o); !errors.Is(err, ErrInvalidOption) {
			t.Errorf("ValidateOptions(nil caps, %+v) = %v, want ErrInvalidOption", o, err)
		}
	}

	good := []Options{
		{},
		{VideoCodec: "h264", AudioCodec: "aac", Container: "mp4"},
		{VideoCodec: "hevc", AudioCodec: "copy", Container: "matroska"},
		{Container: "webm"},
		{Container: "mpegts"},
	}
	for _, o := range good {
		if err := ValidateOptions(&Capabilities{}, o); err != nil {
			t.Errorf("ValidateOptions(%+v) = %v, want nil", o, err)
		}
	}
}
