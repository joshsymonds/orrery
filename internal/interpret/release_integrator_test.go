package interpret

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// integratorAgent is the agent the role file of integratorRoles names for the release integrator.
const integratorAgent = "integrator-agent"

// conflictWorld is a release world whose epic's tip and origin's main both change README from the base, so the
// release's merge of origin's commit into the tip conflicts there. tip is the epic's branch tip and origin origin's
// commit. Nothing has passed yet.
type conflictWorld struct {
	*releaseWorld
	tip, origin string
}

// newConflictWorld returns a conflict world whose epic declares requirements, stored clean with the tag step as its one
// release step.
func newConflictWorld(t *testing.T, requirements ...store.EpicRequirement) conflictWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	w.requirements = requirements
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	remote := mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, "README")
	origin := mergeOriginCommit(t, remote, "README")
	return conflictWorld{releaseWorld: w, tip: tip, origin: origin}
}

// integratorRoles returns a getenv whose role file names releaseAgent, judgeAgent and integratorAgent.
func integratorRoles(t *testing.T) func(string) string {
	t.Helper()
	config := t.TempDir()
	dir := filepath.Join(config, "orrery")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("make role directory: %v", err)
	}
	roles := fmt.Sprintf("release = %q\njudge = %q\nintegrator = %q\n", releaseAgent, judgeAgent, integratorAgent)
	if err := os.WriteFile(filepath.Join(dir, "roles.toml"), []byte(roles), 0o600); err != nil {
		t.Fatalf("write role file: %v", err)
	}
	return func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return config
		}
		return ""
	}
}

// integratorID returns the id of round of the release integrator, as the release derives it from its run at the
// run's first generation.
func (w conflictWorld) integratorID(round int) string {
	w.t.Helper()
	return workID(w.t, w.s, releaseNodePath, fmt.Sprintf("integrator %d", round))
}

// integratorDir returns the directory the release integrator's worktree is under, named by the run and its attempt.
func (w conflictWorld) integratorDir() string {
	w.t.Helper()
	return filepath.Join(w.state, "release-worktrees", workID(w.t, w.s, releaseNodePath, "integrator"))
}

// worktree returns the worktree the release integrator runs in.
func (w conflictWorld) worktree() string {
	w.t.Helper()
	return filepath.Join(w.integratorDir(), "wt")
}

// started returns the start of the step with id, which must have been started.
func (w conflictWorld) started(id string) enginehost.StepStart {
	w.t.Helper()
	i := slices.IndexFunc(w.fakes.steps.calls, func(start enginehost.StepStart) bool { return start.ID == id })
	if i < 0 {
		w.t.Fatalf("step starts = %+v, want one with id %s", w.fakes.steps.calls, id)
	}
	return w.fakes.steps.calls[i]
}

// integratorStarts returns the starts of release integrator steps, in order.
func (w conflictWorld) integratorStarts() []enginehost.StepStart {
	var starts []enginehost.StepStart
	for _, start := range w.fakes.steps.calls {
		if start.Schema == IntegratorSchema {
			starts = append(starts, start)
		}
	}
	return starts
}

// commitMerge finishes the merge in the integrator's worktree as the integrator would: README keeps both sides' lines,
// and the merge is committed with the message the prompt gives. It returns the merge commit.
func (w conflictWorld) commitMerge() string {
	w.t.Helper()
	wt := w.worktree()
	nodeWrite(w.t, wt, "README", "change\norigin\n")
	nodeGit(w.t, wt, "add", "README")
	nodeGit(w.t, wt, "commit", "-q", "-m", mergeMessage(w.tip, w.origin))
	return nodeGit(w.t, wt, "rev-parse", "HEAD")
}

// doneReturn is a done return of the release integrator that names commit and resolves README by keeping both sides,
// with others as its other_changes.
func doneReturn(commit string, others ...map[string]any) map[string]any {
	if others == nil {
		others = []map[string]any{}
	}
	return map[string]any{
		"status": "done", "commit": commit,
		"resolutions":   []map[string]any{{"path": "README", "took": "both", "kept": "both lines stay"}},
		"other_changes": others, "checks": []map[string]any{}, "unresolved": []map[string]any{},
		"evidence": "README keeps both lines",
	}
}

