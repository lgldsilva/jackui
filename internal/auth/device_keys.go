package auth

import (
	"context"
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

// deviceKeySelect is the shared SELECT…JOIN…WHERE prefix. Callers append only
// the predicate (and any ORDER BY) so the column list and "SELECT " live once.
const deviceKeySelect = "SELECT " + deviceKeyCols +
	" FROM device_keys k JOIN users u ON u.id = k.user_id WHERE "

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
func (s *Store) CreateDeviceKey(ctx context.Context, userID int, meta DeviceKeyMeta) (string, int64, error) {
	raw, err := randomToken(32)
	if err != nil {
		return "", 0, err
	}
	plain := DeviceKeyPrefix + raw
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		"UPDATE device_keys SET revoked_at = now() WHERE device_id = ? AND user_id = ? AND revoked_at IS NULL",
		meta.DeviceID, userID,
	); err != nil {
		return "", 0, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx,
		"INSERT INTO device_keys(user_id, device_id, name, model, key_hash, last_ip) VALUES(?, ?, ?, ?, ?, ?) RETURNING id",
		userID, meta.DeviceID, meta.Name, meta.Model, sha256Hex(plain), meta.IP,
	).Scan(&id); err != nil {
		return "", 0, err
	}
	return plain, id, tx.Commit()
}

// LookupDeviceKey resolves an active (non-revoked) key. Does not touch usage
// stamps — see TouchDeviceKey.
func (s *Store) LookupDeviceKey(ctx context.Context, plain string) (*DeviceKey, error) {
	if !strings.HasPrefix(plain, DeviceKeyPrefix) {
		return nil, ErrDeviceKeyInvalid
	}
	k, err := scanDeviceKey(s.db.QueryRowContext(ctx,
		deviceKeySelect+"k.key_hash = ? AND k.revoked_at IS NULL",
		sha256Hex(plain),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeviceKeyInvalid
	}
	return k, err
}

// TouchDeviceKey records a successful use (last_used_at / last_ip).
func (s *Store) TouchDeviceKey(ctx context.Context, id int64, ip string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE device_keys SET last_used_at = now(), last_ip = ? WHERE id = ?", ip, id)
	return err
}

// ListDeviceKeys returns active keys. userID < 0 lists every user's keys (admin).
func (s *Store) ListDeviceKeys(ctx context.Context, userID int) ([]DeviceKey, error) {
	q := deviceKeySelect + "k.revoked_at IS NULL"
	args := []any{}
	if userID >= 0 {
		q += " AND k.user_id = ?"
		args = append(args, userID)
	}
	return s.queryDeviceKeys(ctx, q+" ORDER BY k.device_id, k.created_at", args...)
}

// DeviceProfiles lists the active keys sharing deviceID (the TV's profile picker).
func (s *Store) DeviceProfiles(ctx context.Context, deviceID string) ([]DeviceKey, error) {
	return s.queryDeviceKeys(ctx,
		deviceKeySelect+"k.device_id = ? AND k.revoked_at IS NULL ORDER BY u.username",
		deviceID,
	)
}

func (s *Store) queryDeviceKeys(ctx context.Context, q string, args ...any) ([]DeviceKey, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
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
func (s *Store) RevokeDeviceKey(ctx context.Context, id int64, ownerID int) error {
	q := "UPDATE device_keys SET revoked_at = now() WHERE id = ? AND revoked_at IS NULL"
	args := []any{id}
	if ownerID >= 0 {
		q += " AND user_id = ?"
		args = append(args, ownerID)
	}
	return expectOneRow(s.db.ExecContext(ctx, q, args...))
}

// RevokeAllDeviceKeys revokes every paired device of a user. A device key
// never expires and mints fresh tokens on its own, so it has to die together
// with the refresh sessions whenever the password is (re)set.
func (s *Store) RevokeAllDeviceKeys(ctx context.Context, userID int) error {
	_, err := s.db.ExecContext(ctx, "UPDATE device_keys SET revoked_at = now() WHERE user_id = ? AND revoked_at IS NULL", userID)
	return err
}

// SetDevicePIN sets (or with pin == "" clears) the profile PIN of the owner's key.
func (s *Store) SetDevicePIN(ctx context.Context, id int64, ownerID int, pin string) error {
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
	return expectOneRow(s.db.ExecContext(ctx,
		"UPDATE device_keys SET pin_hash = ?, pin_failures = 0, pin_locked_until = NULL WHERE id = ? AND user_id = ? AND revoked_at IS NULL",
		hash, id, ownerID,
	))
}

// CheckDevicePIN enforces the profile PIN of k (no-op when none is set),
// counting failures and locking after devicePINMaxFailures. The attempt is
// charged in the database BEFORE the PIN is compared, so parallel guesses
// cannot all read the same counter and slip past the limit.
func (s *Store) CheckDevicePIN(ctx context.Context, k *DeviceKey, pin string, now time.Time) error {
	if !k.HasPIN {
		return nil
	}
	if pin == "" {
		if k.pinLocked != nil && now.Before(*k.pinLocked) {
			return ErrDevicePINLocked
		}
		return ErrDevicePINRequired
	}
	locked, err := s.chargeDevicePINAttempt(ctx, k.ID, now)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(k.pinHash), []byte(pin)) == nil {
		_, err := s.db.ExecContext(ctx, "UPDATE device_keys SET pin_failures = 0, pin_locked_until = NULL WHERE id = ?", k.ID)
		return err
	}
	if locked {
		return ErrDevicePINLocked
	}
	return ErrDevicePINInvalid
}

// chargeDevicePINAttempt atomically counts one attempt against key id and
// reports whether it was the one that tripped the lock. A key that is already
// locked matches no row and yields ErrDevicePINLocked without being charged;
// the row lock serialises concurrent callers, each re-checking the predicate.
func (s *Store) chargeDevicePINAttempt(ctx context.Context, id int64, now time.Time) (bool, error) {
	var locked bool
	err := s.db.QueryRowContext(ctx,
		`UPDATE device_keys SET
			pin_failures = CASE WHEN pin_failures + 1 >= ? THEN 0 ELSE pin_failures + 1 END,
			pin_locked_until = CASE WHEN pin_failures + 1 >= ? THEN CAST(? AS TIMESTAMPTZ) END
		WHERE id = ? AND (pin_locked_until IS NULL OR pin_locked_until <= ?)
		RETURNING pin_locked_until IS NOT NULL`,
		devicePINMaxFailures, devicePINMaxFailures, now.Add(devicePINLockWindow).UTC(), id, now.UTC(),
	).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrDevicePINLocked
	}
	return locked, err
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
