package enginehost

import (
	"context"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store"
)

// ErrAttachRefused reports a bad request to attach a capture to briefs. Attach wraps it with the reason.
var ErrAttachRefused = errors.New("evidence attach is refused")

// Attach attaches the capture with id capture to the briefs, the evidence runs by id, that it answers, for the caller
// session. It returns the runs it attached the capture to, as stored. A refused request matches ErrAttachRefused and
// attaches nothing.
func (e *Evidence) Attach(_ context.Context, _, _ string, _ []string) ([]store.EvidenceRun, error) {
	return nil, fmt.Errorf("attach evidence: %w", errors.ErrUnsupported)
}

var _ AttachCommands = (*Evidence)(nil)
