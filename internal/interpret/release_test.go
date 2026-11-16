package interpret

import (
	"bytes"
	"database/sql"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// releaseNodePath is the path of the release node of the release tests' workflow.
const releaseNodePath = "release"

// releaseFlow is the workflow the release tests pin: one release node at the top level.
const releaseFlow = "name: release\nnodes:\n  - id: release\n    kind: release\n"

// releaseSecondEpic is the epic of the test repository that waits behind runEpic for the release slot.
const releaseSecondEpic = "orrery-1a2b3c4/epic-second"

// releaseThen is the release promise the release tests' epics carry, which the judge is asked to check.
const releaseThen = "The tag is on main."

// tagStep returns the release step the tests name: a tag on main, which no later step undoes.
func tagStep() store.EpicReleaseStep {
	return store.EpicReleaseStep{Step: "tag the release", Target: "main", Undo: "none"}
}

// releaseWorld is one release test's world: the store, the check fake, the runner, the scratch repository its epics are
// in, and what each of its passes logged.
type releaseWorld struct {
	t       *testing.T
	s       *store.Store
	path    string
	repo    string
	state   string
	fakes   nodeFakes
	ordered *orderedSteps
	runner  *Runner
	logs    *bytes.Buffer
}

// newReleaseWorld returns a world with no epic stored, over a scratch repository on main, and a runner that ends epics
// through ending, which is nil for none. The runner's state directory is the world's, and its roles are releaseRoles.
func newReleaseWorld(t *testing.T, ending Ending) *releaseWorld {
	t.Helper()
	return newReleaseWorldIn(t, ending, mergeRepo(t))
}

// newReleaseWorldIn is newReleaseWorld over the repository at repo, which the world's epics are in. A test that never
// runs git passes a path no repository is built at.
func newReleaseWorldIn(t *testing.T, ending Ending, repo string) *releaseWorld {
	t.Helper()
	s, path := openNodeStore(t)
	fakes := newNodeFakes(s)
	cfg := nodeConfig(s, fakes)
	cfg.Ending = ending
	state := t.TempDir()
	cfg.StateDir = state
	cfg.Getenv = releaseRoles(t)
	logs := &bytes.Buffer{}
	runner := New(cfg)
	runner.logger = slog.New(slog.NewTextHandler(logs, nil))
	ordered := &orderedSteps{fakeSteps: fakes.steps, store: s}
	runner.steps = ordered
	return &releaseWorld{
		t: t, s: s, path: path, repo: repo, state: state, fakes: fakes, ordered: ordered, runner: runner,
		logs: logs,
	}
}

// createEpic stores the epic id in phase, pinned to the release flow, with steps as its release steps, and a frozen
// review at its base, as an epic that ended clean has. The epics of a world are all in its repository, so they share
// its release slot.
func (w *releaseWorld) createEpic(id, phase string, steps ...store.EpicReleaseStep) {
	w.t.Helper()
	pinned, err := workflow.Load("release", func(string) ([]byte, bool, error) {
		return []byte(releaseFlow), true, nil
	})
	if err != nil {
		w.t.Fatalf("Load release: %v", err)
	}
	if err = w.s.CreateEpic(w.t.Context(), store.EpicRow{
		ID: id, Repository: w.repo, Base: runBase, Title: "Release the epic", Phase: phase, Created: runClock(),
		Changed: runClock(), Gates: nodeGates(), DoneGates: []string{integrateDoneGate}, ReleaseSteps: steps,
		ReleaseThen: releaseThen,
		Workflow: store.EpicWorkflow{
			Name: pinned.Name, Text: pinned.Text, SHA256: pinned.SHA256, Nodes: pinnedNodes(pinned.Workflow.Nodes, ""),
		},
	}); err != nil {
		w.t.Fatalf("CreateEpic %s: %v", id, err)
	}
	freezeReviewRow(w.t, w.path, id, runBase)
}

// freezeReviewRow stores the review of epic id as frozen at revision, the row a review leaves on an epic that ended
// clean. It writes the row directly: a review run's freeze also sets the epic's phase to reviewing, which the release
// tests' epics are not in.
func freezeReviewRow(t *testing.T, path, id, revision string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store file: %v", err)
		}
	}()
	_, err = db.ExecContext(t.Context(), "INSERT INTO epic_reviews (epic, frozen) VALUES (?, ?)", id, revision)
	if err != nil {
		t.Fatalf("insert review of epic %s: %v", id, err)
	}
}

// pass carries one pass out over the world's epics and returns what the pass logged.
func (w *releaseWorld) pass() string {
	w.t.Helper()
	w.logs.Reset()
	w.runner.Pass(w.t.Context())
	return w.logs.String()
}

