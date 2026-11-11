package enginehost

import (
	"context"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// StepObserver receives what a step session hands it. Return receives a turn's end once no background task of the step
// is live; the step then counts busy until Continue sends its next prompt or the session stops. Exited receives the
// failure reason when the step's process ends on its own. Both run on the step's pump goroutine.
type StepObserver interface {
	Return(turn session.TurnEnded)
	Exited(reason string)
}

// StepSession is one step session: its id, its directory, the prompt it starts with, the claude agent it runs, the JSON
// schema its result must match, the engine ops written with its row, and the observer of its hand-offs. A nil Observer
// receives nothing.
type StepSession struct {
	ID       string
	Dir      string
	Prompt   string
	Agent    string
	Schema   string
	Ops      []store.EngineOp
	Observer StepObserver
}

// ErrBadAgent reports a StartStep whose agent is empty or is not 1 to 64 bytes of letters, digits and . _ - [ ] : that
// does not start with -.
var ErrBadAgent = errors.New("step agent must be 1 to 64 letters, digits or . _ - [ ] : and not start with -")

// StartStep starts the step session step as step describes. It checks step, takes a slot under StepCap, records the row
// as working with its Ops in one write, spawns claude with its agent and schema, then records and sends its prompt. A
// step is listed as a step, never as a session, and sends no notice. A spawn or send failure records the row failed and
// returns the error. A step's slot frees when its pump ends.
func (m *Manager) StartStep(ctx context.Context, step StepSession) error {
	if err := checkStep(step); err != nil {
		return err
	}
	if err := m.reserveStep(); err != nil {
		return err
	}
	add := store.EngineAdd{ID: step.ID, Dir: step.Dir, State: string(protocol.StateWorking), Kind: store.KindStep}
	if err := m.writer.Commit(ctx, step.ID, append([]store.EngineOp{add}, step.Ops...)...); err != nil {
		m.releaseStep()
		return fmt.Errorf("record step %s: %w", step.ID, err)
	}
	live, err := m.spawnLive(ctx, step.ID, step.Dir,
		spawnSpec{agent: step.Agent, schema: step.Schema, step: true, observer: step.Observer})
	if err != nil {
		m.releaseStep()
		recordErr := m.writeFailed(ctx, step.ID, err)
		return fmt.Errorf("start step %s: %w", step.ID, errors.Join(err, recordErr))
	}
	if err = m.writer.Put(ctx, step.ID, store.EnginePrompt{ID: step.ID, Text: step.Prompt}); err != nil {
		return m.abandonStep(ctx, live, fmt.Errorf("record prompt of step %s: %w", step.ID, err))
	}
	m.setTurning(live, true)
	if err = live.sess.Prompt(step.Prompt); err != nil {
		m.setTurning(live, false)
		return m.abandonStep(ctx, live, fmt.Errorf("send prompt of step %s: %w", step.ID, err))
	}
	return nil
}

// checkStep reports whether a step's id, directory, prompt and agent are usable, before anything is stored.
func checkStep(step StepSession) error {
	if err := checkID(step.ID); err != nil {
		return err
	}
	if err := checkDir(step.Dir); err != nil {
		return err
	}
	if step.Prompt == "" {
		return ErrEmptyPrompt
	}
	if step.Agent == "" || checkModel(step.Agent) != nil {
		return ErrBadAgent
	}
	return nil
}

// reserveStep takes one step slot under StepCap. It refuses when the manager is closed or the step cap is full.
func (m *Manager) reserveStep() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errManagerClosed
	}
	if m.stepsRunning >= m.stepLimit {
		return fmt.Errorf("%w: %d live steps, step cap %d", ErrCapReached, m.stepsRunning, m.stepLimit)
	}
	m.stepsRunning++
	return nil
}

// releaseStep frees a step slot taken by reserveStep for a step that never started running.
func (m *Manager) releaseStep() {
	m.mu.Lock()
	m.stepsRunning--
	m.mu.Unlock()
}

// abandonStep stops the step live, whose start failed with cause, and records it failed with cause and any stop error as
// its reason. It sends no notice. It returns cause with the errors the stop, the wait and the record hit. A step whose
// process exited already keeps the state its exit recorded.
func (m *Manager) abandonStep(ctx context.Context, live *liveSession, cause error) error {
	stopErr := live.sess.Stop()
	if err := m.waitPump(ctx, live); err != nil {
		return errors.Join(cause, stopErr, err)
	}
	if live.exited {
		return errors.Join(cause, stopErr)
	}
	recordErr := m.writeFailed(ctx, live.id, errors.Join(cause, stopErr))
	return errors.Join(cause, stopErr, recordErr)
}
