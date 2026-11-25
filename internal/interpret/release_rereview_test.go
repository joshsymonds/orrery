package interpret

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// plainReviewPrompt is the prompt the release reviewers fake gives the release reviewer, which a reviewer that reviews
// in full reads alone.
const plainReviewPrompt = "review the release as " + releaseReviewerName

// staleWorld is a release world whose first candidate the release reviewer held, and which was then marked stale
// because origin's main moved past it while its first step ran, so the release integrator merges origin's main again at
// generation 1. candidate is the candidate the reviewer held, tip the epic's branch tip it was made from, moved
// origin's commit the round of generation 1 merges, and reviewer the reviewers fake.
type staleWorld struct {
	*releaseWorld
	candidate, tip, moved string
	reviewer              *releaseReviewers
}

// newStaleWorld returns a stale world whose origin's main moves past the first candidate with a commit that writes file.
// before runs once the first step has started and before origin moves, so a test can move the epic's branch too.
func newStaleWorld(t *testing.T, file string, before func(w *releaseWorld)) staleWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	origin := mergeRemote(t, w.repo)
	candidate, reviewer := startMainMovedRelease(t, w, origin)
	tip, ok := kinds.CandidateTip(w.flowOf(runEpic).Releases, candidate)
	if !ok {
		t.Fatalf("no candidate run records %s", candidate)
	}
	if before != nil {
		before(w)
	}
	moved := mergeOriginCommit(t, origin, file)
	w.endStep(w.stepID(1), "returned", "blocked", map[string]any{
		"status": "blocked", "evidence": "the tag was refused",
	})
	w.pass()
	if starts := w.integratorStarts(); len(starts) != 2 || starts[1].ID != w.generationID("integrator 1") {
		t.Fatalf("integrator starts = %+v, want the round of generation 1", starts)
	}
	return staleWorld{releaseWorld: w, candidate: candidate, tip: tip, moved: moved, reviewer: reviewer}
}

// round returns the id of the release integrator's round of generation 1.
func (w staleWorld) round() string {
	w.t.Helper()
	return w.generationID("integrator 1")
}

// reviewSaid returns the reviews entry of a return that says whether the release reviewer's verdict stands, and why.
func reviewSaid(stands bool, reason string) []map[string]any {
	return []map[string]any{{"reviewer": releaseReviewerName, "stands": stands, "reason": reason}}
}

// integrate ends the round of generation 1 done with ret and passes, once ret passes the round's return check.
func (w staleWorld) integrate(ret map[string]any) {
	w.t.Helper()
	if failures := w.checkReturn(w.round(), ret); len(failures) != 0 {
		w.t.Fatalf("return check = %q, want none", failures)
	}
	w.endStep(w.round(), "done", "done", ret)
	if logged := w.pass(); logged != "" {
		w.t.Fatalf("pass logged: %s", logged)
	}
}

// generationReview returns the review release runs at the release reviewer's position of generation 1, and the
// rereview runs there.
func (w staleWorld) generationReview() ([]store.ReleaseRun, []store.ReleaseRun) {
	w.t.Helper()
	position := store.ReleaseReviewPosition(1, 1, 1)
	at := func(part string) []store.ReleaseRun {
		return slices.DeleteFunc(releaseRunsOf(w.flowOf(runEpic).Releases, part), func(run store.ReleaseRun) bool {
			return run.Position != position
		})
	}
	return at(store.ReleasePartReview), at(store.ReleasePartRereview)
}

