-- 0008: long-lived per-device API keys (TV / device pairing, issue #201).
--
-- A TV pairs through an RFC 8628-style flow (QR on the TV → approve on the
-- phone) and receives an opaque key (`jkd_…`). The key is stored hashed
-- (SHA-256 — 32 random bytes, no need for a slow hash) and exchanged at
-- POST /api/auth/device/session for a short-lived access JWT.
--
-- One ACTIVE key per (device_id, user_id): re-pairing the same user on the same
-- TV revokes the previous key (partial unique index below). Several users on
-- one TV = several rows sharing device_id (profile switching).
--
-- pin_hash is an optional bcrypt'd 4–6 digit profile PIN checked by
-- /device/session; pin_failures/pin_locked_until persist the lockout so a
-- restart doesn't reset a brute-force attempt.

CREATE TABLE device_keys (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id          BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id        TEXT NOT NULL,
    name             TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    key_hash         TEXT NOT NULL UNIQUE,
    pin_hash         TEXT NOT NULL DEFAULT '',
    pin_failures     INTEGER NOT NULL DEFAULT 0,
    pin_locked_until TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at     TIMESTAMPTZ,
    last_ip          TEXT NOT NULL DEFAULT '',
    revoked_at       TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_device_keys_active ON device_keys(device_id, user_id) WHERE revoked_at IS NULL;
CREATE INDEX idx_device_keys_user ON device_keys(user_id);
