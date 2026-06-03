package tmdb

import (
	"fmt"
	"time"
)

// snapshotHistoryWeeks is how many weekly snapshots to retain. We only ever
// compare against the immediately-previous week, so a small window is plenty.
const snapshotHistoryWeeks = 4

// weekKey is the ISO year-week label used to bucket a trending snapshot.
func weekKey(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, w)
}

// applyTrendingDirection tags each item with its movement vs last week's ranking
// (the slice order is the current rank), then persists this week's snapshot.
func (c *Client) applyTrendingDirection(items []Match) {
	cur := weekKey(time.Now())
	prev := c.prevWeekRanks(cur)
	for i := range items {
		setDirection(&items[i], i, prev)
	}
	c.saveSnapshot(cur, items)
}

func setDirection(m *Match, rank int, prevRanks map[int]int) {
	prev, ok := prevRanks[m.TmdbID]
	switch {
	case !ok:
		m.Direction = "new"
	case rank < prev:
		m.Direction, m.RankDelta = "up", prev-rank
	case rank > prev:
		m.Direction, m.RankDelta = "down", rank-prev
	default:
		m.Direction = "same"
	}
}

// prevWeekRanks returns tmdb_id→rank for the most recent snapshot week strictly
// before curWeek. Empty (→ all "new") when there's no prior week yet.
func (c *Client) prevWeekRanks(curWeek string) map[int]int {
	ranks := map[int]int{}
	var prevWeek string
	if err := c.cache.QueryRow(
		`SELECT week_key FROM trending_snapshot WHERE week_key < ? ORDER BY week_key DESC LIMIT 1`,
		curWeek).Scan(&prevWeek); err != nil || prevWeek == "" {
		return ranks
	}
	rows, err := c.cache.Query(`SELECT tmdb_id, rank FROM trending_snapshot WHERE week_key=?`, prevWeek)
	if err != nil {
		return ranks
	}
	defer rows.Close()
	for rows.Next() {
		var id, rank int
		if rows.Scan(&id, &rank) == nil {
			ranks[id] = rank
		}
	}
	return ranks
}

// saveSnapshot records this week's ranking (rank = slice index). Re-runs within
// the same week overwrite (the list shifts across the 6h refresh window), then
// old weeks beyond snapshotHistoryWeeks are pruned.
func (c *Client) saveSnapshot(week string, items []Match) {
	if len(items) == 0 {
		return
	}
	tx, err := c.cache.Begin()
	if err != nil {
		return
	}
	if _, err := tx.Exec(`DELETE FROM trending_snapshot WHERE week_key=?`, week); err != nil {
		_ = tx.Rollback()
		return
	}
	for i, m := range items {
		if _, err := tx.Exec(`INSERT INTO trending_snapshot(week_key, tmdb_id, rank) VALUES(?, ?, ?)`, week, m.TmdbID, i); err != nil {
			_ = tx.Rollback()
			return
		}
	}
	if err := tx.Commit(); err != nil {
		return
	}
	_, _ = c.cache.Exec(`DELETE FROM trending_snapshot WHERE week_key NOT IN (
		SELECT DISTINCT week_key FROM trending_snapshot ORDER BY week_key DESC LIMIT ?)`, snapshotHistoryWeeks)
}
