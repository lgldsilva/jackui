package auth

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// DeviceKeyPrefix marks JackUI device keys so they are recognisable in logs,
// password managers and secret scanners.
const DeviceKeyPrefix = "jkd_"

// DevicePrefix is the Authorization scheme a paired device uses to present its
// key: "Authorization: Device jkd_…".
const DevicePrefix = "Device "

// AMRDevice is the Claims.AMR value of access tokens minted from a device key.
// Those tokens are denied admin-only and account-security routes.
const AMRDevice = "device"

// Device-key PIN lockout: after devicePINMaxFailures wrong PINs the profile is
// locked for devicePINLockWindow (persisted, so a restart doesn't reset it).
const (
	devicePINMaxFailures = 5
	devicePINLockWindow  = 15 * time.Minute
)

var (
	ErrDeviceKeyInvalid  = errors.New("invalid device key")
	ErrDeviceKeyNotFound = errors.New("device key not found")
	ErrDevicePINRequired = errors.New("pin required")
	ErrDevicePINInvalid  = errors.New("invalid pin")
	ErrDevicePINLocked   = errors.New("pin locked")
	ErrDevicePINFormat   = errors.New("pin must be 4 to 6 digits")
)

// DeviceKey is the public view of a paired device credential (never the key).
type DeviceKey struct {
	ID         int64      `json:"id"`
	UserID     int        `json:"userId"`
	Username   string     `json:"username,omitempty"`
	DeviceID   string     `json:"deviceId"`
	Name       string     `json:"name"`
	Model      string     `json:"model"`
	HasPIN     bool       `json:"hasPin"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	LastIP     string     `json:"lastIp"`
	pinHash    string
	pinFails   int
	pinLocked  *time.Time
}

// DeviceKeyMeta describes the device a key is issued to.
type DeviceKeyMeta struct {
	DeviceID string
	Name     string
	Model    string
	IP       string
}

const deviceKeyCols = `k.id, k.user_id, u.username, k.device_id, k.name, k.model, k.pin_hash,
	k.pin_failures, k.pin_locked_until, k.created_at, k.last_used_at, k.last_ip`

type rowScanner interface{ Scan(dest ...any) error }

func scanDeviceKey(r rowScanner) (*DeviceKey, error) {
	var k DeviceKey
	var lastUsed, locked sql.NullTime
	if err := r.Scan(&k.ID, &k.UserID, &k.Username, &k.DeviceID, &k.Name, &k.Model, &k.pinHash,
		&k.pinFails, &locked, &k.CreatedAt, &lastUsed, &k.LastIP); err != nil {
		return nil, err
	}
	if lastUsed.Valid {
		k.LastUsedAt = &lastUsed.Time
	}
	if locked.Valid {
		k.pinLocked = &locked.Time
	}
	k.HasPIN = k.pinHash != ""
	return &k, nil
}

// CreateDeviceKey issues a fresh key for (meta.DeviceID, userID), revoking any
// previous active key of the same pair (re-pairing replaces). Returns the plain
// key — shown once, only its hash is stored.
func (s *Store) CreateDeviceKey(userID int, meta DeviceKeyMeta) (string, int64, error) {
	raw, err := randomToken(32)
	if err != nil {
		return "", 0, err
	}
	plain := DeviceKeyPrefix + raw
	tx, err := s.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(
		"UPDATE device_keys SET revoked_at = now() WHERE device_id = ? AND user_id = ? AND revoked_at IS NULL",
		meta.DeviceID, userID,
	); err != nil {
		return "", 0, err
	}
	var id int64
	if err := tx.QueryRow(
		"INSERT INTO device_keys(user_id, device_id, name, model, key_hash, last_ip) VALUES(?, ?, ?, ?, ?, ?) RETURNING id",
		userID, meta.DeviceID, meta.Name, meta.Model, sha256Hex(plain), meta.IP,
	).Scan(&id); err != nil {
		return "", 0, err
	}
	return plain, id, tx.Commit()
}

// LookupDeviceKey resolves an active (non-revoked) key. Does not touch usage
// stamps — see TouchDeviceKey.
func (s *Store) LookupDeviceKey(plain string) (*DeviceKey, error) {
	if !strings.HasPrefix(plain, DeviceKeyPrefix) {
		return nil, ErrDeviceKeyInvalid
	}
	k, err := scanDeviceKey(s.db.QueryRow(
		"SELECT "+deviceKeyCols+" FROM device_keys k JOIN users u ON u.id = k.user_id WHERE k.key_hash = ? AND k.revoked_at IS NULL",
		sha256Hex(plain),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeviceKeyInvalid
	}
	return k, err
}

// TouchDeviceKey records a successful use (last_used_at / last_ip).
func (s *Store) TouchDeviceKey(id int64, ip string) error {
	_, err := s.db.Exec("UPDATE device_keys SET last_used_at = now(), last_ip = ? WHERE id = ?", ip, id)
	return err
}

// ListDeviceKeys returns active keys. userID < 0 lists every user's keys (admin).
func (s *Store) ListDeviceKeys(userID int) ([]DeviceKey, error) {
	q := "SELECT " + deviceKeyCols + " FROM device_keys k JOIN users u ON u.id = k.user_id WHERE k.revoked_at IS NULL"
	args := []any{}
	if userID >= 0 {
		q += " AND k.user_id = ?"
		args = append(args, userID)
	}
	return s.queryDeviceKeys(q+" ORDER BY k.device_id, k.created_at", args...)
}

// DeviceProfiles lists the active keys sharing deviceID (the TV's profile picker).
func (s *Store) DeviceProfiles(deviceID string) ([]DeviceKey, error) {
	return s.queryDeviceKeys(
		"SELECT "+deviceKeyCols+" FROM device_keys k JOIN users u ON u.id = k.user_id WHERE k.device_id = ? AND k.revoked_at IS NULL ORDER BY u.username",
		deviceID,
	)
}

func (s *Store) queryDeviceKeys(q string, args ...any) ([]DeviceKey, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceKey{}
	for rows.Next() {
		k, err := scanDeviceKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// RevokeDeviceKey revokes key id. ownerID < 0 = admin (any owner); otherwise
// only the owner's own key matches, so a foreign id is indistinguishable from
// a missing one (ErrDeviceKeyNotFound).
func (s *Store) RevokeDeviceKey(id int64, ownerID int) error {
	q := "UPDATE device_keys SET revoked_at = now() WHERE id = ? AND revoked_at IS NULL"
	args := []any{id}
	if ownerID >= 0 {
		q += " AND user_id = ?"
		args = append(args, ownerID)
	}
	return expectOneRow(s.db.Exec(q, args...))
}

// SetDevicePIN sets (or with pin == "" clears) the profile PIN of the owner's key.
func (s *Store) SetDevicePIN(id int64, ownerID int, pin string) error {
	hash := ""
	if pin != "" {
		if !validPIN(pin) {
			return ErrDevicePINFormat
		}
		b, err := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		hash = string(b)
	}
	return expectOneRow(s.db.Exec(
		"UPDATE device_keys SET pin_hash = ?, pin_failures = 0, pin_locked_until = NULL WHERE id = ? AND user_id = ? AND revoked_at IS NULL",
		hash, id, ownerID,
	))
}

// CheckDevicePIN enforces the profile PIN of k (no-op when none is set),
// counting failures and locking after devicePINMaxFailures.
func (s *Store) CheckDevicePIN(k *DeviceKey, pin string, now time.Time) error {
	if !k.HasPIN {
		return nil
	}
	if k.pinLocked != nil && now.Before(*k.pinLocked) {
		return ErrDevicePINLocked
	}
	if pin == "" {
		return ErrDevicePINRequired
	}
	if bcrypt.CompareHashAndPassword([]byte(k.pinHash), []byte(pin)) == nil {
		_, err := s.db.Exec("UPDATE device_keys SET pin_failures = 0, pin_locked_until = NULL WHERE id = ?", k.ID)
		return err
	}
	fails := k.pinFails + 1
	var lock any
	if fails >= devicePINMaxFailures {
		lock, fails = now.Add(devicePINLockWindow).UTC(), 0
	}
	if _, err := s.db.Exec("UPDATE device_keys SET pin_failures = ?, pin_locked_until = ? WHERE id = ?", fails, lock, k.ID); err != nil {
		return err
	}
	if lock != nil {
		return ErrDevicePINLocked
	}
	return ErrDevicePINInvalid
}

func validPIN(pin string) bool {
	if len(pin) < 4 || len(pin) > 6 {
		return false
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func expectOneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrDeviceKeyNotFound
	}
	return nil
}
