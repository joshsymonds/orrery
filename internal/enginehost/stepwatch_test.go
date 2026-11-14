package enginehost

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
)

// watchEpoch is the clock reading at which every watch in these tests starts.
func watchEpoch() time.Time { return time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC) }

// watchToolID is the id of the main-conversation tool call these tests open and close.
const watchToolID = "a"

// watchAt is the clock reading d after watchEpoch.
func watchAt(d time.Duration) time.Time { return watchEpoch().Add(d) }

// watchNewAt returns a watch for a step that started at watchEpoch with the given model-time limit.
func watchNewAt(limit time.Duration) *stepWatch { return newStepWatch(watchEpoch(), limit) }

// watchWantDue asserts that a trip of kind want is due at d and returns it.
func watchWantDue(t *testing.T, w *stepWatch, d time.Duration, want watchTripKind) watchTrip {
	t.Helper()
	trip, ok := w.due(watchAt(d))
	if !ok {
		t.Fatalf("at %s: no trip due, want %s", d, want)
	}
	if trip.Kind != want {
		t.Fatalf("at %s: %s trip due, want %s", d, trip.Kind, want)
	}
	if trip.Why == "" {
		t.Fatalf("at %s: %s trip has no Why", d, trip.Kind)
	}
	return trip
}

// watchWantQuiet asserts that no trip is due at d.
func watchWantQuiet(t *testing.T, w *stepWatch, d time.Duration) {
	t.Helper()
	if trip, ok := w.due(watchAt(d)); ok {
		t.Fatalf("at %s: %s trip due, want none", d, trip.Kind)
	}
}

// watchApply applies verdict to trip at d and returns the action.
func watchApply(w *stepWatch, d time.Duration, trip watchTrip, verdict string) watchAction {
	return w.apply(watchAt(d), trip, verdict)
}

// watchPeriodicChecks answers each periodic check due after from and before until with a progressing verdict.
func watchPeriodicChecks(t *testing.T, w *stepWatch, from, until time.Duration) {
	t.Helper()
	for d := from + 5*time.Minute; d < until; d += 5 * time.Minute {
		trip := watchWantDue(t, w, d, watchTripPeriodic)
		watchApply(w, d, trip, watchVerdictProgressing)
	}
}

// watchStart starts a turn at d.
func watchStart(w *stepWatch, d time.Duration) {
	w.event(watchAt(d), session.Started{})
}

// watchEnd ends the running turn at d.
func watchEndTurn(w *stepWatch, d time.Duration) {
	w.event(watchAt(d), session.TurnEnded{})
}

// watchOpen opens the main-conversation tool call at d.
func watchOpen(w *stepWatch, d time.Duration) {
	w.event(watchAt(d), session.ToolCallStarted{ToolUseID: watchToolID, Name: "Bash"})
}

// watchClose closes the main-conversation tool call at d with its tool result.
func watchClose(w *stepWatch, d time.Duration) {
	w.event(watchAt(d), session.Block{Kind: session.BlockToolResult, Role: "user", ToolUseID: watchToolID})
}

// watchInterrupt models a nudge's interrupt at d: the running turn ends a second later and the nudge's turn begins a
// second after that.
func watchInterrupt(w *stepWatch, d time.Duration) {
	watchEndTurn(w, d+time.Second)
	watchStart(w, d+2*time.Second)
}

// watchNotice reports an informational notice at d, which is an event that resets the silence count.
func watchNotice(w *stepWatch, d time.Duration) {
	w.event(watchAt(d), session.Notice{Kind: session.NoticeInformational, Text: "note"})
}

