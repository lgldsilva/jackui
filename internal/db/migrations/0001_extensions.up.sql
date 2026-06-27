-- Extensions the unified schema depends on. `unaccent` powers the full-text
-- search that replaces SQLite FTS5 (reproduces unicode61 remove_diacritics).
CREATE EXTENSION IF NOT EXISTS unaccent;
