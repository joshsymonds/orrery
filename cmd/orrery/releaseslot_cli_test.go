package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// slotOutsideCLIRepo is the repository the release-slot command lines name.
const slotOutsideCLIRepo = "/work/slot-cli-repo"

// slotOutsideCLILimit is the limit of the first outside hold in these tests.
const slotOutsideCLILimit = 30 * time.Minute

// slotOutsideCLINow is the clock the release slots read: 2026-11-19 10:00:00 UTC.
func slotOutsideCLINow() time.Time {
	return time.Date(2026, time.November, 19, 10, 0, 0, 0, time.UTC)
}

// slotOutsideCLINeverTimer is the CronTimer the release slots are given in these tests. Its channel is nil, so it never
// fires.
type slotOutsideCLINeverTimer struct{}

// C returns a nil channel, which never fires.
func (slotOutsideCLINeverTimer) C() <-chan time.Time { return nil }

// Stop stops nothing, since the timer never fires.
func (slotOutsideCLINeverTimer) Stop() bool { return true }

// slotOutsideCLISlots returns the real release slots over a store in a temporary directory. Their clock reads
// slotOutsideCLINow and their timers never fire. Run is not started, so the slots are only the holds and gives a
// command makes.
func slotOutsideCLISlots(t *testing.T) *enginehost.ReleaseSlots {
	t.Helper()
	return enginehost.NewReleaseSlots(enginehost.ReleaseSlotsConfig{
		Store: openCronStore(t),
		Now:   slotOutsideCLINow,
		NewTimer: func(time.Duration) enginehost.CronTimer {
			return slotOutsideCLINeverTimer{}
		},
	})
}

// slotOutsideCLIServe serves slots as the ReleaseSlots service on the session socket beside a daemon socket in a
// temporary directory, and returns the daemon socket path that `--socket` names. The server stops at cleanup.
func slotOutsideCLIServe(t *testing.T, slots enginehost.ReleaseSlotCommands) string {
	t.Helper()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	ln, err := enginehost.Listen(protocol.SessionSocketPath(daemon))
	if err != nil {
		t.Fatalf("enginehost.Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- enginehost.ServeWith(ctx, ln, enginehost.ServeConfig{ReleaseSlots: slots}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("enginehost.ServeWith: %v", err)
		}
	})
	return daemon
}

// slotOutsideCLIResult is what one run of `orrery release-slot` returned: its exit code and what it wrote.
type slotOutsideCLIResult struct {
	code   int
	stdout string
	stderr string
}

