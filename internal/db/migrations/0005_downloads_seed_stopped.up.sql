-- Seed-stop persistence (#TBD): when a user explicitly stops seeding a completed
-- download (via the "stop seed" / "remove torrent" controls), we mark the row
-- so autoSeedCompleted does NOT bring it back on the next boot. The torrent
-- files stay on disk; the row simply stops being auto-reactivated.
ALTER TABLE downloads ADD COLUMN IF NOT EXISTS seed_stopped_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_dl_seed_stopped ON downloads(seed_stopped_at);
