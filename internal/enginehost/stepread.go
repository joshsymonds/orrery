package enginehost

import (
	"context"

	"github.com/joshsymonds/orrery/internal/store"
)

// MarkRead marks the ended step id read by caller, so the step's end is sent to caller as no caller event. The store
// refuses a step that has not ended and a caller that is not the step's own. A failed mark is logged and not returned:
// a missed mark only costs the caller one extra event.
func (s *Steps) MarkRead(ctx context.Context, id string, caller store.EpicCaller) {
	if _, err := s.store.MarkStepRead(ctx, id, caller); err != nil {
		s.logger.ErrorContext(ctx, "mark step read", "step", id, "err", err)
	}
}
