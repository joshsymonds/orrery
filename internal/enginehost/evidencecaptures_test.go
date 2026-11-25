package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// captureTestEpic is the epic the capture tests capture for, and captureTestStep the running step that asks for captures.
// captureTestGatherer is a second running step, which captureTestStore names the epic's gatherer.
const (
	captureTestEpic      = "capture-epic"
	captureTestStep      = "capture-step"
	captureTestGatherer  = "capture-gatherer"
	captureTestIDPattern = `^[0-9a-f]{32}$`
)

// captureTestProject is the project file whose runner fake runs runner.sh, a script, ahead of the argv it is given.
const captureTestProject = `[release]

[[release.runners]]
name = "fake"
argv = ["/bin/sh", "runner.sh"]
note = "Echoes a line, then runs the argv it is given."
`

// captureTestRunnerScript is the script the runner fake runs: it echoes a line, then runs the argv that follows it.
const captureTestRunnerScript = "echo runner ran\nexec \"$@\"\n"

// captureTestRestartScript appends a line to $STARTED each time it runs, waits until $RELEASE exists, then prints done and
// exits 3. A readopt that ran it again would append a second line.
const captureTestRestartScript = `echo run >> "$STARTED"; while [ ! -e "$RELEASE" ]; do sleep 0.05; done; echo done; exit 3`

// captureTestStore is the store a capture test runs over: the real store, which answers EvidenceGathererOf with the test's gatherer
// step, since the store's gatherers are not built here.
type captureTestStore struct {
	*store.Store
	gatherer string
}

// EvidenceGathererOf names the gatherer step of captureTestStore, and no other step.
func (s captureTestStore) EvidenceGathererOf(_ context.Context, step string) (store.EvidenceGatherer, bool, error) {
	if s.gatherer != "" && step == s.gatherer {
		return store.EvidenceGatherer{Epic: captureTestEpic, Step: step}, true, nil
	}
	return store.EvidenceGatherer{}, false, nil
}

// captureTestSteps is the CaptureSteps of the capture tests: the step rows it holds, by id. An id with no row is no step.
type captureTestSteps map[string]store.StepRow

// Status returns the row of step id, or an error matching store.ErrNoStep when it has none.
func (s captureTestSteps) Status(_ context.Context, id string) (store.StepRow, error) {
	row, ok := s[id]
	if !ok {
		return store.StepRow{}, fmt.Errorf("read step %s: %w", id, store.ErrNoStep)
	}
	return row, nil
}

// captureTestDeliveries is the Deliver of the capture tests. It records each request, and answers the requests in order with
// errs, then with nil.
type captureTestDeliveries struct {
	mu   sync.Mutex
	reqs []DeliverRequest
	errs []error
}

// deliver records req and returns the next of errs, or nil once there are none left.
func (d *captureTestDeliveries) deliver(_ context.Context, req DeliverRequest) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reqs = append(d.reqs, req)
	if len(d.errs) == 0 {
		return nil
	}
	err := d.errs[0]
	d.errs = d.errs[1:]
	return err
}

// requests returns the requests deliver was given, in order.
func (d *captureTestDeliveries) requests() []DeliverRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.reqs)
}

// captureTestTimers is the NewTimer of the busy test. Each timer it arms is recorded, and fires when the test sends on ticks.
type captureTestTimers struct {
	mu        sync.Mutex
	durations []time.Duration
	ticks     chan time.Time
}

// newTimer records d as armed and returns the channel the test fires.
func (f *captureTestTimers) newTimer(d time.Duration) (<-chan time.Time, func() bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.durations = append(f.durations, d)
	return f.ticks, func() bool { return true }
}

// armed returns the durations of the timers armed so far.
func (f *captureTestTimers) armed() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.durations)
}

// captureTestRig is what a capture test's Captures is built over. A restart builds another Captures over the same rig.
// briefs, when set, settles the briefs of each ended capture.
type captureTestRig struct {
	st         captureTestStore
	briefs     CaptureBriefs
	steps      captureTestSteps
	deliveries *captureTestDeliveries
	timers     *captureTestTimers
	pools      *fakeGatePools
	environ    []string
	stateDir   string
	gateRoot   string
	scope      string
}