// Silence and periodic counts run only on model time: a turn with no open main-conversation tool call.
func TestWatchSilenceCountsModelTimeOnly(t *testing.T) {
	t.Parallel()
	w := watchNewAt(time.Hour)
	watchStart(w, 0)
	watchOpen(w, time.Minute)
	// Eleven minutes of wall time pass with the call open. Silence must not have accrued, so the periodic
	// check is what trips, not silent.
	watchApply(w, 12*time.Minute, watchWantDue(t, w, 12*time.Minute, watchTripPeriodic), watchVerdictProgressing)
	watchApply(w, 20*time.Minute, watchWantDue(t, w, 20*time.Minute, watchTripPeriodic), watchVerdictProgressing)
	watchClose(w, 21*time.Minute)
	// Model time resumes at the tool result, so nine minutes of model time have passed at 30 minutes.
	watchApply(w, 30*time.Minute, watchWantDue(t, w, 30*time.Minute, watchTripPeriodic), watchVerdictProgressing)
	watchWantDue(t, w, 31*time.Minute, watchTripSilent)
}

// Time between turns is not model time: the overtime limit counts only the two turns' running time.
func TestWatchTimeBetweenTurnsIsNotModelTime(t *testing.T) {
	t.Parallel()
	w := watchNewAt(30 * time.Minute)
	watchStart(w, 0)
	watchEndTurn(w, 10*time.Minute)
	watchStart(w, 60*time.Minute)
	watchNotice(w, 65*time.Minute)
	watchNotice(w, 70*time.Minute)
	// Model time is 10 minutes plus 15 minutes, which is under the 30 minute limit.
	watchApply(w, 75*time.Minute, watchWantDue(t, w, 75*time.Minute, watchTripPeriodic), watchVerdictProgressing)
	// Five more model minutes reach the limit.
	watchWantDue(t, w, 80*time.Minute, watchTripOvertime)
}

// A silent step is nudged at ten minutes of model time, and an event resets the count.
func TestWatchSilentTripAndReset(t *testing.T) {
	t.Parallel()
	w := watchNewAt(time.Hour)
	watchStart(w, 0)
	watchApply(w, 5*time.Minute, watchWantDue(t, w, 5*time.Minute, watchTripPeriodic), watchVerdictProgressing)
	// Silent wins over the periodic check that is also due at ten minutes.
	silent := watchWantDue(t, w, 10*time.Minute, watchTripSilent)
	if got := watchApply(
		w,
		10*time.Minute,
		silent,
		watchVerdictProgressing,
	); got != (watchAction{Kind: watchActNudge}) {
		t.Fatalf("first silent trip: got %+v, want nudge", got)
	}
	// The nudge quiets the watch until its interrupt ends the turn; the next silent trip is ten fresh minutes into
	// the nudge's turn.
	watchWantQuiet(t, w, 10*time.Minute+time.Second/2)
	watchInterrupt(w, 10*time.Minute)
	const next = 10*time.Minute + 2*time.Second
	periodic := watchWantDue(t, w, next+5*time.Minute, watchTripPeriodic)
	watchApply(w, next+5*time.Minute, periodic, watchVerdictProgressing)
	watchWantDue(t, w, next+10*time.Minute, watchTripSilent)
}

// Any event resets the silence count, not only tool results.
func TestWatchAnyEventResetsSilence(t *testing.T) {
	t.Parallel()
	w := watchNewAt(time.Hour)
	watchStart(w, 0)
	watchNotice(w, 9*time.Minute)
	// The periodic check is due at ten minutes, but silence is one minute since the notice.
	watchApply(w, 10*time.Minute, watchWantDue(t, w, 10*time.Minute, watchTripPeriodic), watchVerdictProgressing)
	watchWantDue(t, w, 19*time.Minute, watchTripSilent)
}

// A tool call open thirty minutes is stuck. Silence does not accrue while it is open.
func TestWatchToolStuckAtThirtyMinutes(t *testing.T) {
	t.Parallel()
	w := watchNewAt(time.Hour)
	watchStart(w, 0)
	watchOpen(w, 0)
	watchPeriodicChecks(t, w, 0, 30*time.Minute)
	watchWantQuiet(t, w, 29*time.Minute)
	trip := watchWantDue(t, w, 30*time.Minute, watchTripToolStuck)
	if trip.ToolUseID != watchToolID {
		t.Fatalf("tool_stuck names call %q, want %q", trip.ToolUseID, watchToolID)
	}
}

