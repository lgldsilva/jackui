-- Reverse of 0006: restore the legacy global-by-name favorites table
-- (PRIMARY KEY (name)), keeping the newest row per name across ALL users
-- (its owner wins).
--
-- NOTE: this path is lossy BY DESIGN — per-user duplicates of the same name
-- collapse into a single global row, so only one user keeps the favorite.
-- Acceptable: it only matters when rolling back past 0006, and the legacy
-- schema could only ever hold one row per name anyway.

CREATE TABLE favorites_legacy (
    name         TEXT PRIMARY KEY,
    info_hash    TEXT,
    favorited_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reason       TEXT NOT NULL DEFAULT 'manual',
    user_id      BIGINT NOT NULL DEFAULT 0 REFERENCES users(id) ON DELETE CASCADE,
    magnet       TEXT NOT NULL DEFAULT '',
    folder_id    BIGINT REFERENCES favorite_folders(id) ON DELETE SET NULL
);

INSERT INTO favorites_legacy (name, info_hash, favorited_at, reason, user_id, magnet, folder_id)
SELECT DISTINCT ON (name)
       name, info_hash, favorited_at, reason, user_id, magnet, folder_id
FROM favorites
ORDER BY name, favorited_at DESC
ON CONFLICT DO NOTHING;

DROP TABLE favorites;
ALTER TABLE favorites_legacy RENAME TO favorites;

CREATE INDEX idx_fav_hash ON favorites(info_hash);
CREATE INDEX idx_fav_user ON favorites(user_id);
