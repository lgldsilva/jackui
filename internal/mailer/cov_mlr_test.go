package mailer

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/luizg/jackui/internal/config"
)

// mlrScript drives a fake SMTP server: for each client line read it writes the
// matching server reply (in order). It deliberately advertises NO STARTTLS so
// the full Send flow can complete without a real TLS handshake — exercising the
// Mail/Rcpt/Data/Write/Close/Quit success path that the existing tests miss.
type mlrScript struct {
	greeting string
	// replies maps a command prefix to the server response line(s).
	ehlo string
	auth string
	mail string
	rcpt string
	data string // reply to DATA command (before body)
	body string // reply after the terminating "." of the body
	quit string
}

// mlrServe spins up a one-shot fake SMTP listener and returns its port. It
// stops once the connection closes or the deadline lapses.
func mlrServe(t *testing.T, s mlrScript) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	go func() {
		conn, cerr := ln.Accept()
		if cerr != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		r := bufio.NewReader(conn)
		write := func(line string) {
			if line != "" {
				_, _ = conn.Write([]byte(line))
			}
		}
		write(s.greeting)
		inData := false
		for {
			line, rerr := r.ReadString('\n')
			if rerr != nil {
				return
			}
			if inData {
				// Wait for the lone "." that terminates DATA.
				if strings.TrimRight(line, "\r\n") == "." {
					inData = false
					write(s.body)
				}
				continue
			}
			up := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
				write(s.ehlo)
			case strings.HasPrefix(up, "AUTH"):
				write(s.auth)
			case strings.HasPrefix(up, "MAIL"):
				write(s.mail)
			case strings.HasPrefix(up, "RCPT"):
				write(s.rcpt)
			case strings.HasPrefix(up, "DATA"):
				write(s.data)
				inData = true
			case strings.HasPrefix(up, "QUIT"):
				write(s.quit)
				return
			default:
				write("250 OK\r\n")
			}
		}
	}()
	return port
}

// mlrOKScript is a fully-cooperative server (no STARTTLS, AUTH accepted, full
// delivery). Tweak individual fields to inject failures.
func mlrOKScript() mlrScript {
	return mlrScript{
		greeting: "220 mlr.test ESMTP\r\n",
		ehlo:     "250-mlr.test\r\n250 AUTH PLAIN LOGIN\r\n",
		auth:     "235 2.7.0 Authentication successful\r\n",
		mail:     "250 OK\r\n",
		rcpt:     "250 OK\r\n",
		data:     "354 End data with <CR><LF>.<CR><LF>\r\n",
		body:     "250 2.0.0 Ok: queued\r\n",
		quit:     "221 2.0.0 Bye\r\n",
	}
}

// TestMlrSendFullSuccess walks the entire happy path (AUTH + DATA + Quit) so the
// success branch of Send is covered.
func Test_mlr_SendFullSuccess(t *testing.T) {
	port := mlrServe(t, mlrOKScript())
	m := New(config.SMTPConfig{
		Host: "127.0.0.1", Port: port, From: "from@mlr.test",
		Username: "u", Password: "p",
	})
	if err := m.Send("to@mlr.test", "mlr subject", "<p>mlr body</p>"); err != nil {
		t.Fatalf("Send: unexpected error: %v", err)
	}
}

// TestMlrSendNoAuthSuccess exercises the path where AUTH is NOT advertised (so
// the auth block is skipped) yet delivery still succeeds.
func Test_mlr_SendNoAuthSuccess(t *testing.T) {
	s := mlrOKScript()
	s.ehlo = "250 mlr.test\r\n" // no AUTH extension advertised
	port := mlrServe(t, s)
	m := New(config.SMTPConfig{
		Host: "127.0.0.1", Port: port, From: "from@mlr.test",
		Username: "u", Password: "p",
	})
	if err := m.Send("to@mlr.test", "mlr sub", "<p>body</p>"); err != nil {
		t.Fatalf("Send: unexpected error: %v", err)
	}
}

// TestMlrSendAuthAdvertisedNoUsername covers the AUTH-advertised-but-no-username
// branch (the && guard short-circuits, auth skipped) yet still delivers.
func Test_mlr_SendAuthAdvertisedNoUsername(t *testing.T) {
	port := mlrServe(t, mlrOKScript())
	m := New(config.SMTPConfig{
		Host: "127.0.0.1", Port: port, From: "from@mlr.test",
		Username: "", // no username → auth branch guarded off
	})
	if err := m.Send("to@mlr.test", "mlr sub", "<p>body</p>"); err != nil {
		t.Fatalf("Send: unexpected error: %v", err)
	}
}

// TestMlrSendAuthError covers the AUTH-failure branch (auth advertised, username
// set, server rejects credentials).
func Test_mlr_SendAuthError(t *testing.T) {
	s := mlrOKScript()
	s.auth = "535 5.7.8 Authentication credentials invalid\r\n"
	port := mlrServe(t, s)
	m := New(config.SMTPConfig{
		Host: "127.0.0.1", Port: port, From: "from@mlr.test",
		Username: "u", Password: "wrong",
	})
	err := m.Send("to@mlr.test", "mlr sub", "<p>body</p>")
	if err == nil {
		t.Fatal("expected error when AUTH is rejected")
	}
	if !strings.Contains(err.Error(), "smtp auth") {
		t.Errorf("expected smtp auth error, got %v", err)
	}
}

// TestMlrSendMailError covers the MAIL FROM rejection branch.
func Test_mlr_SendMailError(t *testing.T) {
	s := mlrOKScript()
	s.mail = "550 5.1.8 Sender address rejected\r\n"
	port := mlrServe(t, s)
	m := New(config.SMTPConfig{
		Host: "127.0.0.1", Port: port, From: "from@mlr.test",
		Username: "u", Password: "p",
	})
	if err := m.Send("to@mlr.test", "mlr sub", "<p>body</p>"); err == nil {
		t.Fatal("expected error when MAIL FROM is rejected")
	}
}
