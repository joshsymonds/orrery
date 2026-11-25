package interpret

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/releasestep"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// releaseReviewerName is the name of the default release reviewer the tests run.
const releaseReviewerName = "release"

// releaseClaim is the claim the refusing release reviewer's finding states in the tests.
const releaseClaim = "a tag would overwrite a published release"

// releaseReviewers stands in for the reviewers service of a release world. It records each start, and refuses every start
// with refuse when refuse is set.
type releaseReviewers struct {
	calls  []reviewWorkCall
	refuse error
}

// Epic returns no reviewers: the release starts do not read them.
func (*releaseReviewers) Epic(store.EpicRow) []kinds.Reviewer {
	return nil
}

// Start records the call, then returns the release reviewer's read-only step at the revision it was given, or the refusal.
func (r *releaseReviewers) Start(
	_ context.Context, epic store.EpicRow, name, base, revision string, doneGates, _ []store.NodeCheck,
) (enginehost.StepStart, error) {
	r.calls = append(r.calls, reviewWorkCall{name: name, base: base, revision: revision, doneGates: doneGates})
	if r.refuse != nil {
		return enginehost.StepStart{}, r.refuse
	}
	return enginehost.StepStart{
		Agent: "reviewer-" + name, Prompt: "review the release as " + name, Schema: `{"type":"object"}`,
		Workspace: epic.Repository, Revision: revision, ReadOnly: true,
	}, nil
}

// defaultReleaseReviewer returns the release reviewer of orrery's defaults, merged as an epic's reviewers are, enabled or
// not as enabled says.
func defaultReleaseReviewer(t *testing.T, enabled bool) []reviewers.Reviewer {
	t.Helper()
	merged, err := reviewers.Merge(reviewers.Defaults())
	if err != nil {
		t.Fatalf("Merge(Defaults()): %v", err)
	}
	i := slices.IndexFunc(merged, func(rev reviewers.Reviewer) bool { return rev.Name == releaseReviewerName })
	if i < 0 {
		t.Fatalf("the defaults hold no %s reviewer", releaseReviewerName)
	}
	merged[i].Enabled = enabled
	return []reviewers.Reviewer{merged[i]}
}

// createReviewedEpic stores the release epic runEpic in the clean phase, as createEpic does, with reviewerList as its
// merged reviewers and steps as its release steps.
func (w *releaseWorld) createReviewedEpic(reviewerList []reviewers.Reviewer, steps ...store.EpicReleaseStep) {
	w.t.Helper()
	pinned, err := workflow.Load("release", func(string) ([]byte, bool, error) {
		return []byte(releaseFlow), true, nil
	})
	if err != nil {
		w.t.Fatalf("Load release: %v", err)
	}
	if err = w.s.CreateEpic(w.t.Context(), store.EpicRow{
		ID:           runEpic,
		Repository:   w.repo,
		Base:         runBase,
		Title:        "Release the epic",
		Phase:        store.PhaseClean,
		Created:      runClock(),
		Changed:      runClock(),
		Gates:        nodeGates(),
		DoneGates:    []string{integrateDoneGate},
		ReleaseSteps: steps,
		ReleaseThen:  releaseThen,
		Reviewers:    reviewerList,
		Workflow: store.EpicWorkflow{
			Name: pinned.Name, Text: pinned.Text, SHA256: pinned.SHA256, Nodes: pinnedNodes(pinned.Workflow.Nodes, ""),
		},
	}); err != nil {
		w.t.Fatalf("CreateEpic %s: %v", runEpic, err)
	}
	freezeReviewRow(w.t, w.path, runEpic, runBase)
}

// reviewID returns the id of the release reviewer's step of the release run's first attempt.
func (w *releaseWorld) reviewID() string {
	w.t.Helper()
	return workID(w.t, w.s, releaseNodePath, "review "+releaseReviewerName)
}

