package watchlist

import (
	"context"

	"github.com/lgldsilva/jackui/internal/jackett"
)

// ContextSearcher is the cancellable form of Searcher. It is optional so the
// small Searcher fakes keep working; jackett.Client implements it, which is
// what lets Stop abort a search that would otherwise run for minutes.
type ContextSearcher interface {
	SearchContext(ctx context.Context, query, category string, indexers []string) ([]jackett.Result, error)
}

func search(ctx context.Context, s Searcher, query, category string) ([]jackett.Result, error) {
	if cs, ok := s.(ContextSearcher); ok {
		return cs.SearchContext(ctx, query, category, nil)
	}
	return s.Search(query, category, nil)
}
