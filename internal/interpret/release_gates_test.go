package interpret

import (
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// lintSkip is the reason a release integrator's return in the gate tests gives for skipping lint.
const lintSkip = "README is all either side changed, and lint reads only Go"

// alwaysLintGates are the gates of the gate tests' epic whose Done gate lint is declared always.
func alwaysLintGates() []store.EpicGate {
	return []store.EpicGate{{
		Name: "lint", Argv: []string{"./bin/lint"}, Limit: 60, Always: true, Note: "lints every Go package",
	}}
}

// newAlwaysWorld returns a conflict world whose epic's one Done gate, lint, is declared always.
func newAlwaysWorld(t *testing.T) conflictWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	w.gates = alwaysLintGates()
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	remote := mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, "README")
	origin := mergeOriginCommit(t, remote, "README")
	return conflictWorld{releaseWorld: w, tip: tip, origin: origin}
}

// withGates returns ret with gates as its Done gate choice.
func withGates(ret map[string]any, gates ...map[string]any) map[string]any {
	if gates == nil {
		gates = []map[string]any{}
	}
	ret["gates"] = gates
	return ret
}

// lintChoice is a choice of the Done gate lint: whether it ran and why.
func lintChoice(run bool, reason string) map[string]any {
	return map[string]any{"gate": integrateDoneGate, "run": run, "reason": reason}
}

// TestReleaseIntegratorReturnCheckHoldsTheGateChoice: the return check passes a done return that names the epic's one
// Done gate once with a reason, whether it ran or was skipped, and refuses one that leaves it out, gives it no reason,
// names it twice, names a gate the epic does not have, or gives a review when its prompt lists no earlier verdict. A
// blocked return's choice is not held to the floors.
func TestReleaseIntegratorReturnCheckHoldsTheGateChoice(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		do   func(w conflictWorld) map[string]any
		want string
	}{
		{name: "lint ran", do: func(w conflictWorld) map[string]any { return doneReturn(w.commitMerge()) }},
		{
			name: "lint skipped with a reason",
			do: func(w conflictWorld) map[string]any {
				return withGates(doneReturn(w.commitMerge()), lintChoice(false, lintSkip))
			},
		},
		{
			name: "lint left out",
			do:   func(w conflictWorld) map[string]any { return withGates(doneReturn(w.commitMerge())) },
			want: "gates: Done gate lint is not named; name every Done gate once",
		},
		{
			name: "lint with no reason",
			do: func(w conflictWorld) map[string]any {
				return withGates(doneReturn(w.commitMerge()), lintChoice(true, ""))
			},
			want: "gates: Done gate lint gives no reason",
		},
		{
			name: "lint named twice",
			do: func(w conflictWorld) map[string]any {
				return withGates(doneReturn(w.commitMerge()), lintChoice(true, "ran"), lintChoice(false, lintSkip))
			},
			want: "gates: Done gate lint is named 2 times",
		},
		{
			name: "a gate the epic does not have",
			do: func(w conflictWorld) map[string]any {
				return withGates(doneReturn(w.commitMerge()), lintChoice(true, "ran"), map[string]any{
					"gate": "build", "run": true, "reason": "built it",
				})
			},
			want: "gates: build is not a Done gate of the epic",
		},
		{
			name: "a review with no earlier verdict listed",
			do: func(w conflictWorld) map[string]any {
				ret := doneReturn(w.commitMerge())
				ret["reviews"] = []map[string]any{{"reviewer": "release", "stands": true, "reason": "nothing moved"}}
				return ret
			},
			want: "reviews: Earlier reviews lists no verdict of release",
		},
		{
			name: "blocked with no choice",
			do: func(conflictWorld) map[string]any {
				return blockedReturn("the two sides decide the greeting differently")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newConflictWorld(t)
			w.pass()
			failures := w.checkReturn(w.integratorID(), tc.do(w))
			if tc.want == "" {
				if len(failures) != 0 {
					t.Errorf("failures = %q, want none", failures)
				}
				return
			}
			if !hasLine(failures, tc.want) {
				t.Errorf("failures = %q, want a line that opens with %q", failures, tc.want)
			}
		})
	}
}

// TestReleaseIntegratorReturnCheckRunsAnAlwaysGate: an epic whose Done gate is declared always gives the integrator's
// prompt that gate with its always line and its note, and the return check refuses a done return that skips it, whatever
// its reason, and passes one that runs it.
func TestReleaseIntegratorReturnCheckRunsAnAlwaysGate(t *testing.T) {
	t.Parallel()
	w := newAlwaysWorld(t)
	w.pass()
	want := "## Done gates\n- lint: command [\"./bin/lint\"]\n  limit: 60s\n  needs: gate x 1\n" +
		"  always: runs on every verification, whatever the choice says\n  note: lints every Go package\n"
	if prompt := w.started(w.integratorID()).Prompt; !strings.Contains(prompt, want) {
		t.Errorf("prompt lacks %q:\n%s", want, prompt)
	}
	merge := w.commitMerge()
	skipped := w.checkReturn(w.integratorID(), withGates(doneReturn(merge), lintChoice(false, lintSkip)))
	if !hasLine(skipped, "gates: Done gate lint is declared always, so it is never skipped") {
		t.Errorf("failures of a skipped always gate = %q, want the always line", skipped)
	}
	if ran := w.checkReturn(w.integratorID(), doneReturn(merge)); len(ran) != 0 {
		t.Errorf("failures of a return that runs the always gate = %q, want none", ran)
	}
}