// TestReleaseReviewerStartsOnceBeforeStepOne: the release reviewer starts read-only once at the candidate, before the
// first release step, over the change from origin's main, where the candidate meets it, to the candidate. A later pass
// that finds its step running starts nothing and runs no step.
func TestReleaseReviewerStartsOnceBeforeStepOne(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	originMain := nodeGit(t, w.repo, "rev-parse", "main")
	nodeEpicBranch(t, w.repo, "epic.txt")
	reviewer := &releaseReviewers{}
	w.runner.reviewers = reviewer
	w.createReviewedEpic(defaultReleaseReviewer(t, true), tagStep())
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	w.pass()

	if len(reviewer.calls) != 1 {
		t.Fatalf("reviewer starts = %+v, want the release reviewer started once", reviewer.calls)
	}
	if call := reviewer.calls[0]; call.name != releaseReviewerName || call.base != originMain ||
		call.revision != candidate {
		t.Errorf("reviewer start = %+v, want %s over %s..%s", call, releaseReviewerName, originMain, candidate)
	}
	if len(w.fakes.steps.calls) != 1 {
		t.Fatalf("step starts = %+v, want only the release reviewer's step started", w.fakes.steps.calls)
	}
	start := w.fakes.steps.calls[0]
	if start.ID != w.reviewID() || start.Epic != runEpic || !start.ReadOnly || start.Revision != candidate ||
		start.Workspace != w.repo {
		t.Errorf("review step start = %+v, want a read-only start at %s in %s", start, candidate, w.repo)
	}
}

// TestReleaseReviewerHoldsLetStepOneStart: a release reviewer whose step ends done holds the candidate. Its review is
// recorded passed before the first release step starts, and that step starts in the same pass.
func TestReleaseReviewerHoldsLetStepOneStart(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	w.runner.reviewers = &releaseReviewers{}
	w.createReviewedEpic(defaultReleaseReviewer(t, true), tagStep())
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	w.endStep(w.reviewID(), "done", "done", map[string]any{"status": "done"})
	w.pass()

	runs := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartReview)
	if len(runs) != 1 || !runs[0].Passed || runs[0].Head != candidate || runs[0].Step != releaseReviewerName ||
		runs[0].Reason != "" {
		t.Fatalf("review runs = %+v, want one passed review of %s by %s", runs, candidate, releaseReviewerName)
	}
	if len(w.fakes.steps.calls) != 2 || w.fakes.steps.calls[1].ID != w.stepID(1) {
		t.Fatalf("step starts = %+v, want the first release step started after the review", w.fakes.steps.calls)
	}
	stored := releaseRunsOf(w.ordered.before[1], store.ReleasePartReview)
	if len(stored) != 1 || !stored[0].Passed {
		t.Errorf("review runs stored when step 1 started = %+v, want the review recorded passed", stored)
	}
}

// TestReleaseReviewerRefusalStopsAsOwnerStop: a release reviewer that refuses the candidate, or that cannot start, records
// its review failed with its reason. In a workflow with no plan that can make a change to the candidate, the release stops
// there as an owner stop naming that reason, and the first step does not start.
func TestReleaseReviewerRefusalStopsAsOwnerStop(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		refuse error
		end    func(w *releaseWorld)
		reason string
	}{
		{
			name: "refused with findings",
			end: func(w *releaseWorld) {
				w.endStep(w.reviewID(), "gate_failed", "done", map[string]any{
					"status":   "done",
					"findings": []map[string]string{{"claim": releaseClaim}},
					"results":  []map[string]string{{"result": "fails"}},
				})
			},
			reason: "the release reviewer refused the candidate: " + releaseClaim,
		},
		{
			name: "ended stopped",
			end: func(w *releaseWorld) {
				w.endStep(w.reviewID(), "stopped", "", map[string]any{})
			},
			reason: "review ended stopped",
		},
		{
			name:   "could not start",
			refuse: enginehost.ErrBadAgent,
			reason: "the reviewer did not start: " + enginehost.ErrBadAgent.Error(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newReleaseWorld(t, &recordingEnding{})
			mergeRemote(t, w.repo)
			nodeEpicBranch(t, w.repo, "epic.txt")
			w.runner.reviewers = &releaseReviewers{refuse: tc.refuse}
			w.createReviewedEpic(defaultReleaseReviewer(t, true), tagStep())
			w.pass()
			if tc.end != nil {
				tc.end(w)
			}
			w.pass()

			runs := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartReview)
			if len(runs) != 1 || runs[0].Passed || runs[0].Reason != tc.reason {
				t.Errorf("review runs = %+v, want one failed review whose reason is %q", runs, tc.reason)
			}
			if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
				t.Errorf("epic phase = %q, want stopped", phase)
			}
			requireQuestionPark(t, w.s, store.ParkClassReleaseOwnerStop, tc.reason)
			for _, call := range w.fakes.steps.calls {
				if call.ID == w.stepID(1) {
					t.Errorf("step starts = %+v, want the first release step never started", w.fakes.steps.calls)
				}
			}
		})
	}
}

