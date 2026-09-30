package handlers

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/lgldsilva/jackui/internal/auth"
	"github.com/lgldsilva/jackui/internal/config"
	"github.com/lgldsilva/jackui/internal/dbtest"
	"github.com/lgldsilva/jackui/internal/mailer"
)

// captureLogs redirects the std log while fn runs and returns what was written.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	fn()
	return buf.String()
}

const leakyLink = flowBaseURL + "/reset-password?token=LIVESECRETTOKEN0123456789"

// The [no-smtp] path must never log the live link: it carries the single-use
// token, and the forgot flow is reachable unauthenticated — a log reader could
// otherwise take over any account.
func TestNotify_NoSMTP_NeverLogsToken(t *testing.T) {
	logs := captureLogs(t, func() {
		notify(nil, "victim@example.com", "JackUI — password recovery", "To reset your password, go to:", leakyLink)
	})
	if strings.Contains(logs, "LIVESECRETTOKEN") || strings.Contains(logs, "token=") {
		t.Fatalf("notify logged the live token link: %q", logs)
	}
	if !strings.Contains(logs, "victim@example.com") {
		t.Fatalf("expected the log to keep flow context (recipient), got %q", logs)
	}
}

// The SMTP-failure path is equally reachable via unauthenticated /forgot and
// must not fall back to logging the link.
func TestNotify_SMTPFailure_NeverLogsToken(t *testing.T) {
	// Host/port that refuse immediately → Enabled() is true, Send fails fast.
	mlr := mailer.New(config.SMTPConfig{Host: "127.0.0.1", Port: 1})
	if !mlr.Enabled() {
		t.Fatal("test precondition: mailer should be enabled")
	}
	logs := captureLogs(t, func() {
		notify(mlr, "victim@example.com", "JackUI — password recovery", "To reset your password, go to:", leakyLink)
	})
	if strings.Contains(logs, "LIVESECRETTOKEN") || strings.Contains(logs, "token=") {
		t.Fatalf("notify logged the live token link on SMTP failure: %q", logs)
	}
}

// End-to-end through the public forgot handler: whatever it logs about the
// reset flow, the token must not be recoverable from the output.
func TestForgot_LogsNoResetToken(t *testing.T) {
	pool := dbtest.NewDB(t)
	store, err := auth.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUserFull("victim", "victim@example.com", "password123", auth.RoleUser, auth.StatusActive); err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	logs := captureLogs(t, func() {
		rec := postFlowJSON(t, Forgot(store, nil, flowBaseURL), "/api/auth/forgot", `{"email":"victim@example.com"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("forgot status = %d, want 200", rec.Code)
		}
	})
	if strings.Contains(logs, "token=") {
		t.Fatalf("forgot flow logged a token link: %q", logs)
	}
	if !strings.Contains(logs, "victim@example.com") {
		t.Fatalf("expected the [no-smtp] flow context in logs, got %q", logs)
	}
}