// captureTestNewRig returns a rig over st, where captureTestStep is running, with a gate pool that grants at once and scratch
// state directories. Its gates' holders live under gateRoot, and run under scope when scope is not "".
func captureTestNewRig(t *testing.T, st captureTestStore, scope string) captureTestRig {
	t.Helper()
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "pools.toml"), stepGatePoolsText("gate"))
	return captureTestRig{
		st:         st,
		steps:      captureTestSteps{captureTestStep: {ID: captureTestStep}},
		deliveries: &captureTestDeliveries{},
		pools:      &fakeGatePools{},
		environ:    gateEnv("XDG_CONFIG_HOME=" + home),
		stateDir:   t.TempDir(),
		gateRoot:   gateShortStateDir(t),
		scope:      scope,
	}
}

// captures returns a Captures over the rig, which Close ends at cleanup.
func (r captureTestRig) captures(t *testing.T) *Captures {
	t.Helper()
	cfg := CapturesConfig{
		Store:        r.st,
		Steps:        r.steps,
		Briefs:       r.briefs,
		Deliver:      r.deliveries.deliver,
		Pools:        r.pools,
		Environ:      r.environ,
		StateDir:     r.stateDir,
		GateStateDir: r.gateRoot,
		GateScope:    r.scope,
	}
	if r.timers != nil {
		cfg.NewTimer = r.timers.newTimer
	}
	c := NewCaptures(cfg)
	t.Cleanup(c.Close)
	return c
}

// blobs returns the blob store the rig's captures keep their bytes in.
func (r captureTestRig) blobs() evidenceBlobs {
	return evidenceBlobs{dir: filepath.Join(r.stateDir, "evidence", "blobs")}
}

// workFinished reports whether c has no drive under way, which holds once each capture it started or resumed has ended and been
// delivered.
func (c *Captures) workFinished() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cancels) == 0
}

// captureTestRepo returns a scratch repository whose HEAD holds marker "head", and the HEAD.
func captureTestRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"marker": "head\n"})
	return repo, head
}

// captureTestSeed stores epic captureTestEpic in st, with repository repo and base, and one waiting brief per limit, each a
// captured run of R1 at base with that limit. It returns the briefs' ids in order.
func captureTestSeed(t *testing.T, st captureTestStore, repo, base string, limits ...int64) []string {
	t.Helper()
	created := time.Unix(1700000000, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: captureTestEpic, Repository: repo, Base: base, Title: "Synthetic capture epic", Intent: "Synthetic intent.",
		Care: "serious", Phase: "accepted", Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("store CreateEpic: %v", err)
	}
	briefs := make([]string, 0, len(limits))
	for i, limit := range limits {
		id := fmt.Sprintf("brief-%d", i+1)
		if err := st.AddEvidenceRun(t.Context(), store.EvidenceRun{
			ID: id, Epic: captureTestEpic, Item: "R1", Revision: base, Phase: evidence.PhaseRed,
			Source: evidence.SourceCaptured, State: store.RunWaiting,
			Spec: evidence.Item{Capture: []string{"/bin/sh", "-c", "true"}, Limit: limit}, Created: created,
		}); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", id, err)
		}
		briefs = append(briefs, id)
	}
	return briefs
}

// captureTestIdle waits until c's work has finished, so every capture it started has ended and been delivered.
func captureTestIdle(t *testing.T, c *Captures) {
	t.Helper()
	waittest.UntilIdle(t, c.workFinished, "the captures' work ends", c.workFinished)
}

// captureTestDelivered waits until d holds n requests, and returns them. It fails at once when c's work has finished first.
func captureTestDelivered(t *testing.T, c *Captures, d *captureTestDeliveries, n int) []DeliverRequest {
	t.Helper()
	waittest.UntilIdle(t, c.workFinished, fmt.Sprintf("%d requests delivered", n), func() bool {
		return len(d.requests()) >= n
	})
	return d.requests()
}

// captureTestStored returns the stored capture with id, failing the test when it cannot be read.
func captureTestStored(t *testing.T, st captureTestStore, id string) store.Capture {
	t.Helper()
	got, err := st.Capture(t.Context(), id)
	if err != nil {
		t.Fatalf("Capture %s: %v", id, err)
	}
	return got
}

// TestCaptureToolLocalRunsItsArgvAtTheCommitItNames runs a local capture at a commit older than HEAD, and checks the stdout its
// end delivers is the older commit's file.
func TestCaptureToolLocalRunsItsArgvAtTheCommitItNames(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	older := stepGitTestCommit(t, repo, map[string]string{"marker": "older\n"})
	stepGitTestCommit(t, repo, map[string]string{"marker": "head\n"})
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, older, 60)
	rig := captureTestNewRig(t, st, "")
	c := rig.captures(t)

	if _, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: older, Argv: []string{"/bin/sh", "-c", "cat marker"}, Briefs: briefs,
	}); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	got := captureTestDelivered(t, c, rig.deliveries, 1)
	if len(got) != 1 || !strings.Contains(got[0].Text, "exit: 0\n") ||
		!strings.Contains(got[0].Text, "stdout, last 200 lines:\nolder\n") {
		t.Fatalf("delivered = %+v, want one block with exit 0 and the older commit's marker on stdout", got)
	}
}

