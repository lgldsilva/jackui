package auth

import (
	"encoding/binary"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

var errSessionExpired = errors.New("passkey: sessão expirada — tente de novo")

// WAManager wraps go-webauthn plus a short-lived in-memory store for the
// challenge (SessionData) that bridges the begin/finish steps of each ceremony.
// Sessions are keyed by an opaque id returned to the client and echoed back.
type WAManager struct {
	wa       *webauthn.WebAuthn
	mu       sync.Mutex
	sessions map[string]waSession
}

type waSession struct {
	data webauthn.SessionData
	exp  time.Time
}

// NewWAManager builds the manager. rpID is the effective domain (no scheme/port,
// e.g. "jackui.example.com"); origin is the full URL the browser uses
// (e.g. "https://jackui.example.com"). Returns nil if config is incomplete.
func NewWAManager(rpID, rpDisplayName, origin string) (*WAManager, error) {
	if rpID == "" || origin == "" {
		return nil, nil
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: rpDisplayName,
		RPOrigins:     []string{origin},
	})
	if err != nil {
		return nil, err
	}
	return &WAManager{wa: wa, sessions: map[string]waSession{}}, nil
}

func (m *WAManager) putSession(data *webauthn.SessionData) string {
	id, _ := randomToken(24)
	m.mu.Lock()
	defer m.mu.Unlock()
	// Opportunistic GC of expired sessions.
	now := time.Now()
	for k, v := range m.sessions {
		if now.After(v.exp) {
			delete(m.sessions, k)
		}
	}
	m.sessions[id] = waSession{data: *data, exp: now.Add(5 * time.Minute)}
	return id
}

func (m *WAManager) takeSession(id string) (*webauthn.SessionData, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id) // single-use
	}
	if !ok || time.Now().After(s.exp) {
		return nil, false
	}
	return &s.data, true
}

// waUser adapts an account + its stored credentials to the webauthn.User
// interface. WebAuthnID is the 8-byte big-endian user id (stable, opaque).
type waUser struct {
	id    int
	name  string
	creds []webauthn.Credential
}

func waUserFrom(id int, name string, creds []webauthn.Credential) *waUser {
	return &waUser{id: id, name: name, creds: creds}
}
func (u *waUser) WebAuthnID() []byte {
	b := make([]byte, 8)
	// #nosec G115 -- conversao limitada (statfs/tempo Unix/id/rune ASCII/fs magic); sem overflow real
	binary.BigEndian.PutUint64(b, uint64(u.id))
	return b
}
func (u *waUser) WebAuthnName() string                       { return u.name }
func (u *waUser) WebAuthnDisplayName() string                { return u.name }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// BeginRegister starts adding a passkey. Returns the creation options (to pass
// to navigator.credentials.create) and a session id to echo back on finish.
func (m *WAManager) BeginRegister(id int, name string, creds []webauthn.Credential) (*protocol.CredentialCreation, string, error) {
	opts, session, err := m.wa.BeginRegistration(waUserFrom(id, name, creds))
	if err != nil {
		return nil, "", err
	}
	return opts, m.putSession(session), nil
}

// FinishRegister verifies the attestation in the request body against the saved
// session and returns the new credential to persist.
func (m *WAManager) FinishRegister(id int, name string, creds []webauthn.Credential, sessionID string, r *http.Request) (*webauthn.Credential, error) {
	session, ok := m.takeSession(sessionID)
	if !ok {
		return nil, errSessionExpired
	}
	return m.wa.FinishRegistration(waUserFrom(id, name, creds), *session, r)
}

// BeginLogin starts a passkey assertion for a known user.
func (m *WAManager) BeginLogin(id int, name string, creds []webauthn.Credential) (*protocol.CredentialAssertion, string, error) {
	opts, session, err := m.wa.BeginLogin(waUserFrom(id, name, creds))
	if err != nil {
		return nil, "", err
	}
	return opts, m.putSession(session), nil
}

// FinishLogin verifies the assertion; returns the matched credential (with an
// updated sign count to persist).
func (m *WAManager) FinishLogin(id int, name string, creds []webauthn.Credential, sessionID string, r *http.Request) (*webauthn.Credential, error) {
	session, ok := m.takeSession(sessionID)
	if !ok {
		return nil, errSessionExpired
	}
	return m.wa.FinishLogin(waUserFrom(id, name, creds), *session, r)
}
