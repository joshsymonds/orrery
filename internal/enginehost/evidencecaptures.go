package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// ErrCaptureRefused reports a bad request to capture evidence. Capture wraps it with the reason.
var ErrCaptureRefused = errors.New("evidence capture is refused")

// CaptureArgs is what a capture runs: the argv of its command, at the commit it runs at, under the project runner it
// names. Briefs are the ids of the evidence runs it answers, and Files are the repository files it keeps.
type CaptureArgs struct {
	Commit string
	Argv   []string
	Runner string
	Briefs []string
	Files  []string
}

// CaptureStore is the store a capture records captures in and reads its briefs with. *store.Store satisfies it.
type CaptureStore interface {
	AddCapture(ctx context.Context, capture store.Capture) error
	SetCaptureGrant(ctx context.Context, id, grant, holder string) error
	RecordCaptureEnd(ctx context.Context, id string, end store.EvidenceCapture) error
	MarkCaptureDelivered(ctx context.Context, id string) error
	Capture(ctx context.Context, id string) (store.Capture, error)
	RunningCaptures(ctx context.Context) ([]store.Capture, error)
	UndeliveredCaptures(ctx context.Context) ([]store.Capture, error)
	CapturesAt(ctx context.Context, repository, commit string) ([]store.Capture, error)
	AttachCapture(ctx context.Context, capture string, runs []string, at time.Time) ([]store.EvidenceRun, error)
}

// CaptureSteps reads the status of the step that asks for a capture. *Steps satisfies it.
type CaptureSteps interface {
	Status(ctx context.Context, id string) (store.StepRow, error)
}

// CapturesConfig configures Captures. Store records the captures, Steps reads the step that asks for one, and Deliver
// tells the briefs of an ended capture that it ended. Pools, Environ, GateStateDir and GateScope run each command as a
// capture does, and StateDir holds the capture worktrees. NewTimer arms a timer that sends on its channel once, after a
// duration, and returns the timer's stop; a nil NewTimer wraps time.NewTimer. A nil Now is time.Now, and a nil Logger
// discards.
type CapturesConfig struct {
	Store        CaptureStore
	Steps        CaptureSteps
	Deliver      func(context.Context, DeliverRequest) error
	Pools        PoolCommands
	Environ      []string
	StateDir     string
	GateStateDir string
	GateScope    string
	Now          func() time.Time
	Logger       *slog.Logger
	NewTimer     func(time.Duration) (<-chan time.Time, func() bool)
}

// Captures runs the commands that CaptureEvidence asks for, records each as a capture, and tells its briefs when it ends.
type Captures struct {
	cfg CapturesConfig
}

// NewCaptures returns a Captures for cfg.
func NewCaptures(cfg CapturesConfig) *Captures {
	return &Captures{cfg: cfg}
}

// Capture runs the command in args for the caller session, which names the step asking for it, and records it as a
// capture. A request that is refused matches ErrCaptureRefused and records nothing. It returns the capture as stored.
func (c *Captures) Capture(_ context.Context, _ string, _ CaptureArgs) (store.Capture, error) {
	return store.Capture{}, fmt.Errorf("capture evidence: %w", errors.ErrUnsupported)
}

// Resume takes up the captures the store holds as running after a restart, and the ended ones not yet delivered.
func (c *Captures) Resume(_ context.Context) error {
	return fmt.Errorf("resume captures: %w", errors.ErrUnsupported)
}

// Close ends the captures under way. It has none to end until Capture starts one.
func (c *Captures) Close() {}

var _ CaptureCommands = (*Captures)(nil)
