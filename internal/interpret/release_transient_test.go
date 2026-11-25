package interpret

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// expectedFlush is the failure git's ls-remote gave three epics' releases on 2026-11-24, which stopped each of them.
const expectedFlush = "git ls-remote --symref origin HEAD: exit status 128: fatal: expected flush after ref listing"

// flakyHost is a Host whose first failures runs of git ls-remote fail with failure, and which runs every other git call,
// and every later ls-remote, on its Host. It counts the ls-remote runs.
type flakyHost struct {
	hostexec.Host
	failures int
	failure  string

	mu        sync.Mutex
	lsRemotes int
}

// Git fails the call with failure when it is one of the first failures runs of ls-remote, and runs it on the Host
// otherwise.
func (h *flakyHost) Git(ctx context.Context, call hostexec.GitCall) ([]byte, error) {
	if len(call.Args) > 0 && call.Args[0] == "ls-remote" {
		h.mu.Lock()
		h.lsRemotes++
		fail := h.lsRemotes <= h.failures
		h.mu.Unlock()
		if fail {
			return nil, errors.New(h.failure)
		}
	}
	out, err := h.Host.Git(ctx, call)
	if err != nil {
		return out, fmt.Errorf("flaky host: %w", err)
	}
	return out, nil
}

// runs returns how many times ls-remote ran.
func (h *flakyHost) runs() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lsRemotes
}

// passedWaits is the afterFunc of the transient retry tests: it records each wait and lets it pass at once, so a retry
// waits on no clock.
type passedWaits struct {
	mu    sync.Mutex
	waits []time.Duration
}

// afterFunc records d and runs f at once.
func (w *passedWaits) afterFunc(d time.Duration, f func()) {
	w.mu.Lock()
	w.waits = append(w.waits, d)
	w.mu.Unlock()
	f()
}

// recorded returns the waits recorded so far.
func (w *passedWaits) recorded() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.waits...)
}

// newFlakyRelease returns a release world whose epic lives on host, a flakyHost over the local host whose first failures
// ls-remote runs fail with failure, and whose Runner's waits are waits. origin's main is an ancestor of the epic's tip,
// which it returns.
func newFlakyRelease(t *testing.T, failures int, failure string) (*releaseWorld, *flakyHost, *passedWaits, string) {
	t.Helper()
	w := newMergeWorld(t)
	mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, "epic.txt")
	if err := w.s.AdoptHost(t.Context(), "flaky"); err != nil {
		t.Fatalf("AdoptHost: %v", err)
	}
	host := &flakyHost{Host: hostexec.NewLocal(), failures: failures, failure: failure}
	cfg := nodeConfig(w.s, w.fakes)
	cfg.Hosts = hostexec.Map{"flaky": host}
	w.runner = New(cfg)
	waits := &passedWaits{}
	w.runner.afterFunc = waits.afterFunc
	return w, host, waits, tip
}

// TestReleaseTransientLsRemoteFailureIsRetried checks that a release prepare whose ls-remote fails twice with git's
// expected-flush failure runs it again after one second, then two, and decides the release from the third run.
func TestReleaseTransientLsRemoteFailureIsRetried(t *testing.T) {
	t.Parallel()
	w, host, waits, tip := newFlakyRelease(t, 2, expectedFlush)

	result, err := w.runner.decideRelease(t.Context(), epicOf(t, w.s))
	if err != nil {
		t.Fatalf("decideRelease: %v", err)
	}
	if result.Candidate != tip {
		t.Errorf("decideRelease = %+v, want the tip %s as the candidate", result, tip)
	}
	if n := host.runs(); n != 3 {
		t.Errorf("ls-remote runs = %d, want 3: two failures and the run that answered", n)
	}
	if got, want := waits.recorded(), []time.Duration{time.Second, 2 * time.Second}; !reflect.DeepEqual(got, want) {
		t.Errorf("waits = %v, want %v", got, want)
	}
}

