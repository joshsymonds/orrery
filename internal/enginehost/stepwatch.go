package enginehost

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/engine/session"
)

// The backstop clocks. Periodic, silent and overtime count model time or turn time as their comments say.
const (
	watchPeriod       = 5 * time.Minute  // turn time between periodic checks
	watchSilence      = 10 * time.Minute // model time without an event
	watchToolStuck    = 30 * time.Minute // wall time a main-conversation tool call may run
	watchToolExtended = 60 * time.Minute // wall time a call may run after its one extension
	watchRecentLen    = 40               // finished blocks and notices kept for the monitor
	watchCutBytes     = 300              // bytes each kept line keeps of its text
)

// watchTripKind names the backstop that tripped.
type watchTripKind string

// The backstops a watch trips.
const (
	watchTripPeriodic  watchTripKind = "periodic"
	watchTripSilent    watchTripKind = "silent"
	watchTripToolStuck watchTripKind = "tool_stuck"
	watchTripOvertime  watchTripKind = "overtime"
)

// watchActionKind names what follows a monitor verdict.
type watchActionKind string

// The actions a verdict can call for.
const (
	watchActNone   watchActionKind = "none"
	watchActNudge  watchActionKind = "nudge"
	watchActExtend watchActionKind = "extend"
	watchActEnd    watchActionKind = "end"
)

// watchEnd names why a step ends.
type watchEnd string

// The reasons a watch ends a step. A looping or wedged verdict ends the step with that verdict as its reason.
const (
	watchEndSilent    watchEnd = "silent"
	watchEndToolStuck watchEnd = "tool_stuck"
	watchEndOvertime  watchEnd = "overtime"
)

// The monitor verdicts the watch acts on. Any other verdict, and a failed call's empty verdict, leaves the
// mechanical action of the trip.
const (
	watchVerdictProgressing = "progressing"
	watchVerdictLongCommand = "long_command"
	watchVerdictLooping     = "looping"
	watchVerdictWedged      = "wedged"
)

// watchTrip is one backstop that tripped. Why is the sentence the monitor is told; Turn is the turn that
// was running at the trip; ToolUseID names the call a tool_stuck trip is about.
type watchTrip struct {
	Kind      watchTripKind
	Why       string
	Turn      int
	ToolUseID string
}

// watchAction is what follows a monitor verdict. End is set only when Kind is watchActEnd.
type watchAction struct {
	Kind watchActionKind
	End  watchEnd
}

// watchCall is a main-conversation tool call that is open, and whether it has had its one extension.
type watchCall struct {
	at       time.Time
	extended bool
}

// watchLine is one finished block or notice as recent renders it: when it finished and its cut text.
type watchLine struct {
	at   time.Time
	text string
}

// stepWatch decides when a step's backstops trip and what follows each monitor verdict. The clock is
// passed in, so the watch never reads time itself. It is safe for concurrent use.
type stepWatch struct {
	mu      sync.Mutex
	limit   time.Duration
	last    time.Time // the clock reading the counters below are current to
	turn    int       // the number of turns the step has started
	running bool      // a turn is running
	model   time.Duration
	silence time.Duration // model time since the last event
	check   time.Duration // turn time since the turn began or the last apply
	calls   map[string]*watchCall
	nudged  bool      // the step has been nudged, which spends its one nudge
	nudging bool      // a nudge has interrupted the running turn; no periodic check is due until the next turn begins
	nudgeAt time.Time // when the nudge interrupted the turn; while nudging, an open call's clock counts from it
	pending bool      // a trip was returned and its apply has not come yet
	lines   []watchLine
}

// newStepWatch returns the watch for a step that started at start and may use limit of model time.
func newStepWatch(start time.Time, limit time.Duration) *stepWatch {
	return &stepWatch{limit: limit, last: start, calls: map[string]*watchCall{}}
}

// advance credits the time since the last reading to the counters. Model time runs only while a turn runs
// with no main-conversation tool call open; turn time runs for the whole turn.
func (w *stepWatch) advance(at time.Time) {
	if !at.After(w.last) {
		return
	}
	elapsed := at.Sub(w.last)
	w.last = at
	if !w.running {
		return
	}
	w.check += elapsed
	if len(w.calls) == 0 {
		w.model += elapsed
		w.silence += elapsed
	}
}

// event reports one session event at a clock reading. Any event resets the silence count; a turn start
// also restarts the periodic count and clears the open calls.
func (w *stepWatch) event(at time.Time, ev session.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.advance(at)
	switch evt := ev.(type) {
	case session.Started:
		w.turn++
		w.running = true
		w.nudging = false
		w.check = 0
		clear(w.calls)
	case session.TurnEnded:
		w.running = false
		clear(w.calls)
	case session.Exited:
		w.running = false
		clear(w.calls)
	case session.ToolCallStarted:
		if evt.ParentToolUseID == "" {
			w.calls[evt.ToolUseID] = &watchCall{at: at}
		}
	case session.Block:
		if evt.Kind == session.BlockToolResult && evt.ParentToolUseID == "" {
			delete(w.calls, evt.ToolUseID)
		}
	}
	w.silence = 0
	if label, body, ok := watchRecord(ev); ok {
		w.remember(at, label, body)
	}
}

// watchRecord returns the label and body a finished block or notice shows in recent, and false for any
// other event.
func watchRecord(ev session.Event) (label, body string, ok bool) {
	switch evt := ev.(type) {
	case session.Block:
		switch evt.Kind {
		case session.BlockToolCall:
			return "call " + evt.Name, string(evt.Input), true
		case session.BlockToolResult:
			label = "result"
			if evt.IsError {
				label = "error result"
			}
			return label, string(evt.Content), true
		default:
			return string(evt.Kind), evt.Text, true
		}
	case session.Notice:
		return "notice " + string(evt.Kind), evt.Text, true
	}
	return "", "", false
}

