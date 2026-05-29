package mailer

import (
	"testing"

	"github.com/luizg/jackui/internal/config"
)

func TestNew(t *testing.T) {
	m := New(config.SMTPConfig{})
	if m == nil {
		t.Fatal("expected non-nil Mailer")
	}
}

func TestEnabled(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config.SMTPConfig
		expect bool
	}{
		{"nil mailer", config.SMTPConfig{}, false},
		{"host only", config.SMTPConfig{Host: "smtp.example.com"}, false},
		{"port only", config.SMTPConfig{Port: 587}, false},
		{"host and port", config.SMTPConfig{Host: "smtp.example.com", Port: 587}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(tc.cfg)
			if got := m.Enabled(); got != tc.expect {
				t.Errorf("Enabled() = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestFrom(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config.SMTPConfig
		expect string
	}{
		{"from set", config.SMTPConfig{From: "from@test.com", Username: "user@test.com"}, "from@test.com"},
		{"from empty", config.SMTPConfig{Username: "user@test.com"}, "user@test.com"},
		{"both empty", config.SMTPConfig{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New(tc.cfg)
			if got := m.from(); got != tc.expect {
				t.Errorf("from() = %q, want %q", got, tc.expect)
			}
		})
	}
}

func TestBuildMessage(t *testing.T) {
	msg := buildMessage("from@test.com", "to@test.com", "Test Subject", "<p>body</p>")
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	if !contains(msg, "From: from@test.com") {
		t.Error("expected From header")
	}
	if !contains(msg, "To: to@test.com") {
		t.Error("expected To header")
	}
	if !contains(msg, "Subject: Test Subject") {
		t.Error("expected Subject header")
	}
	if !contains(msg, "Content-Type: text/html; charset=UTF-8") {
		t.Error("expected Content-Type header")
	}
	if !contains(msg, "<p>body</p>") {
		t.Error("expected body")
	}
}

func TestSend_NotEnabled(t *testing.T) {
	m := New(config.SMTPConfig{})
	err := m.Send("to@test.com", "sub", "body")
	if err == nil {
		t.Fatal("expected error when SMTP not configured")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