// refuseReview ends the release reviewer's step of the release run's current attempt with a finding whose claim is
// releaseClaim, and passes.
func (w replanWorld) refuseReview() {
	w.t.Helper()
	w.endStep(w.reviewID(), "gate_failed", "done", map[string]any{
		"status":   "done",
		"findings": []map[string]string{{"claim": releaseClaim}},
		"results":  []map[string]string{{"result": "fails"}},
	})
	if logged := w.pass(); logged != "" {
		w.t.Fatalf("pass logged: %s", logged)
	}
}

// TestReleaseReviewerRefusalReplans: in a workflow with a plan that can make the change, a release reviewer that refused
// the candidate with findings sends the release back to planning with its reason, and the first release step never
// starts. The candidate is the clean merge of origin's main the release integrator made. The replan records the epic's
// branch tip the candidate was made from, so once a replan leaves that branch where it was, the reviewer's refusal of
// the candidate the integrator makes again stops the release.
func TestReleaseReviewerRefusalReplans(t *testing.T) {
	t.Parallel()
	const refusal = "the release reviewer refused the candidate: " + releaseClaim
	w := newCleanReplanWorld(t, defaultReleaseReviewer(t, true)...)
	w.runner.reviewers = &releaseReviewers{}
	w.pass()
	w.integrateClean(w.integratorID())
	w.refuseReview()
	w.requireReplannedAt(w.tip, refusal)

	turn, _ := w.claimReplanTurn()
	w.completeReplanTurn(turn, "complete")
	again := runAt(t, w.s, releaseNodePath)
	if again.Attempt != 1 || decodeMerge(t, again.Result).Origin != w.origin {
		t.Fatalf("release run = %+v, want attempt 1 merging origin's commit %s again", again, w.origin)
	}
	w.integrateClean(w.integratorID())
	w.refuseReview()
	w.requireReleaseStopped(refusal+"; the epic's branch has not moved since the last replan", 1)
	firstSteps := []string{store.NodeWorkID(again.ID, 0, "release 1"), store.NodeWorkID(again.ID, 1, "release 1")}
	for _, call := range w.fakes.steps.calls {
		if slices.Contains(firstSteps, call.ID) {
			t.Errorf("step starts = %+v, want the first release step never started", w.fakes.steps.calls)
		}
	}
}

// TestReleaseReviewerEndThatRefusesNothingLetsStepOneStart: a release reviewer whose step ends in a way that is not a
// refusal, an undetermined assertion or an engine restart among them, does not refuse the candidate. Its review is recorded
// passed with the reason its end gives, and the first step starts in the same pass.
func TestReleaseReviewerEndThatRefusesNothingLetsStepOneStart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		end    string
		status string
		result map[string]any
		reason string
	}{
		{
			name:   "findings with an undetermined assertion",
			end:    "gate_failed",
			status: "done",
			result: map[string]any{
				"status":   "done",
				"findings": []map[string]string{{"claim": releaseClaim}},
				"results":  []map[string]string{{"result": "undetermined"}},
			},
			reason: "review ended gate_failed with status done",
		},
		{
			name:   "engine restarted",
			end:    "engine_restarted",
			result: map[string]any{},
			reason: "review ended engine_restarted",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newReleaseWorld(t, &recordingEnding{})
			mergeRemote(t, w.repo)
			nodeEpicBranch(t, w.repo, "epic.txt")
			w.runner.reviewers = &releaseReviewers{}
			w.createReviewedEpic(defaultReleaseReviewer(t, true), tagStep())
			w.pass()
			w.endStep(w.reviewID(), tc.end, tc.status, tc.result)
			w.pass()

			runs := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartReview)
			if len(runs) != 1 || !runs[0].Passed || runs[0].Reason != tc.reason {
				t.Errorf("review runs = %+v, want one passed review whose reason is %q", runs, tc.reason)
			}
			if phase := w.flowOf(runEpic).Epic.Phase; phase == store.PhaseStopped {
				t.Errorf("epic phase = %q, want the release not stopped", phase)
			}
			if len(w.fakes.steps.calls) != 2 || w.fakes.steps.calls[1].ID != w.stepID(1) {
				t.Errorf("step starts = %+v, want the first release step started after the review", w.fakes.steps.calls)
			}
		})
	}
}

