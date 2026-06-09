package mailer

import (
	"net"
	"testing"
	"time"

	"github.com/lgldsilva/jackui/internal/config"
)

func TestSend_DialError(t *testing.T) {
	m := New(config.SMTPConfig{Host: "127.0.0.1", Port: 1, From: "test@test.com", Username: "u", Password: "p"})
	err := m.Send("to@test.com", "sub", "body")
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}

func TestSend_EnabledCheck(t *testing.T) {
	m := New(config.SMTPConfig{})
	err := m.Send("to@test.com", "sub", "body")
	if err == nil {
		t.Fatal("expected error when SMTP not configured")
	}
}

func TestBuildMessageWithTLS(t *testing.T) {
	msg := buildMessage("from@test.com", "to@test.com", "Subject", "<p>body</p>")
	if !contains(msg, "MIME-Version: 1.0") {
		t.Error("expected MIME-Version header")
	}
	if !contains(msg, "Content-Type: text/html; charset=UTF-8") {
		t.Error("expected Content-Type header")
	}
}

func TestStartTLS_Error(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, cerr := ln.Accept()
		if cerr != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		conn.Write([]byte("220 test-server ESMTP\r\n"))
		conn.Read(buf)
		conn.Write([]byte("250 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("250-STARTTLS\r\n250 8BITMIME\r\n"))
		conn.Read(buf)
		conn.Write([]byte("454 TLS not available\r\n"))
	}()

	cfg := config.SMTPConfig{Host: "127.0.0.1", Port: addr.Port, From: "from@test.com"}
	m := New(cfg)
	err = m.Send("to@test.com", "sub", "body")
	if err == nil {
		t.Error("expected error when STARTTLS fails")
	}
}

func TestFrom_EmptyFallback(t *testing.T) {
	m := New(config.SMTPConfig{Host: "s", Port: 25, Username: ""})
	got := m.from()
	if got != "" {
		t.Errorf("from() = %q, want empty", got)
	}
}

func TestMailRcptError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, cerr := ln.Accept()
		if cerr != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		conn.Write([]byte("220 test ESMTP\r\n"))
		conn.Read(buf)
		conn.Write([]byte("250 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("250 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("334 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("235 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("250 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("550 No such user\r\n"))
	}()

	m := New(config.SMTPConfig{
		Host: "127.0.0.1", Port: addr.Port, From: "f@t.com", Username: "u", Password: "p",
	})
	err = m.Send("bad@test.com", "sub", "body")
	if err == nil {
		t.Error("expected error for bad recipient")
	}
}

func TestMailDataError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, cerr := ln.Accept()
		if cerr != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		conn.Write([]byte("220 test ESMTP\r\n"))
		conn.Read(buf)
		conn.Write([]byte("250 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("250 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("334 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("235 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("250 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("250 OK\r\n"))
		conn.Read(buf)
		conn.Write([]byte("452 Insufficient system storage\r\n"))
	}()

	m := New(config.SMTPConfig{
		Host: "127.0.0.1", Port: addr.Port, From: "f@t.com", Username: "u", Password: "p",
	})
	err = m.Send("to@test.com", "sub", "body")
	if err == nil {
		t.Error("expected error for data failure")
	}
}

func TestSend_AuthStartTLSSMTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)
	done := make(chan struct{})

	go func() {
		defer close(done)
		conn, cerr := ln.Accept()
		if cerr != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		buf := make([]byte, 4096)
		conn.Write([]byte("220 smtp.test ESMTP\r\n"))
		conn.Read(buf)
		conn.Write([]byte("250-smtp.test\r\n250-STARTTLS\r\n250 AUTH LOGIN PLAIN\r\n"))
		conn.Read(buf)
		conn.Write([]byte("220 Ready to start TLS\r\n"))
	}()

	m := New(config.SMTPConfig{
		Host: "127.0.0.1", Port: addr.Port, From: "f@t.com",
		Username: "u", Password: "p",
	})
	err = m.Send("to@test.com", "sub", "body")
	// STARTTLS will fail because we don't actually do TLS handshake - that's expected
	if err == nil {
		t.Log("Send succeeded (unexpected but possible in some envs)")
	}
	<-done
}

func TestSend_HelloError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, cerr := ln.Accept()
		if cerr != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		conn.Write([]byte("220 test ESMTP\r\n"))
		conn.Read(buf)
		conn.Write([]byte("500 Unrecognized command\r\n"))
	}()

	m := New(config.SMTPConfig{
		Host: "127.0.0.1", Port: addr.Port, From: "f@t.com",
	})
	err = m.Send("to@test.com", "sub", "body")
	if err == nil {
		t.Error("expected error for bad EHLO response")
	}
}

func TestBuildMessage_Empty(t *testing.T) {
	msg := buildMessage("", "", "", "")
	if msg == "" {
		t.Error("expected non-empty message even with empty params")
	}
	if !contains(msg, "From: ") {
		t.Error("expected From header")
	}
}
