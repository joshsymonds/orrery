package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// StepObserver receives what a step session hands it. Event receives each event the session records, before that
// event's hand-off. Done reports whether a turn's structured return is the step's done value. Return receives a turn's
// end when no background task of the step that runs a model is live (see awaitsTask), whatever shell tasks are, or when
// Done reports that turn's return as done; the step then counts busy until Continue sends its next prompt or the
// session stops. Exited receives the failure reason when the step's process ends on its own. Event, Return and Exited
// run on the step's pump goroutine and must not block. Done runs under the manager's lock, so it must not block or take
// a lock of the observer.
type StepObserver interface {
	Event(event session.Event)
	Done(turn session.TurnEnded) bool
	Return(turn session.TurnEnded)
	Exited(reason string)
}

// StepSession is one step session: its id, its directory, the prompt it starts with, the claude agent it runs, the JSON
// object of inline agent definitions claude is started with, the JSON schema its result must match, the engine ops
// written with its row, and the observer of its hand-offs. A nil Observer receives nothing.
type StepSession struct {
	ID       string
	Dir      string
	Prompt   string
	Agent    string
	Agents   string
	Schema   string
	Ops      []store.EngineOp
	Observer StepObserver
}

// ErrBadAgent reports a StartStep whose agent is empty or is not 1 to 64 bytes of letters, digits and . _ - [ ] : that
// does not start with -.
var ErrBadAgent = errors.New("step agent must be 1 to 64 letters, digits or . _ - [ ] : and not start with -")

// ErrBadAgents reports a StartStep whose Agents is not a JSON object with an object as each member's definition.
var ErrBadAgents = errors.New("step agents must be a JSON object of agent definitions")

// stepRefusedError is a StartStep error for a step refused before its row was written. Steps.Start returns its error
// without ending the step. Unwrap keeps the error matching its sentinel.
type stepRefusedError struct{ err error }

func (r stepRefusedError) Error() string { return r.err.Error() }

func (r stepRefusedError) Unwrap() error { return r.err }

// StartStep starts the step session step as step describes. It checks step, that claude would resolve its agent and
// that the manager is open, records the row as working with its Ops in one write, spawns claude with its agent, inline
// agents and schema, then records and sends its prompt. A step always runs on the controller, so its row stores the
// controller's host name. A step is listed as a step, never as a session, and sends no notice. A step refused before
// anything is stored, for a bad step, an agent that does not resolve or a closed manager, returns its error and stores
// nothing. A failure after the row is written, a record, spawn or send failure, records the row failed and returns the
// error.
func (m *Manager) StartStep(ctx context.Context, step StepSession) error {
	defer m.calls.enter()()
	if err := checkStep(step); err != nil {
		return stepRefusedError{err}
	}
	if err := m.resolveStepAgent(step); err != nil {
		return stepRefusedError{err}
	}
	if err := m.checkOpen(); err != nil {
		return stepRefusedError{err}
	}
	add := store.EngineAdd{
		ID: step.ID, Dir: step.Dir, State: string(protocol.StateWorking), Kind: store.KindStep, Host: m.hostName,
	}
	if err := m.writer.Commit(ctx, step.ID, append([]store.EngineOp{add}, step.Ops...)...); err != nil {
		return fmt.Errorf("record step %s: %w", step.ID, err)
	}
	live, err := m.spawnLive(ctx, step.ID, step.Dir, spawnSpec{agent: step.Agent, agents: step.Agents,
		schema: step.Schema, step: true, observer: step.Observer, host: m.local})
	if err != nil {
		recordErr := m.writeFailed(ctx, step.ID, err)
		return fmt.Errorf("start step %s: %w", step.ID, errors.Join(err, recordErr))
	}
	m.mu.Lock()
	m.stepObservers[step.ID] = step.Observer
	m.mu.Unlock()
	writeID, err := newWriteID()
	if err != nil {
		return m.abandonStep(ctx, live, fmt.Errorf("prepare prompt of step %s: %w", step.ID, err))
	}
	prompt := store.EnginePrompt{ID: step.ID, Text: step.Prompt, Sender: store.PromptSenderBrief, WriteID: writeID}
	live.sess.ExpectPrompt(writeID)
	if err = m.writer.Commit(ctx, step.ID, prompt); err != nil {
		live.sess.DropPrompt(writeID)
		return m.abandonStep(ctx, live, fmt.Errorf("record prompt of step %s: %w", step.ID, err))
	}
	m.setTurning(live, true)
	if err = live.sess.Prompt(writeID, step.Prompt); err != nil {
		m.setTurning(live, false)
		return m.abandonStep(ctx, live, fmt.Errorf("send prompt of step %s: %w", step.ID, err))
	}
	return nil
}

