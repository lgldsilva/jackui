DROP INDEX IF EXISTS idx_dl_seed_stopped;
ALTER TABLE downloads DROP COLUMN IF EXISTS seed_stopped_at;
