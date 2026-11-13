package enginehost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// watchLoopKind is the kind a monitor verdict is stored with.
const watchLoopKind = "monitor"

// errWatchLoopNoCaller reports a monitor call for a step run that has no Haiku caller.
var errWatchLoopNoCaller = errors.New("enginehost: step has no Haiku caller for its monitor")

// watchGateLimit ends an agent gate step overtime on the first tick at or past its limit of wall time from the run's
// start. It makes no monitor call, and it ends the step whether or not a turn runs. It reports whether the run goes on.
func (r *stepRun) watchGateLimit(ctx context.Context) bool {
	if r.agentGate == nil || r.now().Sub(r.started) < r.agentGate.Limit {
		return true
	}
	reason := fmt.Sprintf("agent gate ran past its limit of %d seconds", int64(r.agentGate.Limit/time.Second))
	r.finish(ctx, string(watchEndOvertime), "", reason)
	return false
}

// watchTick handles one tick of the run. When the watch has a trip due, it makes the monitor call, stores the verdict
// and carries out the action. It reports whether the run goes on.
func (r *stepRun) watchTick(ctx context.Context) bool {
	trip, due := r.watch.due(r.now())
	if !due {
		return true
	}
	verdict, doing, callErr := r.watchMonitor(ctx, trip)
	action := r.watch.apply(r.now(), trip, verdict)
	kind := action.Kind
	if kind == watchActNudge {
		kind = r.watchNudge(ctx, trip, doing)
	}
	stored := store.StepVerdict{
		Kind: watchLoopKind, Trip: string(trip.Kind), Verdict: verdict, Detail: doing, Action: string(kind),
	}
	if callErr != nil {
		stored.Error = callErr.Error()
	}
	r.logger.InfoContext(ctx, "step watch verdict", "step", r.id, "trip", string(trip.Kind), "verdict", verdict,
		"doing", doing, "action", string(kind), "err", stored.Error)
	if err := r.writer.Commit(ctx, r.id, store.StepWatch{ID: r.id, Verdict: stored, Doing: doing}); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("record verdict of step %s: %v", r.id, err))
		return false
	}
	if kind == watchActEnd {
		r.finish(ctx, string(action.End), "", watchLoopSummary(trip, doing))
		return false
	}
	return true
}

// watchMonitor makes the monitor call for trip and returns the verdict and doing line it gives. A run with no Haiku
// caller, a failed call and an unparsable reply each return an error and an empty verdict.
func (r *stepRun) watchMonitor(ctx context.Context, trip watchTrip) (verdict, doing string, err error) {
	if r.haiku == nil {
		return "", "", errWatchLoopNoCaller
	}
	raw, err := r.haiku.Call(ctx, stepMonitorSchema, stepMonitorInput(trip.Why, r.watch.recent(r.now())))
	if err != nil {
		return "", "", fmt.Errorf("monitor call: %w", err)
	}
	return parseStepMonitor(raw)
}

// watchNudge interrupts the step for trip with its doing line. A landed interrupt sets the text the next error turn end
// is continued with and returns the nudge action. A failed interrupt is logged and returns none, dropping the nudge, so
// the watch keeps its one nudge and keeps watching the running turn.
func (r *stepRun) watchNudge(ctx context.Context, trip watchTrip, doing string) watchActionKind {
	if err := r.sessions.Interrupt(ctx, r.id); err != nil {
		r.logger.ErrorContext(ctx, "interrupt step for nudge", "step", r.id, "trip", string(trip.Kind), "err", err)
		r.watch.dropNudge()
		return watchActNone
	}
	r.nudge = "The watchdog interrupted your turn. " + watchLoopSummary(trip, doing) +
		"\nContinue the step where it stopped, or return now if it is finished."
	return watchActNudge
}

// watchLoopSummary names trip and the doing line, the sentence a nudge or an end carries.
func watchLoopSummary(trip watchTrip, doing string) string {
	summary := string(trip.Kind) + " trip: " + trip.Why
	if doing != "" {
		summary += " Doing: " + doing
	}
	return summary
}
