package mailer

import (
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

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

// fakeSMTPServer implements just enough SMTP to test the mailer's Send method.
type fakeSMTPServer struct {
	t        *testing.T
	ln       net.Listener
	addr     string
	received string
	done     chan struct{}
}

func startFakeSMTP(t *testing.T) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTPServer{
		t:    t,
		ln:   ln,
		addr: ln.Addr().String(),
		done: make(chan struct{}),
	}
	go s.serve()
	return s
}

func (s *fakeSMTPServer) serve() {
	defer close(s.done)
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	tp := textproto.NewConn(conn)
	tp.PrintfLine("220 fake-smtp ESMTP")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			tp.PrintfLine("250-fake-smtp")
			tp.PrintfLine("250 AUTH PLAIN LOGIN")
		case strings.HasPrefix(upper, "AUTH"):
			tp.PrintfLine("235 Authentication successful")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			tp.PrintfLine("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			tp.PrintfLine("250 OK")
		case strings.HasPrefix(upper, "DATA"):
			tp.PrintfLine("354 Start mail input")
			msg, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			s.received = string(msg)
			tp.PrintfLine("250 OK")
		case strings.HasPrefix(upper, "QUIT"):
			tp.PrintfLine("221 Bye")
			return
		default:
			tp.PrintfLine("250 OK")
		}
	}
}

func (s *fakeSMTPServer) stop() {
	s.ln.Close()
	select {
	case <-s.done:
	case <-time.After(time.Second):
	}
}

func TestSend_Success(t *testing.T) {
	srv := startFakeSMTP(t)
	defer srv.stop()
	parts := strings.Split(srv.addr, ":")
	host := parts[0]
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	m := New(config.SMTPConfig{
		Host:     host,
		Port:     port,
		From:     "from@test.com",
		Username: "user@test.com",
		Password: "pass",
	})
	err := m.Send("to@test.com", "Hello", "<h1>World</h1>")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !contains(srv.received, "From: from@test.com") {
		t.Error("expected From header in received message")
	}
	if !contains(srv.received, "To: to@test.com") {
		t.Error("expected To header in received message")
	}
	if !contains(srv.received, "Subject: Hello") {
		t.Error("expected Subject header in received message")
	}
	if !contains(srv.received, "<h1>World</h1>") {
		t.Error("expected body in received message")
	}
}

func TestSend_DialError(t *testing.T) {
	m := New(config.SMTPConfig{
		Host: "127.0.0.1",
		Port: 1, // nothing listening here
	})
	err := m.Send("to@test.com", "sub", "body")
	if err == nil {
		t.Fatal("expected dial error")
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