// TestReleaseIntegratorRecordsItsGateChoice: a passed round records its gate choice as its reason, each Done gate as run
// or skipped with its reason, and its merge becomes the candidate. A skipped gate starts no check and nothing records it
// as passed.
func TestReleaseIntegratorRecordsItsGateChoice(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.pass()
	merge := w.commitMerge()
	ret := withGates(doneReturn(merge), lintChoice(false, lintSkip))
	if failures := w.checkReturn(w.integratorID(), ret); len(failures) != 0 {
		t.Fatalf("return check = %q, want none", failures)
	}
	w.endStep(w.integratorID(), "done", "done", ret)
	w.pass()

	flow := w.flowOf(runEpic)
	rounds := releaseRunsOf(flow.Releases, store.ReleasePartIntegrator)
	want := "verification skipped Done gate lint: " + lintSkip
	if len(rounds) != 1 || !rounds[0].Passed || rounds[0].Head != merge || rounds[0].Reason != want {
		t.Fatalf("integrator runs = %+v, want the round passed at %s with the reason %q", rounds, merge, want)
	}
	if candidates := releaseRunsOf(flow.Releases, store.ReleasePartCandidate); len(candidates) != 1 ||
		candidates[0].Head != merge {
		t.Errorf("candidate runs = %+v, want the merge %s", candidates, merge)
	}
	if len(w.fakes.checks.calls) != 0 || len(flow.Checks) != 0 {
		t.Errorf("checks started = %+v, stored = %+v, want none for the skipped gate", w.fakes.checks.calls,
			flow.Checks)
	}
}

// TestReleaseIntegratorRecordRefusesAChoiceTheFloorsRefuse: a round whose step ended done with a return that skips the
// gate declared always, as a return the step loop's check never saw would, is recorded failed with the merge, the floor
// it failed and the choice it made, and the release stops there with no candidate.
func TestReleaseIntegratorRecordRefusesAChoiceTheFloorsRefuse(t *testing.T) {
	t.Parallel()
	w := newAlwaysWorld(t)
	w.pass()
	w.endStep(w.integratorID(), "done", "done", withGates(doneReturn(w.commitMerge()), lintChoice(false, lintSkip)))
	w.pass()

	flow := w.flowOf(runEpic)
	rounds := releaseRunsOf(flow.Releases, store.ReleasePartIntegrator)
	want := conflictMessage(w.tip, w.origin, []string{"README"}) +
		"; the release integrator's gate choice was refused: gates: Done gate lint is declared always"
	if len(rounds) != 1 || rounds[0].Passed || !strings.HasPrefix(rounds[0].Reason, want) ||
		!strings.HasSuffix(rounds[0].Reason, "\nverification skipped Done gate lint: "+lintSkip) {
		t.Fatalf("integrator runs = %+v, want the round failed with %q and its choice", rounds, want)
	}
	if flow.Epic.Phase != store.PhaseStopped || !strings.HasPrefix(flow.Epic.Parked, want) {
		t.Errorf("epic phase = %q, parked %q, want stopped with %q", flow.Epic.Phase, flow.Epic.Parked, want)
	}
	if candidates := releaseRunsOf(flow.Releases, store.ReleasePartCandidate); len(candidates) != 0 {
		t.Errorf("candidate runs = %+v, want none", candidates)
	}
}

// TestReleaseIntegratorBlockedRoundCarriesItsGateChoice: a blocked round whose return names the Done gates it ran before
// it stopped is recorded with its report and then that choice, and its stop says it too.
func TestReleaseIntegratorBlockedRoundCarriesItsGateChoice(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.pass()
	ret := withGates(blockedReturn("the epic's test fails against main's README", map[string]any{
		"path": "README", "candidate": "the epic's test reads change", "origin": "main says origin",
		"decision": "restate the epic's README test", "side": "candidate",
	}), lintChoice(true, "it ran before the merge stopped"))
	w.endStep(w.integratorID(), "returned", "blocked", ret)
	w.pass()

	flow := w.flowOf(runEpic)
	rounds := releaseRunsOf(flow.Releases, store.ReleasePartIntegrator)
	line := "\nverification ran Done gate lint: it ran before the merge stopped"
	if len(rounds) != 1 || rounds[0].Passed ||
		!strings.HasPrefix(rounds[0].Reason, "the release integrator stopped the merge of origin's commit") ||
		!strings.HasSuffix(rounds[0].Reason, line) {
		t.Fatalf("integrator runs = %+v, want the blocked report followed by %q", rounds, line)
	}
	if !strings.Contains(flow.Epic.Parked, line) {
		t.Errorf("parked = %q, want it to carry %q", flow.Epic.Parked, line)
	}
	if !slices.ContainsFunc(flow.Releases, func(r store.ReleaseRun) bool { return r.Part == store.ReleasePartReplan }) {
		t.Errorf("release runs = %+v, want the replan the blocked path asks for", flow.Releases)
	}
}
