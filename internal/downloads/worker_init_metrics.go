package downloads

import (
	"context"
	"errors"

	"github.com/lgldsilva/jackui/internal/metrics/events"
)

// initOutcome is what an init failure did to a row, ordered by severity so a
// group can keep the worst member outcome with max().
type initOutcome int

const (
	initOutcomeNone   initOutcome = iota // row no longer pending (cancelled/removed)
	initOutcomeRetry                     // transient: next tick retries
	initOutcomeFailed                    // retries exhausted: row marked failed
)

// recordInitOutcome exports one init-failure event. A cancelled ctx is a
// shutdown (Worker.Stop) or a user removal, not an init failure, so it is
// never counted.
func recordInitOutcome(ctx context.Context, o initOutcome) {
	if o == initOutcomeNone || errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	events.DownloadInitFailed(o == initOutcomeFailed)
}
