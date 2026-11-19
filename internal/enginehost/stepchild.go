package enginehost

import (
	"context"

	"github.com/joshsymonds/orrery/internal/store"
)

// DeliverChildEnd delivers the end of a step that the step req.ID started to that step: req.Text, the step_end block, is
// sent to its session as a prompt stored with the event req.PromptID, so the step's turn answers it. The prompt goes out
// whether or not the step's turn is running. A turn that is running takes it as a following prompt, so the turn does
// not end before it is answered (see Manager.Continue). A prompt whose PromptID the session already carries is sent
// once. A step returns ErrStepGating while its loop runs its gate, and ErrSessionBusy while it is held or limited, or while
// its row is open with no loop: the caller holds the end then, and delivers it once the step is woken (see
// stepRunConfig.wake). A step that has ended returns ErrStepNotRunning. An id with no row returns an error matching
// store.ErrNoStep. An error of the continue of the prompt is returned wrapped, and ends the step failed.
func (s *Steps) DeliverChildEnd(ctx context.Context, req DeliverRequest) error {
	defer s.calls.enter()()
	s.mu.Lock()
	run := s.runs[req.ID]
	s.mu.Unlock()
	if run == nil {
		row, err := s.Status(ctx, req.ID)
		if err != nil {
			return err
		}
		if row.End != "" {
			return ErrStepNotRunning
		}
		return ErrSessionBusy
	}
	return run.do(ctx, func(ctx context.Context) error {
		if run.limited || run.held.Load() {
			return ErrSessionBusy
		}
		return run.continueStep(ctx, StepPrompt{
			Text: req.Text, Sender: store.PromptSenderEvent, PromptID: req.PromptID,
		})
	})
}