// checkReturn runs the return check of the step with id on ret and returns its failure lines.
func (w conflictWorld) checkReturn(id string, ret map[string]any) []string {
	w.t.Helper()
	check := w.started(id).ReturnCheck
	if check == nil {
		w.t.Fatalf("step %s carries no return check", id)
	}
	encoded, err := json.Marshal(ret)
	if err != nil {
		w.t.Fatalf("encode return: %v", err)
	}
	return check(encoded)
}

// gateID returns the id of the Done gate check the release runs on the commit of round of the release integrator.
func (w conflictWorld) gateID(round int) string {
	w.t.Helper()
	run := runAt(w.t, w.s, releaseNodePath)
	return store.NodeWorkID(fmt.Sprintf("%s-integrator-%d", run.ID, round), run.Attempt, "done 1")
}

// endIntegrator ends round of the release integrator done with a return that names commit, and passes.
func (w conflictWorld) endIntegrator(round int, commit string) {
	w.t.Helper()
	w.endStep(w.integratorID(round), "done", "done", doneReturn(commit))
	w.pass()
}

// hasLine reports whether one of failures starts with prefix.
func hasLine(failures []string, prefix string) bool {
	return slices.ContainsFunc(failures, func(line string) bool { return strings.HasPrefix(line, prefix) })
}

