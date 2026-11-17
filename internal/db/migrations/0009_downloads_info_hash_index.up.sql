-- downloads had no index leading with info_hash: UNIQUE(user_id, info_hash,
-- file_index) leads with user_id, so the any-owner lookups by hash (completed
-- path resolution for streaming and relocated seeding, per-torrent row scans)
-- fell back to idx_dl_status or a sequential scan.
CREATE INDEX IF NOT EXISTS idx_dl_hash_file ON downloads(info_hash, file_index);
