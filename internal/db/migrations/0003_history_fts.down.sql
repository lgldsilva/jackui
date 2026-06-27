DROP INDEX IF EXISTS idx_results_fts;
ALTER TABLE results DROP COLUMN IF EXISTS fts;
DROP FUNCTION IF EXISTS public.immutable_unaccent(text);
