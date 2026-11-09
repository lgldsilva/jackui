package httpshared

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/lgldsilva/jackui/internal/transcode"
)

func TestHLSStartStatus(t *testing.T) {
	if got := HLSStartStatus(fmt.Errorf("x: %w", transcode.ErrTooManySessions)); got != http.StatusTooManyRequests {
		t.Errorf("cap error = %d, want 429", got)
	}
	if got := HLSStartStatus(errors.New("ffmpeg died")); got != http.StatusInternalServerError {
		t.Errorf("other error = %d, want 500", got)
	}
}
