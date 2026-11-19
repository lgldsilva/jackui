package streamer

import "context"

// Names returns every favorited on-disk name (by any user) as a set, for
// callers that classify many cache entries at once.
func (f *FavoritesStore) Names(ctx context.Context) (map[string]bool, error) {
	names := make(map[string]bool)
	if f == nil {
		return names, nil
	}
	rows, err := f.db.QueryContext(ctx, "SELECT DISTINCT name FROM favorites")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names[name] = true
	}
	return names, rows.Err()
}