// TestReleaseIntegratorListsTheEarlierReview: once origin's main moved past a candidate the release reviewer held, and
// the epic's branch has not moved and git merges origin's commit into it cleanly, the release integrator's round of
// generation 1 is given the reviewer's earlier verdict with what its step returned, and origin's change since the
// candidate it read, from where the two meet. Its return check then wants one reviews entry, with a reason, for that
// reviewer.
func TestReleaseIntegratorListsTheEarlierReview(t *testing.T) {
	t.Parallel()
	w := newStaleWorld(t, "origin-2.txt", nil)
	prompt := w.started(w.round()).Prompt
	base := nodeGit(t, w.repo, "merge-base", w.candidate, w.moved)
	for _, want := range []string{
		"\n## Earlier reviews\n- release held candidate " + w.candidate + "; its review ended done\n" +
			"  its step ended done and returned: {\"status\":\"done\"}\n",
		"\n## Origin since the reviewed candidates\nSince candidate " + w.candidate + ": origin's change from " + base +
			", where the two meet, to origin's commit " + w.moved + "\n- A origin-2.txt\nDiff stat:\n origin-2.txt | 1 +\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	merge := w.commitCleanMerge()
	if failures := w.checkReturn(w.round(), cleanReturn(merge)); !hasLine(failures,
		"reviews: release reviewer release is not named") {
		t.Errorf("failures of a return with no reviews entry = %q, want the reviewer's line", failures)
	}
	reasonless := cleanReturn(merge)
	reasonless["reviews"] = reviewSaid(true, " ")
	if failures := w.checkReturn(w.round(), reasonless); !hasLine(failures,
		"reviews: release reviewer release gives no reason") {
		t.Errorf("failures of a reasonless reviews entry = %q, want the reason line", failures)
	}
	twice := cleanReturn(merge)
	twice["reviews"] = append(reviewSaid(true, "nothing moved"), reviewSaid(false, "the tag moved")...)
	if failures := w.checkReturn(w.round(), twice); !hasLine(failures,
		"reviews: release reviewer release is named 2 times") {
		t.Errorf("failures of a reviewer named twice = %q, want the twice line", failures)
	}
}

// TestReleaseCarriesAReviewWhoseVerdictStands: a round of generation 1 that says the release reviewer's verdict stands
// records, in its own write, the reviewer's review of the new candidate as carried: passed, with the end carried, the
// candidate it read as its start, the new candidate as its head, and a reason naming the earlier review and the
// integrator's reason. The reviewer starts no step, and the first release step starts at the new candidate. The round's
// reason records its gate choice and the carry.
func TestReleaseCarriesAReviewWhoseVerdictStands(t *testing.T) {
	t.Parallel()
	w := newStaleWorld(t, "origin-2.txt", nil)
	merge := w.commitCleanMerge()
	const stands = "origin added origin-2.txt, which no release step and nothing the tag reads"
	ret := cleanReturn(merge)
	ret["reviews"] = reviewSaid(true, stands)
	w.integrate(ret)

	reviews, rereviews := w.generationReview()
	if len(reviews) != 1 || !reviews[0].Passed || reviews[0].End != store.ReleaseCarriedEnd ||
		reviews[0].Start != w.candidate || reviews[0].Head != merge || reviews[0].Step != releaseReviewerName ||
		!strings.HasPrefix(reviews[0].Reason, "carried from the review of release reviewer release at attempt ") ||
		!strings.HasSuffix(reviews[0].Reason, "the release integrator said it stands: "+stands) {
		t.Fatalf("reviews at generation 1 = %+v, want the verdict on %s carried to %s", reviews, w.candidate, merge)
	}
	if len(rereviews) != 0 {
		t.Errorf("rereview runs = %+v, want none", rereviews)
	}
	if len(w.reviewer.calls) != 1 || w.releaseStepStarted(w.generationID("review "+releaseReviewerName)) {
		t.Errorf("reviewer starts = %+v, want only the first candidate's review", w.reviewer.calls)
	}
	if step := w.started(w.generationID("release 1")); step.Revision != merge {
		t.Errorf("first step starts at %s, want the new candidate %s", step.Revision, merge)
	}
	rounds := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartIntegrator)
	want := "verification ran Done gate lint: a Done gate runs by default\nrelease reviewer release's verdict on " +
		"candidate " + w.candidate + " is carried to " + merge + ": " + stands
	if len(rounds) != 2 || !rounds[1].Passed || rounds[1].Reason != want {
		t.Errorf("integrator runs = %+v, want the round of generation 1 passed with %q", rounds, want)
	}
}