// TestReleaseReviewerRunsAgainOnRetry: a release stopped at its first step, resumed as a new attempt on the same candidate,
// has the release reviewer review that candidate again at the new attempt before the first step starts again.
func TestReleaseReviewerRunsAgainOnRetry(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	reviewer := &releaseReviewers{}
	w.runner.reviewers = reviewer
	w.createReviewedEpic(defaultReleaseReviewer(t, true), rollbackTagStep())
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	firstAttempt := runAt(t, w.s, releaseNodePath).Attempt
	w.endStep(w.reviewID(), "done", "done", map[string]any{"status": "done"})
	w.pass()
	w.stopAtFirstStep()

	if err := w.s.ReleaseEpic(w.t.Context(), runEpic, nil, store.Maker{}, runClock()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	w.pass()

	retried := runAt(t, w.s, releaseNodePath)
	if retried.Attempt != firstAttempt+1 {
		t.Fatalf("attempt = %d after the resume, want %d", retried.Attempt, firstAttempt+1)
	}
	if len(reviewer.calls) != 2 || reviewer.calls[1].revision != candidate {
		t.Fatalf("reviewer starts = %+v, want the release reviewer started again at %s", reviewer.calls, candidate)
	}
	if last := w.fakes.steps.calls[len(w.fakes.steps.calls)-1]; last.ID != w.reviewID() {
		t.Fatalf("last step start = %+v, want the review step of attempt %d", last, retried.Attempt)
	}
	w.endStep(w.reviewID(), "done", "done", map[string]any{"status": "done"})
	w.pass()

	reviewed := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartReview)
	if len(reviewed) != 2 || reviewed[1].Attempt != retried.Attempt || !reviewed[1].Passed {
		t.Errorf("review runs = %+v, want the candidate reviewed again at attempt %d", reviewed, retried.Attempt)
	}
	if last := w.fakes.steps.calls[len(w.fakes.steps.calls)-1]; last.ID != w.stepID(1) {
		t.Errorf("last step start = %+v, want the first release step started after the second review", last)
	}
}

// TestReleaseReviewerDisabledLeavesStepOneUngated: with the release reviewer disabled the release starts no review, and
// its first step starts once the candidate is recorded.
func TestReleaseReviewerDisabledLeavesStepOneUngated(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	reviewer := &releaseReviewers{}
	w.runner.reviewers = reviewer
	w.createReviewedEpic(defaultReleaseReviewer(t, false), tagStep())
	w.pass()

	if len(reviewer.calls) != 0 {
		t.Errorf("reviewer starts = %+v, want none with the release reviewer disabled", reviewer.calls)
	}
	if len(w.fakes.steps.calls) != 1 || w.fakes.steps.calls[0].ID != w.stepID(1) {
		t.Errorf("step starts = %+v, want the first release step started", w.fakes.steps.calls)
	}
}

