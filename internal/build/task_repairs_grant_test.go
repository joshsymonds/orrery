package build

import (
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// repairFile is a file the rule grant tests' implementer asks for, which no task of their epic owns.
const repairFile = "internal/other/a.go"

// repairReturn returns the needs_context return of an implementer that names needs, a JSON list of paths.
func repairReturn(needs string) string {
	return `{"status":"needs_context","summary":"the spec is short","needs_files":` + needs + `}`
}

// repairView returns the view of task 3, owning a.go, at orchestrate on an implementer step that ended needs_context
// naming needs, with decisions made, whose orchestrator step is step.
func repairView(decisions int64, needs string, step *store.StepRow) View {
	task := store.EpicTask{Seq: 3, Files: []string{"a.go"}, Status: taskOpen, Build: orchBuild(decisions)}
	return View{
		Epic: decideEpic, Task: task, Step: step, Peers: []Peer{{Task: task, State: Pending}},
		Returned: &store.StepRow{
			ID: orchImplStep, End: endReturned, Status: statusNeedsContext, Result: repairReturn(needs),
		},
	}
}

// TestSharedFilesAcceptedRunTimeRequest checks a needs_context file's rule grant against the peers that hold
// it: a file an open task owns, or an accepted task changed and has not integrated, is held, and is granted by rule
// like a free file, as is a file an accepted task has integrated. A file the task refused is not granted by rule and
// goes to the orchestrator as a request, whatever else its request names.
func TestSharedFilesAcceptedRunTimeRequest(t *testing.T) {
	t.Parallel()
	asked := needsFilesView("needs_context", repairReturn(`["`+repairFile+`"]`))
	requested := store.TaskBuild{
		Stage: StageOrchestrate, Base: decideBranch, Attempt: 1, Rerun: 1, Step: "impl-step", RedRun: "red-run",
	}
	granted := store.TaskBuild{
		Stage: StageImplement, Base: decideBranch, Attempt: 1, Rerun: 2, RedRun: "red-run",
		Failure: "you returned needs_context for files outside your list, and these are now yours: " + repairFile +
			". Your earlier work is in this worktree; continue from it.",
	}
	withPeers := func(peers ...Peer) View {
		view := asked
		view.Peers = peers
		return view
	}
	refused := asked
	refused.Task.Refused = []string{repairFile}
	mixed := needsFilesView("needs_context", repairReturn(`["`+repairFile+`","b.go"]`))
	mixed.Task.Refused = []string{repairFile}
	runDecideCases(t, Decide, []decideCase{
		{
			name: "a file an open task owns is granted by rule",
			view: withPeers(openPeer(4, repairFile)),
			want: Action{
				Kind: ActGrant, Next: granted, NeedsFiles: []string{repairFile},
				Reason: "returned needs_context: the spec is short",
			},
		},
		{
			name: "a file an accepted task changed and has not integrated is granted by rule",
			view: withPeers(acceptedPeer(4, repairFile)),
			want: Action{
				Kind: ActGrant, Next: granted, NeedsFiles: []string{repairFile},
				Reason: "returned needs_context: the spec is short",
			},
		},
		{
			name: "a file an accepted task integrated is granted by rule",
			view: withPeers(Peer{
				Task: store.EpicTask{Seq: 4, Status: taskDone, Files: []string{repairFile}}, State: Integrated,
				Composed: "c4",
			}),
			want: Action{
				Kind: ActGrant, Next: granted, NeedsFiles: []string{repairFile},
				Reason: "returned needs_context: the spec is short",
			},
		},
		{
			name: "a file the task refused is not granted by rule and becomes a request",
			view: refused,
			want: Action{Kind: ActMove, Next: requested},
		},
		{
			name: "one refused file among those a return names keeps the whole request for the orchestrator",
			view: mixed,
			want: Action{Kind: ActMove, Next: requested},
		},
	})
}

// TestTaskRepairsGrantLaterDecisionGrantsARefusedFile follows a file through three decisions. An orchestrator rerun
// refuses the file the implementer named, which is recorded on the decision. The implementer's next return names it
// again, and the owner check does not grant it by rule, since the task refused it. An orchestrator grant that adds the
// file then grants it, and the decision refuses nothing.
func TestTaskRepairsGrantLaterDecisionGrantsARefusedFile(t *testing.T) {
	t.Parallel()
	rerun := Decide(repairView(0, `["b.go"]`, orchDone(orchReturn(t, map[string]any{"decision": "rerun"}))))
	if rerun.Kind != ActDecide || !reflect.DeepEqual(rerun.Decision.Refused, []string{"b.go"}) {
		t.Fatalf("rerun = %+v, want a decision refusing b.go", rerun)
	}

	t.Run("a file an orchestrator decision refused is not granted by rule on the next return", func(t *testing.T) {
		t.Parallel()
		next := owning(needsFilesView("needs_context", repairReturn(`["b.go"]`)), "a.go")
		next.Task.Refused = rerun.Decision.Refused
		if got := Decide(next); got.Kind != ActMove {
			t.Errorf("next return of a refused file = %s, want a request to the orchestrator", got.Kind)
		}
	})
	t.Run("a later orchestrator decision that adds the refused file grants it", func(t *testing.T) {
		t.Parallel()
		grant := repairView(1, `["b.go"]`, orchDone(orchReturn(t, map[string]any{
			"decision": "grant", "version": filesVersion("b.go"),
		})))
		grant.Task.Refused = rerun.Decision.Refused
		got := Decide(grant)
		if got.Kind != ActDecide || !reflect.DeepEqual(got.Version.Files, []string{"b.go"}) {
			t.Fatalf("grant = %s with version files %v, want a decision granting b.go", got.Kind, got.Version.Files)
		}
		if len(got.Decision.Refused) != 0 {
			t.Errorf("grant refused %v, want none", got.Decision.Refused)
		}
	})
}

// TestTaskRepairsGrantImplementerMessageNamesOnlyTheGrantedFiles checks the failure an orchestrator's decision on a
// needs_context request leaves the implementer: it names the files the decision's version granted, or says none were,
// and never a file the decision refused. The refused files are the ones the request names and the version does not add.
func TestTaskRepairsGrantImplementerMessageNamesOnlyTheGrantedFiles(t *testing.T) {
	t.Parallel()
	grant := Decide(repairView(0, `["b.go","c.go"]`, orchDone(orchReturn(t, map[string]any{
		"decision": "grant", "version": filesVersion("b.go"),
	}))))
	if grant.Kind != ActDecide {
		t.Fatalf("grant = %s, want a decision", grant.Kind)
	}
	if want := "returned needs_context: the spec is short; the orchestrator granted b.go"; grant.Next.Failure != want {
		t.Errorf("failure after a grant = %q, want %q", grant.Next.Failure, want)
	}
	if want := []string{"c.go"}; !reflect.DeepEqual(grant.Decision.Refused, want) {
		t.Errorf("refused by a grant = %v, want %v", grant.Decision.Refused, want)
	}

	rerun := Decide(repairView(0, `["b.go","c.go"]`, orchDone(orchReturn(t, map[string]any{"decision": "rerun"}))))
	if rerun.Kind != ActDecide {
		t.Fatalf("rerun = %s, want a decision", rerun.Kind)
	}
	if want := "returned needs_context: the spec is short; the orchestrator granted none"; rerun.Next.Failure != want {
		t.Errorf("failure after a rerun = %q, want %q", rerun.Next.Failure, want)
	}
	if want := []string{"b.go", "c.go"}; !reflect.DeepEqual(rerun.Decision.Refused, want) {
		t.Errorf("refused by a rerun = %v, want %v", rerun.Decision.Refused, want)
	}
	if strings.Contains(grant.Next.Failure, "c.go") {
		t.Errorf("failure %q names the refused c.go", grant.Next.Failure)
	}
}

// acceptChanging accepts task seq of f's epic with a commit on the base that changes file, so the task is accepted and
// not yet integrated, and the file is one its change touched.
func acceptChanging(t *testing.T, f *passFixture, seq int64, file string) {
	t.Helper()
	var from store.TaskBuild
	for _, task := range f.epic().Tasks {
		if task.Seq == seq {
			from = task.Build
		}
	}
	to := from
	to.Base, to.Revision = f.base, commitOff(t, f.repo, f.base, file, "package other\n")
	if err := f.store.SetTaskBuild(f.ctx, passEpic, seq, from, to); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	if err := f.store.AcceptTask(f.ctx, passEpic, seq, to, ReviewUnreviewed); err != nil {
		t.Fatalf("AcceptTask: %v", err)
	}
}

// TestSharedFilesAcceptedRunTimeRequestThroughTheRunner runs a needs_context return through the runner. Task 2 is accepted and
// not yet integrated, and its change touched the file task 1's implementer names, so the file is held. It is granted by
// rule like a free file: task 1 gains the file and its implementer runs again at rerun 1.
func TestSharedFilesAcceptedRunTimeRequestThroughTheRunner(t *testing.T) {
	t.Parallel()
	plan := passTwoTaskPlan()
	plan.Tasks[1].Files = []string{"internal/other/b.go"}
	f := newPlanFixture(t, plan)
	identify(t, f.repo)
	acceptChanging(t, f, 2, repairFile)
	first := []int64{1}
	f.runner.PassTasks(f.ctx, passEpic, first)
	f.addRun(RunID(passEpic, 1, StageRed, 0, 0), evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.runner.PassTasks(f.ctx, passEpic, first)
	f.steps.rows[passStep(StageImplement, 1, 0)] = store.StepRow{
		ID: passStep(StageImplement, 1, 0), End: endReturned, Status: statusNeedsContext,
		Result: repairReturn(`["` + repairFile + `"]`),
	}
	f.runner.PassTasks(f.ctx, passEpic, first)
	task := f.task()
	if task.Build.Stage != StageImplement || task.Build.Rerun != 1 {
		t.Errorf("build at stage %s rerun %d, want the implementer rerun once at implement",
			task.Build.Stage, task.Build.Rerun)
	}
	if want := []string{"thing.go", repairFile}; !reflect.DeepEqual(task.Files, want) {
		t.Errorf("Files = %v, want %v: the file an accepted task changed was granted by rule", task.Files, want)
	}
}
