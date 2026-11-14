package enginehost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// ErrStepNoTurn reports a hold of a step whose session has no turn running.
var ErrStepNoTurn = errors.New("step has no turn running")

// ErrStepHeld reports a hold of a step that is already held.
var ErrStepHeld = errors.New("step is held")

// ErrStepNotRunning reports an owner action on a step that has ended.
var ErrStepNotRunning = errors.New("step is not running")

// ErrStepGating reports an owner action on a step that is running its gate.
var ErrStepGating = errors.New("step is running its gate")

// ErrStepNotLimited reports a resume of a step that no usage limit holds.
var ErrStepNotLimited = errors.New("step is not limited")

// stepResumePrompt is the owner's prompt a resume sends a limited step once its usage limit has reset.
const stepResumePrompt = "The usage limit has reset. Carry on with your task from where you stopped."

// stepOwnerCall is an owner action handed to a run's loop. The loop runs act in its goroutine and sends act's error on
// reply, which is buffered so the loop never waits on the caller.
type stepOwnerCall struct {
	act   func(ctx context.Context) error
	reply chan error
}

// do runs act in the run's loop goroutine and returns its error. It returns ErrStepGating when the run is running its
// gate, or when a gate begins while do waits for the loop, since the loop takes no call during a gate. It returns
// ErrStepNotRunning once the run has ended, and ctx's error when ctx ends before the loop takes the action. Once the
// loop has taken it, do waits for the reply, which the loop always sends.
func (r *stepRun) do(ctx context.Context, act func(ctx context.Context) error) error {
	call := stepOwnerCall{act: act, reply: make(chan error, 1)}
	gateStart, gating := r.gateState()
	if gating {
		return ErrStepGating
	}
	select {
	case r.calls <- call:
	case <-gateStart:
		return ErrStepGating
	case <-r.ended:
		return ErrStepNotRunning
	case <-r.runDone:
		return ErrStepNotRunning
	case <-ctx.Done():
		return fmt.Errorf("owner action on step %s: %w", r.id, ctx.Err())
	}
	return <-call.reply
}

// hold interrupts the running turn and marks the run held. It runs in the loop goroutine. A held run refuses first, then
// a run with no turn running. An interrupt error is returned with the run unheld. A landed hold drops the nudges pending
// for the turn, the watchdog's and the owner's, so the held turn's end continues nothing, and stores the loop state.
func (r *stepRun) hold(ctx context.Context) error {
	if r.held.Load() {
		return ErrStepHeld
	}
	if !r.watch.turnRunning() {
		return ErrStepNoTurn
	}
	if err := r.sessions.Interrupt(ctx, r.id); err != nil {
		return fmt.Errorf("interrupt step %s: %w", r.id, err)
	}
	r.nudge = ""
	r.ownerNudge = ""
	r.held.Store(true)
	r.saveLoop(ctx)
	return nil
}

// nudgeOwner sends text to the run as the owner's message. It runs in the loop goroutine. A held run resumes with text:
// at once when its held turn has already ended, and at that turn end otherwise, where the end is no round. A running turn
// is interrupted and its end is continued with text, in place of a round. A run with no turn running and not held
// refuses with ErrStepNoTurn. Text sent while a nudge is already pending joins it. An interrupt error is returned and
// leaves nothing pending. A failed continue ends the step failed and is returned. Each landed change stores the loop
// state: a pending text when it is set, and the held state when a held run resumes.
func (r *stepRun) nudgeOwner(ctx context.Context, text string) error {
	if r.held.Load() {
		if !r.heldEnded {
			r.ownerNudge = joinNudges(r.ownerNudge, text)
			r.saveLoop(ctx)
			return nil
		}
		r.held.Store(false)
		r.heldEnded = false
		r.saveLoop(ctx)
		return r.continueStep(ctx, StepPrompt{Text: text, Sender: store.PromptSenderOwner})
	}
	if !r.watch.turnRunning() {
		return ErrStepNoTurn
	}
	if r.ownerNudge != "" {
		r.ownerNudge = joinNudges(r.ownerNudge, text)
		r.saveLoop(ctx)
		return nil
	}
	if err := r.sessions.Interrupt(ctx, r.id); err != nil {
		return fmt.Errorf("interrupt step %s: %w", r.id, err)
	}
	r.ownerNudge = text
	r.saveLoop(ctx)
	return nil
}

