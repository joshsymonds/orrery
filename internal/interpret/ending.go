package interpret

import (
	"context"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// Ending ends an epic: it moves the epic to phase, its ending phase, and records body as the tell that announces the
// ending, at time at, and in the same write ends the node runs ends names. An ending to clean adds no tell, since the
// release that follows ends the epic released or done and that ending tells. Tells are `<epic>:tell:<seq>`. The
// conclude and release kinds call it once their node decides the epic's ending, and store.Store.EndEpic implements it.
// EndEpicWithGaps ends an epic in ended_with_gaps as EndEpic does, and in the same write opens question for the owner,
// as store.Store.EndEpicWithGaps does. A conclusion whose ending has gaps calls it.
type Ending interface {
	EndEpic(ctx context.Context, epic, phase, body string, ends []store.NodeEnd, at time.Time) error
	EndEpicWithGaps(
		ctx context.Context, epic, body string, question store.GapsQuestion, ends []store.NodeEnd, at time.Time,
	) error
}
