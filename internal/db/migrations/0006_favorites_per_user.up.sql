-- 0006: favorites becomes per-user.
--
-- 0002 gave favorites PRIMARY KEY (name) alone: a name was one GLOBAL row, so
-- when user B favorited a name user A already had, the row was reassigned to B
-- (Add's old ON CONFLICT(name) DO UPDATE SET user_id = excluded.user_id) and
-- A's listing silently lost the favorite — B's unfavorite deleted it for
-- everyone. The new PRIMARY KEY (user_id, name) gives every user their own row.
--
-- Data preservation: keep one row per (user_id, name) — each user's MOST
-- RECENT favorite wins (favorited_at DESC). Duplicates within a single user
-- could not exist under the old PK, so ON CONFLICT DO NOTHING is a no-op
-- belt-and-braces guard for ties that survive DISTINCT ON.
--
-- Columns, defaults, and FKs are copied verbatim from 0002_init.up.sql
-- (users ON DELETE CASCADE, favorite_folders ON DELETE SET NULL).

CREATE TABLE favorites_new (
    name         TEXT NOT NULL,
    info_hash    TEXT,
    favorited_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reason       TEXT NOT NULL DEFAULT 'manual',
    user_id      BIGINT NOT NULL DEFAULT 0 REFERENCES users(id) ON DELETE CASCADE,
    magnet       TEXT NOT NULL DEFAULT '',
    folder_id    BIGINT REFERENCES favorite_folders(id) ON DELETE SET NULL,
    PRIMARY KEY (user_id, name)
);

INSERT INTO favorites_new (name, info_hash, favorited_at, reason, user_id, magnet, folder_id)
SELECT DISTINCT ON (user_id, name)
       name, info_hash, favorited_at, reason, user_id, magnet, folder_id
FROM favorites
ORDER BY user_id, name, favorited_at DESC
ON CONFLICT DO NOTHING;

DROP TABLE favorites;
ALTER TABLE favorites_new RENAME TO favorites;

-- Recreate the indexes 0002 defined on favorites.
CREATE INDEX idx_fav_hash ON favorites(info_hash);
CREATE INDEX idx_fav_user ON favorites(user_id);