// TestReleaseTransientFailureThatLastsStopsAfterFourRuns checks that a candidate check whose ls-remote keeps failing with
// git's expected-flush failure runs it four times, waiting one, two and four seconds between, and then fails with that
// failure and how many times it ran.
func TestReleaseTransientFailureThatLastsStopsAfterFourRuns(t *testing.T) {
	t.Parallel()
	w, host, waits, tip := newFlakyRelease(t, 10, expectedFlush)
	flaky, err := w.runner.hostOf(epicOf(t, w.s))
	if err != nil {
		t.Fatalf("hostOf: %v", err)
	}

	_, err = w.runner.candidateContainsOrigin(t.Context(), flaky, epicOf(t, w.s), tip)
	if err == nil || !strings.Contains(err.Error(), "expected flush") || !strings.Contains(err.Error(), "run 4 times") {
		t.Errorf("candidateContainsOrigin error = %v, want the expected-flush failure after 4 runs", err)
	}
	if n := host.runs(); n != remoteTries {
		t.Errorf("ls-remote runs = %d, want %d", n, remoteTries)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	if got := waits.recorded(); !reflect.DeepEqual(got, want) {
		t.Errorf("waits = %v, want %v", got, want)
	}
}

// TestReleaseLastingGitFailureIsNotRetried checks that a release prepare whose ls-remote fails on a refused key runs it
// once and waits on nothing, so the release stops on that failure at once.
func TestReleaseLastingGitFailureIsNotRetried(t *testing.T) {
	t.Parallel()
	const refused = "git ls-remote --symref origin HEAD: exit status 128: git@github.com: Permission denied " +
		"(publickey).\nfatal: Could not read from remote repository."
	w, host, waits, _ := newFlakyRelease(t, 1, refused)

	if _, err := w.runner.decideRelease(t.Context(), epicOf(t, w.s)); err == nil ||
		!strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("decideRelease error = %v, want the refused key", err)
	}
	if n := host.runs(); n != 1 {
		t.Errorf("ls-remote runs = %d, want 1: a lasting failure is not retried", n)
	}
	if got := waits.recorded(); len(got) != 0 {
		t.Errorf("waits = %v, want none", got)
	}
}

// TestReleaseTransientRetryEndsWithItsContext checks that a retry whose context has ended runs git no more: the wait
// ends with the context, and the failure is returned after the one run.
func TestReleaseTransientRetryEndsWithItsContext(t *testing.T) {
	t.Parallel()
	w, host, _, tip := newFlakyRelease(t, 10, expectedFlush)
	flaky, err := w.runner.hostOf(epicOf(t, w.s))
	if err != nil {
		t.Fatalf("hostOf: %v", err)
	}
	w.runner.afterFunc = func(time.Duration, func()) {}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = w.runner.candidateContainsOrigin(ctx, flaky, epicOf(t, w.s), tip)
	if err == nil || !strings.Contains(err.Error(), "run 1 times") {
		t.Errorf("candidateContainsOrigin error = %v, want the failure after 1 run", err)
	}
	if n := host.runs(); n != 1 {
		t.Errorf("ls-remote runs = %d, want 1", n)
	}
}

// TestTransientGitClassifiesTransportFailures checks which git failures transientGit counts as transient: the transport
// failures a later run can clear, and not a refused credential, a missing remote or ref, or an unknown failure, even
// when the refusal comes with a transport line.
func TestTransientGitClassifiesTransportFailures(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		expectedFlush: true,
		"git fetch: exit status 128: fetch-pack: unexpected disconnect while reading sideband packet\n" +
			"fatal: early EOF\nfatal: fetch-pack: invalid index-pack output": true,
		"git fetch: exit status 128: fatal: the remote end hung up unexpectedly":                                           true,
		"git fetch: exit status 128: error: RPC failed; curl 92 HTTP/2 stream 5 was not closed cleanly":                    true,
		"git ls-remote: exit status 128: kex_exchange_identification: read: Connection reset by peer":                      true,
		"git ls-remote: exit status 128: ssh: connect to host github.com port 22: Connection timed out":                    true,
		"git ls-remote: exit status 128: ssh: Could not resolve hostname github.com: Temporary failure in name resolution": true,
		"git ls-remote: exit status 128: git@github.com: Permission denied (publickey).\n" +
			"fatal: the remote end hung up unexpectedly": false,
		"git ls-remote: exit status 128: remote: Repository not found.":                          false,
		"git fetch: exit status 128: fatal: couldn't find remote ref main":                       false,
		"git ls-remote: exit status 128: fatal: 'origin' does not appear to be a git repository": false,
		"git ls-remote: exit status 128: fatal: Authentication failed for 'https://github.com/'": false,
		"git fetch: exit status 1: fatal: bad object HEAD":                                       false,
	}
	for text, want := range cases {
		if got := transientGit(errors.New(text)); got != want {
			t.Errorf("transientGit(%q) = %t, want %t", text, got, want)
		}
	}
}
