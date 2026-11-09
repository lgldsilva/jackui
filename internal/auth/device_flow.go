package auth

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// Device authorization grant (RFC 8628 shaped) for TV pairing.
//
// Pending pairings live in memory: they are short-lived (10 min) and a restart
// only costs the TV a new QR. The device code is kept hashed so a heap dump
// doesn't hand out pollable codes.

const (
	DeviceFlowTTL      = 10 * time.Minute
	DeviceFlowInterval = 5 * time.Second
	// userCodeAlphabet avoids 0/O, 1/I/L so codes read back unambiguously on a TV.
	userCodeAlphabet   = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	userCodeLen        = 8
	devicePendingPerIP = 5
	devicePendingMax   = 1000
)

// RFC 8628 §3.5 error codes returned by the token endpoint.
const (
	DeviceErrPending  = "authorization_pending"
	DeviceErrSlowDown = "slow_down"
	DeviceErrExpired  = "expired_token"
	DeviceErrDenied   = "access_denied"
)

var (
	ErrDeviceFlowFull     = errors.New("too many pending pairings")
	ErrDeviceCodeNotFound = errors.New("unknown or expired code")
)

type deviceFlowState int

const (
	deviceStatePending deviceFlowState = iota
	deviceStateApproved
	deviceStateDenied
)

// DevicePairing is a pending pairing request as shown on the approve page.
type DevicePairing struct {
	UserCode  string    `json:"userCode"`
	DeviceID  string    `json:"deviceId"`
	Name      string    `json:"name"`
	Model     string    `json:"model"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`

	codeHash string
	state    deviceFlowState
	userID   int
	lastPoll time.Time
}

// DeviceFlow holds pending pairings keyed by hashed device code.
type DeviceFlow struct {
	mu       sync.Mutex
	byCode   map[string]*DevicePairing // sha256(deviceCode) → pairing
	byUser   map[string]*DevicePairing // userCode → pairing
	now      func() time.Time
	interval time.Duration
}

// NewDeviceFlow builds an empty flow store.
func NewDeviceFlow() *DeviceFlow {
	return &DeviceFlow{
		byCode:   map[string]*DevicePairing{},
		byUser:   map[string]*DevicePairing{},
		now:      time.Now,
		interval: DeviceFlowInterval,
	}
}

// Interval is the minimum polling interval the device must honour.
func (f *DeviceFlow) Interval() time.Duration { return f.interval }

// NormalizeUserCode upper-cases and strips separators ("abcd-efgh" → "ABCDEFGH").
func NormalizeUserCode(s string) string {
	s = strings.ToUpper(s)
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, s)
}

// FormatUserCode renders a normalized code as "ABCD-EFGH".
func FormatUserCode(code string) string {
	if len(code) != userCodeLen {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// Start registers a new pairing and returns the plain device code (for the TV
// to poll with) and the pairing (whose UserCode goes on screen / in the QR).
func (f *DeviceFlow) Start(meta DeviceKeyMeta) (string, *DevicePairing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	f.gcLocked(now)
	if len(f.byCode) >= devicePendingMax || f.countIPLocked(meta.IP) >= devicePendingPerIP {
		return "", nil, ErrDeviceFlowFull
	}
	deviceCode, err := randomToken(32)
	if err != nil {
		return "", nil, err
	}
	userCode, err := f.uniqueUserCodeLocked()
	if err != nil {
		return "", nil, err
	}
	p := &DevicePairing{
		UserCode: userCode, DeviceID: meta.DeviceID, Name: meta.Name, Model: meta.Model, IP: meta.IP,
		CreatedAt: now, ExpiresAt: now.Add(DeviceFlowTTL), codeHash: sha256Hex(deviceCode),
	}
	f.byCode[p.codeHash] = p
	f.byUser[userCode] = p
	return deviceCode, p, nil
}

func (f *DeviceFlow) uniqueUserCodeLocked() (string, error) {
	for {
		c, err := randomFromAlphabet(userCodeLen, userCodeAlphabet)
		if err != nil {
			return "", err
		}
		if _, taken := f.byUser[c]; !taken {
			return c, nil
		}
	}
}

func (f *DeviceFlow) countIPLocked(ip string) int {
	n := 0
	for _, p := range f.byCode {
		if p.IP == ip {
			n++
		}
	}
	return n
}

func (f *DeviceFlow) gcLocked(now time.Time) {
	for h, p := range f.byCode {
		if !now.Before(p.ExpiresAt) {
			delete(f.byCode, h)
			delete(f.byUser, p.UserCode)
		}
	}
}

// Lookup returns a copy of the PENDING pairing for userCode (approve page).
func (f *DeviceFlow) Lookup(userCode string) (DevicePairing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.pendingLocked(userCode)
	if err != nil {
		return DevicePairing{}, err
	}
	return *p, nil
}

func (f *DeviceFlow) pendingLocked(userCode string) (*DevicePairing, error) {
	p, ok := f.byUser[NormalizeUserCode(userCode)]
	if !ok || !f.now().Before(p.ExpiresAt) || p.state != deviceStatePending {
		return nil, ErrDeviceCodeNotFound
	}
	return p, nil
}

// Decide approves (for userID) or denies a pending pairing. Single decision:
// a second call on the same code fails with ErrDeviceCodeNotFound.
func (f *DeviceFlow) Decide(userCode string, userID int, approve bool) (DevicePairing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, err := f.pendingLocked(userCode)
	if err != nil {
		return DevicePairing{}, err
	}
	if approve {
		p.state, p.userID = deviceStateApproved, userID
	} else {
		p.state = deviceStateDenied
	}
	return *p, nil
}

// DevicePoll is the outcome of one token-endpoint poll. Err is one of the
// DeviceErr* codes, or "" when approved (UserID/Pairing then filled).
type DevicePoll struct {
	Err     string
	UserID  int
	Pairing DevicePairing
}

// Poll advances the state machine for deviceCode. An approved pairing is
// returned exactly once and then forgotten; denial is also terminal.
func (f *DeviceFlow) Poll(deviceCode string) DevicePoll {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	h := sha256Hex(deviceCode)
	p, ok := f.byCode[h]
	if !ok || !now.Before(p.ExpiresAt) {
		f.dropLocked(h, p)
		return DevicePoll{Err: DeviceErrExpired}
	}
	switch p.state {
	case deviceStateApproved:
		f.dropLocked(h, p)
		return DevicePoll{UserID: p.userID, Pairing: *p}
	case deviceStateDenied:
		f.dropLocked(h, p)
		return DevicePoll{Err: DeviceErrDenied}
	}
	tooFast := !p.lastPoll.IsZero() && now.Sub(p.lastPoll) < f.interval
	p.lastPoll = now
	if tooFast {
		return DevicePoll{Err: DeviceErrSlowDown}
	}
	return DevicePoll{Err: DeviceErrPending}
}

func (f *DeviceFlow) dropLocked(h string, p *DevicePairing) {
	delete(f.byCode, h)
	if p != nil {
		delete(f.byUser, p.UserCode)
	}
}