// A long_command verdict extends the named call once, to sixty minutes, and then the call ends the step.
func TestWatchToolStuckExtendsOnceToSixtyMinutes(t *testing.T) {
	t.Parallel()
	w := watchNewAt(time.Hour)
	watchStart(w, 0)
	watchOpen(w, 0)
	watchPeriodicChecks(t, w, 0, 30*time.Minute)
	trip := watchWantDue(t, w, 30*time.Minute, watchTripToolStuck)
	if got := watchApply(w, 30*time.Minute, trip, watchVerdictLongCommand); got != (watchAction{Kind: watchActExtend}) {
		t.Fatalf("first tool_stuck: got %+v, want extend", got)
	}
	watchPeriodicChecks(t, w, 30*time.Minute, 60*time.Minute)
	watchWantQuiet(t, w, 59*time.Minute)
	trip = watchWantDue(t, w, 60*time.Minute, watchTripToolStuck)
	want := watchAction{Kind: watchActEnd, End: watchEndToolStuck}
	if got := watchApply(w, 60*time.Minute, trip, watchVerdictLongCommand); got != want {
		t.Fatalf("extended call at sixty minutes: got %+v, want %+v", got, want)
	}
}

// Overtime wins over tool_stuck and periodic when all three are due.
func TestWatchTripOrderOvertimeFirst(t *testing.T) {
	t.Parallel()
	w := watchNewAt(20 * time.Minute)
	watchStart(w, 0)
	watchNotice(w, 5*time.Minute)
	watchNotice(w, 10*time.Minute)
	watchNotice(w, 15*time.Minute)
	// Twenty minutes of model time have passed when the call opens.
	watchOpen(w, 20*time.Minute)
	watchWantDue(t, w, 50*time.Minute, watchTripOvertime)
}

// Overtime fires at the step's model-time limit whatever the verdict, and the verdict is not consulted.
func TestWatchOvertimeEndsAtLimit(t *testing.T) {
	t.Parallel()
	w := watchNewAt(10 * time.Minute)
	watchStart(w, 0)
	// At nine minutes the only trip is the periodic check, so overtime has not reached its limit yet.
	watchApply(w, 9*time.Minute, watchWantDue(t, w, 9*time.Minute, watchTripPeriodic), watchVerdictProgressing)
	trip := watchWantDue(t, w, 10*time.Minute, watchTripOvertime)
	want := watchAction{Kind: watchActEnd, End: watchEndOvertime}
	if got := watchApply(w, 10*time.Minute, trip, watchVerdictProgressing); got != want {
		t.Fatalf("overtime: got %+v, want %+v", got, want)
	}
}

// Silent steps: the first silent trip nudges whatever the verdict, and the second ends the step with the
// verdict's kind when it is looping or wedged, else silent.
func TestWatchSilentPolicy(t *testing.T) {
	nudge := watchAction{Kind: watchActNudge}
	end := func(e watchEnd) watchAction { return watchAction{Kind: watchActEnd, End: e} }
	cases := []struct {
		name                string
		first, second       string
		wantFirst, wantNext watchAction
	}{
		{"progressing", watchVerdictProgressing, watchVerdictProgressing, nudge, end(watchEndSilent)},
		{"failed call", "", "", nudge, end(watchEndSilent)},
		{"long command", watchVerdictLongCommand, watchVerdictProgressing, nudge, end(watchEndSilent)},
		{"looping twice", watchVerdictLooping, watchVerdictLooping, nudge, end(watchEnd(watchVerdictLooping))},
		{"wedged then looping", watchVerdictWedged, watchVerdictLooping, nudge, end(watchEnd(watchVerdictLooping))},
		{"looping then progressing", watchVerdictLooping, watchVerdictProgressing, nudge, end(watchEndSilent)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := watchNewAt(time.Hour)
			watchStart(w, 0)
			trip := watchWantDue(t, w, 10*time.Minute, watchTripSilent)
			if got := watchApply(w, 10*time.Minute, trip, c.first); got != c.wantFirst {
				t.Fatalf("first silent: got %+v, want %+v", got, c.wantFirst)
			}
			watchInterrupt(w, 10*time.Minute)
			const next = 10*time.Minute + 2*time.Second
			watchApply(
				w,
				next+5*time.Minute,
				watchWantDue(t, w, next+5*time.Minute, watchTripPeriodic),
				watchVerdictProgressing,
			)
			trip = watchWantDue(t, w, next+10*time.Minute, watchTripSilent)
			if got := watchApply(w, next+10*time.Minute, trip, c.second); got != c.wantNext {
				t.Fatalf("second silent: got %+v, want %+v", got, c.wantNext)
			}
		})
	}
}

