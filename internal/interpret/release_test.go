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
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// releaseNodePath is the path of the release node of the release tests' workflow.
const releaseNodePath = "release"

// releaseFlow is the workflow the release tests pin: one release node at the top level.
const releaseFlow = "name: release\nnodes:\n  - id: release\n    kind: release\n"

// releaseSecondEpic is the second epic of the test repository, beside runEpic, and releaseSecondBranch the branch its
// work is committed to.
const (
	releaseSecondEpic   = "orrery-1a2b3c4/epic-second"
	releaseSecondBranch = "epic/epic-second"
)

// releaseThen is the release promise the release tests' epics carry, which the judge is asked to check.
const releaseThen = "The tag is on main."

// tagStep returns the release step the tests name: a tag on main, which no later step undoes.
func tagStep() store.EpicReleaseStep {
	return store.EpicReleaseStep{Step: "tag the release", Target: "main", Undo: "none"}
}

// releaseWorld is one release test's world: the store, the check fake, the runner, the scratch repository its epics are
// in, what each of its passes logged, and the Requirements the epics it creates declare, none unless a test sets them.
type releaseWorld struct {
	t            *testing.T
	s            *store.Store
	path         string
	repo         string
	state        string
	fakes        nodeFakes
	ordered      *orderedSteps
	runner       *Runner
	logs         *bytes.Buffer
	requirements []store.EpicRequirement
	reviewerList []reviewers.Reviewer
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

// createEpic stores the epic id in phase, pinned to the release flow, with steps as its release steps, the world's
// Requirements, and a frozen review at its base, as an epic that ended clean has.
func (w *releaseWorld) createEpic(id, phase string, steps ...store.EpicReleaseStep) {
	w.t.Helper()
	w.createEpicOn(releaseFlow, runBase, id, phase, steps...)
}

// createEpicOn is createEpic with the workflow text flow pinned in place of the release flow, and base as the epic's
// base.
func (w *releaseWorld) createEpicOn(flow, base, id, phase string, steps ...store.EpicReleaseStep) {
	w.t.Helper()
	name := strings.TrimPrefix(strings.SplitN(flow, "\n", 2)[0], "name: ")
	pinned, err := workflow.Load(name, func(string) ([]byte, bool, error) {
		return []byte(flow), true, nil
	})
	if err != nil {
		w.t.Fatalf("Load %s: %v", name, err)
	}
	if err = w.s.CreateEpic(w.t.Context(), store.EpicRow{
		ID: id, Repository: w.repo, Base: base, Title: "Release the epic", Phase: phase, Created: runClock(),
		Changed: runClock(), Gates: nodeGates(), DoneGates: []string{integrateDoneGate}, ReleaseSteps: steps,
		ReleaseThen: releaseThen, Requirements: w.requirements, Reviewers: w.reviewerList,
		Workflow: store.EpicWorkflow{
			Name: pinned.Name, Text: pinned.Text, SHA256: pinned.SHA256, Nodes: pinnedNodes(pinned.Workflow.Nodes, ""),
		},
	}); err != nil {
		w.t.Fatalf("CreateEpic %s: %v", id, err)
	}
	freezeReviewRow(w.t, w.path, id, base)
}

// freezeReviewRow stores the review of epic id as frozen at revision, the row a review leaves on an epic that ended
// clean. It writes the row directly: a review run's freeze also sets the epic's phase to reviewing, which the release
// tests' epics are not in.
func freezeReviewRow(t *testing.T, path, id, revision string) {
	t.Helper()
	execStoreFile(t, path, "INSERT INTO epic_reviews (epic, frozen) VALUES (?, ?)", id, revision)
}

// seedVerdict stores verdict as a verdict of the review of epic id, the row a review's end leaves for each reviewer. It
// writes the row directly, as freezeReviewRow does.
func seedVerdict(t *testing.T, path, id string, verdict store.ReviewVerdict) {
	t.Helper()
	execStoreFile(t, path,
		"INSERT INTO epic_review_verdicts (epic, position, reviewer, step, verdict, reason) VALUES (?, ?, ?, ?, ?, ?)",
		id, verdict.Position, verdict.Reviewer, verdict.Step, verdict.Verdict, verdict.Reason)
}

// execStoreFile runs query with args against the store file at path.
func execStoreFile(t *testing.T, path, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store file: %v", err)
		}
	}()
	if _, err = db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("write store file: %v", err)
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

// TestReleaseCleanEpicWithoutStepsEndsDone: a clean epic with no release steps ends done through its Ending, with the
// body its kind gives, which names no gap for its clean review. The recording Ending leaves the phase where it was, so
// the pass may ask for the same ending again, and every call is that ending.
func TestReleaseCleanEpicWithoutStepsEndsDone(t *testing.T) {
	t.Parallel()
	ending := &recordingEnding{}
	w := newReleaseWorldIn(t, ending, filepath.Join(t.TempDir(), "no-repository"))
	w.createEpic(runEpic, store.PhaseClean)
	w.pass()

	want := endedEpic{epic: runEpic, phase: "done", body: kinds.EndingBody("done", nil, nil), at: runClock()}
	if len(ending.calls) == 0 {
		t.Fatalf("EndEpic calls = none, want %+v", want)
	}
	for _, call := range ending.calls {
		if !reflect.DeepEqual(call, want) {
			t.Errorf("EndEpic call = %+v, want %+v", call, want)
		}
	}
}