// TestReleaseRereviewsWithTheDelta: a round of generation 1 that says the release reviewer's verdict does not stand, or
// gives no entry for it, records a rereview run at the reviewer's position with the candidate it read as its start, the
// new candidate as its head, its earlier verdict as its evidence and why it reviews again as its reason. The reviewer
// then starts at the new candidate over the whole change, and its prompt adds its earlier verdict, that reason and the
// change since the candidate it read.
func TestReleaseRereviewsWithTheDelta(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		reviews []map[string]any
		reason  string
	}{
		{
			name:    "its verdict does not stand",
			reviews: reviewSaid(false, "origin changed the tag script"),
			reason:  "the release integrator said its verdict does not stand: origin changed the tag script",
		},
		{
			name:    "no entry for it",
			reviews: []map[string]any{},
			reason:  "the release integrator's return gave no entry for it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newStaleWorld(t, "origin-2.txt", nil)
			merge := w.commitCleanMerge()
			ret := cleanReturn(merge)
			ret["reviews"] = tc.reviews
			w.endStep(w.round(), "done", "done", ret)
			w.pass()

			reviews, rereviews := w.generationReview()
			held := "held candidate " + w.candidate + "; its review ended done"
			if len(rereviews) != 1 || rereviews[0].Start != w.candidate || rereviews[0].Head != merge ||
				rereviews[0].Evidence != held || rereviews[0].Reason != tc.reason || rereviews[0].Passed {
				t.Fatalf("rereview runs = %+v, want one from %s to %s with %q", rereviews, w.candidate, merge,
					tc.reason)
			}
			if len(reviews) != 0 {
				t.Errorf("reviews at generation 1 = %+v, want none before the reviewer ends", reviews)
			}
			if len(w.reviewer.calls) != 2 || w.reviewer.calls[1].revision != merge {
				t.Fatalf("reviewer starts = %+v, want the reviewer started again at %s", w.reviewer.calls, merge)
			}
			prompt := w.started(w.generationID("review " + releaseReviewerName)).Prompt
			for _, want := range []string{
				plainReviewPrompt + "\n\nYou reviewed an earlier candidate of this release: you " + held + ".",
				"Your verdict was not carried over: " + tc.reason + ".",
				"`git diff " + w.candidate + " " + merge + "`",
			} {
				if !strings.Contains(prompt, want) {
					t.Errorf("reviewer prompt lacks %q:\n%s", want, prompt)
				}
			}
		})
	}
}

// TestReleaseReviewsInFullWhenTheEpicsChangeMayHaveMoved: a candidate prepared again whose epic's change may have moved
// carries no verdict and records no rereview, so the release reviewer reviews the new candidate in full with the plain
// prompt: when the epic's branch moved, when git's merge of origin's commit conflicts, and when the merge changes a path
// the epic's own change touched outside git's merge. In the first two the integrator's prompt lists no earlier review;
// in the last it does, and the round's record says why no verdict was carried though the integrator said it stands.
func TestReleaseReviewsInFullWhenTheEpicsChangeMayHaveMoved(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		file   string
		before func(t *testing.T) func(w *releaseWorld)
		ret    func(w staleWorld) map[string]any
		listed bool
		why    string
	}{
		{
			name: "the epic's branch moved",
			file: "origin-2.txt",
			before: func(t *testing.T) func(w *releaseWorld) {
				t.Helper()
				return func(w *releaseWorld) {
					nodeGit(t, w.repo, "checkout", "-q", nodeBranchName)
					nodeWrite(t, w.repo, "epic-2.txt", "more\n")
					nodeGit(t, w.repo, "add", "epic-2.txt")
					nodeGit(t, w.repo, "commit", "-q", "-m", "more epic work")
					nodeGit(t, w.repo, "checkout", "-q", "main")
				}
			},
			ret: func(w staleWorld) map[string]any { return cleanReturn(w.commitCleanMerge()) },
		},
		{
			name: "git's merge conflicts",
			file: "epic.txt",
			ret: func(w staleWorld) map[string]any {
				nodeWrite(w.t, w.worktree(), "epic.txt", "change\norigin\n")
				nodeGit(w.t, w.worktree(), "add", "epic.txt")
				ret := cleanReturn(w.commitCleanMerge())
				ret["resolutions"] = []map[string]any{{"path": "epic.txt", "took": "both", "kept": "both lines stay"}}
				return ret
			},
		},
		{
			name: "the merge changes the epic's own file",
			file: "origin-2.txt",
			ret: func(w staleWorld) map[string]any {
				nodeWrite(w.t, w.worktree(), "epic.txt", "change, against main\n")
				nodeGit(w.t, w.worktree(), "add", "epic.txt")
				ret := cleanReturn(w.commitCleanMerge())
				ret["other_changes"] = []map[string]any{{"path": "epic.txt", "change": "reads main's new file"}}
				ret["reviews"] = reviewSaid(true, "origin added a file no release step reads")
				return ret
			},
			listed: true,
			why:    "the merge changes epic.txt outside git's merge, which the epic's own change touched",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var before func(w *releaseWorld)
			if tc.before != nil {
				before = tc.before(t)
			}
			w := newStaleWorld(t, tc.file, before)
			prompt := w.started(w.round()).Prompt
			none := "\n## Earlier reviews\nNone.\n\n## Origin since the reviewed candidates\nNone.\n"
			if listed := !strings.Contains(prompt, none); listed != tc.listed {
				t.Errorf("prompt lists an earlier review = %t, want %t:\n%s", listed, tc.listed, prompt)
			}
			w.integrate(tc.ret(w))

			rounds := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartIntegrator)
			if len(rounds) != 2 || !rounds[1].Passed {
				t.Fatalf("integrator runs = %+v, want the round of generation 1 passed", rounds)
			}
			merge := rounds[1].Head
			reviews, rereviews := w.generationReview()
			if len(reviews) != 0 || len(rereviews) != 0 {
				t.Errorf("reviews = %+v and rereviews = %+v at generation 1, want neither", reviews, rereviews)
			}
			if len(w.reviewer.calls) != 2 || w.reviewer.calls[1].revision != merge {
				t.Fatalf("reviewer starts = %+v, want the reviewer started again at %s", w.reviewer.calls, merge)
			}
			if prompt := w.started(w.generationID("review " + releaseReviewerName)).Prompt; prompt !=
				plainReviewPrompt {
				t.Errorf("reviewer prompt = %q, want the plain prompt %q", prompt, plainReviewPrompt)
			}
			full := "every release reviewer reviews candidate " + merge + " in full: " + tc.why
			if got := rounds[1].Reason; tc.why != "" && !strings.HasSuffix(got, full) {
				t.Errorf("round reason = %q, want it to end %q", got, full)
			}
		})
	}
}