// Periodic checks do nothing on progressing and long_command, and a looping or wedged verdict nudges once
// and ends with its kind when it repeats.
func TestWatchPeriodicPolicy(t *testing.T) {
	none := watchAction{Kind: watchActNone}
	nudge := watchAction{Kind: watchActNudge}
	end := func(e watchEnd) watchAction { return watchAction{Kind: watchActEnd, End: e} }
	cases := []struct {
		name                string
		first, second       string
		wantFirst, wantNext watchAction
	}{
		{"progressing", watchVerdictProgressing, watchVerdictProgressing, none, none},
		{"long command", watchVerdictLongCommand, watchVerdictLongCommand, none, none},
		{"failed call", "", "", none, none},
		{"looping twice", watchVerdictLooping, watchVerdictLooping, nudge, end(watchEnd(watchVerdictLooping))},
		{"wedged twice", watchVerdictWedged, watchVerdictWedged, nudge, end(watchEnd(watchVerdictWedged))},
		{"looping then progressing", watchVerdictLooping, watchVerdictProgressing, nudge, none},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := watchNewAt(time.Hour)
			watchStart(w, 0)
			// A notice before each check keeps silence from tripping on the same ticks.
			watchNotice(w, 5*time.Minute)
			trip := watchWantDue(t, w, 5*time.Minute, watchTripPeriodic)
			if got := watchApply(w, 5*time.Minute, trip, c.first); got != c.wantFirst {
				t.Fatalf("first periodic: got %+v, want %+v", got, c.wantFirst)
			}
			// Every case ends the turn and starts the next, as a nudge's interrupt does.
			watchInterrupt(w, 5*time.Minute)
			const next = 5*time.Minute + 2*time.Second
			watchNotice(w, next+5*time.Minute)
			trip = watchWantDue(t, w, next+5*time.Minute, watchTripPeriodic)
			if got := watchApply(w, next+5*time.Minute, trip, c.second); got != c.wantNext {
				t.Fatalf("second periodic: got %+v, want %+v", got, c.wantNext)
			}
		})
	}
}

// tool_stuck: long_command extends, and every other verdict, including a failed call, ends the step with
// tool_stuck. A looping verdict follows the same once-then-end rule as the other trips.
func TestWatchToolStuckPolicy(t *testing.T) {
	cases := []struct {
		name    string
		verdict string
		want    watchAction
	}{
		{"progressing", watchVerdictProgressing, watchAction{Kind: watchActEnd, End: watchEndToolStuck}},
		{"failed call", "", watchAction{Kind: watchActEnd, End: watchEndToolStuck}},
		{"looping nudges first", watchVerdictLooping, watchAction{Kind: watchActNudge}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := watchNewAt(time.Hour)
			watchStart(w, 0)
			watchOpen(w, 0)
			watchPeriodicChecks(t, w, 0, 30*time.Minute)
			trip := watchWantDue(t, w, 30*time.Minute, watchTripToolStuck)
			if got := watchApply(w, 30*time.Minute, trip, c.verdict); got != c.want {
				t.Fatalf("tool_stuck with %q: got %+v, want %+v", c.verdict, got, c.want)
			}
		})
	}
}

// A failed monitor call on a silent step leaves the mechanical action: one nudge, then end.
func TestWatchFailedCallIsMechanical(t *testing.T) {
	t.Parallel()
	w := watchNewAt(time.Hour)
	watchStart(w, 0)
	trip := watchWantDue(t, w, 10*time.Minute, watchTripSilent)
	if got := watchApply(w, 10*time.Minute, trip, ""); got != (watchAction{Kind: watchActNudge}) {
		t.Fatalf("failed silent call: got %+v, want nudge", got)
	}
}