// TestCaptureToolRunnerRunsItsPrefixAtTheCommit names a runner in the project file at the epic's base, whose prefix is a script
// committed at the capture's commit. The script runs ahead of the capture's argv, at that commit.
func TestCaptureToolRunnerRunsItsPrefixAtTheCommit(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	base := stepGitTestCommit(t, repo, map[string]string{
		".orrery/project.toml": captureTestProject, "runner.sh": captureTestRunnerScript, "marker": "base\n",
	})
	commit := stepGitTestCommit(t, repo, map[string]string{"marker": "at commit\n"})
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, base, 60)
	rig := captureTestNewRig(t, st, "")
	c := rig.captures(t)

	capture, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: commit, Runner: "fake", Argv: []string{"/bin/sh", "-c", "cat marker"}, Briefs: briefs,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if capture.Runner != "fake" {
		t.Fatalf("stored runner = %q, want fake", capture.Runner)
	}
	got := captureTestDelivered(t, c, rig.deliveries, 1)
	if len(got) != 1 || !strings.Contains(got[0].Text, "exit: 0\n") ||
		!strings.Contains(got[0].Text, "stdout, last 200 lines:\nrunner ran\nat commit\n") {
		t.Fatalf("delivered = %+v, want the runner's line, then the argv's output at the commit, with exit 0", got)
	}
}

// TestCaptureToolLimitIsTheLargestLimitOfItsBriefs names briefs whose limits are 60, 300 and 120, and checks the capture is
// stored with 300.
func TestCaptureToolLimitIsTheLargestLimitOfItsBriefs(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 60, 300, 120)
	rig := captureTestNewRig(t, st, "")
	c := rig.captures(t)

	capture, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: head, Argv: []string{"/bin/sh", "-c", "true"}, Briefs: briefs,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if capture.Limit != 300 {
		t.Fatalf("stored limit = %d, want 300, the largest of the briefs' limits", capture.Limit)
	}
	captureTestDelivered(t, c, rig.deliveries, 1)
}

// TestCaptureToolIDReturnsBeforeTheGateEnds starts a capture whose script waits for a release file. Capture returns the capture
// as running, with nothing delivered and its work still under way; the release then ends the capture.
func TestCaptureToolIDReturnsBeforeTheGateEnds(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 60)
	rig := captureTestNewRig(t, st, "")
	c := rig.captures(t)
	release := filepath.Join(t.TempDir(), "release")

	capture, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: head, Argv: []string{"/bin/sh", "-c", "while [ ! -e " + release + " ]; do sleep 0.05; done; echo done"},
		Briefs: briefs,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if capture.State != "running" {
		t.Fatalf("returned capture state = %q, want running", capture.State)
	}
	if got := captureTestStored(t, st, capture.ID); got.State != "running" {
		t.Fatalf("stored capture state = %q, want running while its gate runs", got.State)
	}
	if n := len(rig.deliveries.requests()); n != 0 {
		t.Fatalf("%d requests delivered before the gate ended, want none", n)
	}
	if c.workFinished() {
		t.Fatal("the capture's work finished when Capture returned, want its gate still under way")
	}
	if err = os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	captureTestDelivered(t, c, rig.deliveries, 1)
}

