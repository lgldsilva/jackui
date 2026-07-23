package mailer

import (
	"testing"

	"github.com/lgldsilva/jackui/internal/config"
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
	msg, err := buildMessage("from@test.com", "to@test.com", "Test Subject", "<p>body</p>")
	if err != nil {
		t.Fatal(err)
	}
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

func TestBuildMessage_WithSpecialChars(t *testing.T) {
	msg, err := buildMessage("from@test.com", "to@test.com", "Test & Subject <3", "<p>hello & goodbye</p>")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(msg, "Subject: Test & Subject <3") {
		t.Error("expected Subject with special chars")
	}
	if !contains(msg, "<p>hello & goodbye</p>") {
		t.Error("expected body with special chars")
	}
}

func TestSend_DialFailure(t *testing.T) {
	m := New(config.SMTPConfig{Host: "192.0.2.1", Port: 1})
	err := m.Send("to@test.com", "sub", "body")
	if err == nil {
		t.Fatal("expected error for unreachable SMTP")
	}
}

func TestSend_NilMailer(t *testing.T) {
	var m *Mailer
	err := m.Send("to@test.com", "sub", "body")
	if err == nil {
		t.Fatal("expected error for nil mailer")
	}
}

func TestBuildMessage_WithNewlines(t *testing.T) {
	msg, err := buildMessage("from@test.com", "to@test.com", "Subject", "line1\nline2\r\nline3")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(msg, "\r\nline3") {
		t.Error("expected body to preserve newlines in SMTP format")
	}
}

func TestBuildMessage_RejectsHeaderCRLF(t *testing.T) {
	if _, err := buildMessage("from@test.com", "to@test.com\r\nBcc: evil@x", "Subject", "body"); err == nil {
		t.Fatal("expected error for CR/LF in To")
	}
	if _, err := buildMessage("from@test.com", "to@test.com", "Sub\r\nX-Injected: 1", "body"); err == nil {
		t.Fatal("expected error for CR/LF in Subject")
	}
}

func TestSend_InvalidRecipient(t *testing.T) {
	m := New(config.SMTPConfig{Host: "smtp.example.com", Port: 587})
	if err := m.Send("not-an-email", "sub", "body"); err == nil {
		t.Fatal("expected error for invalid recipient")
	}
}

func TestBuildMessage_NonASCII(t *testing.T) {
	msg, err := buildMessage("from@test.com", "to@test.com", "Assunto com ção", "<p>coração</p>")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(msg, "Subject: Assunto com ção") {
		t.Error("expected Subject with non-ASCII chars")
	}
}

func TestBuildMessage_HasDateHeader(t *testing.T) {
	msg, err := buildMessage("f@t.com", "t@t.com", "S", "b")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(msg, "Date: ") {
		t.Error("expected Date header")
	}
}

func TestBuildMessage_HasMIMEHeader(t *testing.T) {
	msg, err := buildMessage("f@t.com", "t@t.com", "S", "b")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(msg, "MIME-Version: 1.0") {
		t.Error("expected MIME-Version header")
	}
	if !contains(msg, "Content-Type: text/html; charset=UTF-8") {
		t.Error("expected Content-Type header")
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
