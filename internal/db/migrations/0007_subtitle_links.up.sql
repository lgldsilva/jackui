-- 0007: auto-fetched subtitle decisions (ptbr-subs-dual).
--
-- One row per streamable item, keyed by (info_hash, file_index), recording the
-- last automatic OpenSubtitles decision. The store in
-- internal/subtitles/links.go writes a `pending` row when an automatic search
-- starts and flips it to `ready`/`failed` when it resolves, so a second play
-- of the same item reads the persisted decision instead of re-searching (zero
-- extra OpenSubtitles calls) and the decision survives restarts. Failure
-- reasons (`no_results`, `quota`, `no_api_key`, `error`) let the UI show a
-- quiet hint instead of retrying forever.
--
-- info_hash is a plain text key, nothing torrent-specific: the hex infohash
-- for torrents, or the web's synthetic `local:...` hash for local files.
--
-- Conventions follow 0002 (IDENTITY PK, TIMESTAMPTZ DEFAULT now()); the three
-- flag columns are BOOLEAN per the lane contract — Go scans them directly as
-- bool and there are no legacy rows to migrate.

CREATE TABLE subtitle_links (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    info_hash        TEXT NOT NULL,
    file_index       INTEGER NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending',
    reason           TEXT NOT NULL DEFAULT '',
    file_id          TEXT NOT NULL DEFAULT '',
    language         TEXT NOT NULL DEFAULT '',
    languages        TEXT NOT NULL DEFAULT '',
    hearing_impaired BOOLEAN NOT NULL DEFAULT FALSE,
    forced           BOOLEAN NOT NULL DEFAULT FALSE,
    vtt_path         TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL DEFAULT '',
    hash_refined     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (info_hash, file_index)
);