// TestCaptureToolEndIsDeliveredWithItsTailsAndKeptFiles runs a capture that prints to stdout and stderr, keeps one of two named
// files, and exits 3. Its end is delivered once to the asking step, as one block under the capture's PromptID, which names the
// exit, both tails and each file's state. The kept file's bytes are stored as a blob.
func TestCaptureToolEndIsDeliveredWithItsTailsAndKeptFiles(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 60)
	rig := captureTestNewRig(t, st, "")
	c := rig.captures(t)
	script := "printf 'out line\\n'; printf 'err line\\n' >&2; printf 'kept bytes\\n' > small.txt; exit 3"

	capture, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: head,
		Argv:   []string{"/bin/sh", "-c", script},
		Briefs: briefs,
		Files:  []string{"small.txt", "third.txt"},
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	got := captureTestDelivered(t, c, rig.deliveries, 1)
	captureTestIdle(t, c)
	if len(got) != 1 {
		t.Fatalf("delivered %d requests, want one", len(got))
	}
	req := got[0]
	if req.ID != captureTestStep || req.PromptID != "capture:"+capture.ID {
		t.Fatalf(
			"delivered to %q under %q, want step %s under capture:%s",
			req.ID,
			req.PromptID,
			captureTestStep,
			capture.ID,
		)
	}
	for _, want := range []string{
		"Evidence capture " + capture.ID + " ended.\n",
		"exit: 3\n",
		"timed out: false\n",
		"not captured: none\n",
		"stdout, last 200 lines:\nout line\n",
		"stderr, last 200 lines:\nerr line\n",
		"files:\nsmall.txt: kept\nthird.txt: missing",
	} {
		if !strings.Contains(req.Text, want) {
			t.Fatalf("block = %q, want it to contain %q", req.Text, want)
		}
	}
	stored := captureTestStored(t, st, capture.ID)
	if stored.State != "ended" || stored.Exit != 3 || !stored.Delivered || len(stored.Artifacts) != 2 {
		t.Fatalf(
			"stored end = state %q exit %d delivered %t artifacts %d, want ended, exit 3, delivered, two artifacts",
			stored.State,
			stored.Exit,
			stored.Delivered,
			len(stored.Artifacts),
		)
	}
	kept := stored.Artifacts[0]
	if kept.Path != "small.txt" || kept.State != "kept" {
		t.Fatalf("small.txt = %+v, want kept", kept)
	}
	data, err := rig.blobs().read(kept.Blob)
	if err != nil {
		t.Fatalf("read small.txt blob: %v", err)
	}
	if string(data) != "kept bytes\n" {
		t.Fatalf("small.txt blob = %q, want the file's bytes", data)
	}
}

// TestCaptureToolTimedOutEndIsDeliveredAsKilled delivers a stored timed-out end. The end is stored as a capture that timed out at
// its limit, with the stdout it made and a kept file, and Resume delivers it: the one block the asking step is told says it timed
// out, was killed at its limit, and keeps the output and the file it made. No command runs and no clock is waited on.
func TestCaptureToolTimedOutEndIsDeliveredAsKilled(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 1)
	rig := captureTestNewRig(t, st, "")
	c := rig.captures(t)

	const id = "timed-out-end"
	stdout := "out line\n"
	if err := st.AddCapture(t.Context(), store.Capture{
		ID: id, Epic: captureTestEpic, Repository: repo, Step: captureTestStep, Role: captureRoleStep,
		Runner: localRunnerName, Commit: head, Argv: []string{"/bin/sh", "-c", "printf 'out line\\n'"},
		Files: []string{"small.txt"}, Briefs: briefs, Limit: 1, Created: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("AddCapture %s: %v", id, err)
	}
	logHash, _, err := rig.blobs().put([]byte(stdout), ".log")
	if err != nil {
		t.Fatalf("store stdout log: %v", err)
	}
	if err := st.RecordCaptureEnd(t.Context(), id, store.EvidenceCapture{
		TimedOut: true, NotCaptured: evidenceCaptureKilled, LogHash: logHash, LogBytes: int64(len(stdout)),
		Artifacts: []store.EvidenceArtifact{{Path: "small.txt", State: "kept"}}, At: time.Unix(1700000001, 0),
	}); err != nil {
		t.Fatalf("RecordCaptureEnd %s: %v", id, err)
	}
	if err := c.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got := captureTestDelivered(t, c, rig.deliveries, 1)
	for _, want := range []string{"timed out: true\n", "not captured: killed at its limit\n",
		"stdout, last 200 lines:\nout line\n", "small.txt: kept"} {
		if len(got) != 1 || !strings.Contains(got[0].Text, want) {
			t.Fatalf("delivered = %+v, want a block containing %q", got, want)
		}
	}
}

