package streamer

import (
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// Favorites recovery: re-link favorites whose magnet went missing (the inert-row
// bug — a card favorited while the .torrent→magnet conversion was failing got
// saved with an empty magnet, so Play/Download are blocked on FavoritesPage).
//
// Three layers, safest first. ReconcileMagnets does the two deterministic,
// network-free passes and is safe to run on every boot. RecoverViaSearch is the
// best-effort Jackett re-search for what's left (opt-in, bounded).

// ReconcileMagnets repairs magnet-less favorites without any network call:
//
//  1. info_hash present, magnet empty → synthesize a tracker-less magnet
//     (anacrolix finds peers via DHT — same shape the quick-favorite fallback uses).
//  2. both empty, but the metadata cache (same DB) has a row with the same name
//     → adopt that info_hash and synthesize the magnet. Only helps favorites whose
//     torrent was activated at least once (metadata is written post-activation).
//
// Returns the number of rows repaired. Idempotent.
func (f *FavoritesStore) ReconcileMagnets() (int, error) {
	if f == nil {
		return 0, nil
	}
	r1, err := f.db.Exec(`
		UPDATE favorites
		SET magnet = 'magnet:?xt=urn:btih:' || info_hash
		WHERE (magnet IS NULL OR magnet = '')
		  AND info_hash IS NOT NULL AND info_hash != ''`)
	if err != nil {
		return 0, err
	}
	n1, _ := r1.RowsAffected()

	r2, err := f.db.Exec(`
		UPDATE favorites AS f
		SET info_hash = m.info_hash,
		    magnet = 'magnet:?xt=urn:btih:' || m.info_hash
		FROM metadata AS m
		WHERE f.name = m.name
		  AND (f.magnet IS NULL OR f.magnet = '')
		  AND (f.info_hash IS NULL OR f.info_hash = '')
		  AND m.info_hash IS NOT NULL AND m.info_hash != ''`)
	if err != nil {
		return int(n1), err
	}
	n2, _ := r2.RowsAffected()
	return int(n1 + n2), nil
}

// MagnetMatch is the minimal slice of a search result the recovery needs to
// re-link a magnet-less favorite by re-searching its title.
type MagnetMatch struct {
	Title    string
	Magnet   string
	InfoHash string
	Seeders  int
}

// MagnetSearcher re-resolves a favorite by its stored name (its release title).
// The adapter in cmd/server wraps the Jackett client; tests inject a fake.
type MagnetSearcher interface {
	SearchByName(name string) ([]MagnetMatch, error)
}

// magnetlessFavorites lists the distinct favorite names still missing both a
// magnet and an info_hash after ReconcileMagnets — the camada-3 candidates.
func (f *FavoritesStore) magnetlessFavorites() ([]string, error) {
	rows, err := f.db.Query(`
		SELECT DISTINCT name FROM favorites
		WHERE (magnet IS NULL OR magnet = '')
		  AND (info_hash IS NULL OR info_hash = '')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && n != "" {
			names = append(names, n)
		}
	}
	return names, rows.Err()
}

// fillMagnet links a magnet/info_hash onto an existing favorite WITHOUT touching
// its user_id, folder, or timestamp — unlike Add (which upserts by name and would
// overwrite the owner). Only fills rows that are still magnet-less.
func (f *FavoritesStore) fillMagnet(name, infoHash, magnet string) error {
	_, err := f.db.Exec(`
		UPDATE favorites SET info_hash = ?, magnet = ?
		WHERE name = ? AND (magnet IS NULL OR magnet = '')`, infoHash, magnet, name)
	return err
}

// RecoverViaSearch re-searches each magnet-less favorite by name and links the
// best confident match (see bestMagnetMatch). Bounded by limit (re-links per
// boot) and conservative on purpose: a wrong match is worse than none. Returns
// the number repaired. A nil searcher or limit<=0 is a no-op.
func (f *FavoritesStore) RecoverViaSearch(searcher MagnetSearcher, limit int) (int, error) {
	if f == nil || searcher == nil || limit <= 0 {
		return 0, nil
	}
	names, err := f.magnetlessFavorites()
	if err != nil {
		return 0, err
	}
	fixed := 0
	for _, name := range names {
		if fixed >= limit {
			break
		}
		results, serr := searcher.SearchByName(name)
		if serr != nil {
			continue
		}
		match, ok := bestMagnetMatch(name, results)
		if !ok {
			continue
		}
		magnet, infoHash, ok := magnetAndHash(match)
		if !ok {
			continue
		}
		if err := f.fillMagnet(name, infoHash, magnet); err == nil {
			fixed++
		}
	}
	return fixed, nil
}

// bestMagnetMatch picks a confident result for a favorite name: prefer results
// whose normalized title equals the favorite's (highest seeders wins); else, if
// exactly one result carries a magnet/info_hash, take it. Otherwise no match —
// re-linking the wrong torrent is worse than leaving the favorite for the user.
func bestMagnetMatch(name string, results []MagnetMatch) (MagnetMatch, bool) {
	want := normalizeTitle(name)
	var exact []MagnetMatch
	var usable []MagnetMatch
	for _, r := range results {
		if r.Magnet == "" && r.InfoHash == "" {
			continue
		}
		usable = append(usable, r)
		if normalizeTitle(r.Title) == want {
			exact = append(exact, r)
		}
	}
	if len(exact) > 0 {
		return maxSeeders(exact), true
	}
	if len(usable) == 1 {
		return usable[0], true
	}
	return MagnetMatch{}, false
}

func maxSeeders(rs []MagnetMatch) MagnetMatch {
	best := rs[0]
	for _, r := range rs[1:] {
		if r.Seeders > best.Seeders {
			best = r
		}
	}
	return best
}

// magnetAndHash resolves a match to a (magnet, infoHash) pair, synthesizing a
// tracker-less magnet from a bare info_hash and back-filling the hash from the
// magnet when only the magnet is present. ok=false when neither is available.
func magnetAndHash(m MagnetMatch) (magnet, infoHash string, ok bool) {
	if m.Magnet != "" {
		infoHash = m.InfoHash
		if infoHash == "" {
			if mi, err := metainfo.ParseMagnetUri(m.Magnet); err == nil {
				infoHash = mi.InfoHash.HexString()
			}
		}
		return m.Magnet, infoHash, true
	}
	if m.InfoHash != "" {
		return "magnet:?xt=urn:btih:" + m.InfoHash, m.InfoHash, true
	}
	return "", "", false
}

// normalizeTitle lowercases and strips every non-alphanumeric rune so
// "O.Definitivo.Bau-Parte 01" and "O Definitivo Bau Parte 01" compare equal.
func normalizeTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
