package enginehost

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// errEndRefused is the error a refusingEndStore refuses an end with, as a store Litestream holds refuses a write.
var errEndRefused = errors.New("begin store write: database is locked (5) (SQLITE_BUSY)")

// refusingEndStore refuses the first refusals writes of a check's end with errEndRefused and passes the rest through.
// It reports each try's error on tries.
type refusingEndStore struct {
	NodeCheckStore
	tries chan error

	mu       sync.Mutex
	refusals int
}

// EndNodeCheck refuses the end while refusals remain, else stores it, and reports the try's error on tries.
func (s *refusingEndStore) EndNodeCheck(ctx context.Context, id string, end store.NodeCheckEnd, at time.Time) error {
	s.mu.Lock()
	refuse := s.refusals > 0
	if refuse {
		s.refusals--
	}
	s.mu.Unlock()
	err := errEndRefused
	if !refuse {
		err = s.NodeCheckStore.EndNodeCheck(ctx, id, end, at)
	}
	s.tries <- err
	return err
}

// endRetryClock is the After of a NodeChecks under test. Each wait it is asked for is reported on waits, and every wait
// ends when the test sends on tick.
type endRetryClock struct {
	waits chan time.Duration
	tick  chan time.Time
}

// after reports d on waits and returns the tick channel.
func (c endRetryClock) after(d time.Duration) <-chan time.Time {
	c.waits <- d
	return c.tick
}

// refuseEnds makes f's NodeChecks write each end through a store that refuses the first refusals, and wait on the
// returned clock between tries. It runs before the check under test starts.
func refuseEnds(f nodeCheckFixture, refusals int) (*refusingEndStore, endRetryClock) {
	refusing := &refusingEndStore{NodeCheckStore: f.checks.store, tries: make(chan error, 8), refusals: refusals}
	clock := endRetryClock{waits: make(chan time.Duration, 8), tick: make(chan time.Time)}
	f.checks.store = refusing
	f.checks.after = clock.after
	return refusing, clock
}

// wantRefusedEnd waits for a try of the end that the store refused, then for the wait before the next try.
func wantRefusedEnd(t *testing.T, refusing *refusingEndStore, clock endRetryClock) {
	t.Helper()
	if err := <-refusing.tries; !errors.Is(err, errEndRefused) {
		t.Fatalf("end try = %v, want the store's refusal", err)
	}
	if d := <-clock.waits; d != storeRetry {
		t.Fatalf("wait before the next try = %v, want %v", d, storeRetry)
	}
}

// TestNodeChecksRetriesARefusedEndUntilTheStoreTakesIt runs a check whose end the store refuses twice. The check stays
// running in the store while its end is refused, each refusal waits storeRetry for the next try, and the third
// try stores the end the gate ran to.
func TestNodeChecksRetriesARefusedEndUntilTheStoreTakesIt(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	refusing, clock := refuseEnds(f, 2)
	if _, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 60, "/bin/sh", "-c", "echo out; exit 4")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for range 2 {
		wantRefusedEnd(t, refusing, clock)
		if got := nodeCheckStored(t, f.st); got.State != store.NodeRunning {
			t.Fatalf("stored check while its end is refused = %+v, want running", got)
		}
		clock.tick <- time.Time{}
	}
	if err := <-refusing.tries; err != nil {
		t.Fatalf("third end try = %v, want stored", err)
	}
	nodeCheckEnded(t, f.ended)
	got := nodeCheckStored(t, f.st)
	if got.State != store.NodeEnded || got.Exit != 4 || got.Stdout != "out\n" || got.NotRun != "" {
		t.Fatalf("stored check = %+v, want ended with exit 4 and stdout %q", got, "out\n")
	}
}

// TestNodeChecksLeavesAnEndUnstoredAtCloseForResumeToEndLost runs a check whose end the store refuses every time. Close
// stops the retries after one last try, and leaves the check running with the grant it ran under. A restarted runner's
// Resume then ends it lost at a restart, the end a check that runs again gets, without running its gate again.
func TestNodeChecksLeavesAnEndUnstoredAtCloseForResumeToEndLost(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	refusing, clock := refuseEnds(f, 1000)
	if _, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 60, "/bin/sh", "-c", "exit 0")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantRefusedEnd(t, refusing, clock)

	f.checks.Close()
	if err := <-refusing.tries; !errors.Is(err, errEndRefused) {
		t.Fatalf("last end try at Close = %v, want the store's refusal", err)
	}
	if n := len(refusing.tries); n != 0 {
		t.Fatalf("end tries after the last one at Close = %d, want none", n)
	}
	left := nodeCheckStored(t, f.st)
	if left.State != store.NodeRunning || left.Grant != "grant-1" {
		t.Fatalf("stored check after Close = %+v, want running under grant-1", left)
	}
	granted := len(f.pools.requested())

	ended := make(chan error, 1)
	restarted := NewNodeChecks(NodeChecksConfig{
		Store:        nodeCheckStore{Store: f.st, ended: ended},
		Pools:        f.pools,
		Environ:      f.checks.environ,
		StateDir:     filepath.Dir(f.checks.worktrees),
		GateStateDir: f.checks.gateStateDir,
	})
	t.Cleanup(restarted.Close)
	if err := restarted.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	nodeCheckEnded(t, ended)
	if got := nodeCheckStored(t, f.st); got.State != store.NodeEnded || got.NotRun != store.LostAtRestart {
		t.Fatalf("stored check after Resume = %+v, want ended %q", got, store.LostAtRestart)
	}
	if n := len(f.pools.requested()); n != granted {
		t.Fatalf("grant requests after Resume = %d, want %d: the lost gate is not run again", n, granted)
	}
}

// refusingGrantStore refuses the first record of a check's grant with errEndRefused, signaling refused, and passes the
// rest through.
type refusingGrantStore struct {
	NodeCheckStore
	refused chan struct{}
	once    sync.Once
}

// SetNodeCheckGrant refuses its first try and passes the rest through.
func (s *refusingGrantStore) SetNodeCheckGrant(ctx context.Context, id, grant, holder string) error {
	refuse := false
	s.once.Do(func() { refuse = true })
	if refuse {
		s.refused <- struct{}{}
		return errEndRefused
	}
	return s.NodeCheckStore.SetNodeCheckGrant(ctx, id, grant, holder)
}

// TestNodeChecksRetriesARefusedGrantRecordAndRunsTheGate runs a check whose grant record the store refuses once. The
// record waits storeRetry and is tried again, the gate then launches, and the check ends with its command's exit
// under the grant it recorded.
func TestNodeChecksRetriesARefusedGrantRecordAndRunsTheGate(t *testing.T) {
	t.Parallel()
	f := nodeCheckSetup(t)
	refusing := &refusingGrantStore{NodeCheckStore: f.checks.store, refused: make(chan struct{}, 1)}
	clock := endRetryClock{waits: make(chan time.Duration, 1), tick: make(chan time.Time)}
	f.checks.store = refusing
	f.checks.after = clock.after
	if _, err := f.checks.Run(t.Context(), nodeCheckOf(f.head, 60, "/bin/sh", "-c", "exit 5")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	<-refusing.refused
	if d := <-clock.waits; d != storeRetry {
		t.Fatalf("wait before the next grant record = %v, want %v", d, storeRetry)
	}
	clock.tick <- time.Time{}
	nodeCheckEnded(t, f.ended)
	got := nodeCheckStored(t, f.st)
	if got.State != store.NodeEnded || got.Exit != 5 || got.NotRun != "" || got.Grant != "grant-1" {
		t.Fatalf("stored check = %+v, want ended with exit 5 under grant-1", got)
	}
}
