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
	"github.com/joshsymonds/orrery/internal/hostexec"
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

// newCleanWorld returns a conflict world whose epic's tip adds epic.txt and whose origin's main adds origin.txt, so git
// merges origin's commit into the tip with no conflict. Its epic declares requirements, and is stored clean with the tag
// step as its one release step.
func newCleanWorld(t *testing.T, requirements ...store.EpicRequirement) conflictWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	w.requirements = requirements
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	remote := mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, "epic.txt")
	origin := mergeOriginCommit(t, remote, "origin.txt")
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

// integratorID returns the id of the release integrator's round of the release run's current attempt, at the first
// generation of its candidate. It is the id a store that ran up to three rounds a generation gave the first of them.
func (w *releaseWorld) integratorID() string {
	w.t.Helper()
	return workID(w.t, w.s, releaseNodePath, "integrator 1")
}

// integratorDir returns the directory the release integrator's worktree is under, named by the run and its attempt.
func (w *releaseWorld) integratorDir() string {
	w.t.Helper()
	return filepath.Join(w.state, "release-worktrees", workID(w.t, w.s, releaseNodePath, "integrator"))
}

// worktree returns the worktree the release integrator runs in.
func (w *releaseWorld) worktree() string {
	w.t.Helper()
	return filepath.Join(w.integratorDir(), "wt")
}

// started returns the start of the step with id, which must have been started.
func (w *releaseWorld) started(id string) enginehost.StepStart {
	w.t.Helper()
	i := slices.IndexFunc(w.fakes.steps.calls, func(start enginehost.StepStart) bool { return start.ID == id })
	if i < 0 {
		w.t.Fatalf("step starts = %+v, want one with id %s", w.fakes.steps.calls, id)
	}
	return w.fakes.steps.calls[i]
}

// integratorStarts returns the starts of release integrator steps, in order.
func (w *releaseWorld) integratorStarts() []enginehost.StepStart {
	var starts []enginehost.StepStart
	for _, start := range w.fakes.steps.calls {
		if start.Schema == IntegratorSchema {
			starts = append(starts, start)
		}
	}
	return starts
}

// commitCleanMerge commits the merge git made with no conflict in the release integrator's worktree, as the integrator
// would once its checks passed, with the message the prompt gives for the tip and origin's commit the run's Result
// names. It returns the merge commit.
func (w *releaseWorld) commitCleanMerge() string {
	w.t.Helper()
	merge := decodeMerge(w.t, runAt(w.t, w.s, releaseNodePath).Result)
	wt := w.worktree()
	nodeGit(w.t, wt, "commit", "-q", "-m", mergeMessage(merge.Tip, merge.Origin))
	return nodeGit(w.t, wt, "rev-parse", "HEAD")
}

// integrateClean commits the clean merge in the release integrator's worktree (commitCleanMerge), ends the integrator's
// step id done with a return that names the merge (cleanReturn), and passes. It returns the merge commit.
func (w *releaseWorld) integrateClean(id string) string {
	w.t.Helper()
	merge := w.commitCleanMerge()
	w.endStep(id, "done", "done", cleanReturn(merge))
	if logged := w.pass(); logged != "" {
		w.t.Fatalf("pass logged: %s", logged)
	}
	return merge
}

// integratorChecks are the checks a done return of the release integrator lists in the tests: one build that passed.
func integratorChecks() []map[string]any {
	return []map[string]any{{"command": "go build ./...", "result": "passed"}}
}