// flowOf returns the rows of the epic id as the store reads them.
func (w *releaseWorld) flowOf(id string) store.EpicFlow {
	w.t.Helper()
	flow, err := w.s.EpicFlow(w.t.Context(), id)
	if err != nil {
		w.t.Fatalf("EpicFlow %s: %v", id, err)
	}
	return flow
}

// endGate ends the Done gate check of the release run with exit.
func (w *releaseWorld) endGate(exit int) {
	w.t.Helper()
	nodeEndCheck(w.t, w.s, workID(w.t, w.s, releaseNodePath, "done 1"), exit, "")
}

// TestReleaseCleanEpicWithoutStepsEndsDone: a clean epic with no release steps ends done through its Ending, with the
// body its kind gives, which names no gap for its clean review. The recording Ending leaves the phase where it was, so
// the pass may ask for the same ending again, and every call is that ending.
func TestReleaseCleanEpicWithoutStepsEndsDone(t *testing.T) {
	t.Parallel()
	ending := &recordingEnding{}
	w := newReleaseWorldIn(t, ending, filepath.Join(t.TempDir(), "no-repository"))
	w.createEpic(runEpic, store.PhaseClean)
	w.pass()

	want := endedEpic{epic: runEpic, phase: "done", body: "Done. No gaps.", at: runClock()}
	if len(ending.calls) == 0 {
		t.Fatalf("EndEpic calls = none, want %+v", want)
	}
	for _, call := range ending.calls {
		if !reflect.DeepEqual(call, want) {
			t.Errorf("EndEpic call = %+v, want %+v", call, want)
		}
	}
}

// TestReleaseCleanEpicTakesSlotAndWaitsBehindHolder: a clean epic with release steps takes the release slot of its
// repository and moves to releasing, and its prepare makes the candidate at the tip. A second epic of the repository
// waits while the slot is held, and shows the holder.
func TestReleaseCleanEpicTakesSlotAndWaitsBehindHolder(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	w.pass()
	holder := w.flowOf(runEpic)
	if holder.Epic.Phase != store.PhaseReleasing || holder.Slot != runEpic {
		t.Fatalf("epic phase = %q and slot = %q, want releasing holding its slot", holder.Epic.Phase, holder.Slot)
	}

	w.createEpic(releaseSecondEpic, store.PhaseClean, tagStep())
	w.pass()
	second := w.flowOf(releaseSecondEpic)
	if second.Epic.Phase != store.PhaseClean || second.Slot != runEpic {
		t.Errorf("second epic phase = %q and slot = %q, want clean, waiting on %s",
			second.Epic.Phase, second.Slot, runEpic)
	}
	if len(second.Releases) != 0 || len(second.Runs) != 1 || second.Runs[0].State != store.NodeRunning {
		t.Errorf("second epic runs = %+v and releases = %+v, want its release run running, no release",
			second.Runs, second.Releases)
	}
}

// TestReleaseRedDoneGateOnBehindCandidateStopsRelease: a candidate behind the target runs the epic's Done gate at the
// candidate. A red gate stops the release, which parks with its one tell and moves the epic to stopped, and the gate is
// not run again.
func TestReleaseRedDoneGateOnBehindCandidateStopsRelease(t *testing.T) {
	t.Parallel()
	ending := &recordingEnding{}
	w := newReleaseWorld(t, ending)
	origin := mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	mergeOriginCommit(t, origin, "origin.txt")
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	if logs := w.pass(); logs != "" {
		t.Fatalf("pass logged %q, want nothing while the Done gate runs", logs)
	}
	candidate := runAt(t, w.s, releaseNodePath).Revision

	wantCheck := store.NodeCheck{
		ID: workID(t, w.s, releaseNodePath, "done 1"), Epic: runEpic, Path: releaseNodePath, Revision: candidate,
		Argv: []string{"./bin/lint"}, Limit: 60, Needs: []store.EpicGateNeed{{Pool: "lint", Count: 2}},
	}
	if want := []store.NodeCheck{wantCheck}; !reflect.DeepEqual(w.fakes.checks.calls, want) {
		t.Fatalf("checks run = %+v, want %+v", w.fakes.checks.calls, want)
	}
	w.endGate(1)
	if logs := w.pass(); logs != "" {
		t.Fatalf("pass logged %q, want nothing once the release stops", logs)
	}

	reason := "done gate " + integrateDoneGate + " did not pass at " + candidate
	if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeParked || run.Outcome != "failed" ||
		run.Output != reason {
		t.Errorf("release run = %+v, want parked failed with %q", run, reason)
	}
	if flow := w.flowOf(runEpic); flow.Epic.Phase != store.PhaseStopped || flow.Epic.Parked != reason {
		t.Errorf("epic phase = %q and parked = %q, want stopped and parked with %q",
			flow.Epic.Phase, flow.Epic.Parked, reason)
	}
	if tells := nodeTells(t, w.path); !reflect.DeepEqual(tells, []string{reason}) {
		t.Errorf("tells = %q, want the one tell %q", tells, reason)
	}
	if len(ending.calls) != 0 {
		t.Errorf("EndEpic calls = %+v, want none for a stopped release", ending.calls)
	}

	w.pass()
	if tells := nodeTells(t, w.path); !reflect.DeepEqual(tells, []string{reason}) {
		t.Errorf("tells = %q after a later pass, want still the one tell", tells)
	}
	if got := len(w.fakes.checks.calls); got != 1 {
		t.Errorf("checks run = %d after a later pass, want the gate run once", got)
	}
}