// TestReleaseReviewerStartPromptNamesDestination: the release reviewer's prompt names the promise its release makes, each
// release step with its target and undo, and the kind it runs as when it names one, the Must Not Ship entries, and the
// epic's own change from where the candidate meets origin's main to the candidate.
func TestReleaseReviewerStartPromptNamesDestination(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{
		ID: "repo/epic-x", Title: "Release the epic", Repository: "/repo/epic-x", Base: "base111",
		ReleaseSteps: []store.EpicReleaseStep{
			{Step: "tag the release", Target: "main", Undo: "none"},
			{Step: "publish the tag", Target: "origin", Undo: "delete the tag on origin"},
			{
				Step: "push main", Target: "origin's main", Undo: "push a revert", Kind: releasestep.KindPushMain,
				Params: releasestep.Params{Branch: "main"},
			},
		},
		ReleaseThen: "The tag is on main.",
		Exclusions:  []string{"no new dependency"},
		Reviewers:   defaultReleaseReviewer(t, true),
	}
	got, err := NewEpicReviewers().Start(context.Background(), epic, releaseReviewerName, "base111", "cand333", nil, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := strings.Join([]string{
		"Release of epic repo/epic-x (Release the epic)",
		"The release promises:",
		"The tag is on main.",
		"Release steps:",
		"- tag the release (target main, undo none)",
		"- publish the tag (target origin, undo delete the tag on origin)",
		"- push main (target origin's main, undo push a revert, run as code: push-main)",
		"Must Not Ship:",
		"- no new dependency",
		"The change under review is the epic's own change as the release lands it on origin's main: " +
			"base111..cand333, from base111, where the candidate meets origin's main, to the candidate. It holds " +
			"none of origin's own changes since the epic began. This worktree is checked out at cand333. " +
			"Inspect it with `git diff base111 cand333`.",
	}, "\n")
	if got.Prompt != want {
		t.Errorf("Prompt\n got:\n%s\nwant:\n%s", got.Prompt, want)
	}
	if !got.ReadOnly || got.Revision != "cand333" || got.Workspace != epic.Repository || got.Epic != epic.ID {
		t.Errorf("ReadOnly, Revision, Workspace, Epic = %t, %q, %q, %q; want true, cand333, %q, %q",
			got.ReadOnly, got.Revision, got.Workspace, got.Epic, epic.Repository, epic.ID)
	}
}

// TestReleaseReviewBaseIsWhereTheCandidateMeetsOrigin: once the release integrator has merged origin's main into the
// epic's branch, the release reviewers' change starts at origin's commit the merge took in, not at the epic's base, so
// the change from it to the candidate holds only the epic's own files and none of the files origin's main changed since
// the epic began.
func TestReleaseReviewBaseIsWhereTheCandidateMeetsOrigin(t *testing.T) {
	t.Parallel()
	repo := mergeRepo(t)
	origin := mergeRemote(t, repo)
	tip := nodeEpicBranch(t, repo, "epic.txt")
	moved := mergeOriginCommit(t, origin, "origin.txt")
	epic := store.EpicRow{ID: runEpic, Repository: repo}
	nodeGit(t, repo, "fetch", "-q", "origin", "+main:"+releaseRef(epic, "origin"))
	nodeGit(t, repo, "checkout", "-q", "--detach", tip)
	nodeGit(t, repo, "merge", "-q", "--no-edit", moved)
	candidate := nodeGit(t, repo, "rev-parse", "HEAD")

	base, err := releaseReviewBase(t.Context(), hostexec.NewLocal(), epic, candidate)
	if err != nil {
		t.Fatalf("releaseReviewBase: %v", err)
	}
	if base != moved {
		t.Fatalf("release review base = %s, want origin's commit %s the merge took in", base, moved)
	}
	if files := nodeGit(t, repo, "diff", "--name-only", base, candidate); files != "epic.txt" {
		t.Errorf("files of the reviewed change = %q, want only the epic's epic.txt", files)
	}
	if files := nodeGit(t, repo, "diff", "--name-only", runBaseOf(t, repo), candidate); !strings.Contains(
		files, "origin.txt",
	) {
		t.Errorf("files from the epic's base = %q, want origin's origin.txt among them", files)
	}
}

// runBaseOf returns the first commit of repo, the base the scratch epic's branch starts from.
func runBaseOf(t *testing.T, repo string) string {
	t.Helper()
	return nodeGit(t, repo, "rev-list", "--max-parents=0", "HEAD")
}