// cleanReturn is a done return of the release integrator that names commit, the merge of a merge that did not
// conflict, so it resolves no path, and lists the one build it ran.
func cleanReturn(commit string) map[string]any {
	return map[string]any{
		"status":        "done",
		"commit":        commit,
		"resolutions":   []map[string]any{},
		"other_changes": []map[string]any{},
		"checks":        integratorChecks(),
		"unresolved":    []map[string]any{},
		"evidence":      "the two sides add different files and touch nothing the other reads, so a build shows the merge holds",
	}
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

// doneReturn is a done return of the release integrator that names commit, resolves README by keeping both sides and
// lists the one build it ran, with others as its other_changes.
func doneReturn(commit string, others ...map[string]any) map[string]any {
	if others == nil {
		others = []map[string]any{}
	}
	return map[string]any{
		"status": "done", "commit": commit,
		"resolutions":   []map[string]any{{"path": "README", "took": "both", "kept": "both lines stay"}},
		"other_changes": others, "checks": integratorChecks(), "unresolved": []map[string]any{},
		"evidence": "README keeps both lines",
	}
}

// checkReturn runs the return check of the step with id on ret and returns its failure lines.
func (w *releaseWorld) checkReturn(id string, ret map[string]any) []string {
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

// endIntegrator ends the release integrator's round done with a return that names commit, and passes.
func (w conflictWorld) endIntegrator(commit string) {
	w.t.Helper()
	w.endStep(w.integratorID(), "done", "done", doneReturn(commit))
	w.pass()
}

// hasLine reports whether one of failures starts with prefix.
func hasLine(failures []string, prefix string) bool {
	return slices.ContainsFunc(failures, func(line string) bool { return strings.HasPrefix(line, prefix) })
}

// requireSections fails unless prompt holds each of headings, as a `## ` heading on a line of its own, in order.
func requireSections(t *testing.T, prompt string, headings ...string) {
	t.Helper()
	at := -1
	for _, heading := range headings {
		i := strings.Index(prompt, "\n## "+heading+"\n")
		if i <= at {
			t.Errorf("prompt has %q at %d, want it after the section before it at %d", heading, i, at)
		}
		at = i
	}
}

// integratorSections returns the sections of the release integrator's prompt, in order.
func integratorSections() []string {
	return []string{
		"Contract", "Decisions", "Merge", "Conflicted paths", "Tasks", "Candidate commits", "Origin commits",
		"Same change", "Candidate changes", "Origin changes", "Changed on both sides", "Verification references",
	}
}

// TestEpicBaseReleaseConflictStartsIntegratorWithMergeInProgress: a merge of origin's commit that conflicts records the
// conflict, origin's commit and its branch, and no candidate or revision, and does not stop the release. It starts the
// release integrator as an unowned writing step, labeled with the epic and the run's path, in a worktree that holds the
// merge in progress at the tip with zdiff3 conflicts. Its role file names no integrator, so it runs under the release
// agent. Its prompt is the role text, then each section in order: both sides' changed paths, the paths both changed,
// and the epic's Done gates and evidence commands as references.
func TestEpicBaseReleaseConflictStartsIntegratorWithMergeInProgress(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t, nodeRequirements()...)
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	var got struct {
		mergeWire
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

	start := w.started(w.integratorID())
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
	if !strings.HasPrefix(prompt, strings.TrimRight(integratorRole, "\n")+"\n") {
		t.Errorf("prompt starts %q, want the role text", prompt[:min(len(prompt), 80)])
	}
	requireSections(t, prompt, integratorSections()...)
	for _, want := range []string{
		"Worktree: " + wt,
		"Release tip: " + w.tip,
		"Origin's commit: " + w.origin + " from origin's refs/heads/main",
		"Merge base: " + nodeGit(t, w.repo, "merge-base", w.tip, w.origin),
		"Commit message: Merge origin's commit " + w.origin + " into release tip " + w.tip,
		"- UU README\n  candidate: " + w.tip + " epic work\n  origin: " + w.origin + " origin README\n",
		"## Candidate commits\n" + nodeGit(t, w.repo, "log", "--oneline", "-n1", w.tip) + "\n",
		"## Origin commits\n" + nodeGit(t, w.repo, "log", "--oneline", "-n1", w.origin) + "\n",
		"## Candidate changes\n- M README\n",
		"## Origin changes\n- M README\n",
		"## Changed on both sides\n- README\n",
		"## Verification references\nDone gates:\n- lint: [\"./bin/lint\"]\nEvidence commands:\n- R1: [\"scripts/check\"]\n",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "## Previous attempt") {
		t.Errorf("prompt has a Previous attempt section, want none: the integrator runs one round:\n%s", prompt)
	}
}

// TestReleaseCleanMergeStartsIntegrator: a merge of origin's commit that git makes with no conflict is not committed at
// the prepare: its Result names origin's commit and its branch, no conflict, no candidate and no revision, nothing is
// pinned under the run's release ref, and no Done gate starts. The release integrator starts with the merge in progress
// and no unmerged path, and its prompt says no path conflicts and names each side's change. Once it commits the merge
// and returns done, the merge, with the tip and origin's commit as its parents, is pinned and recorded as the candidate
// with no Done gate or capture run, and the first release step starts from it.
func TestReleaseCleanMergeStartsIntegrator(t *testing.T) {
	t.Parallel()
	w := newCleanWorld(t, nodeRequirements()...)
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	if strings.Contains(run.Result, `"conflicts"`) {
		t.Errorf("Result = %s, want no conflicts key for a clean merge", run.Result)
	}
	got := decodeMerge(t, run.Result)
	if got.Error != "" || !got.Behind || got.Tip != w.tip || got.Origin != w.origin || got.Candidate != "" ||
		run.Revision != "" || !strings.Contains(run.Result, `"branch":"refs/heads/main"`) {
		t.Errorf("Result = %s with revision %q, want origin's commit %s and its branch, and no candidate",
			run.Result, run.Revision, w.origin)
	}
	ref := fmt.Sprintf("refs/orrery/epic-runner/release/%s-%d", run.ID, run.Attempt)
	if pinned := nodeGit(t, w.repo, "for-each-ref", ref); pinned != "" {
		t.Errorf("%s = %q at the prepare, want nothing pinned before the integrator commits", ref, pinned)
	}
	if len(w.fakes.checks.calls) != 0 {
		t.Errorf("checks = %+v, want no Done gate run on the merge", w.fakes.checks.calls)
	}

	start := w.started(w.integratorID())
	if start.Workspace != w.worktree() || start.Revision != w.tip || !start.Unowned || start.ReturnCheck == nil {
		t.Errorf("integrator start = %+v, want an unowned writing step in %s at %s", start, w.worktree(), w.tip)
	}
	wt := w.worktree()
	if mergeHead := nodeGit(t, wt, "rev-parse", "MERGE_HEAD"); mergeHead != w.origin {
		t.Errorf("worktree MERGE_HEAD = %s, want origin's commit %s", mergeHead, w.origin)
	}
	if head := nodeGit(t, wt, "rev-parse", "HEAD"); head != w.tip {
		t.Errorf("worktree HEAD = %s, want the tip %s: the merge is not committed", head, w.tip)
	}
	if unmerged := nodeGit(t, wt, "ls-files", "-u"); unmerged != "" {
		t.Errorf("unmerged paths = %q, want none", unmerged)
	}
	requireSections(t, start.Prompt, integratorSections()...)
	for _, want := range []string{
		"## Conflicted paths\nNone.\n",
		"## Candidate changes\n- A epic.txt\n",
		"## Origin changes\n- A origin.txt\n",
		"## Changed on both sides\nNone.\n",
		"Evidence commands:\n- R1: [\"scripts/check\"]\n",
	} {
		if !strings.Contains(start.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, start.Prompt)
		}
	}

	if failures := w.checkReturn(w.integratorID(), cleanReturn(w.commitCleanMerge())); len(failures) != 0 {
		t.Fatalf("return check of the clean merge = %q, want no failure", failures)
	}
	merge := nodeGit(t, wt, "rev-parse", "HEAD")
	w.endStep(w.integratorID(), "done", "done", cleanReturn(merge))
	if logged := w.pass(); logged != "" {
		t.Fatalf("pass logged: %s", logged)
	}

	parents := strings.Fields(nodeGit(t, w.repo, "rev-list", "--parents", "-n", "1", merge))
	if want := []string{merge, w.tip, w.origin}; !slices.Equal(parents, want) {
		t.Errorf("merge and parents = %q, want %q", parents, want)
	}
	if pinned := nodeGit(t, w.repo, "rev-parse", ref); pinned != merge {
		t.Errorf("%s = %s, want the merge %s", ref, pinned, merge)
	}
	candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
	if len(candidates) != 1 || candidates[0].Head != merge || candidates[0].Start != w.tip ||
		!slices.Equal(candidates[0].Commits, []string{merge}) {
		t.Errorf("candidate runs = %+v, want the merge %s from the tip %s", candidates, merge, w.tip)
	}
	if len(w.fakes.checks.calls) != 0 || len(w.fakes.evidence.calls) != 0 {
		t.Errorf("checks = %+v and captures = %+v, want no Done gate or capture run on the merge",
			w.fakes.checks.calls, w.fakes.evidence.calls)
	}
	if step := w.started(w.stepID(1)); step.Revision != merge {
		t.Errorf("release step 1 starts at %s, want the merge %s", step.Revision, merge)
	}
}

// TestReleaseIntegratorRunsUnderItsOwnRole: a role file that names an integrator runs the release integrator under that
// agent, not the release agent.
func TestReleaseIntegratorRunsUnderItsOwnRole(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.runner.getenv = integratorRoles(t)
	w.pass()

	if got := w.started(w.integratorID()).Agent; got != integratorAgent {
		t.Errorf("integrator agent = %q, want %q from the role file", got, integratorAgent)
	}
}

// TestReleaseIntegratorWithoutRoleStopsNamingTheMerge: with no role file the integrator cannot start, so the release
// stops at once with the merge and the reason the integrator did not start. A merge that conflicts is named with its
// conflicted path, and a clean merge is named without a word of conflict. The integrator's worktree is not left behind.
func TestReleaseIntegratorWithoutRoleStopsNamingTheMerge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		world func(t *testing.T) conflictWorld
		want  func(w conflictWorld) string
	}{
		{
			name:  "conflicting merge",
			world: func(t *testing.T) conflictWorld { t.Helper(); return newConflictWorld(t) },
			want: func(w conflictWorld) string {
				return conflictMessage(w.tip, w.origin, []string{"README"}) + "; the release integrator did not start: "
			},
		},
		{
			name:  "clean merge",
			world: func(t *testing.T) conflictWorld { t.Helper(); return newCleanWorld(t) },
			want: func(w conflictWorld) string {
				return fmt.Sprintf("merge of origin's commit %s into %s; the release integrator did not start: ",
					w.origin, w.tip)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := tc.world(t)
			w.runner.getenv = func(string) string { return "" }
			w.pass()

			flow := w.flowOf(runEpic)
			if flow.Epic.Phase != store.PhaseStopped {
				t.Fatalf("epic phase = %q, want stopped", flow.Epic.Phase)
			}
			if want := tc.want(w); !strings.HasPrefix(flow.Epic.Parked, want) {
				t.Errorf("parked = %q, want it to open with %q", flow.Epic.Parked, want)
			}
			if tc.name == "clean merge" && strings.Contains(flow.Epic.Parked, "conflict") {
				t.Errorf("parked = %q, want a clean merge not named as conflicting", flow.Epic.Parked)
			}
			if len(w.integratorStarts()) != 0 {
				t.Errorf("integrator starts = %+v, want none", w.integratorStarts())
			}
			if _, err := os.Stat(w.integratorDir()); !os.IsNotExist(err) {
				t.Errorf("integrator directory stat = %v, want it absent", err)
			}
		})
	}
}