// TestCaptureToolStoresTheStepRunnerCommitAndArgv stores a capture from a step that is not a gatherer, given a short commit,
// and checks the stored capture names its epic, repository, step, role, runner, full commit, argv, files and briefs, and is
// running with a new 32-hex id.
func TestCaptureToolStoresTheStepRunnerCommitAndArgv(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 60, 90)
	rig := captureTestNewRig(t, st, "")
	c := rig.captures(t)
	argv := []string{"/bin/sh", "-c", "true"}
	files := []string{"small.txt"}

	got, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: head[:12], Argv: argv, Files: files, Briefs: briefs,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	captureTestDelivered(t, c, rig.deliveries, 1)
	if !regexp.MustCompile(captureTestIDPattern).MatchString(got.ID) {
		t.Fatalf("capture id %q is not 32 hex digits", got.ID)
	}
	if got.Epic != captureTestEpic || got.Repository != repo || got.Step != captureTestStep || got.Role != "step" ||
		got.Runner != "local" || got.Commit != head || !slices.Equal(got.Argv, argv) || !slices.Equal(got.Files, files) ||
		!slices.Equal(got.Briefs, briefs) || got.Limit != 90 || got.State != "running" {
		t.Fatalf("stored capture = %+v, want epic %s, step %s, role step, runner local, commit %s, argv %v, "+
			"files %v, briefs %v, limit 90, running", got, captureTestEpic, captureTestStep, head, argv, files, briefs)
	}
}

// TestCaptureToolRoleIsEvidenceGathererForTheGatherer asks for a capture from the step the store names the epic's gatherer, and
// checks the stored capture's role is the evidence gatherer's.
func TestCaptureToolRoleIsEvidenceGathererForTheGatherer(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t), gatherer: captureTestGatherer}
	briefs := captureTestSeed(t, st, repo, head, 60)
	rig := captureTestNewRig(t, st, "")
	rig.steps[captureTestGatherer] = store.StepRow{ID: captureTestGatherer}
	c := rig.captures(t)

	got, err := c.Capture(t.Context(), captureTestGatherer, CaptureArgs{
		Commit: head, Argv: []string{"/bin/sh", "-c", "true"}, Briefs: briefs,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if got.Role != "evidence gatherer" || got.Step != captureTestGatherer {
		t.Fatalf(
			"stored capture role %q step %q, want evidence gatherer from %s",
			got.Role,
			got.Step,
			captureTestGatherer,
		)
	}
	captureTestDelivered(t, c, rig.deliveries, 1)
}

// TestCaptureToolRestartReadoptsARunningGate starts a capture whose script waits for a release file, and closes its Captures
// under a gate scope while the gate runs. A restarted Captures resumes the capture: the gate is readopted, not run again, and
// once the release file exists its end is delivered to the step.
func TestCaptureToolRestartReadoptsARunningGate(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 60)
	started := filepath.Join(t.TempDir(), "started")
	release := filepath.Join(t.TempDir(), "release")
	rig := captureTestNewRig(t, st, fakeSystemdRunPath(t))
	rig.environ = append(slices.Clone(rig.environ), "STARTED="+started, "RELEASE="+release)
	argv := []string{"/bin/sh", "-c", captureTestRestartScript}

	first := rig.captures(t)
	capture, err := first.Capture(t.Context(), captureTestStep, CaptureArgs{Commit: head, Argv: argv, Briefs: briefs})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	evidenceCloseEndHolder(t, rig.gateRoot, capture.ID)
	waittest.UntilIdle(t, first.workFinished, "the gate starts and its grant is stored", func() bool {
		_, statErr := os.Stat(started)
		return statErr == nil && captureTestStored(t, st, capture.ID).Grant != ""
	})
	first.Close()

	holder := holderDir(t, rig.gateRoot, "evidence-"+capture.ID)
	if _, err = os.Stat(holder); err != nil {
		t.Fatalf("holder of the running gate after Close: %v, want it kept for a restart", err)
	}
	if n := len(rig.deliveries.requests()); n != 0 {
		t.Fatalf("%d requests delivered at Close, want none", n)
	}

	second := rig.captures(t)
	if err = second.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if err = os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	got := captureTestDelivered(t, second, rig.deliveries, 1)
	captureTestIdle(t, second)
	if len(got) != 1 || !strings.Contains(got[0].Text, "exit: 3\n") ||
		!strings.Contains(got[0].Text, "stdout, last 200 lines:\ndone\n") {
		t.Fatalf("delivered = %+v, want the readopted gate's exit 3 and its output", got)
	}
	data, err := os.ReadFile(started)
	if err != nil {
		t.Fatalf("read started: %v", err)
	}
	if n := strings.Count(string(data), "run\n"); n != 1 {
		t.Fatalf("the gate ran %d times, want once", n)
	}
	if stored := captureTestStored(t, st, capture.ID); stored.State != "ended" || stored.Exit != 3 {
		t.Fatalf("stored capture after restart = state %q exit %d, want ended with exit 3", stored.State, stored.Exit)
	}
}

