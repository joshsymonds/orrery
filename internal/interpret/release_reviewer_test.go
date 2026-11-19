package interpret

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
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
	_ context.Context, epic store.EpicRow, name, base, revision string, doneGates []store.NodeCheck,
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
// first release step, and a later pass that finds its step running starts nothing and runs no step.
func TestReleaseReviewerStartsOnceBeforeStepOne(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
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
	if call := reviewer.calls[0]; call.name != releaseReviewerName || call.base != runBase ||
		call.revision != candidate {
		t.Errorf("reviewer start = %+v, want %s over %s..%s", call, releaseReviewerName, runBase, candidate)
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
// its review failed with its reason. The release stops there as an owner stop naming that reason, and the first step does
// not start.
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
			requireOwnerPark(t, w.s, store.ParkClassReleaseOwnerStop, tc.reason)
			for _, call := range w.fakes.steps.calls {
				if call.ID == w.stepID(1) {
					t.Errorf("step starts = %+v, want the first release step never started", w.fakes.steps.calls)
				}
			}
		})
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
// release step with its target and undo, the Must Not Ship entries, and the change from the epic's base to the candidate.
func TestReleaseReviewerStartPromptNamesDestination(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{
		ID: "repo/epic-x", Title: "Release the epic", Repository: "/repo/epic-x", Base: "base111",
		ReleaseSteps: []store.EpicReleaseStep{
			{Step: "tag the release", Target: "main", Undo: "none"},
			{Step: "publish the tag", Target: "origin", Undo: "delete the tag on origin"},
		},
		ReleaseThen: "The tag is on main.",
		Exclusions:  []string{"no new dependency"},
		Reviewers:   defaultReleaseReviewer(t, true),
	}
	got, err := NewEpicReviewers().Start(context.Background(), epic, releaseReviewerName, "base111", "cand333", nil)
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
		"Must Not Ship:",
		"- no new dependency",
		"The change under review is base111..cand333; this worktree is checked out at cand333. " +
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
