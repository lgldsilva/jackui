-- library.watched (issue #201): set by the progress upsert when the reported
-- position reaches 90% of the duration. Native clients read it to show
-- watched state; rewatching from the start clears it (the flag mirrors the
-- latest heartbeat, not a lifetime max).
ALTER TABLE library ADD COLUMN watched SMALLINT NOT NULL DEFAULT 0;
