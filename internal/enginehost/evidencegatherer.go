package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// GathererStore is the store a gatherer records its steps in and takes its briefs with. *store.Store satisfies it.
type GathererStore interface {
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	OpenBriefs(ctx context.Context, epic string) ([]store.EvidenceRun, error)
	EpicsWithWaitingBriefs(ctx context.Context) ([]string, error)
	StartEvidenceGatherer(ctx context.Context, epic, step string, at time.Time) (store.EvidenceGatherer, error)
	TakeEvidenceBrief(ctx context.Context, run, step string) error
	EndEvidenceGatherer(
		ctx context.Context, epic, step, leftReason string, at time.Time,
	) ([]store.EvidenceRun, error)
	RunningEvidenceGatherer(ctx context.Context, epic string) (store.EvidenceGatherer, bool, error)
	RunningEvidenceGatherers(ctx context.Context) ([]store.EvidenceGatherer, error)
	EvidenceGathererOf(ctx context.Context, step string) (store.EvidenceGatherer, bool, error)
}

// GatherersConfig configures Gatherers. Store records the gatherers and takes their briefs, Steps starts the gatherer
// steps and subscribes to their ends, and Deliver sends a step its text. Getenv reads the environment a step runs under.
// A nil Now is time.Now, and a nil Logger discards. After returns the channel a wake waits on before it reads an epic
// again, and nil is time.After. NewTimer arms a timer that sends on its channel once, after a duration, and returns the
// timer's stop; a nil NewTimer wraps time.NewTimer.
type GatherersConfig struct {
	Store    GathererStore
	Steps    EvidenceSteps
	Deliver  func(context.Context, DeliverRequest) error
	Getenv   func(string) string
	Now      func() time.Time
	Logger   *slog.Logger
	After    func(time.Duration) <-chan time.Time
	NewTimer func(time.Duration) (<-chan time.Time, func() bool)
}

// Gatherers starts the gatherer step of an epic whose briefs wait, and attaches the captures the gatherer finds to them.
type Gatherers struct {
	cfg GatherersConfig
}

// NewGatherers returns a Gatherers for cfg.
func NewGatherers(cfg GatherersConfig) *Gatherers {
	return &Gatherers{cfg: cfg}
}

// Wake starts a gatherer for epic when the epic holds briefs that wait and no gatherer is running for it.
func (g *Gatherers) Wake(_ context.Context, _ string) {}

// Resume takes up the gatherers the store holds as running after a restart, each under its step.
func (g *Gatherers) Resume(_ context.Context) error {
	return fmt.Errorf("resume gatherers: %w", errors.ErrUnsupported)
}

// Close ends the gatherers under way. It has none to end until Wake starts one.
func (g *Gatherers) Close() {}

var _ EvidenceGatherers = (*Gatherers)(nil)