// A trip whose turn ended during its monitor call acts as none, and so does a tool_stuck whose call closed.
func TestWatchStaleTripActsNone(t *testing.T) {
	none := watchAction{Kind: watchActNone}
	t.Run("turn ended and a new turn began", func(t *testing.T) {
		w := watchNewAt(time.Hour)
		watchStart(w, 0)
		trip := watchWantDue(t, w, 5*time.Minute, watchTripPeriodic)
		watchEndTurn(w, 6*time.Minute)
		watchStart(w, 7*time.Minute)
		if got := watchApply(w, 7*time.Minute, trip, watchVerdictLooping); got != none {
			t.Fatalf("stale periodic: got %+v, want none", got)
		}
		watchWantDue(t, w, 12*time.Minute, watchTripPeriodic)
	})
	t.Run("tool call closed during the monitor call", func(t *testing.T) {
		w := watchNewAt(time.Hour)
		watchStart(w, 0)
		watchOpen(w, 0)
		watchPeriodicChecks(t, w, 0, 30*time.Minute)
		trip := watchWantDue(t, w, 30*time.Minute, watchTripToolStuck)
		watchClose(w, 31*time.Minute)
		if got := watchApply(w, 31*time.Minute, trip, watchVerdictLongCommand); got != none {
			t.Fatalf("closed call: got %+v, want none", got)
		}
	})
}

// A trip is not due again while its monitor call is pending, and the periodic count restarts at apply.
func TestWatchPendingTripBlocksDue(t *testing.T) {
	t.Parallel()
	w := watchNewAt(time.Hour)
	watchStart(w, 0)
	watchNotice(w, 5*time.Minute)
	trip := watchWantDue(t, w, 5*time.Minute, watchTripPeriodic)
	watchWantQuiet(t, w, 6*time.Minute)
	watchApply(w, 6*time.Minute, trip, watchVerdictProgressing)
	watchNotice(w, 10*time.Minute)
	watchWantQuiet(t, w, 10*time.Minute)
	watchWantDue(t, w, 11*time.Minute, watchTripPeriodic)
}

// recent keeps the last forty blocks and notices, oldest first, each with its age and its text cut to 300 bytes.
func TestWatchRecentWindowAndCuts(t *testing.T) {
	t.Run("window", func(t *testing.T) {
		w := watchNewAt(time.Hour)
		for i := range 45 {
			w.event(
				watchAt(time.Duration(i)*time.Second),
				session.Block{Kind: session.BlockText, Text: fmt.Sprintf("text %d", i)},
			)
		}
		lines := strings.Split(w.recent(watchAt(50*time.Second)), "\n")
		if len(lines) != 40 {
			t.Fatalf("recent has %d lines, want 40", len(lines))
		}
		if lines[0] != "45s ago text: text 5" {
			t.Fatalf("first line %q, want %q", lines[0], "45s ago text: text 5")
		}
		if lines[39] != "6s ago text: text 44" {
			t.Fatalf("last line %q, want %q", lines[39], "6s ago text: text 44")
		}
	})
	t.Run("cut at 300 bytes", func(t *testing.T) {
		w := watchNewAt(time.Hour)
		w.event(watchAt(0), session.Block{Kind: session.BlockText, Text: strings.Repeat("a", 400)})
		want := "0s ago text: " + strings.Repeat("a", 300)
		if got := w.recent(watchAt(0)); got != want {
			t.Fatalf("cut line %q, want %q", got, want)
		}
	})
	t.Run("cut never splits a rune", func(t *testing.T) {
		w := watchNewAt(time.Hour)
		// The é is two bytes at 299 and 300, so a 300 byte cut would split it; the cut backs off to 299.
		w.event(watchAt(0), session.Block{Kind: session.BlockText, Text: strings.Repeat("a", 299) + "é"})
		want := "0s ago text: " + strings.Repeat("a", 299)
		if got := w.recent(watchAt(0)); got != want {
			t.Fatalf("cut line %q, want %q", got, want)
		}
	})
}

