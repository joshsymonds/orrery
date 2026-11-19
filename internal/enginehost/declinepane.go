package enginehost

import (
	"context"
	"errors"
	"fmt"
)

// ErrPaneInMode reports a pane Decline of a pane in a tmux mode, such as copy mode, which takes the Escape key in place
// of the pane. Nothing is sent to the pane.
var ErrPaneInMode = errors.New("pane is in a tmux mode")

// DeclinePane declines the open question on pane, which asks questionHash, by sending the pane its guarded Escape. Once
// the Escape is sent, it reports the pane seen at the manager's time, which ends the pane's pending notice. A refused
// call sends nothing and reports nothing. It returns an error wrapping ErrQuestionNotOpen when pane or questionHash is
// empty, when the pane is not waiting, or when it asks another question now. It returns an error wrapping ErrNoPane
// when tmux lists no pane with that id, and one wrapping ErrPutOffUnavailable when the manager has no Panes.
func (m *Manager) DeclinePane(ctx context.Context, pane, questionHash string) error {
	defer m.calls.enter()()
	if pane == "" {
		return fmt.Errorf("decline: pane id is empty: %w", ErrQuestionNotOpen)
	}
	if questionHash == "" {
		return fmt.Errorf("decline pane %s: question_hash is empty: %w", pane, ErrQuestionNotOpen)
	}
	if m.panes == nil {
		return fmt.Errorf("decline pane %s: %w", pane, ErrPutOffUnavailable)
	}
	question, err := m.panes.PaneQuestion(ctx, pane)
	if err != nil {
		return fmt.Errorf("decline: %w", err)
	}
	if !question.Waiting {
		return fmt.Errorf("pane %s has no open question: %w", pane, ErrQuestionNotOpen)
	}
	if question.Hash != questionHash {
		return fmt.Errorf("pane %s asks another question now: question hash mismatch: it is %s, not %s: %w",
			pane, question.Hash, questionHash, ErrQuestionNotOpen)
	}
	if err = m.panes.DeclinePane(ctx, pane, questionHash); err != nil {
		return fmt.Errorf("decline: %w", err)
	}
	m.panes.PaneSeen(ctx, pane, m.now())
	return nil
}