// resumeLimited sends the owner's resume prompt to a limited run, which respawns its released process in its session. It
// runs in the loop goroutine. A run that no limit holds refuses with ErrStepNotLimited. A Continue the step cap refuses
// leaves the run limited, and any other failed Continue ends the step failed. A sent resume clears the limit, and stores
// the loop state with the step's stored rate limit deleted.
func (r *stepRun) resumeLimited(ctx context.Context) error {
	if !r.limited {
		return ErrStepNotLimited
	}
	prompt := StepPrompt{Text: stepResumePrompt, Sender: store.PromptSenderOwner}
	if err := r.sessions.Continue(ctx, r.id, prompt); err != nil {
		err = fmt.Errorf("continue step %s: %w", r.id, err)
		if !errors.Is(err, ErrCapReached) {
			r.finish(ctx, stepLoopEndFailed, "", err.Error())
		}
		return err
	}
	r.limited = false
	r.limitKind, r.limitResets = "", time.Time{}
	// The limit's stored rate limit is cleared with the loop state, before the resumed turn's end is handled, so that
	// turn end does not read the old limit as its own.
	if err := r.writer.Commit(ctx, r.id, store.EngineRateLimitCleared{ID: r.id}, r.loopState()); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("record resume of step %s: %v", r.id, err))
		return err
	}
	return nil
}

// turnRunning reports whether a turn is running as the watch has seen its events.
func (w *stepWatch) turnRunning() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running
}

// Hold interrupts the running turn of the step with id and holds the step: it stays held until it is stopped or nudged,
// and the end of the held turn is no round. The hold runs in the step's loop. Hold returns ErrStepNoTurn when no turn is
// running, ErrStepHeld when the step is already held, ErrStepGating when the step is running its gate, and
// ErrStepNotRunning when the step has ended or has no loop. An id with no row returns an error matching store.ErrNoStep.
// An interrupt error is returned and leaves the step unheld.
func (s *Steps) Hold(ctx context.Context, id string) error {
	s.mu.Lock()
	run := s.runs[id]
	s.mu.Unlock()
	if run == nil {
		if _, err := s.Status(ctx, id); err != nil {
			return err
		}
		return ErrStepNotRunning
	}
	return run.do(ctx, run.hold)
}

// Held reports whether the step with id is held. A step with no loop is not held.
func (s *Steps) Held(id string) bool {
	s.mu.Lock()
	run := s.runs[id]
	s.mu.Unlock()
	return run != nil && run.held.Load()
}

// Nudge sends text to the step with id as the owner's message. A held step resumes with it. A running turn is
// interrupted and its end is continued with text in place of a round, and it is refused with ErrStepNoTurn when no turn
// is running. A step running its gate refuses with ErrStepGating at once. Nudge does not spend the watchdog's one nudge
// or replace its pending text; a pending watchdog text is continued ahead of the owner's. Nudge returns ErrEmptyPrompt for
// empty text, an error matching store.ErrNoStep for an id with no row, and ErrStepNotRunning when the step has ended or
// has no loop. An interrupt or continue error is returned.
func (s *Steps) Nudge(ctx context.Context, id, text string) error {
	if text == "" {
		return ErrEmptyPrompt
	}
	s.mu.Lock()
	run := s.runs[id]
	s.mu.Unlock()
	if run == nil {
		if _, err := s.Status(ctx, id); err != nil {
			return err
		}
		return ErrStepNotRunning
	}
	return run.do(ctx, func(ctx context.Context) error {
		return run.nudgeOwner(ctx, text)
	})
}

// Resume resumes the limited step with id: it sends the owner's prompt that the usage limit has reset, which respawns the
// step's released process in its session with --resume. The resume runs in the step's loop, as Hold does. Resume returns
// ErrStepNotLimited for a step that no limit holds, ErrStepNotRunning when the step has ended or has no loop, and
// ErrStepGating when the step is running its gate. An id with no row returns an error matching store.ErrNoStep. A
// Continue refused by the step cap returns an error matching ErrCapReached and leaves the step limited. Any other
// Continue error ends the step failed and is returned.
func (s *Steps) Resume(ctx context.Context, id string) error {
	s.mu.Lock()
	run := s.runs[id]
	s.mu.Unlock()
	if run == nil {
		if _, err := s.Status(ctx, id); err != nil {
			return err
		}
		return ErrStepNotRunning
	}
	return run.do(ctx, run.resumeLimited)
}