// TestCaptureToolBusyStepIsTriedAgainAfterFiveSeconds delivers an end to a step that is busy, and checks the retry is armed for
// five seconds, delivers again once it fires, and then marks the capture delivered.
func TestCaptureToolBusyStepIsTriedAgainAfterFiveSeconds(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 60)
	rig := captureTestNewRig(t, st, "")
	rig.deliveries.errs = []error{ErrSessionBusy}
	rig.timers = &captureTestTimers{ticks: make(chan time.Time)}
	c := rig.captures(t)

	capture, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: head, Argv: []string{"/bin/sh", "-c", "true"}, Briefs: briefs,
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	waittest.UntilIdle(t, c.workFinished, "the retry is armed", func() bool { return len(rig.timers.armed()) == 1 })
	if got := rig.timers.armed(); got[0] != 5*time.Second {
		t.Fatalf("retry armed for %v, want 5s", got[0])
	}
	if n := len(rig.deliveries.requests()); n != 1 {
		t.Fatalf("%d requests before the retry, want the busy attempt alone", n)
	}
	rig.timers.ticks <- time.Time{}
	if got := captureTestDelivered(t, c, rig.deliveries, 2); len(got) != 2 || got[0].PromptID != got[1].PromptID {
		t.Fatalf("delivered = %+v, want the same block tried twice", got)
	}
	captureTestIdle(t, c)
	if stored := captureTestStored(t, st, capture.ID); !stored.Delivered {
		t.Fatal("capture not marked delivered after the retry was delivered")
	}
}

// TestCaptureToolUnknownRunnerIsRefused asks for a runner the project at the epic's base does not name. The refusal names the
// runner, and nothing is stored.
func TestCaptureToolUnknownRunnerIsRefused(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 60)
	c := captureTestNewRig(t, st, "").captures(t)

	_, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: head, Runner: "nope", Argv: []string{"/bin/sh", "-c", "true"}, Briefs: briefs,
	})
	if !errors.Is(err, ErrCaptureRefused) || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("Capture with an unknown runner = %v, want a refusal naming runner nope", err)
	}
	if running, listErr := st.RunningCaptures(t.Context()); listErr != nil || len(running) != 0 {
		t.Fatalf("running captures = %v, %v, want none stored", running, listErr)
	}
}

// TestCaptureToolUnresolvableCommitIsRefused asks for a commit the epic's repository does not hold. The refusal matches
// ErrCaptureRefused, and nothing is stored.
func TestCaptureToolUnresolvableCommitIsRefused(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 60)
	c := captureTestNewRig(t, st, "").captures(t)

	_, err := c.Capture(t.Context(), captureTestStep, CaptureArgs{
		Commit: "not-a-commit", Argv: []string{"/bin/sh", "-c", "true"}, Briefs: briefs,
	})
	if !errors.Is(err, ErrCaptureRefused) {
		t.Fatalf("Capture at an unresolvable commit = %v, want ErrCaptureRefused", err)
	}
	if running, listErr := st.RunningCaptures(t.Context()); listErr != nil || len(running) != 0 {
		t.Fatalf("running captures = %v, %v, want none stored", running, listErr)
	}
}

// TestCaptureToolNonStepCallerIsRefused asks for captures from a caller with no step and from a step that has ended. Each
// refusal matches ErrCaptureRefused and ErrCaptureNotStep, and nothing is stored.
func TestCaptureToolNonStepCallerIsRefused(t *testing.T) {
	t.Parallel()
	repo, head := captureTestRepo(t)
	st := captureTestStore{Store: epicTestStore(t)}
	briefs := captureTestSeed(t, st, repo, head, 60)
	rig := captureTestNewRig(t, st, "")
	rig.steps["ended-step"] = store.StepRow{ID: "ended-step", End: "done"}
	c := rig.captures(t)
	args := CaptureArgs{Commit: head, Argv: []string{"/bin/sh", "-c", "true"}, Briefs: briefs}

	for _, caller := range []string{"no-such-step", "ended-step"} {
		_, err := c.Capture(t.Context(), caller, args)
		if !errors.Is(err, ErrCaptureRefused) || !errors.Is(err, ErrCaptureNotStep) {
			t.Fatalf("Capture from %s = %v, want ErrCaptureRefused and ErrCaptureNotStep", caller, err)
		}
	}
	if running, listErr := st.RunningCaptures(t.Context()); listErr != nil || len(running) != 0 {
		t.Fatalf("running captures = %v, %v, want none stored", running, listErr)
	}
}
