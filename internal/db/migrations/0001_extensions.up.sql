-- Extensions the unified schema depends on. `unaccent` powers the full-text
-- search that replaces SQLite FTS5 (reproduces unicode61 remove_diacritics).
-- Pinned to public: an extension's functions live in one schema, so with the
-- per-test schema isolation in dbtest (and IF NOT EXISTS being database-global)
-- we keep it where every search_path can reach it. Production runs in public.
CREATE EXTENSION IF NOT EXISTS unaccent WITH SCHEMA public;
