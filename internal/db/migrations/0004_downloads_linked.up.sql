-- Cross-torrent dedup (#23): mark a completed download row that points at a
-- PRE-EXISTING file (adopted instead of re-downloaded) rather than at bytes this
-- row fetched itself. SMALLINT 0/1 flag per the schema convention (keeps Go int
-- semantics; avoids the BOOLEAN `WHERE x = 0` trap).
ALTER TABLE downloads ADD COLUMN IF NOT EXISTS linked SMALLINT NOT NULL DEFAULT 0;