// TestEarlierReviewsFloor: earlierReviews lists the release reviewer's passed review of the candidate a generation
// replaces only for a clean merge of origin's commit into the tip that candidate was made from. A renumber of a tip that
// holds origin's commit, a merge that conflicts, a tip that moved, generation 0, a review that refused and a review
// that held the candidate only because its step ended at an engine restart list none. A verdict carried to the
// replaced candidate is listed with the candidate it was given on, and the return of the step that gave it.
// epicChangeMoved names a renumber and a conflict as moving the epic's change before any git runs.
func TestEarlierReviewsFloor(t *testing.T) {
	t.Parallel()
	run := store.NodeRun{ID: "run", Path: releaseNodePath, Kind: kindRelease, Attempt: 1}
	reviewersOf := defaultReleaseReviewer(t, true)
	given := store.ReleaseReviewWorkID(run.ID, 1, 0, releaseReviewerName)
	flow := func(refused bool) store.EpicFlow {
		return store.EpicFlow{
			Epic:  store.EpicRow{Reviewers: reviewersOf},
			Steps: []store.StepRow{{ID: given, End: "done", Result: `{"status":"done"}`}},
			Releases: []store.ReleaseRun{
				{Part: store.ReleasePartCandidate, Attempt: 1, Start: "t1", Head: "c1", Passed: true},
				{
					Part: store.ReleasePartReview, Attempt: 1, Position: 1, Step: releaseReviewerName, Head: "c1",
					End: "done", Passed: !refused,
				},
				{Part: store.ReleasePartVerify, Attempt: 1, Head: "c1"},
			},
		}
	}
	clean := kinds.ReleaseResult{Behind: true, Tip: "t1", Origin: "o2"}
	got := earlierReviews(flow(false), run, clean)
	if len(got) != 1 || got[0].reviewer != releaseReviewerName || got[0].reviewed != "c1" || got[0].position != 2 ||
		got[0].ret != `{"status":"done"}` {
		t.Fatalf("earlierReviews(clean merge) = %+v, want the release reviewer's review of c1 at position 2", got)
	}
	held := clean
	held.Behind = false
	conflicted := clean
	conflicted.Conflicts = []string{"epic.txt"}
	moved := clean
	moved.Tip = "t2"
	first := flow(false)
	first.Releases = first.Releases[:2]
	restarted := flow(false)
	restarted.Releases[1].End, restarted.Releases[1].Reason = "engine_restarted", "review ended engine_restarted"
	for name, tc := range map[string]struct {
		flow   store.EpicFlow
		result kinds.ReleaseResult
	}{
		"a renumber of a tip that holds origin":  {flow: flow(false), result: held},
		"a merge that conflicts":                 {flow: flow(false), result: conflicted},
		"a tip that moved":                       {flow: flow(false), result: moved},
		"generation 0":                           {flow: first, result: clean},
		"a review that refused":                  {flow: flow(true), result: clean},
		"a review whose step ended at a restart": {flow: restarted, result: clean},
	} {
		if listed := earlierReviews(tc.flow, run, tc.result); len(listed) != 0 {
			t.Errorf("earlierReviews(%s) = %+v, want none", name, listed)
		}
	}

	chain := flow(false)
	chain.Releases = append(chain.Releases,
		store.ReleaseRun{Part: store.ReleasePartCandidate, Attempt: 1, Position: 1, Start: "t1", Head: "c2",
			Passed: true},
		store.ReleaseRun{
			Part: store.ReleasePartReview, Attempt: 1, Position: 2, Step: releaseReviewerName, Start: "c1",
			Head: "c2", End: store.ReleaseCarriedEnd, Passed: true, Reason: "carried",
		},
		store.ReleaseRun{Part: store.ReleasePartVerify, Attempt: 1, Position: 1, Head: "c2"},
	)
	carried := earlierReviews(chain, run, clean)
	if len(carried) != 1 || carried[0].reviewed != "c1" || carried[0].verdict.Head != "c2" ||
		carried[0].position != 3 ||
		carried[0].ret != `{"status":"done"}` {
		t.Errorf("earlierReviews(carried) = %+v, want the verdict given on c1, carried to c2, at position 3", carried)
	}

	for _, merge := range []integratorMerge{{held: true}, {conflicts: []string{"epic.txt"}}} {
		why, err := epicChangeMoved(t.Context(), nil, merge, integratorReturn{})
		if err != nil || why == "" {
			t.Errorf("epicChangeMoved(%+v) = %q, %v, want why the epic's change moved", merge, why, err)
		}
	}
}