// TestReleaseIntegratorMergeBecomesCandidateWithNoGateOrCapture: a done return whose merge passes the return check is
// recorded as a passed round at position 1 with the merge as its head, and the merge is pinned under the run's release
// ref. The merge is recorded as the candidate in the same pass, though the epic has a Done gate and a Requirement whose
// item captures: neither runs on it. The integrator's worktree is removed and the first release step starts from the
// merge.
func TestReleaseIntegratorMergeBecomesCandidateWithNoGateOrCapture(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t, nodeRequirements()...)
	w.pass()
	merge := w.commitMerge()
	if failures := w.checkReturn(w.integratorID(), doneReturn(merge)); len(failures) != 0 {
		t.Fatalf("return check of a correct merge = %q, want no failure", failures)
	}
	w.endIntegrator(merge)

	run := runAt(t, w.s, releaseNodePath)
	flow := w.flowOf(runEpic)
	rounds := releaseRunsOf(flow.Releases, store.ReleasePartIntegrator)
	if len(rounds) != 1 || !rounds[0].Passed || rounds[0].Head != merge || rounds[0].Start != w.tip ||
		rounds[0].Position != 1 {
		t.Fatalf("integrator runs = %+v, want the round passed at %s at position 1", rounds, merge)
	}
	ref := fmt.Sprintf("refs/orrery/epic-runner/release/%s-%d", run.ID, run.Attempt)
	if pinned := nodeGit(t, w.repo, "rev-parse", ref); pinned != merge {
		t.Errorf("%s = %s, want the merge %s", ref, pinned, merge)
	}
	candidates := releaseRunsOf(flow.Releases, store.ReleasePartCandidate)
	if len(candidates) != 1 || candidates[0].Head != merge || candidates[0].Start != w.tip ||
		!slices.Equal(candidates[0].Commits, []string{merge}) {
		t.Errorf("candidate runs = %+v, want the merge %s from the tip %s", candidates, merge, w.tip)
	}
	if len(w.fakes.checks.calls) != 0 || len(w.fakes.evidence.calls) != 0 {
		t.Errorf("checks = %+v and captures = %+v, want no Done gate or capture started on the merge",
			w.fakes.checks.calls, w.fakes.evidence.calls)
	}
	if _, err := os.Stat(w.integratorDir()); !os.IsNotExist(err) {
		t.Errorf("integrator directory stat = %v, want it removed once the candidate is recorded", err)
	}
	if step := w.started(w.stepID(1)); step.Revision != merge {
		t.Errorf("release step 1 starts at %s, want the merge %s", step.Revision, merge)
	}
	if starts := w.integratorStarts(); len(starts) != 1 {
		t.Errorf("integrator starts = %d, want the one round", len(starts))
	}
}