// TestReleaseCleanEpicBeginsItsReleaseAtOnce: a clean epic with release steps moves to releasing in the pass that reaches
// its release, and its release run then prepares the candidate.
func TestReleaseCleanEpicBeginsItsReleaseAtOnce(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	w.pass()
	if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseReleasing {
		t.Fatalf("epic phase = %q, want releasing", phase)
	}
	if run := runAt(t, w.s, releaseNodePath); run.Revision == "" {
		t.Errorf("release run = %+v, want its candidate prepared at once", run)
	}
}

// TestEpicBaseReleaseCandidateIsRecordedThenStepStarts: a candidate records as a passed candidate run of its attempt,
// from the tip it was made from, and no Done gate runs on it. A tip behind the target has the release integrator make
// the merge of origin's commit, and the merge records as the candidate with itself as its commit. The release then
// starts its first step at the candidate.
func TestEpicBaseReleaseCandidateIsRecordedThenStepStarts(t *testing.T) {
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
			candidate := tip
			if tc.behind {
				candidate = w.integrateClean(w.integratorID())
			}
			logs := w.pass()

			if (candidate == tip) == tc.behind {
				t.Fatalf("candidate = %q with tip %q, want behind = %t", candidate, tip, tc.behind)
			}
			want := store.ReleaseRun{
				Part: store.ReleasePartCandidate, Attempt: runAt(t, w.s, releaseNodePath).Attempt, Start: tip,
				Head: candidate, Passed: true, At: runClock(),
			}
			if tc.behind {
				want.Commits = []string{candidate}
			}
			flow := w.flowOf(runEpic)
			if got := releaseRunsOf(flow.Releases, store.ReleasePartCandidate); !reflect.DeepEqual(
				got, []store.ReleaseRun{want}) {
				t.Errorf("candidate runs = %+v, want the one candidate %+v", got, want)
			}
			if rounds := releaseRunsOf(flow.Releases, store.ReleasePartIntegrator); len(rounds) != btoi(tc.behind) {
				t.Errorf("integrator rounds = %+v, want %d", rounds, btoi(tc.behind))
			}
			if logs != "" {
				t.Errorf("logs = %q, want none: the first release step starts", logs)
			}
			if step := w.started(w.stepID(1)); step.Revision != candidate {
				t.Errorf("step 1 start = %+v, want it started at candidate %s", step, candidate)
			}
			if len(w.fakes.checks.calls) != 0 {
				t.Errorf("checks = %+v, want no Done gate run on the candidate", w.fakes.checks.calls)
			}
		})
	}
}

// btoi returns 1 for true and 0 for false.
func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
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

// TestReleaseEndedWithGapsReleasesOnOwnersWordAndNamesGaps: an epic that ended with gaps and has no release steps is not
// released by a pass on its own. Once the owner releases it, the next pass ends it done with the body that names its
// gap, and the review's verdicts, the gap among them, are still in its flow. The recording Ending leaves the phase
// where it was, so the pass may ask for the same ending again, and every call is that ending.
func TestReleaseEndedWithGapsReleasesOnOwnersWordAndNamesGaps(t *testing.T) {
	t.Parallel()
	ending := &recordingEnding{}
	w := newReleaseWorldIn(t, ending, filepath.Join(t.TempDir(), "no-repository"))
	w.createEpic(runEpic, "ended_with_gaps")
	gap := store.ReviewVerdict{
		Position: 1, Reviewer: "security", Step: "review security 1", Verdict: "gap", Reason: "the tag is not signed",
	}
	seedVerdict(t, w.path, runEpic, gap)

	w.pass()
	if len(ending.calls) != 0 {
		t.Fatalf("EndEpic calls = %+v before the owner releases, want none", ending.calls)
	}

	if err := w.s.ReleaseEpic(t.Context(), runEpic, nil, store.Maker{}, runClock()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	w.pass()

	want := endedEpic{
		epic: runEpic, phase: "done",
		body: kinds.EndingBody("done", []string{"reviewer security: the tag is not signed"}, nil), at: runClock(),
	}
	if len(ending.calls) == 0 {
		t.Fatalf("EndEpic calls = none, want %+v", want)
	}
	for _, call := range ending.calls {
		if !reflect.DeepEqual(call, want) {
			t.Errorf("EndEpic call = %+v, want %+v", call, want)
		}
	}

	flow := w.flowOf(runEpic)
	if !reflect.DeepEqual(flow.Review.Verdicts, []store.ReviewVerdict{gap}) {
		t.Errorf("review verdicts = %+v, want the gap %+v", flow.Review.Verdicts, gap)
	}
}
