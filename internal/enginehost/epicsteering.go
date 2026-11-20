package enginehost

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// steeringInstruction is the first line of a steering session's first prompt. It says what the session does for the
// epic and that it changes nothing in the repository.
const steeringInstruction = "orreryd builds this epic; this session steers it by answering its questions and events, " +
	"and changes nothing in the repository."

// steeringSessions reads the engine row of a session by id. *store.Store and the Manager's Store satisfy it.
type steeringSessions interface {
	EngineSession(ctx context.Context, id string) (store.EngineRow, error)
}

// EpicSteeringConfig configures EpicSteering. Manager starts and stops the steering sessions. Sessions reads the row
// of a session once it is started, so Start can tell a session whose spawn failed. Render returns the contract and log
// lines of an epic, which Start puts in the session's first prompt. PaneDir returns the current directory of a tmux
// pane, given the store column of its server and its id.
type EpicSteeringConfig struct {
	Manager  *Manager
	Sessions steeringSessions
	Render   func(store.EpicRow) ([]string, error)
	PaneDir  func(ctx context.Context, server, pane string) (string, error)
}

// EpicSteering starts and stops the steering session of an epic: the one headless session that answers the epic's
// questions and events and changes nothing in its repository.
type EpicSteering struct {
	manager  *Manager
	sessions steeringSessions
	render   func(store.EpicRow) ([]string, error)
	paneDir  func(ctx context.Context, server, pane string) (string, error)
}

// NewEpicSteering returns the EpicSteering that cfg configures.
func NewEpicSteering(cfg EpicSteeringConfig) *EpicSteering {
	return &EpicSteering{manager: cfg.Manager, sessions: cfg.Sessions, render: cfg.Render, paneDir: cfg.PaneDir}
}

// SteeringPrompt returns the first prompt of a steering session of the epic with id: the instruction line, the line
// "Epic: " followed by id, then lines, joined by newlines.
func SteeringPrompt(id string, lines []string) string {
	return strings.Join(append([]string{steeringInstruction, "Epic: " + id}, lines...), "\n")
}

// Start starts the steering session of the epic row in the row's repository and returns its id. The first prompt is
// SteeringPrompt of the row's id and the lines Render returns for the row. A Render error is returned and starts
// nothing. A start refused by the session cap returns an error that names the cap and wraps ErrCapReached. A session
// whose spawn fails returns an error that names the session and the reason it failed, and no id. A started session
// whose row cannot be read is stopped, even when ctx has ended, since no caller gets its id to stop it, and the read
// error is returned with no id.
func (s *EpicSteering) Start(ctx context.Context, row store.EpicRow) (string, error) {
	lines, err := s.render(row)
	if err != nil {
		return "", fmt.Errorf("render steering prompt of epic %s: %w", row.ID, err)
	}
	prompt := SteeringPrompt(row.ID, lines)
	id, err := s.manager.Start(ctx, StartRequest{Dir: row.Repository, Prompt: prompt})
	if errors.Is(err, ErrCapReached) {
		return "", fmt.Errorf("start steering session of epic %s: the session cap of %d is full: %w",
			row.ID, s.manager.limit, err)
	}
	if err != nil {
		return "", fmt.Errorf("start steering session of epic %s: %w", row.ID, err)
	}
	started, err := s.sessions.EngineSession(ctx, id)
	if err != nil {
		readErr := fmt.Errorf("read steering session %s: %w", id, err)
		if stopErr := s.manager.Stop(context.WithoutCancel(ctx), id); stopErr != nil {
			return "", errors.Join(readErr, fmt.Errorf("stop steering session %s: %w", id, stopErr))
		}
		return "", readErr
	}
	if started.State == stateFailed {
		return "", fmt.Errorf("steering session %s failed: %s", id, started.Reason)
	}
	return id, nil
}

// Stop stops the steering session id through the Manager, as Manager.Stop does.
func (s *EpicSteering) Stop(ctx context.Context, id string) error {
	return s.manager.Stop(ctx, id)
}

// PaneDir returns the current directory of the tmux pane on server, as the config's PaneDir reads it.
func (s *EpicSteering) PaneDir(ctx context.Context, server, pane string) (string, error) {
	return s.paneDir(ctx, server, pane)
}
