package enginehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
	"github.com/joshsymonds/orrery/internal/store"
)

// hostRoutingGatesHosts returns the Hosts the gate routing tests run on: recorders a and b over this machine, and the
// Hosts that name them.
func hostRoutingGatesHosts() (*hostexectest.Recorder, *hostexectest.Recorder, hostexec.Hosts) {
	a := hostexectest.NewRecorder(hostexec.NewLocal())
	b := hostexectest.NewRecorder(hostexec.NewLocal())
	return a, b, hostexec.Map{"a": a, "b": b}
}

// hostRoutingGatesEpic stores hostRoutingEpic in st on host b, with repo as its repository, and returns the Host the epic
// lives on, resolved through hosts as a gate's caller resolves it.
func hostRoutingGatesEpic(t *testing.T, st *store.Store, repo string, hosts hostexec.Hosts) hostexec.Host {
	t.Helper()
	created := time.Unix(1700000000, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: hostRoutingEpic, Repository: repo, Host: "b", Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	host, err := hostOfEpic(t.Context(), st, hosts, hostRoutingEpic)
	if err != nil {
		t.Fatalf("host of epic %s: %v", hostRoutingEpic, err)
	}
	return host
}

// hostRoutingGatesEvidence runs the capture of an evidence run on the host its epic lives on. The capture's gate is launched
// there and detached while it runs, so it ends under its holder. A readopt then reads the holder's exit record and clears
// the holder. It returns the gate's holder directory and the gate state directory the holder lives under.
func hostRoutingGatesEvidence(t *testing.T, hosts hostexec.Hosts) (string, string) {
	t.Helper()
	repo, head := evidenceCaptureRepo(t)
	st := openTestStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	host := hostRoutingGatesEpic(t, st, repo, hosts)
	release := filepath.Join(t.TempDir(), "release")
	startedFile := filepath.Join(t.TempDir(), "started")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	c := evidenceCaptureFor(t, &fakeGatePools{}, "STARTED="+startedFile, "RELEASE="+release).on(host)
	const id = "routed"
	item := evidence.Item{Capture: []string{"/bin/sh", "-c", evidenceCaptureDetachScript}, Limit: 60}

	granted := make(chan string, 1)
	started := func(ctx context.Context, grant, _ string) error {
		granted <- grant
		return ctx.Err()
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, runErr = c.run(ctx, repo, head, id, item, started)
	}()
	gateWaitFile(t, startedFile, done)
	cancel(errGateDetach)
	<-done
	if !errors.Is(runErr, errGateDetach) {
		t.Fatalf("run err = %v, want errGateDetach", runErr)
	}

	stateDir, err := gateStateDir(c.gateStateDir)
	if err != nil {
		t.Fatalf("gate state dir: %v", err)
	}
	holderDir := filepath.Join(stateDir, gateHoldersName, "evidence-"+id)
	if err = os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	gateWaitExit(t, holderDir)
	gateWaitLockFree(t, holderDir)
	got, err := c.readopt(t.Context(), repo, head, id, item, <-granted)
	if err != nil {
		t.Fatalf("readopt: %v", err)
	}
	if got.NotCaptured != "" || got.Exit != 3 {
		t.Fatalf("readopt = %+v, want exit 3 and captured", got)
	}
	return holderDir, stateDir
}

// hostRoutingGatesStep runs a writing step on the host its epic lives on, whose gate prints to both streams and exits 1, so
// the gate's output is kept in a log. Its gate is run and its log is kept through that host. It returns the step's loop
// fixture, whose run has ended.
func hostRoutingGatesStep(t *testing.T, hosts hostexec.Hosts) *stepLoopFixture {
	t.Helper()
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	gate := stepDoneScript(t, "echo kept-out; echo kept-err >&2; exit 1")
	f := newStepLoopFixture(t, nil)
	host := hostRoutingGatesEpic(t, f.store, dir, hosts)
	// The run's clock and deadline timer are fakes, so no real clock or timer runs.
	clock := &watchLoopClock{at: watchEpoch()}
	timers := &stepDeadlineTimers{}
	cfg := f.run.stepRunConfig
	stepDoneWriting(dir, start, []string{gate}, time.Minute, &fakeGatePools{})(&cfg)
	cfg.host = host
	cfg.now = clock.now
	cfg.newTimer = timers.newTimer
	f.run = newStepRun(cfg)
	_, done := f.start(t)
	for range stepLoopMaxContinues + 1 {
		f.run.Return(stepDoneReturn(commit))
	}
	waitRunEnd(t, done)
	return f
}

// hostRoutingCallOf is one call a test expects a Recorder to have received: its operation and the path it names.
type hostRoutingCallOf struct {
	op, path string
}

// hostRoutingWantCalls fails t for each of want that calls does not hold.
func hostRoutingWantCalls(t *testing.T, host string, calls []hostexectest.Call, want ...hostRoutingCallOf) {
	t.Helper()
	for _, w := range want {
		if !hostRoutingCallAt(calls, w.op, w.path) {
			t.Errorf("host %s recorded no %s of %s: %v", host, w.op, w.path, calls)
		}
	}
}

// TestEpicHostRoutingGates covers the routing of gates to the host their epic is stored on. An evidence run's gate is
// launched, its exit record is read and its holder is cleared through host b. A step's gate is launched, and its kept log is
// written and read, through host b. Host a, which no epic is stored on, records no call.
func TestEpicHostRoutingGates(t *testing.T) {
	t.Parallel()
	t.Run("evidence gate held on b", func(t *testing.T) {
		t.Parallel()
		_, b, hosts := hostRoutingGatesHosts()
		holder, stateDir := hostRoutingGatesEvidence(t, hosts)
		hostRoutingWantCalls(t, "b", b.Calls(),
			hostRoutingCallOf{"Launch", stateDir},
			hostRoutingCallOf{"ExitRecord", holder},
			hostRoutingCallOf{"Clear", holder},
		)
	})
	t.Run("step gate held on b", func(t *testing.T) {
		t.Parallel()
		_, b, hosts := hostRoutingGatesHosts()
		f := hostRoutingGatesStep(t, hosts)
		stateDir, err := gateStateDir(f.run.stateDir)
		if err != nil {
			t.Fatalf("gate state dir: %v", err)
		}
		holder := filepath.Join(stateDir, gateHoldersName, stepGateSession(stepLoopID, 0))
		gateCheck := stepDoneCheck(t, stepDoneRoundChecks(f.row(t).Checks, 1), stepLoopGateName)
		if gateCheck.Log == "" {
			t.Fatalf("gate check = %+v, want a kept log", gateCheck)
		}
		// The kept log is read on the step's host, as the step's check reads it.
		logs := &Steps{gateStateDir: f.run.stateDir}
		text, kept, err := checkOutput(t.Context(), b, gateCheck, logs)
		if err != nil {
			t.Fatalf("checkOutput: %v", err)
		}
		if want := "== stdout ==\nkept-out\n== stderr ==\nkept-err\n"; !kept || text != want {
			t.Fatalf("kept log = %q (kept %v), want %q", text, kept, want)
		}
		hostRoutingWantCalls(t, "b", b.Calls(),
			hostRoutingCallOf{"Launch", stateDir},
			hostRoutingCallOf{"MkdirAll", filepath.Dir(gateCheck.Log)},
			hostRoutingCallOf{"WriteFile", gateCheck.Log},
			hostRoutingCallOf{"ReadRooted", gateCheck.Log},
			hostRoutingCallOf{"Clear", holder},
		)
	})
	t.Run("host a records nothing", func(t *testing.T) {
		t.Parallel()
		a, b, hosts := hostRoutingGatesHosts()
		hostRoutingGatesEvidence(t, hosts)
		hostRoutingGatesStep(t, hosts)
		// Host b must have seen the gates' calls, so a silent run does not pass for the routing.
		if calls := b.Calls(); len(calls) == 0 {
			t.Errorf("host b recorded no calls, so the gates did not run on it")
		}
		if calls := a.Calls(); len(calls) != 0 {
			t.Errorf("host a recorded %d calls, want none: %v", len(calls), calls)
		}
	})
}