// TestReleaseGatedCandidateIsRecordedThenStepStarts: a candidate records as a passed candidate run of its attempt, from
// the tip it was made from. A candidate behind the target records once its Done gate has passed, with itself as its
// commit. The release then starts its first step at the candidate.
func TestReleaseGatedCandidateIsRecordedThenStepStarts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		behind bool
	}{
		{name: "behind the target", behind: true},
		{name: "at the target", behind: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newReleaseWorld(t, &recordingEnding{})
			origin := mergeRemote(t, w.repo)
			tip := nodeEpicBranch(t, w.repo, "epic.txt")
			if tc.behind {
				mergeOriginCommit(t, origin, "origin.txt")
			}
			w.createEpic(runEpic, store.PhaseClean, tagStep())
			w.pass()
			if tc.behind {
				w.endGate(0)
			}
			logs := w.pass()

			run := runAt(t, w.s, releaseNodePath)
			if (run.Revision == tip) == tc.behind {
				t.Fatalf("candidate = %q with tip %q, want behind = %t", run.Revision, tip, tc.behind)
			}
			want := store.ReleaseRun{
				Part: store.ReleasePartCandidate, Attempt: run.Attempt, Start: tip, Head: run.Revision,
				Passed: true, At: runClock(),
			}
			if tc.behind {
				want.Commits = []string{run.Revision}
			}
			if got := w.flowOf(runEpic).Releases; !reflect.DeepEqual(got, []store.ReleaseRun{want}) {
				t.Errorf("release runs = %+v, want the one candidate %+v", got, want)
			}
			if logs != "" {
				t.Errorf("logs = %q, want none: the first release step starts", logs)
			}
			if got := len(w.fakes.steps.calls); got != 1 || w.fakes.steps.calls[0].Revision != run.Revision {
				t.Errorf(
					"step starts = %+v, want the one step started at candidate %s",
					w.fakes.steps.calls,
					run.Revision,
				)
			}
		})
	}
}

// TestReleaseFreeGivesUpSlot: the free action frees the release slot the epic holds. A released or done epic is the one
// that holds the slot when the decision frees it, and the Ending that moves an epic to either phase is a later task, so
// this drives the action the decision hands over.
func TestReleaseFreeGivesUpSlot(t *testing.T) {
	t.Parallel()
	w := newReleaseWorldIn(t, nil, filepath.Join(t.TempDir(), "no-repository"))
	w.createEpic(runEpic, store.PhaseStopped)
	if _, err := w.s.TakeReleaseSlot(w.t.Context(), runEpic); err != nil {
		t.Fatalf("TakeReleaseSlot: %v", err)
	}
	if slot := w.flowOf(runEpic).Slot; slot != runEpic {
		t.Fatalf("slot = %q before the free, want %q", slot, runEpic)
	}

	act := Action{Kind: ActWork, Work: kinds.ActFree, NodeKind: kindRelease, Path: releaseNodePath}
	if err := w.runner.work(w.t.Context(), epicOf(t, w.s), act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}
	if slot := w.flowOf(runEpic).Slot; slot != "" {
		t.Errorf("slot = %q after the free, want it free", slot)
	}
}

// TestReleaseNilEndingReportsNoEndingConfigured: a clean epic with no release steps asks its Ending to end it, and a
// runner with no Ending reports that as the error of the pass, and leaves the epic where it was.
func TestReleaseNilEndingReportsNoEndingConfigured(t *testing.T) {
	t.Parallel()
	w := newReleaseWorldIn(t, nil, filepath.Join(t.TempDir(), "no-repository"))
	w.createEpic(runEpic, store.PhaseClean)
	logs := w.pass()

	if !strings.Contains(logs, "no Ending configured") {
		t.Errorf("logs = %q, want the error %q", logs, "no Ending configured")
	}
	if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseClean {
		t.Errorf("epic phase = %q, want clean after the refused ending", phase)
	}
}