// remember keeps a finished block or notice for recent, dropping the oldest past the window.
func (w *stepWatch) remember(at time.Time, label, body string) {
	w.lines = append(w.lines, watchLine{at: at, text: label + ": " + watchCut(body)})
	if len(w.lines) > watchRecentLen {
		w.lines = w.lines[len(w.lines)-watchRecentLen:]
	}
}

// watchCut returns the first watchCutBytes bytes of s, backed off to a rune boundary.
func watchCut(text string) string {
	if len(text) <= watchCutBytes {
		return text
	}
	n := watchCutBytes
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n]
}

// due returns the backstop that has tripped at a clock reading, the highest first: overtime, tool_stuck,
// silent, periodic. It returns false while a turn is not running or a trip awaits its apply. A nudge that interrupted
// the turn leaves the other backstops running and turns off the periodic one.
func (w *stepWatch) due(at time.Time) (watchTrip, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.advance(at)
	if w.pending || !w.running {
		return watchTrip{}, false
	}
	var trip watchTrip
	switch id, ok := w.stuckCall(at); {
	case w.model >= w.limit:
		trip = watchTrip{
			Kind: watchTripOvertime,
			Why:  fmt.Sprintf("The step has reached its model-time limit of %s.", w.limit),
		}
	case ok:
		trip = watchTrip{
			Kind: watchTripToolStuck,
			Why: fmt.Sprintf(
				"A tool call has run for %s; decide whether it is a long command or stuck.",
				at.Sub(w.calls[id].at).Round(time.Minute),
			),
			ToolUseID: id,
		}
	case w.silence >= watchSilence:
		trip = watchTrip{
			Kind: watchTripSilent,
			Why:  "The step has produced no event for ten minutes of model time; decide whether it is working or stuck.",
		}
	case w.check >= watchPeriod && !w.nudging:
		trip = watchTrip{
			Kind: watchTripPeriodic,
			Why:  "Five minutes of turn time have passed since the last check; decide whether the step is progressing, looping, or wedged.",
		}
	default:
		return watchTrip{}, false
	}
	trip.Turn = w.turn
	w.pending = true
	return trip, true
}

// stuckCall returns the open call that has run past its limit at a clock reading, the earliest-started
// first, and false when no call has. While a nudge has interrupted the turn, a call's limit counts from the nudge.
func (w *stepWatch) stuckCall(at time.Time) (string, bool) {
	best := ""
	found := false
	for id, call := range w.calls {
		limit := watchToolStuck
		if call.extended {
			limit = watchToolExtended
		}
		from := call.at
		if w.nudging && w.nudgeAt.After(from) {
			from = w.nudgeAt
		}
		if at.Sub(from) < limit {
			continue
		}
		if !found || call.at.Before(w.calls[best].at) {
			best, found = id, true
		}
	}
	return best, found
}

// apply returns the action for a monitor verdict on a trip. Every apply restarts the periodic count. A trip whose turn
// is no longer running, or whose tool call has closed, acts as none.
func (w *stepWatch) apply(at time.Time, trip watchTrip, verdict string) watchAction {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.advance(at)
	w.pending = false
	w.check = 0
	if !w.running || trip.Turn != w.turn {
		return watchAction{Kind: watchActNone}
	}
	call, open := w.calls[trip.ToolUseID]
	if trip.Kind == watchTripToolStuck && !open {
		return watchAction{Kind: watchActNone}
	}
	if trip.Kind == watchTripOvertime {
		return watchAction{Kind: watchActEnd, End: watchEndOvertime}
	}
	if verdict == watchVerdictLooping || verdict == watchVerdictWedged {
		if !w.nudged {
			return w.nudge(at)
		}
		return watchAction{Kind: watchActEnd, End: watchEnd(verdict)}
	}
	switch trip.Kind {
	case watchTripToolStuck:
		if verdict == watchVerdictLongCommand && !call.extended {
			call.extended = true
			return watchAction{Kind: watchActExtend}
		}
		return watchAction{Kind: watchActEnd, End: watchEndToolStuck}
	case watchTripSilent:
		if !w.nudged {
			return w.nudge(at)
		}
		return watchAction{Kind: watchActEnd, End: watchEndSilent}
	default:
		return watchAction{Kind: watchActNone}
	}
}

// nudge spends the step's one nudge, restarts the silence count and each open call's clock, and turns off the
// periodic check until the next turn begins.
func (w *stepWatch) nudge(at time.Time) watchAction {
	w.nudged = true
	w.nudging = true
	w.nudgeAt = at
	w.silence = 0
	return watchAction{Kind: watchActNudge}
}

// restore sets the watch's state for a re-adopted step: whether its one nudge is spent, whether a turn is running, and
// whether a nudge is interrupting that turn, which then counts from at, the clock reading of the restore.
func (w *stepWatch) restore(nudged, running, nudging bool, at time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nudged = nudged
	w.running = running
	w.nudging = nudging
	if nudging {
		w.nudgeAt = at
	}
}

// dropNudge undoes the nudge apply just returned when it could not be sent: the step keeps its one nudge and the watch
// trips again in the running turn.
func (w *stepWatch) dropNudge() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nudged = false
	w.nudging = false
}

// recent renders the kept blocks and notices, oldest first, one per line, each with its age at a clock
// reading.
func (w *stepWatch) recent(at time.Time) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out strings.Builder
	for i, line := range w.lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		age := max(at.Sub(line.at), 0)
		fmt.Fprintf(&out, "%s ago %s", age.Round(time.Second), line.text)
	}
	return out.String()
}