// Release stops the live session id's process without ending it, as checkIdle stops an idle session: it records nothing,
// its observer is told of no exit, and its row stays as it was. Release waits for the session's pump to end. A later
// Continue respawns the step in its claude session with --resume. A drained step with no live session leaves the drained
// set (see retireLocked). It returns nil for an id with no live session, or a session Close has begun closing.
func (m *Manager) Release(ctx context.Context, id string) error {
	defer m.calls.enter()()
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	m.mu.Lock()
	live := m.sessions[id]
	if live == nil {
		m.dropKeptLocked(id)
	}
	if live == nil || live.closing {
		m.mu.Unlock()
		return nil
	}
	live.idleStopped = true
	delete(m.sessions, id)
	m.mu.Unlock()
	stopErr := live.sess.Stop()
	waitErr := m.waitPump(ctx, live)
	if err := errors.Join(stopErr, waitErr); err != nil {
		return fmt.Errorf("release session %s: %w", id, err)
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

// resolveStepAgent returns an error unless claude would resolve the agent of step for its directory under the manager's
// environment. An agent that step's Agents defines resolves, except web-fetch, which claude never runs as a step's
// agent. Any other agent is resolved as claude reads agents under the HOME and CLAUDE_CONFIG_DIR of that environment and
// under the managed root. An unset CLAUDE_CONFIG_DIR is "" and takes the default, and an unset HOME is "", which Resolve
// refuses. The error names the agent and each place checked, and an Agents that is not a JSON object of definitions is
// refused with an error matching ErrBadAgents.
func (m *Manager) resolveStepAgent(step StepSession) error {
	defined, err := inlineAgents(step.Agents)
	if err != nil {
		return fmt.Errorf("resolve agent of step %s: %w", step.ID, err)
	}
	if _, ok := defined[step.Agent]; ok && step.Agent != "web-fetch" {
		return nil
	}
	home, _ := environValue(m.environ, "HOME")
	configDir, _ := environValue(m.environ, "CLAUDE_CONFIG_DIR")
	where := agents.Where{Dir: step.Dir, Home: home, ConfigDir: configDir, Managed: m.managedDir}
	if err = agents.Resolve(step.Agent, where); err != nil {
		return fmt.Errorf("resolve agent of step %s: %w", step.ID, err)
	}
	return nil
}

// inlineAgents returns the agent definitions of agents, the JSON object a step's Agents holds, keyed by agent name. Empty
// agents defines none. It returns an error matching ErrBadAgents unless agents is a JSON object whose every member is a
// JSON object.
func inlineAgents(agents string) (map[string]json.RawMessage, error) {
	if agents == "" {
		return map[string]json.RawMessage{}, nil
	}
	var defs map[string]json.RawMessage
	if err := json.Unmarshal([]byte(agents), &defs); err != nil || defs == nil {
		return nil, fmt.Errorf("%w: the value is not a JSON object", ErrBadAgents)
	}
	for name, def := range defs {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(def, &fields); err != nil || fields == nil {
			return nil, fmt.Errorf("%w: agent %q is not a JSON object", ErrBadAgents, name)
		}
	}
	return defs, nil
}

// claimRespawn takes the step id out of the drained set for its respawn (see retireLocked). It refuses when the manager is
// closed, and then leaves id where it was.
func (m *Manager) claimRespawn(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errManagerClosed
	}
	delete(m.kept, id)
	return nil
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
