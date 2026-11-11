package enginehost

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepSessions is what a step run drives: its session starts, takes a follow-up message, and stops. Manager satisfies
// it once Continue lands.
type stepSessions interface {
	StartStep(ctx context.Context, step StepSession) error
	Continue(ctx context.Context, id, text string) error
	Stop(ctx context.Context, id string) error
}

// The ends a step run stores.
const (
	stepLoopEndDone         = "done"
	stepLoopEndReturned     = "returned"
	stepLoopEndChecksFailed = "checks_failed"
	stepLoopEndFailed       = "failed"
)

// stepLoopSchemaName is the name of the check that validates a return against the step's schema.
const stepLoopSchemaName = "schema"

// stepLoopMaxContinues is how many failing returns the session is sent back to fix. The next failure ends checks_failed.
const stepLoopMaxContinues = 3

// stepLoopBuffer is the capacity of the returns and exits channels. A send waits only while the buffer is full.
const stepLoopBuffer = 8

// stepRun runs one step's loop. Each return is one round: it is schema-checked and stored with its checks, and a failing
// return is sent back to the session by Continue, up to stepLoopMaxContinues times. A passing return ends the step done
// when its status is doneValue and returned otherwise. Exited ends it failed.
type stepRun struct {
	id        string
	schema    string
	doneValue string
	sessions  stepSessions
	writer    *Writer
	logger    *slog.Logger
	cleanup   func(context.Context) error

	returns chan json.RawMessage
	exits   chan string
	// ended is closed once finish has acted.
	ended chan struct{}
	// runDone is closed when run returns, so Return and Exited never wait on a run that will not drain them.
	runDone chan struct{}
	once    sync.Once
}

// newStepRun returns a stepRun for step id that is not running. Call run to start it. A nil cleanup has nothing to run.
func newStepRun(id, schema, doneValue string, sessions stepSessions, writer *Writer, logger *slog.Logger,
	cleanup func(context.Context) error) *stepRun {
	return &stepRun{
		id: id, schema: schema, doneValue: doneValue, sessions: sessions, writer: writer, logger: logger,
		cleanup: cleanup, returns: make(chan json.RawMessage, stepLoopBuffer), exits: make(chan string, stepLoopBuffer),
		ended: make(chan struct{}), runDone: make(chan struct{}),
	}
}

// Return hands the structured return of the session's ended turn to run. It returns once run has queued it, the step has
// ended, or run has returned.
func (r *stepRun) Return(ended session.TurnEnded) {
	select {
	case r.returns <- ended.StructuredOutput:
	case <-r.ended:
	case <-r.runDone:
	}
}

// Exited hands the session's exit to run with its reason. It returns once run has queued it, the step has ended, or
// run has returned.
func (r *stepRun) Exited(reason string) {
	select {
	case r.exits <- reason:
	case <-r.ended:
	case <-r.runDone:
	}
}

// run handles returns and exits until the step ends or ctx ends. A ctx that ends stores no end.
func (r *stepRun) run(ctx context.Context) {
	defer close(r.runDone)
	continues := 0
	round := 0
	for {
		select {
		case <-ctx.Done():
			return
		case reason := <-r.exits:
			r.finish(ctx, stepLoopEndFailed, "", reason)
			return
		case value := <-r.returns:
			round++
			if !r.judge(ctx, round, value, &continues) {
				return
			}
		}
	}
}

// judge checks the return of round, stores it with its checks, and ends the step or sends the failed checks back. It
// reports whether the run goes on.
func (r *stepRun) judge(ctx context.Context, round int, value json.RawMessage, continues *int) bool {
	schemaErr := validateReturn(r.schema, value)
	check := store.StepCheck{Round: round, Name: stepLoopSchemaName, OK: schemaErr == nil}
	if schemaErr != nil {
		check.Detail = schemaErr.Error()
	}
	checks := []store.StepCheck{check}
	ret := store.StepReturn{ID: r.id, Result: string(value), Checks: checks}
	if err := r.writer.Commit(ctx, r.id, ret); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("record return of step %s: %v", r.id, err))
		return false
	}
	if schemaErr != nil {
		return r.retry(ctx, checks, continues)
	}
	var head struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(value, &head); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("read status of step %s: %v", r.id, err))
		return false
	}
	end := stepLoopEndReturned
	if head.Status == r.doneValue {
		end = stepLoopEndDone
	}
	r.finish(ctx, end, head.Status, "")
	return false
}

// retry sends the failed checks back to the session while it has continues left, and otherwise ends checks_failed. It
// reports whether the run goes on.
func (r *stepRun) retry(ctx context.Context, checks []store.StepCheck, continues *int) bool {
	failures := stepLoopFailures(checks)
	if *continues == stepLoopMaxContinues {
		r.finish(ctx, stepLoopEndChecksFailed, "", strings.Join(failures, "; "))
		return false
	}
	*continues++
	text := "Your return failed these checks. Return again with a result that passes them:\n- " +
		strings.Join(failures, "\n- ")
	if err := r.sessions.Continue(ctx, r.id, text); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("continue step %s: %v", r.id, err))
		return false
	}
	return true
}

// stepLoopFailures names each failed check with its detail.
func stepLoopFailures(checks []store.StepCheck) []string {
	var failures []string
	for _, check := range checks {
		if !check.OK {
			failures = append(failures, check.Name+": "+check.Detail)
		}
	}
	return failures
}

// finish ends the step once. It stores StepEnd, stops the session, runs cleanup and closes ended. A store, stop or cleanup
// error is logged, since nothing above finish can act on it. A ctx that has ended stores no end, so finish does nothing
// then. It reports whether this call acted.
func (r *stepRun) finish(ctx context.Context, end, status, reason string) bool {
	if ctx.Err() != nil {
		return false
	}
	acted := false
	r.once.Do(func() {
		acted = true
		defer close(r.ended)
		stepEnd := store.StepEnd{ID: r.id, End: end, Status: status, Reason: reason}
		if err := r.writer.Commit(ctx, r.id, stepEnd); err != nil {
			r.logger.ErrorContext(ctx, "record step end", "step", r.id, "err", err)
		}
		if err := r.sessions.Stop(ctx, r.id); err != nil {
			r.logger.ErrorContext(ctx, "stop step session", "step", r.id, "err", err)
		}
		if r.cleanup != nil {
			if err := r.cleanup(ctx); err != nil {
				r.logger.ErrorContext(ctx, "clean up step", "step", r.id, "err", err)
			}
		}
	})
	return acted
}
