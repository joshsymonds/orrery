package enginehost

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/holder"
)

// gateWaitLockFree polls until the session lock in dir is free, which it is once the holder that served dir has ended.
// The holder writes its exit record before it releases the lock, so a free lock means the record is there to read.
func gateWaitLockFree(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, holder.LockName)
	deadline := time.Now().Add(10 * time.Second)
	for {
		lock, err := holder.Lock(path)
		if err == nil {
			if closeErr := lock.Close(); closeErr != nil {
				t.Fatalf("release lock %s: %v", path, closeErr)
			}
			return
		}
		if !errors.Is(err, holder.ErrLocked) {
			t.Fatalf("take lock %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the holder in %s still holds its lock", dir)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// gateWaitRequests polls until pools has been asked for at least n grants.
func gateWaitRequests(t *testing.T, pools *fakeGatePools, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(pools.requested()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("pools were asked for %d grants, want %d", len(pools.requested()), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// gateReadoptRun returns the run a re-adoption test gives its gate, named name in stateDir. The gate counts its starts
// in counter, waits until release exists, then prints done and exits 3. The cleanup writes release, so a failed test
// does not leave the gate running.
func gateReadoptRun(t *testing.T, stateDir, name, counter, release string) gateRun {
	t.Helper()
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	return gateRun{
		Argv: []string{"/bin/sh", "-c",
			`n=$(cat "$COUNTER" 2>/dev/null || echo 0); echo $((n + 1)) > "$COUNTER"; ` +
				`while [ ! -e "$RELEASE" ]; do sleep 0.05; done; echo done; exit 3`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Name:     name,
		StateDir: stateDir,
		Environ:  gateEnv("COUNTER="+counter, "RELEASE="+release),
	}
}

// gateDetachedRun runs run as a granted gate on pools, detaches it once the started file exists, and returns when the
// detach has returned. The gate keeps running under its holder with its grant.
func gateDetachedRun(t *testing.T, pools *fakeGatePools, run gateRun, started string) {
	t.Helper()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	done := make(chan error, 1)
	go func() {
		_, err := runGrantedGate(ctx, pools, gateGrantRequest(), run)
		done <- err
	}()
	gateWaitFile(t, started)
	cancel(errGateDetach)
	select {
	case err := <-done:
		if !errors.Is(err, errGateDetach) {
			t.Fatalf("runGrantedGate err = %v, want errGateDetach", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runGrantedGate did not return after the detach")
	}
}

func TestGateReadoptRunsDetachedGateOnceAndHoldsItsReclaimedGrant(t *testing.T) {
	stateDir := gateShortStateDir(t)
	counter := filepath.Join(t.TempDir(), "counter")
	release := filepath.Join(t.TempDir(), "release")
	released := make(chan struct{})
	pools := &fakeGatePools{onEnd: func() { close(released) }}
	run := gateReadoptRun(t, stateDir, "readopted", counter, release)
	gateDetachedRun(t, pools, run, counter)

	type outcome struct {
		result gateResult
		err    error
	}
	readopted := make(chan outcome, 1)
	go func() {
		result, err := readoptGrantedGate(t.Context(), pools, gateGrantRequest(), run, "grant-1")
		readopted <- outcome{result, err}
	}()
	gateWaitRequests(t, pools, 2)
	time.Sleep(200 * time.Millisecond)
	select {
	case <-released:
		t.Fatal("the reclaimed grant ended while its gate still ran")
	default:
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	var got outcome
	select {
	case got = <-readopted:
	case <-time.After(10 * time.Second):
		t.Fatal("readoptGrantedGate did not return after the gate exited")
	}
	if got.err != nil {
		t.Fatalf("readoptGrantedGate: %v", got.err)
	}
	if got.result.Exit != 3 || !strings.Contains(got.result.Output, "done\n") {
		t.Fatalf("result = %+v, want the gate's exit 3 and its output", got.result)
	}
	if want := []grants.Member{{Pool: "gpu", Name: "gpu-1"}}; !slices.Equal(got.result.Members, want) {
		t.Fatalf("Members = %v, want the reclaimed %v", got.result.Members, want)
	}
	dir := filepath.Join(stateDir, "holders", "readopted")
	if got.result.Holder != dir {
		t.Fatalf("Holder = %q, want %q", got.result.Holder, dir)
	}
	select {
	case <-released:
	default:
		t.Fatal("the reclaimed grant was not released once the gate exited")
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("counter = %q, %v: want the gate started once", data, err)
	}
	requests := pools.requested()
	if len(requests) != 2 {
		t.Fatalf("Acquire was called %d times, want 2", len(requests))
	}
	if requests[1].GrantID != "grant-1" || requests[1].Holder != dir {
		t.Fatalf("reclaim request GrantID = %q, Holder = %q, want grant-1 and %q",
			requests[1].GrantID, requests[1].Holder, dir)
	}
}

func TestGateReadoptOfGateThatEndedWhileDetachedReturnsItsExitRecord(t *testing.T) {
	stateDir := gateShortStateDir(t)
	counter := filepath.Join(t.TempDir(), "counter")
	release := filepath.Join(t.TempDir(), "release")
	pools := &fakeGatePools{}
	run := gateReadoptRun(t, stateDir, "ended", counter, release)
	gateDetachedRun(t, pools, run, counter)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	dir := filepath.Join(stateDir, "holders", "ended")
	gateWaitExit(t, dir)
	gateWaitLockFree(t, dir)

	result, err := readoptGrantedGate(t.Context(), pools, gateGrantRequest(), run, "grant-1")
	if err != nil {
		t.Fatalf("readoptGrantedGate: %v", err)
	}
	if result.Exit != 3 || !strings.Contains(result.Output, "done\n") {
		t.Fatalf("result = %+v, want the gate's exit 3 and its output", result)
	}
	if result.Holder != dir {
		t.Fatalf("Holder = %q, want %q", result.Holder, dir)
	}
	// The gate needs no new grant: the second Acquire reclaims the grant it held, naming its holder, and ends it.
	requests := pools.requested()
	if len(requests) != 2 {
		t.Fatalf("Acquire was called %d times, want 2: the run and the reclaim that ends its grant", len(requests))
	}
	if got := requests[1]; got.GrantID != "grant-1" || got.Holder != dir {
		t.Fatalf("second Acquire = grant %q holder %q, want a reclaim of grant-1 naming %s", got.GrantID, got.Holder, dir)
	}
	if !pools.ended("grant-1") {
		t.Fatal("the reclaimed grant-1 is still held after readoptGrantedGate returned, want it ended")
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("counter = %q, %v: want the gate started once", data, err)
	}
}

func TestGateReadoptOfStoppedGateRunsItOnce(t *testing.T) {
	stateDir := gateShortStateDir(t)
	counter := filepath.Join(t.TempDir(), "counter")
	pools := &fakeGatePools{}
	run := gateRun{
		Argv: []string{"/bin/sh", "-c",
			`n=$(cat "$COUNTER" 2>/dev/null || echo 0); n=$((n + 1)); echo $n > "$COUNTER"; ` +
				`if [ $n -eq 1 ]; then sleep 60; fi; echo done; exit 3`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Name:     "stopped",
		StateDir: stateDir,
		Environ:  gateEnv("COUNTER=" + counter),
	}
	gateDetachedRun(t, pools, run, counter)
	dir := filepath.Join(stateDir, "holders", "stopped")
	if err := holder.Stop(t.Context(), dir); err != nil {
		t.Fatalf("stop the detached holder: %v", err)
	}

	result, err := readoptGrantedGate(t.Context(), pools, gateGrantRequest(), run, "grant-1")
	if err != nil {
		t.Fatalf("readoptGrantedGate: %v", err)
	}
	if result.Exit != 3 || !strings.Contains(result.Output, "done\n") {
		t.Fatalf("result = %+v, want the rerun's exit 3 and its output", result)
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.TrimSpace(string(data)) != "2" {
		t.Fatalf("counter = %q, %v: want the gate started twice, once and its one rerun", data, err)
	}
	requests := pools.requested()
	if len(requests) != 2 {
		t.Fatalf("Acquire was called %d times, want 2", len(requests))
	}
	if requests[1].GrantID != "grant-1" {
		t.Fatalf("rerun GrantID = %q, want the stopped grant %q", requests[1].GrantID, "grant-1")
	}
}

func TestGateReadoptWithoutExitRecordIsLostAndRunsNothing(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	pools := &fakeGatePools{}

	_, err := readoptGrantedGate(t.Context(), pools, gateGrantRequest(), gateRun{
		Argv:     []string{"/bin/sh", "-c", `touch "$MARKER"`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Name:     "missing",
		StateDir: gateShortStateDir(t),
		Environ:  gateEnv("MARKER=" + marker),
	}, "grant-1")
	if !errors.Is(err, errGateLost) {
		t.Fatalf("err = %v, want errGateLost", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the gate ran although no record says how it ended")
	}
	if n := len(pools.requested()); n != 0 {
		t.Fatalf("Acquire was called %d times, want none", n)
	}
}

func TestGateReadoptRefusedReclaimEndsTheLiveGate(t *testing.T) {
	stateDir := gateShortStateDir(t)
	counter := filepath.Join(t.TempDir(), "counter")
	release := filepath.Join(t.TempDir(), "release")
	refused := errors.New("pools refused the reclaim")
	pools := &fakeGatePools{}
	run := gateReadoptRun(t, stateDir, "refused", counter, release)
	gateDetachedRun(t, pools, run, counter)
	pools.err = refused

	// grant-9 is a grant the pools no longer hold, so the reclaim is refused.
	_, err := readoptGrantedGate(t.Context(), pools, gateGrantRequest(), run, "grant-9")
	if !errors.Is(err, errGateLost) {
		t.Fatalf("err = %v, want errGateLost", err)
	}
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the pools' refusal", err)
	}
	dir := filepath.Join(stateDir, "holders", "refused")
	if _, statErr := os.Stat(dir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("holder directory %s after a refused reclaim: stat error %v, want not exist", dir, statErr)
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("counter = %q, %v: want the gate started once", data, err)
	}
}

func TestGateReadoptOfUnattachableHolderStopsItAndIsLost(t *testing.T) {
	stateDir := gateShortStateDir(t)
	counter := filepath.Join(t.TempDir(), "counter")
	release := filepath.Join(t.TempDir(), "release")
	pools := &fakeGatePools{}
	run := gateReadoptRun(t, stateDir, "unattachable", counter, release)
	gateDetachedRun(t, pools, run, counter)
	dir := filepath.Join(stateDir, "holders", "unattachable")
	// The holder is alive, so its lock is held, but its socket is gone and no controller can attach to it.
	if err := os.Remove(filepath.Join(dir, holder.SocketName)); err != nil {
		t.Fatalf("remove the holder's socket: %v", err)
	}

	_, err := readoptGrantedGate(t.Context(), pools, gateGrantRequest(), run, "grant-1")
	if !errors.Is(err, errGateLost) {
		t.Fatalf("err = %v, want errGateLost", err)
	}
	if !errors.Is(err, holder.ErrUnattachable) {
		t.Fatalf("err = %v, want the attach failure", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("holder directory %s after an unattachable holder: stat error %v, want not exist", dir, statErr)
	}
	if n := len(pools.requested()); n != 1 {
		t.Fatalf("Acquire was called %d times, want 1: a lost gate reclaims no grant", n)
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("counter = %q, %v: want the gate started once", data, err)
	}
}

func TestGateReadoptOfEndingHolderReturnsItsExitRecord(t *testing.T) {
	stateDir := gateShortStateDir(t)
	counter := filepath.Join(t.TempDir(), "counter")
	release := filepath.Join(t.TempDir(), "release")
	pools := &fakeGatePools{}
	run := gateReadoptRun(t, stateDir, "ending", counter, release)
	gateDetachedRun(t, pools, run, counter)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	dir := filepath.Join(stateDir, "holders", "ending")
	gateWaitExit(t, dir)
	gateWaitLockFree(t, dir)
	// An ending holder has written its record and removed its socket, and still holds its lock. Holding the lock here
	// puts the directory in that state.
	lock, err := holder.Lock(filepath.Join(dir, holder.LockName))
	if err != nil {
		t.Fatalf("hold the holder's lock: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := lock.Close(); closeErr != nil {
			t.Errorf("free the holder's lock: %v", closeErr)
		}
	})

	result, err := readoptGrantedGate(t.Context(), pools, gateGrantRequest(), run, "grant-1")
	if err != nil {
		t.Fatalf("readoptGrantedGate of an ending holder: %v, want its exit record", err)
	}
	if result.Exit != 3 || !strings.Contains(result.Output, "done\n") {
		t.Fatalf("result = %+v, want the gate's exit 3 and its output", result)
	}
	requests := pools.requested()
	if len(requests) != 2 || requests[1].GrantID != "grant-1" {
		t.Fatalf("Acquire requests = %d, want the run and a reclaim of grant-1 that ends it", len(requests))
	}
	if !pools.ended("grant-1") {
		t.Fatal("the reclaimed grant-1 is still held after readoptGrantedGate returned, want it ended")
	}
	data, err := os.ReadFile(counter)
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		t.Fatalf("counter = %q, %v: want the gate started once", data, err)
	}
}

func TestGateReadoptOfIdleHolderIsLost(t *testing.T) {
	stateDir := gateShortStateDir(t)
	marker := filepath.Join(t.TempDir(), "started")
	pools := &fakeGatePools{}
	run := gateRun{
		Argv:     []string{"/bin/sh", "-c", `touch "$MARKER"`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Name:     "idle",
		StateDir: stateDir,
		Environ:  gateEnv("MARKER=" + marker),
	}
	// A holder the idle timeout ended left its spool and its record, and no lock. The record names no end of the gate.
	dir := filepath.Join(stateDir, "holders", "idle")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create the holder directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stdout-00000000000000000000"), nil, 0o600); err != nil {
		t.Fatalf("write the spool: %v", err)
	}
	record := filepath.Join(dir, holder.ExitName)
	if err := holder.WriteExit(record, holder.Exit{Reason: holder.ReasonIdle, Code: -1}); err != nil {
		t.Fatalf("write the exit record: %v", err)
	}
	if _, err := holder.OpenGone(dir, 0); err != nil {
		t.Fatalf("the fixture is not a holder's end: %v", err)
	}

	_, err := readoptGrantedGate(t.Context(), pools, gateGrantRequest(), run, "grant-1")
	if !errors.Is(err, errGateLost) {
		t.Fatalf("err = %v, want errGateLost", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the gate ran after its holder idled")
	}
	if n := len(pools.requested()); n != 0 {
		t.Fatalf("Acquire was called %d times, want none", n)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("holder directory %s of a lost gate: stat error %v, want not exist", dir, statErr)
	}
}

func TestGateReadoptOfGateKilledAtItsLimitReturnsTimedOut(t *testing.T) {
	stateDir := gateShortStateDir(t)
	counter := filepath.Join(t.TempDir(), "counter")
	pools := &fakeGatePools{}
	run := gateRun{
		Argv:     []string{"/bin/sh", "-c", `echo 1 > "$COUNTER"; sleep 60`},
		Dir:      t.TempDir(),
		Limit:    time.Second,
		Name:     "limited",
		StateDir: stateDir,
		Environ:  gateEnv("COUNTER=" + counter),
	}
	gateDetachedRun(t, pools, run, counter)
	dir := filepath.Join(stateDir, "holders", "limited")
	ended := gateWaitExit(t, dir)
	gateWaitLockFree(t, dir)
	if ended.Reason != holder.ReasonLimit {
		t.Fatalf("exit record reason = %q, want %q", ended.Reason, holder.ReasonLimit)
	}

	result, err := readoptGrantedGate(t.Context(), pools, gateGrantRequest(), run, "grant-1")
	if err != nil {
		t.Fatalf("readoptGrantedGate: %v", err)
	}
	if !result.TimedOut {
		t.Fatal("TimedOut = false for a gate killed at its limit")
	}
	if result.Holder != dir {
		t.Fatalf("Holder = %q, want %q", result.Holder, dir)
	}
	requests := pools.requested()
	if len(requests) != 2 || requests[1].GrantID != "grant-1" {
		t.Fatalf("Acquire requests = %d, want the run and a reclaim of grant-1 that ends it", len(requests))
	}
	if !pools.ended("grant-1") {
		t.Fatal("the reclaimed grant-1 is still held after readoptGrantedGate returned, want it ended")
	}
}

func TestGateReadoptErrorsWithoutPools(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")

	_, err := readoptGrantedGate(t.Context(), nil, gateGrantRequest(), gateRun{
		Argv:     []string{"/bin/sh", "-c", `touch "$MARKER"`},
		Dir:      t.TempDir(),
		Limit:    time.Minute,
		Name:     "nopools",
		StateDir: gateShortStateDir(t),
		Environ:  gateEnv("MARKER=" + marker),
	}, "grant-1")
	if !errors.Is(err, errGateNoPools) {
		t.Fatalf("err = %v, want errGateNoPools", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the gate ran without pools")
	}
}