// TestReleaseIntegratorReadoptCheckKeepsTheEarlierReviews: the re-adopted step of a round of generation 1 gets back the
// check its start gave it, with the earlier verdict it lists and the Done gates: it refuses a return that leaves out the
// listed reviewer, one that names a reviewer not listed, and one that leaves out the Done gate, and passes a complete
// return.
func TestReleaseIntegratorReadoptCheckKeepsTheEarlierReviews(t *testing.T) {
	t.Parallel()
	w := newStaleWorld(t, "origin-2.txt", nil)
	row, err := w.runner.storedStep(t.Context(), runEpic, w.round())
	if err != nil {
		t.Fatalf("stored integrator step: %v", err)
	}
	check, err := ReadoptCheck(w.s, hostexec.LocalHosts(), w.runner.getenv)(t.Context(), row)
	if err != nil || check == nil {
		t.Fatalf("ReadoptCheck = %v, %v, want the integrator's check", check != nil, err)
	}
	merge := w.commitCleanMerge()
	complete := cleanReturn(merge)
	complete["reviews"] = reviewSaid(true, "nothing origin changed bears on the tag")
	unlisted := cleanReturn(merge)
	unlisted["reviews"] = append(reviewSaid(true, "nothing moved"), map[string]any{
		"reviewer": "security", "stands": true, "reason": "nothing moved",
	})
	ungated := withGates(cleanReturn(merge))
	ungated["reviews"] = reviewSaid(true, "nothing moved")
	for _, tc := range []struct {
		name string
		ret  map[string]any
		want string
	}{
		{name: "the reviewer left out", ret: cleanReturn(merge), want: "reviews: release reviewer release is not named"},
		{name: "a reviewer not listed", ret: unlisted, want: "reviews: Earlier reviews lists no verdict of security"},
		{name: "the gate left out", ret: ungated, want: "gates: Done gate lint is not named"},
		{name: "complete", ret: complete},
	} {
		encoded, encodeErr := json.Marshal(tc.ret)
		if encodeErr != nil {
			t.Fatalf("encode return: %v", encodeErr)
		}
		failures := check(encoded)
		if tc.want == "" && len(failures) != 0 {
			t.Errorf("re-adopted check of %s = %q, want none", tc.name, failures)
		}
		if tc.want != "" && !hasLine(failures, tc.want) {
			t.Errorf("re-adopted check of %s = %q, want a line that opens with %q", tc.name, failures, tc.want)
		}
	}
}
