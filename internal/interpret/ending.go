package interpret

import (
	"context"
	"time"
)

// Ending ends an epic: it moves the epic to phase, its ending phase, and records body as the tell that announces the
// ending, at time at. An ending to clean adds no tell, since the release that follows ends the epic released or done
// and that ending tells. Tells are `<epic>:tell:<seq>`. The conclude and release kinds call it once their node decides
// the epic's ending, and store.Store.EndEpic implements it.
type Ending interface {
	EndEpic(ctx context.Context, epic, phase, body string, at time.Time) error
}