// slotOutsideCLIRun runs `orrery --socket daemon release-slot ARGS...` and returns what it returned.
func slotOutsideCLIRun(daemon string, args ...string) slotOutsideCLIResult {
	var stdout, stderr bytes.Buffer
	argv := append([]string{"--socket", daemon, "release-slot"}, args...)
	code := run(argv, nil, &stdout, &stderr, envFrom(nil))
	return slotOutsideCLIResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// slotOutsideCLIExpect checks that the run named what exited with code and wrote exactly stdout and stderr.
func slotOutsideCLIExpect(t *testing.T, what string, res slotOutsideCLIResult, code int, stdout, stderr string) {
	t.Helper()
	if res.code != code {
		t.Errorf("%s: exit status %d, want %d; stderr %q", what, res.code, code, res.stderr)
	}
	if res.stdout != stdout {
		t.Errorf("%s: stdout %q, want %q", what, res.stdout, stdout)
	}
	if res.stderr != stderr {
		t.Errorf("%s: stderr %q, want %q", what, res.stderr, stderr)
	}
}

// slotOutsideCLIEpicSlot is a ReleaseSlotCommands for a repository an epic holds. Hold refuses with the epic as the
// holder, and Show names that holder. It records each call by name, so a test can tell that no call was made.
type slotOutsideCLIEpicSlot struct {
	mu    sync.Mutex
	calls []string
}

// slotOutsideCLIEpicHolder is the epic that holds the slot.
func slotOutsideCLIEpicHolder() store.ReleaseSlotHolder {
	return store.ReleaseSlotHolder{Kind: store.ReleaseSlotEpic, Epic: "epic-slot-7"}
}

// record appends call to the calls the fake has received.
func (f *slotOutsideCLIEpicSlot) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

// received returns the calls the fake has received, in order.
func (f *slotOutsideCLIEpicSlot) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Hold records the call and refuses the hold: the epic holds the slot.
func (f *slotOutsideCLIEpicSlot) Hold(
	_ context.Context, _, _ string, _ time.Duration,
) (store.ReleaseSlotHolder, bool, error) {
	f.record("Hold")
	return slotOutsideCLIEpicHolder(), false, nil
}

// HoldWait records the call and refuses it: the epic never frees the slot, so a wait has no answer.
func (f *slotOutsideCLIEpicSlot) HoldWait(
	_ context.Context, _, _ string, _ time.Duration, _ func(store.ReleaseSlotHolder) error,
) (store.ReleaseSlotHolder, error) {
	f.record("HoldWait")
	return store.ReleaseSlotHolder{}, errors.New("the epic never frees the slot")
}

// Give records the call and frees nothing.
func (f *slotOutsideCLIEpicSlot) Give(_ context.Context, _, _ string) (bool, error) {
	f.record("Give")
	return false, nil
}

// Show records the call and names the epic as the holder.
func (f *slotOutsideCLIEpicSlot) Show(_ context.Context, _ string) (store.ReleaseSlotHolder, bool, error) {
	f.record("Show")
	return slotOutsideCLIEpicHolder(), true, nil
}

// slotOutsideCLIReporter is the real release slots, whose HoldWait reports each waiting call on waits before it calls
// the given callback, so a test can act at the first wait.
type slotOutsideCLIReporter struct {
	*enginehost.ReleaseSlots
	waits chan store.ReleaseSlotHolder
}

// HoldWait reports each holder that keeps the slot on waits, and then calls waiting with it, as HoldWait does.
func (r slotOutsideCLIReporter) HoldWait(
	ctx context.Context, repository, label string, limit time.Duration,
	waiting func(store.ReleaseSlotHolder) error,
) (store.ReleaseSlotHolder, error) {
	return r.ReleaseSlots.HoldWait(ctx, repository, label, limit, func(holder store.ReleaseSlotHolder) error {
		r.waits <- holder
		return waiting(holder)
	})
}

// TestSlotOutsideCLIHoldShowGive holds a repository's release slot for an outside label over the real slots, and checks
// that a second label is refused with the first one's holder, that giving by the second label frees nothing, and that
// giving by the first label frees the slot.
func TestSlotOutsideCLIHoldShowGive(t *testing.T) {
	t.Parallel()
	daemon := slotOutsideCLIServe(t, slotOutsideCLISlots(t))
	const e1Holds = "held /work/slot-cli-repo for gambit:e1 until 2026-11-19T10:30:00Z\n"
	const e1Holder = "/work/slot-cli-repo: held by outside gambit:e1 until 2026-11-19T10:30:00Z\n"

	hold := slotOutsideCLIRun(daemon, "hold", slotOutsideCLIRepo, "--holder", "gambit:e1", "--limit", "30m")
	slotOutsideCLIExpect(t, "hold gambit:e1", hold, exitOK, e1Holds, "")

	show := slotOutsideCLIRun(daemon, "show", slotOutsideCLIRepo)
	slotOutsideCLIExpect(t, "show after hold", show, exitOK, e1Holder, "")

	refused := slotOutsideCLIRun(daemon, "hold", slotOutsideCLIRepo, "--holder", "gambit:e2", "--limit", "10m")
	slotOutsideCLIExpect(t, "hold gambit:e2", refused, exitFailure, "",
		"orrery release-slot: /work/slot-cli-repo is held by outside gambit:e1 until 2026-11-19T10:30:00Z\n")

	nothing := slotOutsideCLIRun(daemon, "give", slotOutsideCLIRepo, "--holder", "gambit:e2")
	slotOutsideCLIExpect(t, "give gambit:e2", nothing, exitOK,
		"/work/slot-cli-repo is not held by gambit:e2; nothing freed\n", "")

	show = slotOutsideCLIRun(daemon, "show", slotOutsideCLIRepo)
	slotOutsideCLIExpect(t, "show after give by gambit:e2", show, exitOK, e1Holder, "")

	freed := slotOutsideCLIRun(daemon, "give", slotOutsideCLIRepo, "--holder", "gambit:e1")
	slotOutsideCLIExpect(t, "give gambit:e1", freed, exitOK, "freed /work/slot-cli-repo\n", "")

	show = slotOutsideCLIRun(daemon, "show", slotOutsideCLIRepo)
	slotOutsideCLIExpect(t, "show after give by gambit:e1", show, exitOK, "/work/slot-cli-repo: free\n", "")
}

// TestSlotOutsideCLIHoldRefusedNamesEpic runs a hold that an epic's slot refuses, and checks that the refusal names the
// epic, and that show names the same epic as the holder.
func TestSlotOutsideCLIHoldRefusedNamesEpic(t *testing.T) {
	t.Parallel()
	daemon := slotOutsideCLIServe(t, &slotOutsideCLIEpicSlot{})
	hold := slotOutsideCLIRun(daemon, "hold", slotOutsideCLIRepo, "--holder", "gambit:e2", "--limit", "10m")
	slotOutsideCLIExpect(t, "hold", hold, exitFailure, "",
		"orrery release-slot: /work/slot-cli-repo is held by epic epic-slot-7\n")

	show := slotOutsideCLIRun(daemon, "show", slotOutsideCLIRepo)
	slotOutsideCLIExpect(t, "show", show, exitOK, "/work/slot-cli-repo: held by epic epic-slot-7\n", "")
}

// TestSlotOutsideCLIHoldWaitTakesFreedSlot holds a slot for gambit:e1, starts a wait for gambit:e2 on another
// goroutine, and gives the slot back once the wait reports that it waits. The wait then takes the slot, and the run
// checks the waiting line and the held line.
func TestSlotOutsideCLIHoldWaitTakesFreedSlot(t *testing.T) {
	t.Parallel()
	slots := slotOutsideCLISlots(t)
	ctx := t.Context()
	if _, held, err := slots.Hold(ctx, slotOutsideCLIRepo, "gambit:e1", slotOutsideCLILimit); err != nil || !held {
		t.Fatalf("Hold gambit:e1: held %t, err %v; want held", held, err)
	}
	waits := make(chan store.ReleaseSlotHolder, 1)
	daemon := slotOutsideCLIServe(t, slotOutsideCLIReporter{ReleaseSlots: slots, waits: waits})

	done := make(chan slotOutsideCLIResult, 1)
	go func() {
		done <- slotOutsideCLIRun(daemon, "hold", slotOutsideCLIRepo, "--holder", "gambit:e2", "--limit", "10m", "--wait")
	}()
	select {
	case <-waits:
	case res := <-done:
		t.Fatalf("hold --wait ended before it waited: exit %d, stdout %q, stderr %q", res.code, res.stdout, res.stderr)
	}
	if _, err := slots.Give(ctx, slotOutsideCLIRepo, "gambit:e1"); err != nil {
		t.Fatalf("Give gambit:e1: %v", err)
	}
	res := <-done
	slotOutsideCLIExpect(t, "hold --wait gambit:e2", res, exitOK,
		"held /work/slot-cli-repo for gambit:e2 until 2026-11-19T10:10:00Z\n",
		"orrery release-slot: waiting: /work/slot-cli-repo is held by outside gambit:e1 until 2026-11-19T10:30:00Z\n")
}

// TestSlotOutsideCLIHoldRoundsLimitUp holds over the real slots with a limit that is not whole seconds, and checks that
// the hold lasts the limit rounded up: 1500ms holds for 2 seconds. Then it gives the slot back and holds for 2h, the
// longest limit the command accepts.
func TestSlotOutsideCLIHoldRoundsLimitUp(t *testing.T) {
	t.Parallel()
	daemon := slotOutsideCLIServe(t, slotOutsideCLISlots(t))
	short := slotOutsideCLIRun(daemon, "hold", slotOutsideCLIRepo, "--holder", "gambit:e1", "--limit", "1500ms")
	slotOutsideCLIExpect(t, "hold 1500ms", short, exitOK,
		"held /work/slot-cli-repo for gambit:e1 until 2026-11-19T10:00:02Z\n", "")
	freed := slotOutsideCLIRun(daemon, "give", slotOutsideCLIRepo, "--holder", "gambit:e1")
	slotOutsideCLIExpect(t, "give gambit:e1", freed, exitOK, "freed /work/slot-cli-repo\n", "")
	long := slotOutsideCLIRun(daemon, "hold", slotOutsideCLIRepo, "--holder", "gambit:e1", "--limit", "2h")
	slotOutsideCLIExpect(t, "hold 2h", long, exitOK,
		"held /work/slot-cli-repo for gambit:e1 until 2026-11-19T12:00:00Z\n", "")
}

// TestSlotOutsideCLIUsageRefused runs each command line the usage refuses, and checks that each exits 2 and makes no
// call to the release slots.
func TestSlotOutsideCLIUsageRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{"hold without --limit", []string{"hold", slotOutsideCLIRepo, "--holder", "gambit:e2"}},
		{"hold with --limit 0s", []string{"hold", slotOutsideCLIRepo, "--holder", "gambit:e2", "--limit", "0s"}},
		{"hold with --limit 2h0m1s", []string{
			"hold", slotOutsideCLIRepo, "--holder", "gambit:e2", "--limit", "2h0m1s",
		}},
		{"hold without --holder", []string{"hold", slotOutsideCLIRepo, "--limit", "10m"}},
		{"give without --holder", []string{"give", slotOutsideCLIRepo}},
		{"unknown verb", []string{"frob", slotOutsideCLIRepo}},
		{"missing REPO", []string{"show"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &slotOutsideCLIEpicSlot{}
			daemon := slotOutsideCLIServe(t, fake)
			res := slotOutsideCLIRun(daemon, tc.args...)
			if res.code != exitUsage {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}
			if calls := fake.received(); len(calls) != 0 {
				t.Errorf("release slots received %q, want no call", calls)
			}
		})
	}
}
