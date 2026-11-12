package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/grants"
)

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
		deadline := time.Now().Add(5 * time.Second)
		for _, field := range fields {
			pid, err := strconv.Atoi(field)
			if err != nil {
				t.Errorf("pid %q: %v", field, err)
				return
			}
			for !gateProcessGone(pid) && time.Now().Before(deadline) {
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
	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("gate never finished after its grant")
	}
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
	queue := make(chan struct{})
	pools := &fakeGatePools{queue: queue}
	time.AfterFunc(1500*time.Millisecond, func() { close(queue) })

	started := time.Now()
	result, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
		Argv:    []string{"/bin/sh", "-c", "exit 0"},
		Dir:     t.TempDir(),
		Limit:   time.Second,
		Environ: gateEnv(),
	})
	if err != nil {
		t.Fatalf("runGrantedGate: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 1500*time.Millisecond {
		t.Fatalf("runGrantedGate returned after %v, before the grant it queued for", elapsed)
	}
	if result.TimedOut {
		t.Fatal("TimedOut = true: the time spent queued counted toward Limit")
	}
	if result.Exit != 0 {
		t.Fatalf("Exit = %d, want 0", result.Exit)
	}
}

func TestGrantedGateEachAttemptGetsItsOwnLimit(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first")
	pools := &fakeGatePools{stop: make(chan struct{})}

	type outcome struct {
		result gateResult
		err    error
	}
	done := make(chan outcome, 1)
	started := time.Now()
	go func() {
		result, err := runGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
			Argv: []string{
				"/bin/sh",
				"-c",
				`if [ -e "$FIRST" ]; then sleep 0.6; else echo 1 > "$FIRST"; sleep 60; fi; exit 0`,
			},
			Dir:     t.TempDir(),
			Limit:   time.Second,
			Environ: gateEnv("FIRST=" + first),
		})
		done <- outcome{result, err}
	}()

	gateWaitFile(t, first)
	time.Sleep(600 * time.Millisecond)
	pools.stopGrant()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate never returned after the stop")
	}
	if got.err != nil {
		t.Fatalf("runGrantedGate: %v", got.err)
	}
	if elapsed := time.Since(started); elapsed < 1200*time.Millisecond {
		t.Fatalf("runGrantedGate returned after %v: the two attempts must run past one Limit together", elapsed)
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

	group, child := gateParsePids(t, pids)
	pools.stopGrant()

	var got outcome
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate never returned after the stop")
	}
	if got.err != nil {
		t.Fatalf("runGrantedGate: %v", got.err)
	}
	gateWaitGone(t, group, child)
	if got.result.Exit != 7 {
		t.Fatalf("Exit = %d, want the rerun's 7: the stopped attempt must yield no result", got.result.Exit)
	}
	if !strings.Contains(got.result.Output, "rerun\n") {
		t.Fatalf("Output %q does not show the rerun", got.result.Output)
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

	group, child := gateParsePids(t, pids)
	cancel()
	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("runGrantedGate returned no error after its context ended: %+v", got.result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate did not return after its context ended")
	}
	gateWaitGone(t, group, child)
}

func TestGrantedGateKillsGateWhenItsGrantEndsEarly(t *testing.T) {
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

	group, child := gateParsePids(t, pids)
	pools.drop <- errBroker
	select {
	case got := <-done:
		if !errors.Is(got.err, errGateUngranted) || !errors.Is(got.err, errBroker) {
			t.Fatalf("err = %v, want errGateUngranted wrapping the grant's error", got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate did not return after its grant ended")
	}
	gateWaitGone(t, group, child)
}

func TestGrantedGateErrorsWhenAcquireFails(t *testing.T) {
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
