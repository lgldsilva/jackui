-- Full-text search for the history `results` table, replacing SQLite FTS5.
--
-- unaccent() is only STABLE, so it can't be used directly in a GENERATED column
-- (Postgres requires the expression to be IMMUTABLE). Wrap it in an IMMUTABLE
-- function — the standard trick; the unaccent dictionary is effectively constant
-- in practice. Pinned to public so every search_path (incl. the per-test
-- schemas) resolves it.
-- Created only if missing (not CREATE OR REPLACE): the test harness runs this
-- migration concurrently across many schemas, and an unconditional replace
-- rewrites the shared catalog row in each → "tuple concurrently updated". A
-- guarded create is a no-op once it exists (the harness pre-creates it; in
-- production this migration runs once and creates it).
DO $$
BEGIN
    IF to_regprocedure('public.immutable_unaccent(text)') IS NULL THEN
        CREATE FUNCTION public.immutable_unaccent(text)
            RETURNS text
            LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS
        $fn$ SELECT public.unaccent('public.unaccent', $1) $fn$;
    END IF;
END $$;

-- Generated tsvector over the searchable columns. 'simple' (no language
-- stemming) + immutable_unaccent reproduces SQLite's unicode61 remove_diacritics
-- tokenizer. The STORED column replaces the FTS5 virtual table + its 3 sync
-- triggers — Postgres keeps it in sync automatically.
--
-- regexp_replace collapses every non-alphanumeric run to a space BEFORE
-- tokenizing: release names are dotted ("Breaking.Bad.1080p"), and Postgres'
-- default parser would otherwise keep the whole dotted string as one host/file
-- token (so "1080p" wouldn't match). SQLite's unicode61 split on those
-- separators — this restores that behaviour. regexp_replace is IMMUTABLE.
ALTER TABLE results ADD COLUMN fts tsvector GENERATED ALWAYS AS (
    to_tsvector('simple',
        public.immutable_unaccent(regexp_replace(coalesce(title, ''),   '[^[:alnum:]]+', ' ', 'g')) || ' ' ||
        public.immutable_unaccent(regexp_replace(coalesce(query, ''),   '[^[:alnum:]]+', ' ', 'g')) || ' ' ||
        public.immutable_unaccent(regexp_replace(coalesce(tracker, ''), '[^[:alnum:]]+', ' ', 'g')))
) STORED;

CREATE INDEX idx_results_fts ON results USING GIN (fts);