// TestEpicBaseReleaseConflictStartsIntegratorWithMergeInProgress: a merge of origin's commit that conflicts records the
// conflict, origin's commit and its branch, and no candidate or revision, and does not stop the release. It starts the
// release integrator as an unowned writing step, labeled with the epic and the run's path, in a worktree that holds the
// merge in progress at the tip with zdiff3 conflicts. Its role file names no integrator, so it runs under the release
// agent. Its prompt is the role text, then each section in order.
func TestEpicBaseReleaseConflictStartsIntegratorWithMergeInProgress(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	var got struct {
		mergeWire
		Origin    string   `json:"origin"`
		Branch    string   `json:"branch"`
		Conflicts []string `json:"conflicts"`
	}
	if err := json.Unmarshal([]byte(run.Result), &got); err != nil {
		t.Fatalf("Result %q: %v", run.Result, err)
	}
	if got.Error != "" || !got.Behind || got.Tip != w.tip || got.Candidate != "" || got.Origin != w.origin ||
		got.Branch != "refs/heads/main" || !slices.Equal(got.Conflicts, []string{"README"}) || run.Revision != "" {
		t.Errorf("Result = %+v with revision %q, want the conflict in README of %s into %s and no candidate",
			got, run.Revision, w.origin, w.tip)
	}
	if flow := w.flowOf(runEpic); flow.Epic.Phase != store.PhaseReleasing || flow.Epic.Parked != "" {
		t.Errorf("epic phase = %q, parked %q, want releasing and not parked", flow.Epic.Phase, flow.Epic.Parked)
	}

	start := w.started(w.integratorID(1))
	if start.Agent != releaseAgent || start.Workspace != w.worktree() || start.Revision != w.tip || start.ReadOnly ||
		!start.Unowned || start.DoneValue != "done" || start.Epic != runEpic || start.Task != releaseNodePath ||
		start.Schema != IntegratorSchema || start.ReturnCheck == nil {
		t.Errorf("integrator start = %+v, want an unowned writing step under %s in %s at %s, labeled %s and %s",
			start, releaseAgent, w.worktree(), w.tip, runEpic, releaseNodePath)
	}
	wt := w.worktree()
	if head := nodeGit(t, wt, "rev-parse", "HEAD"); head != w.tip {
		t.Errorf("worktree HEAD = %s, want the tip %s", head, w.tip)
	}
	if mergeHead := nodeGit(t, wt, "rev-parse", "MERGE_HEAD"); mergeHead != w.origin {
		t.Errorf("worktree MERGE_HEAD = %s, want origin's commit %s", mergeHead, w.origin)
	}
	if unmerged := nodeGit(t, wt, "diff", "--name-only", "--diff-filter=U"); unmerged != "README" {
		t.Errorf("unmerged paths = %q, want README", unmerged)
	}
	readme, err := os.ReadFile(filepath.Join(wt, "README"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	for _, marker := range []string{"<<<<<<< ", "||||||| ", "=======\n", ">>>>>>> ", "base\n"} {
		if !strings.Contains(string(readme), marker) {
			t.Errorf("README = %q, want the zdiff3 conflict with %q", readme, marker)
		}
	}

	prompt := start.Prompt
	if !strings.HasPrefix(prompt, "You are the release integrator for one epic.") {
		t.Errorf("prompt starts %q, want the role text", prompt[:min(len(prompt), 80)])
	}
	at := -1
	for _, heading := range []string{
		"## Contract", "## Decisions", "## Merge", "## Conflicted paths", "## Tasks", "## Candidate commits",
		"## Origin commits", "## Same change", "## Checks after you", "## Previous attempt",
	} {
		i := strings.Index(prompt, "\n"+heading+"\n")
		if i <= at {
			t.Errorf("prompt has %q at %d, want it after the section before it at %d", heading, i, at)
		}
		at = i
	}
	for _, want := range []string{
		"Worktree: " + wt,
		"Release tip: " + w.tip,
		"Origin's commit: " + w.origin + " from origin's refs/heads/main",
		"Merge base: " + nodeGit(t, w.repo, "merge-base", w.tip, w.origin),
		"Commit message: Merge origin's commit " + w.origin + " into release tip " + w.tip,
		"- UU README\n  candidate: " + w.tip + " epic work\n  origin: " + w.origin + " origin README\n",
		"## Candidate commits\n" + nodeGit(t, w.repo, "log", "--oneline", "-n1", w.tip) + "\n",
		"## Origin commits\n" + nodeGit(t, w.repo, "log", "--oneline", "-n1", w.origin) + "\n",
		"Done gates:\n- lint: [\"./bin/lint\"]\nCaptures: None.\n",
		"## Previous attempt\nNone.\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

// TestReleaseIntegratorRunsUnderItsOwnRole: a role file that names an integrator runs the release integrator under that
// agent, not the release agent.
func TestReleaseIntegratorRunsUnderItsOwnRole(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.runner.getenv = integratorRoles(t)
	w.pass()

	if got := w.started(w.integratorID(1)).Agent; got != integratorAgent {
		t.Errorf("integrator agent = %q, want %q from the role file", got, integratorAgent)
	}
}

// TestReleaseConflictWithoutRoleStopsWithConflictReason: with no role file the integrator cannot start, so the release
// stops at once with the conflict, which names the conflicted path, and the reason the integrator did not start. Its
// worktree is not left behind.
func TestReleaseConflictWithoutRoleStopsWithConflictReason(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.runner.getenv = func(string) string { return "" }
	w.pass()

	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != store.PhaseStopped {
		t.Fatalf("epic phase = %q, want stopped", flow.Epic.Phase)
	}
	want := conflictMessage(w.tip, w.origin, []string{"README"}) + "; the release integrator did not start: "
	if !strings.HasPrefix(flow.Epic.Parked, want) {
		t.Errorf("parked = %q, want it to open with %q", flow.Epic.Parked, want)
	}
	if len(w.integratorStarts()) != 0 {
		t.Errorf("integrator starts = %+v, want none", w.integratorStarts())
	}
	if _, err := os.Stat(w.integratorDir()); !os.IsNotExist(err) {
		t.Errorf("integrator directory stat = %v, want it absent", err)
	}
}

// TestReleaseIntegratorMergeRunsGatesAndCapturesThenBecomesCandidate: a done return whose merge passes the return check
// is recorded as a passed round with the merge as its head, and the merge is pinned under the run's release ref. The
// epic's Done gate runs at the merge, then its capture, in the green phase, and once both pass the merge is recorded as
// the candidate, the integrator's worktree is removed, and the first release step starts from the merge.
func TestReleaseIntegratorMergeRunsGatesAndCapturesThenBecomesCandidate(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t, nodeRequirements()...)
	w.pass()
	merge := w.commitMerge()
	if failures := w.checkReturn(w.integratorID(1), doneReturn(merge)); len(failures) != 0 {
		t.Fatalf("return check of a correct merge = %q, want no failure", failures)
	}
	w.endIntegrator(1, merge)

	run := runAt(t, w.s, releaseNodePath)
	rounds := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartIntegrator)
	if len(rounds) != 1 || !rounds[0].Passed || rounds[0].Head != merge || rounds[0].Start != w.tip ||
		rounds[0].Position != 1 {
		t.Fatalf("integrator runs = %+v, want round 1 passed at %s", rounds, merge)
	}
	ref := fmt.Sprintf("refs/orrery/epic-runner/release/%s-%d", run.ID, run.Attempt)
	if pinned := nodeGit(t, w.repo, "rev-parse", ref); pinned != merge {
		t.Errorf("%s = %s, want the merge %s", ref, pinned, merge)
	}
	gate, err := w.s.NodeCheck(t.Context(), w.gateID(1))
	if err != nil || gate.Revision != merge {
		t.Fatalf("Done gate check = %+v, %v, want it started at the merge %s", gate, err, merge)
	}
	if len(w.fakes.evidence.calls) != 0 {
		t.Errorf("captures = %+v before the Done gate passed, want none", w.fakes.evidence.calls)
	}

	nodeEndCheck(t, w.s, w.gateID(1), 0, "")
	w.pass()
	if len(w.fakes.evidence.calls) != 1 {
		t.Fatalf("captures = %+v, want the capture of R1", w.fakes.evidence.calls)
	}
	capture := w.fakes.evidence.calls[0]
	if capture.item != "R1" || capture.revision != merge || capture.phase != "green" {
		t.Errorf("capture = %+v, want R1 green at %s", capture, merge)
	}

	nodeEndEvidence(t, w.s, capture.id)
	w.pass()
	candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
	if len(candidates) != 1 || candidates[0].Head != merge || candidates[0].Start != w.tip ||
		!slices.Equal(candidates[0].Commits, []string{merge}) {
		t.Errorf("candidate runs = %+v, want the merge %s from the tip %s", candidates, merge, w.tip)
	}
	if _, err = os.Stat(w.integratorDir()); !os.IsNotExist(err) {
		t.Errorf("integrator directory stat = %v, want it removed once the candidate is recorded", err)
	}
	step := w.started(w.stepID(1))
	if step.Revision != merge {
		t.Errorf("release step 1 starts at %s, want the merge %s", step.Revision, merge)
	}
}

// TestReleaseIntegratorReturnCheckRefusesBadCommits: the return check passes a correct merge and refuses each commit or
// return the engine's checks rule out, naming the check: parents other than the tip and then origin's commit, a commit
// added on top of the merge, a conflict marker left in, a change outside the conflicts that other_changes does not
// list, a merge not committed, a resolution missing, and a blocked return that names a commit.
func TestReleaseIntegratorReturnCheckRefusesBadCommits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		do   func(w conflictWorld) map[string]any
		want string
	}{
		{
			name: "correct merge",
			do:   func(w conflictWorld) map[string]any { return doneReturn(w.commitMerge()) },
		},
		{
			name: "wrong parents",
			do: func(w conflictWorld) map[string]any {
				merge := w.commitMerge()
				swapped := nodeGit(w.t, w.worktree(), "commit-tree", merge+"^{tree}", "-p", w.origin, "-p", w.tip,
					"-m", "swapped")
				nodeGit(w.t, w.worktree(), "checkout", "-q", "--detach", swapped)
				return doneReturn(swapped)
			},
			want: "parents: ",
		},
		{
			name: "extra commit",
			do: func(w conflictWorld) map[string]any {
				w.commitMerge()
				return doneReturn(commitIn(w.t, w.worktree(), "extra.txt"), map[string]any{
					"path": "extra.txt", "change": "an extra file",
				})
			},
			want: "parents: ",
		},
		{
			name: "leftover marker",
			do: func(w conflictWorld) map[string]any {
				nodeGit(w.t, w.worktree(), "add", "README")
				nodeGit(w.t, w.worktree(), "commit", "-q", "-m", mergeMessage(w.tip, w.origin))
				return doneReturn(nodeGit(w.t, w.worktree(), "rev-parse", "HEAD"))
			},
			want: "markers: conflict-marker lines remain in README",
		},
		{
			name: "unlisted change",
			do: func(w conflictWorld) map[string]any {
				nodeWrite(w.t, w.worktree(), "notes.txt", "notes\n")
				nodeGit(w.t, w.worktree(), "add", "notes.txt")
				return doneReturn(w.commitMerge())
			},
			want: "other_changes: the commit changes what git merged without conflict in notes.txt",
		},
		{
			name: "listed change",
			do: func(w conflictWorld) map[string]any {
				nodeWrite(w.t, w.worktree(), "notes.txt", "notes\n")
				nodeGit(w.t, w.worktree(), "add", "notes.txt")
				merge := w.commitMerge()
				return doneReturn(merge, map[string]any{"path": "notes.txt", "change": "notes the merge needs"})
			},
		},
		{
			name: "merge not committed",
			do:   func(w conflictWorld) map[string]any { return doneReturn(w.tip) },
			want: "unmerged: git ls-files -u lists README",
		},
		{
			name: "resolution missing",
			do: func(w conflictWorld) map[string]any {
				ret := doneReturn(w.commitMerge())
				ret["resolutions"] = []map[string]any{}
				return ret
			},
			want: "resolutions: conflicted path README has no resolution",
		},
		{
			name: "blocked naming a commit",
			do: func(w conflictWorld) map[string]any {
				return map[string]any{
					"status": "blocked", "commit": w.tip, "resolutions": []map[string]any{},
					"other_changes": []map[string]any{}, "checks": []map[string]any{},
					"unresolved": []map[string]any{}, "evidence": "stopped",
				}
			},
			want: "commit: a blocked return names no commit",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newConflictWorld(t)
			w.pass()
			failures := w.checkReturn(w.integratorID(1), tc.do(w))
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

// TestReleaseIntegratorBlockedParksWithUnresolved: a blocked return on a merge-side decision stops the release, and the
// epic's park carries the integrator's report: each path it stopped on with its side, the decision that would settle it
// and what each side meant. The integrator's worktree is removed with the stop, and no replan is recorded.
func TestReleaseIntegratorBlockedParksWithUnresolved(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.pass()
	w.endStep(w.integratorID(1), "returned", "blocked", map[string]any{
		"status": "blocked", "commit": "", "resolutions": []map[string]any{}, "other_changes": []map[string]any{},
		"checks": []map[string]any{},
		"unresolved": []map[string]any{{
			"path": "README", "candidate": "the epic greets in English", "origin": "main greets in French",
			"decision": "which greeting the product ships", "side": "merge",
		}},
		"evidence": "the two sides decide the greeting differently",
	})
	w.pass()

	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != store.PhaseStopped {
		t.Fatalf("epic phase = %q, want stopped", flow.Epic.Phase)
	}
	want := fmt.Sprintf("the release integrator stopped the merge of origin's commit %s into release tip %s:\n"+
		"- README: side: merge; decision: which greeting the product ships; candidate: the epic greets in English; "+
		"origin: main greets in French", w.origin, w.tip)
	if flow.Epic.Parked != want {
		t.Errorf("parked = %q, want %q", flow.Epic.Parked, want)
	}
	if run := runAt(t, w.s, releaseNodePath); run.Output != want {
		t.Errorf("run output = %q, want %q", run.Output, want)
	}
	if _, err := os.Stat(w.integratorDir()); !os.IsNotExist(err) {
		t.Errorf("integrator directory stat = %v, want it removed with the stop", err)
	}
	if slices.ContainsFunc(flow.Releases, func(r store.ReleaseRun) bool { return r.Part == store.ReleasePartReplan }) {
		t.Errorf("release runs = %+v, want no replan for a merge-side decision", flow.Releases)
	}
}

// TestBlockedReasonFitsThePark: a blocked report with many long paths fits the park's limit, keeps the first path and
// its decision, and counts the paths it had no room for.
func TestBlockedReasonFitsThePark(t *testing.T) {
	t.Parallel()
	var ret integratorReturn
	for i := range 12 {
		ret.Unresolved = append(ret.Unresolved, integratorUnresolved{
			Path: fmt.Sprintf("internal/pkg%d/file.go", i), Decision: "decide " + strings.Repeat("d", 80),
			Candidate: strings.Repeat("c", 200), Origin: strings.Repeat("o", 200), Side: "merge",
		})
	}
	merge := integratorMerge{tip: strings.Repeat("a", 40), origin: strings.Repeat("b", 40)}
	got := blockedReason(merge, ret)
	if len(got) > store.TurnReasonMax {
		t.Errorf("reason is %d bytes, want at most %d", len(got), store.TurnReasonMax)
	}
	for _, want := range []string{"- internal/pkg0/file.go: side: merge; decision: decide ddd", "\npaths not shown: "} {
		if !strings.Contains(got, want) {
			t.Errorf("reason = %q, want it to hold %q", got, want)
		}
	}
}

// TestReleaseIntegratorGateFailureSendsBackTwiceThenStops: a Done gate that fails on the integrator's merge starts the
// next round in the same worktree, with the merge as HEAD and the gate's exit and output under Previous attempt. A gate
// that fails on the merges of all three rounds stops the release with the gate's reason, and no fourth round starts.
func TestReleaseIntegratorGateFailureSendsBackTwiceThenStops(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.pass()
	merge := w.commitMerge()
	w.endIntegrator(1, merge)
	nodeEndCheck(t, w.s, w.gateID(1), 2, "lint: README has two lines")
	w.pass()

	second := w.started(w.integratorID(2))
	if second.Workspace != w.worktree() || second.Revision != w.tip || second.Agent != releaseAgent {
		t.Errorf("second round start = %+v, want it in %s at the tip %s", second, w.worktree(), w.tip)
	}
	for _, want := range []string{
		"Committed: an earlier round committed this merge as " + merge + ", which is HEAD now.",
		"## Previous attempt\nDone gate lint failed on commit " + merge + ": it exited 2.\nstderr tail:\n" +
			"lint: README has two lines\n",
	} {
		if !strings.Contains(second.Prompt, want) {
			t.Errorf("second round prompt lacks %q:\n%s", want, second.Prompt)
		}
	}
	if flow := w.flowOf(runEpic); flow.Epic.Phase != store.PhaseReleasing {
		t.Errorf("epic phase = %q after one failed gate, want releasing", flow.Epic.Phase)
	}

	w.endIntegrator(2, merge)
	nodeEndCheck(t, w.s, w.gateID(2), 2, "lint: still two lines")
	w.pass()
	w.endIntegrator(3, merge)
	nodeEndCheck(t, w.s, w.gateID(3), 2, "lint: still two lines")
	w.pass()

	if starts := w.integratorStarts(); len(starts) != 3 {
		t.Errorf("integrator starts = %d, want three rounds", len(starts))
	}
	flow := w.flowOf(runEpic)
	if want := fmt.Sprintf("done gate lint did not pass at %s", merge); flow.Epic.Phase != store.PhaseStopped ||
		flow.Epic.Parked != want {
		t.Errorf("epic phase = %q, parked %q, want stopped with %q", flow.Epic.Phase, flow.Epic.Parked, want)
	}
}

// TestReleaseIntegratorChecksFailedStopsWithConflict: a round whose step ends without a done return, as one whose
// checks failed four times does, stops the release with the conflict and how the step ended.
func TestReleaseIntegratorChecksFailedStopsWithConflict(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.pass()
	w.endStep(w.integratorID(1), "checks_failed", "", doneReturn(w.tip))
	w.pass()

	flow := w.flowOf(runEpic)
	want := conflictMessage(w.tip, w.origin, []string{"README"}) + "; release integrator ended checks_failed"
	if flow.Epic.Phase != store.PhaseStopped || !strings.HasPrefix(flow.Epic.Parked, want) {
		t.Errorf("epic phase = %q, parked %q, want stopped with %q", flow.Epic.Phase, flow.Epic.Parked, want)
	}
}

// TestReleaseCleanMergeStartsNoIntegrator: a merge of origin's commit that does not conflict is the candidate as it
// always was: its Result names no conflict, it is held at the Done gate, and no release integrator starts.
func TestReleaseCleanMergeStartsNoIntegrator(t *testing.T) {
	t.Parallel()
	w := newMergeWorld(t)
	origin := mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	mergeOriginCommit(t, origin, "origin.txt")
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	for _, key := range []string{`"conflicts"`, `"origin"`, `"branch"`} {
		if strings.Contains(run.Result, key) {
			t.Errorf("Result = %s, want no %s key for a clean merge", run.Result, key)
		}
	}
	if run.Revision == "" {
		t.Errorf("revision is empty, want the clean merge")
	}
	for _, start := range w.fakes.steps.calls {
		if start.Schema == IntegratorSchema {
			t.Errorf("step start = %+v, want no release integrator", start)
		}
	}
	if _, err := w.s.NodeCheck(t.Context(), workID(t, w.s, releaseNodePath, "done 1")); err != nil {
		t.Errorf("Done gate check of the clean merge: %v, want it started", err)
	}
}

// TestReleaseIntegratorRestartFindsStepByID: a runner started again over the same store and state directory finds the
// running round under its id, so it starts no second one, and the re-adopted row gets the integrator's return check
// back, which refuses a leftover marker and passes the correct merge. A row of any other step gets no check.
func TestReleaseIntegratorRestartFindsStepByID(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.pass()

	fakes := newNodeFakes(w.s)
	cfg := nodeConfig(w.s, fakes)
	cfg.StateDir, cfg.Getenv, cfg.Ending = w.state, releaseRoles(t), &recordingEnding{}
	New(cfg).Pass(t.Context())
	if len(fakes.steps.calls) != 0 {
		t.Errorf("starts after the restart = %+v, want none: the round is running", fakes.steps.calls)
	}

	id := w.integratorID(1)
	row, err := w.runner.storedStep(t.Context(), runEpic, id)
	if err != nil {
		t.Fatalf("stored integrator step: %v", err)
	}
	check, err := ReadoptCheck(w.s)(t.Context(), row)
	if err != nil || check == nil {
		t.Fatalf("ReadoptCheck = %v, %v, want the integrator's check", check != nil, err)
	}
	nodeGit(t, w.worktree(), "add", "README")
	nodeGit(t, w.worktree(), "commit", "-q", "-m", mergeMessage(w.tip, w.origin))
	marked, err := json.Marshal(doneReturn(nodeGit(t, w.worktree(), "rev-parse", "HEAD")))
	if err != nil {
		t.Fatalf("encode return: %v", err)
	}
	if failures := check(marked); !hasLine(failures, "markers: ") {
		t.Errorf("re-adopted check of a merge with markers = %q, want a markers line", failures)
	}
	nodeWrite(t, w.worktree(), "README", "change\norigin\n")
	nodeGit(t, w.worktree(), "commit", "-q", "-a", "--amend", "--no-edit")
	correct, err := json.Marshal(doneReturn(nodeGit(t, w.worktree(), "rev-parse", "HEAD")))
	if err != nil {
		t.Fatalf("encode return: %v", err)
	}
	if failures := check(correct); len(failures) != 0 {
		t.Errorf("re-adopted check of the correct merge = %q, want none", failures)
	}

	row.Schema = wantReleaseStepSchema
	if other, otherErr := ReadoptCheck(w.s)(t.Context(), row); other != nil || otherErr != nil {
		t.Errorf("ReadoptCheck of another step = %v, %v, want no check", other != nil, otherErr)
	}
}

// TestStopEpicStopsTheOpenIntegrator: StopEpic on a releasing epic whose release integrator runs names that step as
// the one to stop.
func TestStopEpicStopsTheOpenIntegrator(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.pass()

	step, err := w.s.StopEpic(t.Context(), runEpic, store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if step != w.integratorID(1) {
		t.Errorf("StopEpic step = %q, want the integrator %s", step, w.integratorID(1))
	}
}

// TestSameChangeLinesPairsEqualPatchesAndDatedSubjects: a candidate commit and an origin commit with equal patch ids are
// paired as the same change, and a pair with equal subject and author date whose patches differ is paired as differs.
// A commit on one side alone is not paired.
func TestSameChangeLinesPairsEqualPatchesAndDatedSubjects(t *testing.T) {
	t.Parallel()
	repo, base := nodeRepo(t)
	commit := func(file, text, subject, date string) string {
		nodeWrite(t, repo, file, text)
		nodeGit(t, repo, "add", file)
		nodeGit(t, repo, "commit", "-q", "-m", subject, "--date", date)
		return nodeGit(t, repo, "rev-parse", "HEAD")
	}
	nodeGit(t, repo, "checkout", "-q", "-b", "candidate")
	shared := commit("shared.txt", "shared\n", "T3: share a helper", "2026-01-02T00:00:00Z")
	dated := commit("a.txt", "a\n", "T5: tune the cache", "2026-01-03T00:00:00Z")
	commit("only.txt", "only\n", "T6: candidate only", "2026-01-04T00:00:00Z")
	tip := nodeGit(t, repo, "rev-parse", "HEAD")
	nodeGit(t, repo, "checkout", "-q", "--detach", base)
	copied := commit("shared.txt", "shared\n", "land T3 by hand", "2026-02-01T00:00:00Z")
	redone := commit("b.txt", "b\n", "T5: tune the cache", "2026-01-03T00:00:00Z")
	origin := nodeGit(t, repo, "rev-parse", "HEAD")

	got, err := sameChangeLines(t.Context(), repo, base, integratorMerge{repo: repo, tip: tip, origin: origin})
	if err != nil {
		t.Fatalf("sameChangeLines: %v", err)
	}
	want := []string{
		fmt.Sprintf("- candidate %s and origin %s, differs: T5: tune the cache", dated, redone),
		fmt.Sprintf("- candidate %s and origin %s, same: T3: share a helper", shared, copied),
	}
	if !slices.Equal(got, want) {
		t.Errorf("sameChangeLines = %q, want %q", got, want)
	}
}

// TestConflictStatusCodesByStage: a path both sides add differently is AA and a path both sides change is UU, as git
// status names them.
func TestConflictStatusCodesByStage(t *testing.T) {
	t.Parallel()
	repo, base := nodeRepo(t)
	side := func(text string) string {
		nodeGit(t, repo, "checkout", "-q", "--detach", base)
		nodeWrite(t, repo, "README", text)
		nodeWrite(t, repo, "new.txt", text)
		nodeGit(t, repo, "add", "README", "new.txt")
		nodeGit(t, repo, "commit", "-q", "-m", text)
		return nodeGit(t, repo, "rev-parse", "HEAD")
	}
	tip, origin := side("tip\n"), side("origin\n")
	got, err := conflictStatus(t.Context(), integratorMerge{repo: repo, tip: tip, origin: origin})
	if err != nil {
		t.Fatalf("conflictStatus: %v", err)
	}
	if got["README"] != "UU" || got["new.txt"] != "AA" || len(got) != 2 {
		t.Errorf("conflictStatus = %v, want README UU and new.txt AA", got)
	}
}

// TestIntegratorTaskLinesNameComposedCommits: each accepted task is listed with its deliverable, its files and the
// commit the integrate run that passed composed for it, and a task with no composed commit says None. Open and failed
// tasks are not listed.
func TestIntegratorTaskLinesNameComposedCommits(t *testing.T) {
	t.Parallel()
	tasks := []store.EpicTask{
		{Seq: 1, Title: "add the cache", Deliverable: "a cache", Files: []string{"cache.go", "cache_test.go"},
			Status: "done"},
		{Seq: 2, Title: "open work", Status: "open"},
		{Seq: 3, Title: "tune it", Deliverable: "a tuned cache", Files: []string{"tune.go"}, Status: "done"},
	}
	runs := []store.NodeRun{
		{Kind: kindIntegrate, Outcome: outcomeFailed, Result: `{"composed":[{"task":1,"commit":"failed1"}]}`},
		{Kind: kindIntegrate, Outcome: outcomePassed, Result: `{"composed":[{"task":1,"commit":"aaa111"}]}`},
	}
	want := []string{
		"- T1 add the cache", "  deliverable: a cache", "  files: cache.go, cache_test.go", "  commit: aaa111",
		"- T3 tune it", "  deliverable: a tuned cache", "  files: tune.go", "  commit: None.",
	}
	if got := integratorTaskLines(tasks, runs); !slices.Equal(got, want) {
		t.Errorf("integratorTaskLines = %q, want %q", got, want)
	}
}
