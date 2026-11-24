// Package stage serves the Stage service: the contract stage's tools over a design's record, which orreryd answers on
// its private socket. Each RPC is implemented in its own file.
package stage

import (
	"context"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Config is what the Stage service reads: the store it keeps the stage's records in, the steps it starts, the sessions
// it starts to continue a design's conversation, the clock it stamps records with and the environment it reads.
type Config struct {
	Store    *store.Store
	Steps    StepStarter
	Sessions SessionStarter
	Now      func() time.Time
	Getenv   func(string) string
}

// StepStarter starts a step. *enginehost.Steps satisfies it.
type StepStarter interface {
	Start(ctx context.Context, start enginehost.StepStart) (string, error)
}

// SessionStarter starts a session that continues a design's conversation, and stops a session it started. The
// enginehost Manager satisfies it.
type SessionStarter interface {
	Start(ctx context.Context, req enginehost.StartRequest) (string, error)
	Stop(ctx context.Context, id string) error
}

// Service answers the Stage service. The RPCs it does not implement answer unimplemented.
type Service struct {
	enginev1connect.UnimplementedStageHandler
	cfg Config
}

// NewService returns the Stage service over cfg.
func NewService(cfg Config) *Service {
	return &Service{cfg: cfg}
}
