package interpret

import (
	"context"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// Ending ends an epic: it moves the epic to phase, its ending phase, and records body as the tell that announces the
// ending, at time at, and in the same write ends the node runs ends names. Endings are tells, `<epic>:tell:<seq>`. The
// conclude and release kinds call it once their node decides the epic's ending, and store.Store.EndEpic implements it.
type Ending interface {
	EndEpic(ctx context.Context, epic, phase, body string, ends []store.NodeEnd, at time.Time) error
}
