package enginehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// gateGrantPoll is how often a test reads the fake pools while it waits for a request to reach them.
const gateGrantPoll = 10 * time.Millisecond

// fakeGatePools is a PoolCommands that grants at once. Each Acquire records its request. A grant holds one member for
// each need, named <pool>-1 up to the need's count, and a reclaim keeps the grant id it names. A value sent to stop makes
// the granted call send a stop, and a value sent to drop makes the granted call return that error. onEnd runs when the
// granted call's ctx ends, before the call returns. queue, when not nil, holds each Acquire until it is closed or its ctx
// ends. err, when not nil, is what Acquire returns without queuing.
type fakeGatePools struct {
	queue chan struct{}
	err   error
	stop  chan struct{}
	drop  chan error
	onEnd func()

	mu       sync.Mutex
	requests []grants.Request
	// reclaimsEnded holds the grant id of each reclaim whose call has returned.
	reclaimsEnded []string
}

// Acquire implements PoolCommands for the fake.
func (f *fakeGatePools) Acquire(ctx context.Context, req grants.Request, send func(grants.Event) error) error {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	id := req.GrantID
	if id == "" {
		id = fmt.Sprintf("grant-%d", len(f.requests))
	}
	f.mu.Unlock()
	if req.GrantID != "" {
		defer func() {
			f.mu.Lock()
			f.reclaimsEnded = append(f.reclaimsEnded, req.GrantID)
			f.mu.Unlock()
		}()
	}
	if f.err != nil {
		return f.err
	}
	if f.queue != nil {
		select {
		case <-f.queue:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	grant := grants.Grant{ID: id, Members: fakeGateMembers(req.Needs)}
	if err := send(grants.Event{Grant: &grant}); err != nil {
		return err
	}
	select {
	case <-f.stop:
		if err := send(grants.Event{Stop: grant.Members}); err != nil {
			return err
		}
		<-ctx.Done()
	case err := <-f.drop:
		return err
	case <-ctx.Done():
	}
	if f.onEnd != nil {
		f.onEnd()
	}
	return ctx.Err()
}

// Status implements PoolCommands for the fake. The gate tests do not read it.
func (f *fakeGatePools) Status() grants.Status {
	return grants.Status{}
}

// Hold implements PoolCommands for the fake. The gate tests do not place holds.
func (f *fakeGatePools) Hold(context.Context, string) error {
	return nil
}

// Free implements PoolCommands for the fake. The gate tests do not free holds.
func (f *fakeGatePools) Free(context.Context, string) error {
	return nil
}

// stopGrant makes the granted call send its stop, and waits until the call takes it.
func (f *fakeGatePools) stopGrant() {
	f.stop <- struct{}{}
}

// requested returns the requests Acquire has been called with, in order.
func (f *fakeGatePools) requested() []grants.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// ended reports whether a call that reclaimed the grant id has returned, which ends the reclaimed grant.
func (f *fakeGatePools) ended(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.reclaimsEnded, id)
}

// fakeGateMembers returns the members a grant of needs holds: <pool>-1 up to each need's count, pool by pool.
func fakeGateMembers(needs []grants.Need) []grants.Member {
	var members []grants.Member
	for _, need := range needs {
		for i := 1; i <= need.Count; i++ {
			members = append(members, grants.Member{Pool: need.Pool, Name: fmt.Sprintf("%s-%d", need.Pool, i)})
		}
	}
	return members
}

// gateGrantRequest is the request the granted gate tests queue: one member of the gpu pool.
func gateGrantRequest() grants.Request {
	return grants.Request{
		Project:   "orrery",
		Needs:     []grants.Need{{Pool: "gpu", Count: 1}},
		ClientPID: os.Getpid(),
	}
}

// gateGroupGoneAtEnd returns an onEnd hook that checks the group and background child a gate wrote to pids have exited
// when the grant's ctx ends. A SIGKILL is delivered asynchronously, so the hook polls. It reports through t.Error, which
// is safe off the test goroutine.
func gateGroupGoneAtEnd(t *testing.T, pids string) func() {
	t.Helper()
	return func() {
		data, err := os.ReadFile(pids)
		fields := strings.Fields(string(data))
		if err != nil || len(fields) != 2 {
			t.Errorf("pid file %s = %q, %v: the gate had not written its pids when its grant ended", pids, data, err)
			return
		}
		for _, field := range fields {
			pid, err := strconv.Atoi(field)
			if err != nil {
				t.Errorf("pid %q: %v", field, err)
				return
			}
			for !gateProcessGone(pid) {
				time.Sleep(20 * time.Millisecond)
			}
			if !gateProcessGone(pid) {
				t.Errorf(
					"process %d still runs when the grant's ctx ends: the grant ended before the group exited",
					pid,
				)
			}
		}
	}
}

func TestGrantedGateRunsOnlyAfterTheGrantAndReportsItsMembers(t *testing.T) {
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	marker := filepath.Join(t.TempDir(), "started")
	queue := make(chan struct{})
	pools := &fakeGatePools{queue: queue, onEnd: gateGroupGoneAtEnd(t, pids)}

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
			Argv:    []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; touch "$MARKER"; exit 0`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Environ: gateEnv("PIDS="+pids, "MARKER="+marker),
		})
		done <- outcome{result, err}
	}()

	select {
	case got := <-done:
		t.Fatalf("gate finished before its grant: %+v, %v", got.result, got.err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("gate started before its grant")
	}

	close(queue)
	got := <-done
	if got.err != nil {
		t.Fatalf("runGrantedGate: %v", got.err)
	}
	if got.result.Exit != 0 {
		t.Fatalf("Exit = %d, want 0", got.result.Exit)
	}
	if want := []grants.Member{{Pool: "gpu", Name: "gpu-1"}}; !slices.Equal(got.result.Members, want) {
		t.Fatalf("Members = %v, want the granted %v", got.result.Members, want)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("gate did not run after its grant: %v", err)
	}
	requests := pools.requested()
	if len(requests) != 1 {
		t.Fatalf("Acquire was called %d times, want once", len(requests))
	}
	if requests[0].GrantID != "" {
		t.Fatalf("first request GrantID = %q, want none", requests[0].GrantID)
	}
	if want := gateGrantRequest().Needs; !slices.Equal(requests[0].Needs, want) {
		t.Fatalf("request Needs = %v, want the gate's needs %v", requests[0].Needs, want)
	}
}

func TestGrantedGateQueuedTimeDoesNotCountTowardLimit(t *testing.T) {
	t.Parallel()
	// The gate's limit is its holder's, set when the holder launches, so time queued for the grant can count toward it
	// only if the gate starts before the grant. While the grant is held queued, the gate must not have started and
	// runGrantedGate must not have returned.
	queue := make(chan struct{})
	pools := &fakeGatePools{queue: queue}
	ran := filepath.Join(t.TempDir(), "ran")

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
			Argv:    []string{"/bin/sh", "-c", `echo 1 > "$RAN"; exit 0`},
			Dir:     t.TempDir(),
			Limit:   time.Second,
			Environ: gateEnv("RAN=" + ran),
		})
		done <- outcome{result, err}
	}()
	for len(pools.requested()) == 0 {
		time.Sleep(gateGrantPoll)
	}
	select {
	case got := <-done:
		t.Fatalf("runGrantedGate returned (%+v, %v) while its grant was queued", got.result, got.err)
	default:
	}
	if _, err := os.Stat(ran); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the gate ran while its grant was queued: stat %s = %v", ran, err)
	}

	close(queue)
	got := <-done
	result, err := got.result, got.err
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if result.TimedOut {
		t.Fatal("TimedOut = true: the time spent queued counted toward Limit")
	}
	if result.Exit != 0 {
		t.Fatalf("Exit = %d, want 0", result.Exit)
	}
}

// gateHolderLimit returns the --gate-limit argument the running holder of the gate in dir was started with, as its
// /proc cmdline holds it.
func gateHolderLimit(t *testing.T, dir string) string {
	t.Helper()
	pids := holderPids(t, dir)
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pids.Holder), "cmdline"))
	if err != nil {
		t.Fatalf("read the cmdline of holder %d: %v", pids.Holder, err)
	}
	args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	for i, arg := range args {
		if arg == "--gate-limit" && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("holder %d argv %q has no --gate-limit", pids.Holder, args)
	return ""
}

// TestGrantedGateEachAttemptGetsItsOwnLimit checks that a gate rerun after a stop gets a whole Limit of its own. The
// limit is its holder's, which counts from the holder's start, so each attempt's holder must be started with the full
// Limit. The first attempt runs until its grant is stopped, the rerun until the test releases it, and the rerun's
// result stands with no time out.
func TestGrantedGateEachAttemptGetsItsOwnLimit(t *testing.T) {
	t.Parallel()
	first := filepath.Join(t.TempDir(), "first")
	second := filepath.Join(t.TempDir(), "second")
	release := filepath.Join(t.TempDir(), "release")
	pools := &fakeGatePools{stop: make(chan struct{})}
	started := make(chan string, 2)

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
			Argv: []string{
				"/bin/sh",
				"-c",
				`if [ -e "$FIRST" ]; then echo 2 > "$SECOND"; until [ -e "$RELEASE" ]; do sleep 0.01; done; ` +
					`else echo 1 > "$FIRST"; sleep 60; fi; exit 0`,
			},
			Dir:      t.TempDir(),
			Limit:    time.Hour,
			StateDir: gateShortStateDir(t),
			Environ:  gateEnv("FIRST="+first, "SECOND="+second, "RELEASE="+release),
			Started: func(_ context.Context, _, dir string) error {
				started <- dir
				return nil
			},
		})
		done <- outcome{result, err}
	}()

	firstDir := gateWaitStarted(t, started)
	ended, doneResult := waittest.Closed(done)
	gateWaitFile(t, first, ended)
	if got := gateHolderLimit(t, firstDir); got != "3600" {
		t.Fatalf("first attempt's holder --gate-limit = %s, want the whole one-hour Limit", got)
	}
	pools.stopGrant()

	rerunDir := gateWaitStarted(t, started)
	gateWaitFile(t, second, ended)
	if rerunDir == firstDir {
		t.Fatalf("the rerun ran under the first attempt's holder directory %s, want a holder of its own", rerunDir)
	}
	if got := gateHolderLimit(t, rerunDir); got != "3600" {
		t.Fatalf("rerun's holder --gate-limit = %s, want the whole one-hour Limit", got)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the rerun: %v", err)
	}

	got := <-doneResult
	if got.err != nil {
		t.Fatalf("runGrantedGate: %v", got.err)
	}
	if got.result.TimedOut {
		t.Fatal("TimedOut = true: the first attempt's run time counted toward the rerun's Limit")
	}
	if got.result.Exit != 0 {
		t.Fatalf("Exit = %d, want 0", got.result.Exit)
	}
	requests := pools.requested()
	if len(requests) != 2 {
		t.Fatalf("Acquire was called %d times, want 2", len(requests))
	}
	if requests[1].GrantID != "grant-1" {
		t.Fatalf("rerun GrantID = %q, want the stopped grant %q", requests[1].GrantID, "grant-1")
	}
}

func TestGrantedGateStopKillsGroupAndReclaimsItsGrant(t *testing.T) {
	t.Parallel()
	first := filepath.Join(t.TempDir(), "first")
	pids := filepath.Join(t.TempDir(), "pids")
	pools := &fakeGatePools{stop: make(chan struct{})}

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
			Argv: []string{
				"/bin/sh",
				"-c",
				`if [ -e "$FIRST" ]; then echo rerun; exit 7; fi; echo 1 > "$FIRST"; sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`,
			},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Environ: gateEnv("FIRST="+first, "PIDS="+pids),
		})
		done <- outcome{result, err}
	}()

	ended, doneResult := waittest.Closed(done)
	group, child := gateParsePids(t, pids, ended)
	pools.stopGrant()

	got := <-doneResult
	if got.err != nil {
		t.Fatalf("runGrantedGate: %v", got.err)
	}
	gateWaitGone(t, group, child)
	if got.result.Exit != 7 {
		t.Fatalf("Exit = %d, want the rerun's 7: the stopped attempt must yield no result", got.result.Exit)
	}
	if !strings.Contains(got.result.Stdout, "rerun\n") {
		t.Fatalf("Stdout %q does not show the rerun", got.result.Stdout)
	}
	requests := pools.requested()
	if len(requests) != 2 {
		t.Fatalf("Acquire was called %d times, want 2", len(requests))
	}
	if requests[1].GrantID != "grant-1" {
		t.Fatalf("rerun GrantID = %q, want the stopped grant %q", requests[1].GrantID, "grant-1")
	}
}

func TestGrantedGateCancelKillsGroupAndErrors(t *testing.T) {
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pools := &fakeGatePools{onEnd: gateGroupGoneAtEnd(t, pids)}

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(ctx, pools, gateGrantRequest(), gateRun{
			Argv:    []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Environ: gateEnv("PIDS=" + pids),
		})
		done <- outcome{result, err}
	}()

	ended, doneResult := waittest.Closed(done)
	group, child := gateParsePids(t, pids, ended)
	cancel()
	got := <-doneResult
	if got.err == nil {
		t.Fatalf("runGrantedGate returned no error after its context ended: %+v", got.result)
	}
	gateWaitGone(t, group, child)
}

func TestGrantedGateKillsGateWhenItsGrantEndsEarly(t *testing.T) {
	t.Parallel()
	pids := filepath.Join(t.TempDir(), "pids")
	errBroker := errors.New("broker closed")
	pools := &fakeGatePools{drop: make(chan error)}

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
			Argv:    []string{"/bin/sh", "-c", `sleep 60 & echo "$$ $!" > "$PIDS"; sleep 60`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Environ: gateEnv("PIDS=" + pids),
		})
		done <- outcome{result, err}
	}()

	ended, doneResult := waittest.Closed(done)
	group, child := gateParsePids(t, pids, ended)
	pools.drop <- errBroker
	got := <-doneResult
	if !errors.Is(got.err, errGateUngranted) || !errors.Is(got.err, errBroker) {
		t.Fatalf("err = %v, want errGateUngranted wrapping the grant's error", got.err)
	}
	gateWaitGone(t, group, child)
}

func TestGrantedGateErrorsWhenAcquireFails(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "started")
	refused := errors.New("pools refused the request")
	pools := &fakeGatePools{err: refused}

	_, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `touch "$MARKER"`},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Environ: gateEnv("MARKER=" + marker),
	})
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the Acquire error", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("gate ran although its grant was refused")
	}
}

func TestGrantedGateErrorsWithoutPools(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "started")

	_, err := runGrantedGate(t.Context(), nil, gateGrantRequest(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `touch "$MARKER"`},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Environ: gateEnv("MARKER=" + marker),
	})
	if !errors.Is(err, errGateNoPools) {
		t.Fatalf("err = %v, want errGateNoPools", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("gate ran without pools")
	}
}

func TestGrantedGateErrorsWhenCtxEndsBeforeGrant(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "started")
	pools := &fakeGatePools{queue: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	_, err := runGrantedGate(ctx, pools, gateGrantRequest(), gateRun{
		Argv:    []string{"/bin/sh", "-c", `touch "$MARKER"`},
		Dir:     t.TempDir(),
		Limit:   time.Minute,
		Environ: gateEnv("MARKER=" + marker),
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the context's deadline", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("gate ran before its grant")
	}
}

func TestGrantedGateNeverStartsOnAGrantThatEndedAsItArrived(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// The fake grants at once and, its ctx already ended, returns right after the grant: the grant has ended by the
	// time the gate could start. Each round gives the select a fresh chance to pick the grant first.
	for range 50 {
		_, err := runGrantedGate(ctx, &fakeGatePools{}, gateGrantRequest(), gateRun{
			Argv:    []string{"/bin/sh", "-c", `touch "$MARKER"`},
			Dir:     t.TempDir(),
			Limit:   time.Minute,
			Environ: gateEnv("MARKER=" + marker),
		})
		if err == nil {
			t.Fatal("runGrantedGate returned no error for a grant that ended as it arrived")
		}
		if _, statErr := os.Stat(marker); statErr == nil {
			t.Fatal("gate started on a grant that had already ended")
		}
	}
}
