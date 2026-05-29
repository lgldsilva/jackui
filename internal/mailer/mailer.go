// Package mailer sends transactional email (password reset, email verification,
// invites) over SMTP. It's intentionally tiny: stdlib net/smtp with STARTTLS,
// no external deps. When no host is configured it's "disabled" — Enabled()
// returns false and callers fall back to surfacing a copyable link instead.
package mailer

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/luizg/jackui/internal/config"
)

type Mailer struct {
	cfg config.SMTPConfig
}

func New(cfg config.SMTPConfig) *Mailer { return &Mailer{cfg: cfg} }

// Enabled reports whether SMTP is configured enough to send.
func (m *Mailer) Enabled() bool {
	return m != nil && m.cfg.Host != "" && m.cfg.Port != 0
}

func (m *Mailer) from() string {
	if m.cfg.From != "" {
		return m.cfg.From
	}
	return m.cfg.Username
}

// Send delivers a single HTML email. Uses STARTTLS on the standard submission
// flow; returns an error the caller can log (the flow then still succeeds with
// a neutral response so it doesn't leak whether an address exists).
func (m *Mailer) Send(to, subject, htmlBody string) error {
	if !m.Enabled() {
		return fmt.Errorf("mailer: SMTP not configured")
	}
	addr := net.JoinHostPort(m.cfg.Host, fmt.Sprintf("%d", m.cfg.Port))
	from := m.from()

	msg := buildMessage(from, to, subject, htmlBody)

	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Hello("jackui"); err != nil {
		return err
	}
	// Upgrade to TLS when the server advertises STARTTLS (port 587 / submission).
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: m.cfg.Host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if ok, _ := c.Extension("AUTH"); ok && m.cfg.Username != "" {
		auth := smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func buildMessage(from, to, subject, htmlBody string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlBody)
	return b.String()
}
