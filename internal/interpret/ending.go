package interpret

import (
	"context"
	"time"
)

// Ending ends an epic: it moves the epic to phase, its ending phase, and records body as the tell that announces the
// ending, at time at. Endings are tells, `<epic>:tell:<seq>`. The conclude and release kinds call it once their node
// decides the epic's ending; O3e4 implements it. Reserved: filled by O3e4, and called by O3e2 (conclude) and O3e3
// (release).
type Ending interface {
	EndEpic(ctx context.Context, epic, phase, body string, at time.Time) error
}