// TestReleaseIntegratorReturnCheckRefusesBadCommits: the return check passes a correct merge and refuses each commit or
// return the engine's checks rule out, naming the check: parents other than the tip and then origin's commit, a commit
// added on top of the merge, a conflict marker left in, a change outside the conflicts that other_changes does not
// list, a merge not committed, a resolution missing, a done return that lists no check it ran, and a blocked return
// that names a commit.
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
			name: "no check listed",
			do: func(w conflictWorld) map[string]any {
				ret := doneReturn(w.commitMerge())
				ret["checks"] = []map[string]any{}
				return ret
			},
			want: "checks: a done return lists each command it ran to verify the merged tree",
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

// TestReleaseCleanMergeReturnCheck: the return check of a clean merge passes the merge committed with no resolution, and
// refuses a resolution of a path that did not conflict, a change to what git merged that other_changes does not list,
// and a merge not committed, whose HEAD is still the tip.
func TestReleaseCleanMergeReturnCheck(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		do   func(w conflictWorld) map[string]any
		want string
	}{
		{
			name: "merge committed with no resolution",
			do:   func(w conflictWorld) map[string]any { return cleanReturn(w.commitCleanMerge()) },
		},
		{
			name: "a resolution of a path that did not conflict",
			do: func(w conflictWorld) map[string]any {
				ret := cleanReturn(w.commitCleanMerge())
				ret["resolutions"] = []map[string]any{{"path": "epic.txt", "took": "candidate", "kept": "the epic's"}}
				return ret
			},
			want: "resolutions: epic.txt is not a conflicted path",
		},
		{
			name: "unlisted change",
			do: func(w conflictWorld) map[string]any {
				nodeWrite(w.t, w.worktree(), "notes.txt", "notes\n")
				nodeGit(w.t, w.worktree(), "add", "notes.txt")
				return cleanReturn(w.commitCleanMerge())
			},
			want: "other_changes: the commit changes what git merged without conflict in notes.txt",
		},
		{
			name: "merge not committed",
			do:   func(w conflictWorld) map[string]any { return cleanReturn(w.tip) },
			want: "parents: ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newCleanWorld(t)
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

// TestReleaseIntegratorBlockedParksWithUnresolved: a blocked return on a merge-side decision asks for a replan, and in
// a workflow with no plan that can carry it the release stops: the epic's park carries the integrator's report, each
// path it stopped on with its side, the decision that would settle it and what each side meant, then the note that the
// change cannot be planned. The integrator's worktree is removed with the stop.
func TestReleaseIntegratorBlockedParksWithUnresolved(t *testing.T) {
	t.Parallel()
	w := newConflictWorld(t)
	w.pass()
	w.endStep(w.integratorID(), "returned", "blocked", map[string]any{
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
		"origin: main greets in French; the workflow has no plan whose body holds an integrate node, so the release "+
		"cannot plan the change to the candidate", w.origin, w.tip)
	if flow.Epic.Parked != want {
		t.Errorf("parked = %q, want %q", flow.Epic.Parked, want)
	}
	if run := runAt(t, w.s, releaseNodePath); run.Output != want {
		t.Errorf("run output = %q, want %q", run.Output, want)
	}
	if _, err := os.Stat(w.integratorDir()); !os.IsNotExist(err) {
		t.Errorf("integrator directory stat = %v, want it removed with the stop", err)
	}
	if !slices.ContainsFunc(flow.Releases, func(r store.ReleaseRun) bool { return r.Part == store.ReleasePartReplan }) {
		t.Errorf("release runs = %+v, want the replan a merge-side decision asks for", flow.Releases)
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

// TestReleaseIntegratorEndWithoutDoneStopsNamingTheMerge: a round whose step ends without a done return, as one whose
// checks failed four times does, stops the release with the merge and how the step ended. A merge that conflicts is
// named with its conflicted path, and a clean merge without a word of conflict.
func TestReleaseIntegratorEndWithoutDoneStopsNamingTheMerge(t *testing.T) {
	t.Parallel()
	t.Run("conflicting merge", func(t *testing.T) {
		t.Parallel()
		w := newConflictWorld(t)
		w.pass()
		w.endStep(w.integratorID(), "checks_failed", "", doneReturn(w.tip))
		w.pass()

		flow := w.flowOf(runEpic)
		want := conflictMessage(w.tip, w.origin, []string{"README"}) + "; release integrator ended checks_failed"
		if flow.Epic.Phase != store.PhaseStopped || !strings.HasPrefix(flow.Epic.Parked, want) {
			t.Errorf("epic phase = %q, parked %q, want stopped with %q", flow.Epic.Phase, flow.Epic.Parked, want)
		}
	})
	t.Run("clean merge", func(t *testing.T) {
		t.Parallel()
		w := newCleanWorld(t)
		w.pass()
		w.endStep(w.integratorID(), "checks_failed", "", cleanReturn(w.tip))
		w.pass()

		flow := w.flowOf(runEpic)
		want := fmt.Sprintf("merge of origin's commit %s into %s; release integrator ended checks_failed",
			w.origin, w.tip)
		if flow.Epic.Phase != store.PhaseStopped || !strings.HasPrefix(flow.Epic.Parked, want) {
			t.Errorf("epic phase = %q, parked %q, want stopped with %q", flow.Epic.Phase, flow.Epic.Parked, want)
		}
	})
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

	id := w.integratorID()
	row, err := w.runner.storedStep(t.Context(), runEpic, id)
	if err != nil {
		t.Fatalf("stored integrator step: %v", err)
	}
	check, err := ReadoptCheck(w.s, hostexec.LocalHosts())(t.Context(), row)
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
	if other, otherErr := ReadoptCheck(w.s, hostexec.LocalHosts())(t.Context(), row); other != nil || otherErr != nil {
		t.Errorf("ReadoptCheck of another step = %v, %v, want no check", other != nil, otherErr)
	}
}

// TestReleaseCleanMergeReadoptCheck: the re-adopted row of an integrator that makes a clean merge gets its return check
// back, though the merge conflicts in no path: the check refuses the merge not committed and passes it committed. A row
// whose release run records no merge of origin's commit into the row's revision is refused, so it is not re-adopted.
func TestReleaseCleanMergeReadoptCheck(t *testing.T) {
	t.Parallel()
	w := newCleanWorld(t)
	w.pass()
	row, err := w.runner.storedStep(t.Context(), runEpic, w.integratorID())
	if err != nil {
		t.Fatalf("stored integrator step: %v", err)
	}
	readopt := ReadoptCheck(w.s, hostexec.LocalHosts())
	check, err := readopt(t.Context(), row)
	if err != nil || check == nil {
		t.Fatalf("ReadoptCheck = %v, %v, want the integrator's check for the clean merge", check != nil, err)
	}
	uncommitted, err := json.Marshal(cleanReturn(w.tip))
	if err != nil {
		t.Fatalf("encode return: %v", err)
	}
	if failures := check(uncommitted); !hasLine(failures, "parents: ") {
		t.Errorf("re-adopted check of the merge not committed = %q, want a parents line", failures)
	}
	committed, err := json.Marshal(cleanReturn(w.commitCleanMerge()))
	if err != nil {
		t.Fatalf("encode return: %v", err)
	}
	if failures := check(committed); len(failures) != 0 {
		t.Errorf("re-adopted check of the committed merge = %q, want none", failures)
	}

	row.Revision = w.origin
	if other, otherErr := readopt(t.Context(), row); other != nil || otherErr == nil {
		t.Errorf("ReadoptCheck of a row at another revision = %v, %v, want it refused", other != nil, otherErr)
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
	if step != w.integratorID() {
		t.Errorf("StopEpic step = %q, want the integrator %s", step, w.integratorID())
	}
}

// TestChangeLinesNameEachPathAndCountThePathsPastTheLimit: each side's changed paths are listed with their status, past
// integratorChangeLimit the list stops with a line that counts them all and names the command that lists them, and the
// paths both sides changed are listed in the candidate's order.
func TestChangeLinesNameEachPathAndCountThePathsPastTheLimit(t *testing.T) {
	t.Parallel()
	candidate := []pathChange{{status: "A", path: "a.go"}, {status: "M", path: "b.go"}, {status: "D", path: "c.go"}}
	origin := []pathChange{{status: "M", path: "c.go"}, {status: "M", path: "b.go"}}
	if got, want := changeLines(candidate, "base", "tip"), []string{"- A a.go", "- M b.go", "- D c.go"}; !slices.Equal(
		got, want) {
		t.Errorf("changeLines = %q, want %q", got, want)
	}
	if got, want := bothLines(candidate, origin), []string{"- b.go", "- c.go"}; !slices.Equal(got, want) {
		t.Errorf("bothLines = %q, want %q", got, want)
	}
	many := make([]pathChange, integratorChangeLimit+2)
	for i := range many {
		many[i] = pathChange{status: "M", path: fmt.Sprintf("f%d.go", i)}
	}
	lines := changeLines(many, "base", "tip")
	want := fmt.Sprintf("These are the first %d of %d changed paths; git diff --name-status base tip lists them all.",
		integratorChangeLimit, integratorChangeLimit+2)
	if len(lines) != integratorChangeLimit+1 || lines[len(lines)-1] != want {
		t.Errorf("changeLines of %d paths ends %q after %d lines, want %d lines ending %q", len(many),
			lines[len(lines)-1], len(lines), integratorChangeLimit+1, want)
	}
	both := bothLines(many, many)
	if len(both) != integratorChangeLimit+1 || !strings.HasPrefix(both[len(both)-1], "These are the first ") {
		t.Errorf("bothLines of %d shared paths = %d lines, want %d and a count", len(many), len(both),
			integratorChangeLimit+1)
	}
}

// TestNameStatusReadsEachSidesChange: nameStatus reads each path a side changed from the merge base with its status, an
// added, a modified and a deleted path among them, in git's order.
func TestNameStatusReadsEachSidesChange(t *testing.T) {
	t.Parallel()
	repo, base := nodeRepo(t)
	nodeWrite(t, repo, "added.txt", "added\n")
	nodeWrite(t, repo, "README", "changed\n")
	nodeGit(t, repo, "add", "added.txt", "README")
	nodeGit(t, repo, "commit", "-q", "-m", "change")
	nodeGit(t, repo, "rm", "-q", "added.txt")
	nodeWrite(t, repo, "kept.txt", "kept\n")
	nodeGit(t, repo, "add", "kept.txt")
	nodeGit(t, repo, "commit", "-q", "-m", "more")
	middle := nodeGit(t, repo, "rev-parse", "HEAD~1")
	tip := nodeGit(t, repo, "rev-parse", "HEAD")

	got, err := nameStatus(t.Context(), hostexec.NewLocal(), repo, base, middle)
	if err != nil {
		t.Fatalf("nameStatus: %v", err)
	}
	if want := []pathChange{{status: "M", path: "README"}, {status: "A", path: "added.txt"}}; !slices.Equal(got, want) {
		t.Errorf("nameStatus(base, middle) = %+v, want %+v", got, want)
	}
	got, err = nameStatus(t.Context(), hostexec.NewLocal(), repo, middle, tip)
	if err != nil {
		t.Fatalf("nameStatus: %v", err)
	}
	if want := []pathChange{
		{status: "D", path: "added.txt"},
		{status: "A", path: "kept.txt"},
	}; !slices.Equal(
		got,
		want,
	) {
		t.Errorf("nameStatus(middle, tip) = %+v, want %+v", got, want)
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

	got, err := sameChangeLines(
		t.Context(), hostexec.NewLocal(), repo, base, integratorMerge{repo: repo, tip: tip, origin: origin},
	)
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
	got, err := conflictStatus(t.Context(), hostexec.NewLocal(), integratorMerge{repo: repo, tip: tip, origin: origin})
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