// recent labels every kind of block and notice it keeps.
func TestWatchRecentLabels(t *testing.T) {
	t.Parallel()
	w := watchNewAt(time.Hour)
	w.event(watchAt(0), session.Block{Kind: session.BlockThinking, Text: "hmm"})
	w.event(watchAt(0), session.Block{Kind: session.BlockToolCall, Name: "Bash", Input: []byte(`{"command":"ls"}`)})
	w.event(watchAt(0), session.Block{Kind: session.BlockToolResult, Content: []byte("out")})
	w.event(watchAt(0), session.Block{Kind: session.BlockToolResult, Content: []byte("boom"), IsError: true})
	w.event(watchAt(0), session.Notice{Kind: session.NoticeAPIRetry, Text: "retrying"})
	want := strings.Join([]string{
		"0s ago thinking: hmm",
		`0s ago call Bash: {"command":"ls"}`,
		"0s ago result: out",
		"0s ago error result: boom",
		"0s ago notice api_retry: retrying",
	}, "\n")
	if got := w.recent(watchAt(0)); got != want {
		t.Fatalf("recent:\n%s\nwant:\n%s", got, want)
	}
}

// event and due run on different goroutines, and a race between them must neither race nor lose the window.
func TestWatchEventRacesDue(t *testing.T) {
	t.Parallel()
	w := watchNewAt(time.Hour)
	watchStart(w, 0)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 1; i <= 500; i++ {
			watchNotice(w, time.Duration(i)*time.Second)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 1; i <= 500; i++ {
			d := time.Duration(i) * time.Second
			if trip, ok := w.due(watchAt(d)); ok {
				w.apply(watchAt(d), trip, watchVerdictProgressing)
			}
		}
	}()
	wg.Wait()
	if n := strings.Count(w.recent(watchAt(500*time.Second)), "\n"); n != 39 {
		t.Fatalf("recent holds %d line breaks, want 39 (40 lines)", n)
	}
}

// A nudge on a stuck tool call quiets the watch until the interrupted turn ends and the next begins, so the same call
// does not trip again on the next tick.
func TestWatchNudgeQuietsUntilNextTurn(t *testing.T) {
	t.Parallel()
	w := watchNewAt(2 * time.Hour)
	watchStart(w, 0)
	watchOpen(w, 0)
	watchPeriodicChecks(t, w, 0, 30*time.Minute)
	trip := watchWantDue(t, w, 30*time.Minute, watchTripToolStuck)
	if got := watchApply(w, 30*time.Minute, trip, watchVerdictLooping); got.Kind != watchActNudge {
		t.Fatalf("looping on a stuck call: got %+v, want nudge", got)
	}
	watchWantQuiet(t, w, 30*time.Minute+5*time.Second)
	watchWantQuiet(t, w, 40*time.Minute)
	watchEndTurn(w, 40*time.Minute+time.Second)
	watchStart(w, 40*time.Minute+2*time.Second)
	watchWantDue(t, w, 45*time.Minute+2*time.Second, watchTripPeriodic)
}

// A stale apply still restarts the periodic count, so no periodic check follows it on the next tick.
func TestWatchStaleApplyRestartsPeriodic(t *testing.T) {
	t.Parallel()
	w := watchNewAt(2 * time.Hour)
	watchStart(w, 0)
	watchOpen(w, 0)
	watchPeriodicChecks(t, w, 0, 30*time.Minute)
	trip := watchWantDue(t, w, 30*time.Minute, watchTripToolStuck)
	watchClose(w, 31*time.Minute)
	if got := watchApply(w, 31*time.Minute, trip, watchVerdictLongCommand); got.Kind != watchActNone {
		t.Fatalf("closed call: got %+v, want none", got)
	}
	watchWantQuiet(t, w, 31*time.Minute+5*time.Second)
	watchWantDue(t, w, 36*time.Minute, watchTripPeriodic)
}
