package build

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/joshsymonds/orrery/internal/store"
)

// orchImplStep is the id of the implementer step whose request the orchestrate tests decide.
const orchImplStep = "impl-step"

// orchReturned is the implementer step that ended returned blocked, which the orchestrate tests decide.
func orchReturned() *store.StepRow {
	return &store.StepRow{
		ID: orchImplStep, End: endReturned, Status: "needs_context",
		Result: `{"status":"needs_context","summary":"the spec is short","needs_files":["b.go"]}`,
	}
}

// orchBuild is task 3's build at orchestrate on the implementer step orchImplStep, with decisions made.
func orchBuild(decisions int64) store.TaskBuild {
	return store.TaskBuild{
		Stage: StageOrchestrate, Base: decideBranch, Attempt: 1, Rerun: 2, Step: orchImplStep, RedRun: "red-run",
		Decisions: decisions,
	}
}

// orchView returns the view of task 3, owning a.go, at orchestrate with decisions made, whose orchestrator step is step,
// among peers.
func orchView(decisions int64, step *store.StepRow, peers ...Peer) View {
	task := store.EpicTask{Seq: 3, Files: []string{"a.go"}, Status: taskOpen, Build: orchBuild(decisions)}
	return View{
		Epic: decideEpic, Task: task, Step: step, Returned: orchReturned(),
		Peers: append([]Peer{{Task: task, State: Pending}}, peers...),
	}
}

// orchDone returns the orchestrator's step ended done with the return ret.
func orchDone(ret string) *store.StepRow {
	return &store.StepRow{ID: "orch-step", End: endDone, Status: statusDone, Result: ret}
}

// orchFail is the fallback of orchReturned: the task fails as it did before the orchestrator.
func orchFail() Action {
	return Action{Kind: ActFail, Reason: "returned needs_context: the spec is short", NeedsFiles: []string{"b.go"}}
}

// openPeer returns open task seq owning files, at implement.
func openPeer(seq int64, files ...string) Peer {
	return Peer{
		Task:  store.EpicTask{Seq: seq, Status: taskOpen, Files: files, Build: store.TaskBuild{Stage: StageImplement}},
		State: Pending,
	}
}

// acceptedPeer returns accepted task seq, pending integration, whose change touched changed.
func acceptedPeer(seq int64, changed ...string) Peer {
	return Peer{Task: store.EpicTask{Seq: seq, Status: taskDone, Files: changed}, State: Pending, Changed: changed}
}

// emptyVersion returns the version of a return that changes nothing: every field the schema requires, empty.
func emptyVersion() map[string]any {
	return map[string]any{
		"changed": []string{}, "deliverable": "", "files": []string{}, "remove": []string{}, "test_changes": []string{},
		"gate": "", "evidence": map[string]any{
			"capture": []string{}, "limit": 0, "artifacts": []string{}, "supplied": []string{}, "conclusion": "",
			"judge": "", "kind": "", "tests": []string{},
		},
	}
}

// versionWith returns the version emptyVersion gives, with the fields given set.
func versionWith(fields map[string]any) map[string]any {
	version := emptyVersion()
	maps.Copy(version, fields)
	return version
}

// filesVersion returns a version that adds files to the task: it changes files, and names them.
func filesVersion(files ...string) map[string]any {
	return versionWith(map[string]any{"changed": []string{"files"}, "files": files})
}

// orchReturn renders an orchestrator return with every field the schema requires, from the fields given.
func orchReturn(t *testing.T, fields map[string]any) string {
	t.Helper()
	ret := map[string]any{
		"status": "done", "evidence": "a.go:3 calls b", "decision": "", "version": emptyVersion(),
		"after_tasks": []int64{}, "failure": "", "needs_files": []string{}, "reason": "the code says so",
		"question": map[string]any{
			"label": "", "text": "", "choices": []any{}, "recommended": "", "why": "", "confidence": "",
			"catastrophe": "",
		},
	}
	for key, value := range fields {
		ret[key] = value
	}
	data, err := json.Marshal(ret)
	if err != nil {
		t.Fatalf("encode return: %v", err)
	}
	return string(data)
}

// askQuestion is a question an ask may put to the owner.
func askQuestion() map[string]any {
	return map[string]any{
		"label": "capture-broken", "text": "R1's capture exits 2 as written", "recommended": "edit",
		"choices":    []map[string]string{{"id": "edit", "does": "edit the contract"}, {"id": "end", "does": "end it"}},
		"why":        "nothing in the epic can change the capture",
		"confidence": "high", "catastrophe": "contract-cannot-hold",
	}
}

func TestOrchestratorSchemaAcceptsEachDecisionAndUsesTheEngineSubset(t *testing.T) {
	t.Parallel()
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(OrchestratorSchema), &schema); err != nil {
		t.Fatalf("OrchestratorSchema is not a schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, ret := range []string{
		orchReturn(t, map[string]any{
			"decision": "rerun", "after_tasks": []int64{2},
			"version": versionWith(map[string]any{"changed": []string{"gate"}, "gate": "check"}),
		}),
		orchReturn(t, map[string]any{"decision": "grant", "version": filesVersion("b.go")}),
		orchReturn(t, map[string]any{"decision": "re_brief", "version": versionWith(map[string]any{
			"changed": []string{"files"}, "files": []string{"c.go"}, "remove": []string{"a.go"},
		})}),
		orchReturn(t, map[string]any{"decision": "fail", "failure": "f", "needs_files": []string{"b.go"}}),
		orchReturn(t, map[string]any{"decision": "ask", "question": askQuestion()}),
		orchReturn(t, map[string]any{"status": "blocked"}),
	} {
		var instance any
		if err := json.Unmarshal([]byte(ret), &instance); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(instance); err != nil {
			t.Errorf("schema refused %s: %v", ret, err)
		}
	}
	if strings.Contains(OrchestratorSchema, "anyOf") || strings.Contains(OrchestratorSchema, "oneOf") {
		t.Error("schema uses a keyword outside the engine's subset")
	}
}

func TestOrchestratorCheckShape(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 3, Files: []string{"a.go"}, Status: taskOpen}
	check := OrchestratorCheck(task, []Peer{{Task: task}})
	cases := []struct {
		name string
		ret  string
		want []string
	}{
		{
			"a rerun changing its deliverable and a reason passes",
			orchReturn(t, map[string]any{"decision": "rerun", "version": versionWith(map[string]any{
				"changed": []string{"deliverable"}, "deliverable": "d",
			})}),
			nil,
		},
		{
			"a rerun naming files and a failure is refused per field",
			orchReturn(t, map[string]any{"decision": "rerun", "version": filesVersion("b.go"), "failure": "f"}),
			[]string{"version.changed: files named on a rerun", "failure: not empty on rerun"},
		},
		{
			"a rerun adding files its changed list does not name is refused",
			orchReturn(t, map[string]any{"decision": "rerun", "version": versionWith(map[string]any{
				"files": []string{"b.go"},
			})}),
			[]string{"version.files: not empty when changed does not name it"},
		},
		{
			"a fail with a version and after_tasks is refused",
			orchReturn(t, map[string]any{
				"decision": "fail", "failure": "f", "after_tasks": []int64{2},
				"version": versionWith(map[string]any{"changed": []string{"gate"}, "gate": "check"}),
			}),
			[]string{"version: not empty on fail", "after_tasks: not empty on fail"},
		},
		{
			"a fail with no failure is refused",
			orchReturn(t, map[string]any{"decision": "fail"}),
			[]string{"failure: empty"},
		},
		{
			"an ask with no catastrophe and one choice is refused",
			orchReturn(t, map[string]any{"decision": "ask", "question": map[string]any{
				"label": "q", "text": "t", "choices": []map[string]string{{"id": "a", "does": "d"}},
				"recommended": "a", "why": "w", "confidence": "low", "catastrophe": "",
			}}),
			[]string{
				"question.catastrophe: not irreversible-step, contract-cannot-hold, premise-false, cannot-proceed",
				"question.choices: fewer than two choices",
			},
		},
		{
			"an ask offering other is refused",
			orchReturn(t, map[string]any{"decision": "ask", "question": map[string]any{
				"label": "q", "text": "t", "recommended": "a", "why": "w", "confidence": "low",
				"catastrophe": "cannot-proceed",
				"choices":     []map[string]string{{"id": "a", "does": "d"}, {"id": "other", "does": "d"}},
			}}),
			[]string{"question.choices[1].id: other is offered by orrery"},
		},
		{"a full ask passes", orchReturn(t, map[string]any{"decision": "ask", "question": askQuestion()}), nil},
		{
			"a done return with no decision and no reason is refused",
			orchReturn(t, map[string]any{"reason": ""}),
			[]string{"reason: empty", "decision: empty on a done return"},
		},
		{
			"a blocked return naming a decision is refused",
			orchReturn(t, map[string]any{"status": "blocked", "decision": "rerun"}),
			[]string{"decision: not empty on a blocked return"},
		},
		{"a blocked return with evidence passes", orchReturn(t, map[string]any{"status": "blocked"}), nil},
		{"a return that does not decode is refused", `{"status":7}`, nil},
	}
	for _, tc := range cases {
		got := check(json.RawMessage(tc.ret))
		if tc.name == "a return that does not decode is refused" {
			if len(got) != 1 || !strings.HasPrefix(got[0], "orchestrator return: ") {
				t.Errorf("%s: check = %q, want one decode refusal", tc.name, got)
			}
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: check = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestOrchestratorCheckRefusesStateTheEngineCannotCarryOut(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 3, Files: []string{"a.go"}, Status: taskOpen}
	failed := Peer{Task: store.EpicTask{Seq: 6, Status: "failed"}, State: Lost, Why: "failed"}
	integrated := Peer{Task: store.EpicTask{Seq: 7, Status: taskDone}, State: Integrated, Composed: "c7"}
	waiter := openPeer(8, "w.go")
	waiter.Task.Build.Held, waiter.Task.Build.After = HeldWaiting, "3"
	chained := openPeer(9, "x.go")
	chained.Task.Build.Held, chained.Task.Build.After = HeldWaiting, "8"
	stale := openPeer(10, "y.go")
	stale.Task.Build.After = "3"
	peers := []Peer{
		{Task: task}, openPeer(4, "owned.go"), acceptedPeer(5, "changed.go"), failed, integrated, waiter, chained,
		stale,
	}
	check := OrchestratorCheck(task, peers)
	cases := []struct {
		name string
		ret  map[string]any
		want []string
	}{
		{
			"after_tasks naming this task, no task, a failed, an integrated task and one twice",
			map[string]any{"decision": "rerun", "after_tasks": []int64{3, 42, 6, 7, 4, 4}},
			[]string{
				"after_tasks[0]: T3 is this task",
				"after_tasks[1]: T42 is not a task of this epic",
				"after_tasks[2]: T6 is neither open nor accepted and waiting to be integrated",
				"after_tasks[3]: T7 is neither open nor accepted and waiting to be integrated",
				"after_tasks[5]: T4 is named twice",
			},
		},
		{
			"after_tasks that wait on this task directly or through another are a cycle",
			map[string]any{"decision": "rerun", "after_tasks": []int64{8, 9}},
			[]string{"after_tasks[0]: T8 waits on this task", "after_tasks[1]: T9 waits on this task"},
		},
		{
			"a task that once waited on this task but waits no longer is no cycle",
			map[string]any{"decision": "rerun", "after_tasks": []int64{10}},
			nil,
		},
		{
			"open and accepted pending tasks may be waited for",
			map[string]any{"decision": "rerun", "after_tasks": []int64{4, 5}},
			nil,
		},
		{
			"a grant of a refused path, the task's own file, a held file and one named twice",
			map[string]any{"decision": "grant", "version": filesVersion(
				".git/config", "a.go", "owned.go", "changed.go", "free.go", "free.go",
			)},
			[]string{
				"version.files[0]: .git/config is refused: inside a .git directory",
				"version.files[1]: a.go is already one of the task's files",
				"version.files[5]: free.go is named twice",
			},
		},
		{
			"a grant of held files behind their holders passes",
			map[string]any{
				"decision": "grant", "version": filesVersion("owned.go", "changed.go", "free.go"),
				"after_tasks": []int64{4, 5},
			},
			nil,
		},
		{
			"a fail is not checked against state",
			map[string]any{"decision": "fail", "failure": "f", "needs_files": []string{"owned.go"}},
			nil,
		},
	}
	for _, tc := range cases {
		if got := check(json.RawMessage(orchReturn(t, tc.ret))); !slices.Equal(got, tc.want) {
			t.Errorf("%s: check = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDecideRoutesRequestsToTheOrchestrator(t *testing.T) {
	t.Parallel()
	implement := store.TaskBuild{
		Stage: StageImplement, Base: decideBranch, Attempt: 1, Rerun: 1, NoVerdict: 1, Step: orchImplStep,
		RedRun: "red-run",
	}
	orchestrating := implement
	orchestrating.Stage = StageOrchestrate
	orchestrating.NoVerdict = 0
	ownedOnly := &store.StepRow{
		ID:     orchImplStep,
		End:    endChecksFailed,
		Reason: "owned: b.go is not owned",
		Checks: []store.StepCheck{
			{Round: 3, Name: "gate", OK: false},
			{Round: 4, Name: "head", OK: true}, {Round: 4, Name: "clean", OK: true},
			{Round: 4, Name: checkOwned, OK: false, Detail: "b.go is not owned"},
		},
	}
	ownedAndClean := &store.StepRow{
		ID:     orchImplStep,
		End:    endChecksFailed,
		Reason: "owned; clean",
		Checks: []store.StepCheck{
			{Round: 4, Name: "clean", OK: false}, {Round: 4, Name: checkOwned, OK: false},
		},
	}
	const spentOwnedFailure = "checks_failed: owned: b.go is not owned"
	const spentBothFailure = "checks_failed: owned; clean"
	exhausted := implement
	exhausted.Decisions = decisionLimit
	runDecideCases(t, Decide, []decideCase{
		{
			name: "blocked with decisions left moves to orchestrate",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: implement}, Step: &store.StepRow{
				ID: orchImplStep, End: endReturned, Status: "blocked", Result: `{"status":"blocked","summary":"s"}`,
			}},
			want: Action{Kind: ActMove, Next: orchestrating},
		},
		{
			name: "checks_failed whose last round failed only owned moves to orchestrate",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: implement}, Step: ownedOnly},
			want: Action{Kind: ActMove, Next: orchestrating},
		},
		{
			name: "checks_failed failing owned and another check spends the attempt",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: implement}, Step: ownedAndClean},
			want: Action{Kind: ActFail, Reason: spentBothFailure},
		},
		{
			name: "checks_failed failing only owned with no decisions left spends the attempt",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: exhausted}, Step: ownedOnly},
			want: Action{Kind: ActFail, Reason: spentOwnedFailure},
		},
	})
}

func TestDecideOrchestrate(t *testing.T) {
	t.Parallel()
	rerun := orchBuild(0)
	rerun.Stage, rerun.Rerun, rerun.Step, rerun.Decisions = StageImplement, 3, "", 1
	rerun.Failure = "returned needs_context: the spec is short; the orchestrator granted none"
	grantedRerun := rerun
	grantedRerun.Failure = "returned needs_context: the spec is short; the orchestrator granted b.go"
	waiting := rerun
	waiting.Held, waiting.After = HeldWaiting, "4,5"
	waitingOnFour := rerun
	waitingOnFour.Held, waitingOnFour.After = HeldWaiting, "4"
	asked := orchBuild(0)
	asked.Held, asked.Decisions = HeldAsked, 1
	restarted := orchBuild(1)
	restarted.Rerun = 3
	stopped := orchBuild(1)
	stopped.Held = HeldStopped
	deliverable := "The thing holds: thing.go:3 names the field."
	rerunRet := orchReturn(t, map[string]any{"decision": "rerun", "version": versionWith(map[string]any{
		"changed": []string{"deliverable"}, "deliverable": deliverable,
	})})
	waitRet := orchReturn(t, map[string]any{"decision": "rerun", "after_tasks": []int64{4, 5}})
	grantRet := orchReturn(t, map[string]any{"decision": "grant", "version": filesVersion("b.go")})
	nothing := store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "orch-step", Reason: "the code says so", Files: []string{},
		Remove: []string{},
	}
	redone := nothing
	redone.Deliverable = &deliverable
	granted := nothing
	granted.Files = []string{"b.go"}
	failRet := orchReturn(t, map[string]any{
		"decision": "fail", "failure": "The task's files cannot hold its deliverable.", "needs_files": []string{"b.go"},
	})
	askRet := orchReturn(t, map[string]any{"decision": "ask", "question": askQuestion()})
	cycleRet := orchReturn(t, map[string]any{"decision": "rerun", "after_tasks": []int64{8}})
	waiter := openPeer(8)
	waiter.Task.Build.Held, waiter.Task.Build.After = HeldWaiting, "3"
	fallback := orchFail()
	grantFallback := fallback
	question := store.EpicQuestionInput{
		Label: "capture-broken", Text: "T3 (contract-cannot-hold): R1's capture exits 2 as written",
		Choices:     []store.EpicChoice{{ID: "edit", Does: "edit the contract"}, {ID: "end", Does: "end it"}},
		Recommended: "edit", Why: "nothing in the epic can change the capture", Confidence: "high",
	}
	runDecideCases(t, Decide, []decideCase{
		{
			name: "no step starts the orchestrator under its derived id",
			view: orchView(0, nil),
			want: Action{
				Kind: ActStartStep, Unit: StageOrchestrate, ID: StepID(decideEpic, 3, StageOrchestrate, 1, 2, 0, 0),
			},
		},
		{
			name: "no step while paused waits",
			view: func() View { v := orchView(0, nil); v.Paused = true; return v }(),
			want: Action{Kind: ActNone},
		},
		{
			name: "no step with three decisions made settles as before the orchestrator",
			view: orchView(decisionLimit, nil),
			want: fallback,
		},
		{
			name: "a running step waits",
			view: orchView(0, &store.StepRow{ID: "orch-step"}),
			want: Action{Kind: ActNone},
		},
		{
			name: "rerun moves to implement at the next rerun with its version, counting the decision",
			view: orchView(0, orchDone(rerunRet)),
			want: Action{
				Kind: ActDecide, Next: rerun, Fallback: &fallback, Behind: []int64{}, Version: redone,
				Decision: &store.TaskDecision{
					ID: "T3-request-1", Text: "T3 rerun: the code says so", Refused: []string{"b.go"},
				},
			},
		},
		{
			name: "rerun after open tasks holds the build waiting on them",
			view: orchView(0, orchDone(waitRet), openPeer(4), acceptedPeer(5)),
			want: Action{
				Kind: ActDecide, Next: waiting, Behind: []int64{4, 5}, Fallback: &fallback, Version: nothing,
				Decision: &store.TaskDecision{
					ID: "T3-request-1", Text: "T3 rerun after T4, T5: the code says so", Refused: []string{"b.go"},
				},
			},
		},
		{
			name: "grant carries its files",
			view: orchView(0, orchDone(grantRet)),
			want: Action{
				Kind: ActDecide, Next: grantedRerun, Version: granted, Fallback: &grantFallback, Behind: []int64{},
				Decision: &store.TaskDecision{ID: "T3-request-1", Text: "T3 grant b.go: the code says so"},
			},
		},
		{
			name: "fail fails the task with the orchestrator's failure and needs_files",
			view: orchView(1, orchDone(failRet)),
			want: Action{
				Kind: ActFail, Reason: "The task's files cannot hold its deliverable.", NeedsFiles: []string{"b.go"},
				Decision: &store.TaskDecision{
					ID: "T3-request-2", Text: "T3 fail: the code says so", Refused: []string{"b.go"},
				},
				Move: store.RungMove{MovedBy: store.MovedByFailDecision},
			},
		},
		{
			name: "ask holds the build asked with the question naming its catastrophe",
			view: orchView(0, orchDone(askRet)),
			want: Action{
				Kind:     ActAsk,
				Next:     asked,
				Reason:   question.Text,
				Question: &question,
				Fallback: &fallback,
				Decision: &store.TaskDecision{
					ID:   "T3-request-1",
					Text: "T3 ask (contract-cannot-hold): the code says so",
				},
			},
		},
		{
			name: "a wait that became a cycle while the step ran settles as before, naming the refusal",
			view: orchView(0, orchDone(cycleRet), waiter),
			want: Action{
				Kind: ActFail, NeedsFiles: []string{"b.go"},
				Reason: "returned needs_context: the spec is short; orreryd refused the orchestrator's rerun: " +
					"after_tasks[0]: T8 waits on this task",
			},
		},
		{
			name: "a wait for a task integrated while the step ran is carried out, and resolves on the next pass",
			view: orchView(0, orchDone(orchReturn(t, map[string]any{
				"decision": "rerun", "after_tasks": []int64{4},
			})), Peer{Task: store.EpicTask{Seq: 4, Status: taskDone}, State: Integrated, Composed: "c4"}),
			want: Action{
				Kind: ActDecide, Next: waitingOnFour, Behind: []int64{4}, Fallback: &fallback, Version: nothing,
				Decision: &store.TaskDecision{
					ID: "T3-request-1", Text: "T3 rerun after T4: the code says so", Refused: []string{"b.go"},
				},
			},
		},
		{
			name: "a wait for a task that failed while the step ran settles as before, naming the refusal",
			view: orchView(0, orchDone(orchReturn(t, map[string]any{
				"decision": "rerun", "after_tasks": []int64{4},
			})), Peer{Task: store.EpicTask{Seq: 4, Status: taskFailed}, State: Lost, Why: "failed"}),
			want: Action{
				Kind: ActFail, NeedsFiles: []string{"b.go"},
				Reason: "returned needs_context: the spec is short; orreryd refused the orchestrator's rerun: " +
					"after_tasks[0]: T4 is neither open nor accepted and waiting to be integrated",
			},
		},
		{
			name: "a blocked return settles as before the orchestrator",
			view: orchView(0, orchDone(orchReturn(t, map[string]any{"status": "blocked"}))),
			want: fallback,
		},
		{
			name: "a return that does not decode settles as before the orchestrator",
			view: orchView(0, orchDone("not json")),
			want: fallback,
		},
		{
			name: "a failed step settles as before the orchestrator",
			view: orchView(0, &store.StepRow{ID: "orch-step", End: "failed", Reason: "claude exited"}),
			want: fallback,
		},
		{
			name: "a step the engine ended on a restart reruns the unit",
			view: orchView(1, &store.StepRow{ID: "orch-step", End: endEngineRestarted}),
			want: Action{Kind: ActMove, Next: restarted},
		},
		{
			name: "a stopped step holds the build stopped",
			view: orchView(1, &store.StepRow{ID: "orch-step", End: endStopped}),
			want: Action{Kind: ActPause, Next: stopped},
		},
	})
}

func TestDecideOrchestrateFallbackForAnOwnedCheckSpendsTheAttempt(t *testing.T) {
	t.Parallel()
	view := orchView(0, orchDone(orchReturn(t, map[string]any{"status": "blocked"})))
	view.Returned = &store.StepRow{ID: orchImplStep, End: endChecksFailed, Reason: "owned: b.go"}
	runDecideCases(t, Decide, []decideCase{{
		name: "a blocked return on an owned-only request spends the attempt", view: view,
		want: Action{Kind: ActFail, Reason: "checks_failed: owned: b.go"},
	}})
}

func TestDecideOrchestrateWithoutTheImplementerStepFails(t *testing.T) {
	t.Parallel()
	view := orchView(decisionLimit, nil)
	view.Returned = nil
	runDecideCases(t, Decide, []decideCase{{
		name: "a missing implementer step fails the task", view: view,
		want: Action{Kind: ActFail, Reason: "orchestrate: the implementer's step impl-step is not stored"},
	}})
}

func TestDecideStartErrorOfOrchestrateSettlesAsBefore(t *testing.T) {
	t.Parallel()
	got := DecideStartError(orchView(0, nil), errGitRefused)
	want := orchFail()
	want.Reason += "; orchestrate did not start: git refused the write"
	if !slices.Equal([]string{got.Reason}, []string{want.Reason}) || got.Kind != ActFail {
		t.Errorf("DecideStartError = %+v, want %+v", got, want)
	}
}

func TestDecideWaitAndAsked(t *testing.T) {
	t.Parallel()
	waiting := store.TaskBuild{
		Stage: StageImplement, Base: "old-base", Attempt: 1, Rerun: 3, Held: HeldWaiting, After: "4,5", Decisions: 1,
	}
	resumed := waiting
	resumed.Held, resumed.Base = "", "new-base"
	wait := func(peers ...Peer) View {
		return View{
			Epic: decideEpic, NewBase: "new-base", Peers: peers,
			Task: store.EpicTask{Seq: 3, Status: taskOpen, Build: waiting},
		}
	}
	integrated := func(seq int64) Peer {
		return Peer{Task: store.EpicTask{Seq: seq, Status: taskDone}, State: Integrated, Composed: "c"}
	}
	asked := store.TaskBuild{Stage: StageOrchestrate, Held: HeldAsked, Question: 7, Decisions: 1, Step: orchImplStep}
	released := asked
	released.Held, released.Question = "", 0
	runDecideCases(t, Decide, []decideCase{
		{
			name: "a task waited for still open waits",
			view: wait(openPeer(4), integrated(5)),
			want: Action{Kind: ActNone},
		},
		{
			name: "a task waited for that is accepted and not yet integrated waits",
			view: wait(integrated(4), acceptedPeer(5)), want: Action{Kind: ActNone},
		},
		{
			name: "every task waited for integrated resumes on the new base, keeping After",
			view: wait(integrated(4), integrated(5)),
			want: Action{Kind: ActResume, Next: resumed, Revision: "new-base"},
		},
		{
			name: "a waited task that failed fails the build",
			view: wait(Peer{Task: store.EpicTask{Seq: 4, Status: "failed"}, State: Lost, Why: "failed"}, integrated(5)),
			want: Action{Kind: ActFail, Reason: "T3 waited on T4, which failed"},
		},
		{
			name: "a waited task left out of its batch fails the build",
			view: wait(integrated(4), Peer{
				Task: store.EpicTask{Seq: 5, Status: taskDone}, State: Lost, Why: "was left out of its batch: overlap",
			}),
			want: Action{Kind: ActFail, Reason: "T3 waited on T5, which was left out of its batch: overlap"},
		},
		{
			name: "a waited task that is not stored fails the build",
			view: wait(integrated(4)),
			want: Action{Kind: ActFail, Reason: "T3 waited on T5, which is not a task of the epic"},
		},
		{
			name: "a paused epic does not resume",
			view: func() View { v := wait(integrated(4), integrated(5)); v.Paused = true; return v }(),
			want: Action{Kind: ActNone},
		},
		{
			name: "an open question waits",
			view: View{Epic: decideEpic, Task: store.EpicTask{Seq: 3, Build: asked}},
			want: Action{Kind: ActNone},
		},
		{
			name: "an answered question releases the build to orchestrate again",
			view: View{Epic: decideEpic, Answered: true, Task: store.EpicTask{Seq: 3, Build: asked}},
			want: Action{Kind: ActMove, Next: released},
		},
	})
}

func TestIntegrationOfReadsTheBatchThatClaimedTheTask(t *testing.T) {
	t.Parallel()
	result := `{"composed":[{"task":1,"commit":"c1"}],"leftOut":[{"task":2,"reason":"T2 overlaps T1 at a.go"}]}`
	runs := []store.NodeRun{
		{Path: "plan/S2/integrate", State: store.NodeEnded, Result: result},
		{Path: "plan/S5/integrate", State: store.NodeRunning, Result: `{"composed":[{"task":5,"commit":"c5"}]}`},
		{Path: "plan/S6/integrate", State: store.NodeEnded, Result: "not json"},
	}
	cases := []struct {
		task  store.EpicTask
		state Integration
		why   string
	}{
		{store.EpicTask{Seq: 1, Status: taskDone, Batch: "plan/S2/integrate"}, Integrated, ""},
		{store.EpicTask{Seq: 2, Status: taskDone, Batch: "plan/S2/integrate"}, Lost,
			"was left out of its batch: T2 overlaps T1 at a.go"},
		{store.EpicTask{Seq: 3, Status: taskFailed}, Lost, "failed"},
		{store.EpicTask{Seq: 4, Status: taskDone}, Pending, ""},
		{store.EpicTask{Seq: 5, Status: taskDone, Batch: "plan/S5/integrate"}, Pending, ""},
		{store.EpicTask{Seq: 6, Status: taskDone, Batch: "plan/S6/integrate"}, Lost,
			"was claimed by a batch whose result does not decode"},
		{store.EpicTask{Seq: 7, Status: taskOpen}, Pending, ""},
	}
	for _, tc := range cases {
		got := integrationOf(tc.task, runs)
		if got.State != tc.state || got.Why != tc.why {
			t.Errorf("T%d: integration %d (%q), want %d (%q)", tc.task.Seq, got.State, got.Why, tc.state, tc.why)
		}
	}
}

func TestAfterSeqsRoundTripsAndSkipsJunk(t *testing.T) {
	t.Parallel()
	if got := AfterSeqs(afterText([]int64{4, 12})); !slices.Equal(got, []int64{4, 12}) {
		t.Errorf("AfterSeqs(afterText) = %v, want [4 12]", got)
	}
	if got := AfterSeqs(""); len(got) != 0 {
		t.Errorf("AfterSeqs(\"\") = %v, want none", got)
	}
	if got := AfterSeqs("x,3"); !slices.Equal(got, []int64{3}) {
		t.Errorf("AfterSeqs(x,3) = %v, want [3]", got)
	}
}

func TestOrchestratorCheckGrantsAHeldFileWithOrWithoutItsHolders(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 3, Files: []string{"a.go"}, Status: taskOpen}
	behind := openPeer(1, "shared.go")
	behind.Task.Build.Held, behind.Task.Build.After = HeldWaiting, "2"
	peers := []Peer{{Task: task}, behind, openPeer(2, "shared.go")}
	if got := holderOf("shared.go", task, peers).Text; got !=
		"owned by open T1 (implement, waiting) and owned by open T2 (implement)" {
		t.Errorf("holder = %q, want both owners", got)
	}
	check := OrchestratorCheck(task, peers)
	grant := func(after ...int64) []string {
		return check(json.RawMessage(orchReturn(t, map[string]any{
			"decision": "grant", "version": filesVersion("shared.go"), "after_tasks": after,
		})))
	}
	for _, after := range [][]int64{nil, {2}, {1, 2}} {
		if got := grant(after...); len(got) != 0 {
			t.Errorf("grant behind %v = %q, want none", after, got)
		}
	}
}

func TestOrchestratorCheckRefusesAnAskAsTheLastDecision(t *testing.T) {
	t.Parallel()
	ask := json.RawMessage(orchReturn(t, map[string]any{"decision": "ask", "question": askQuestion()}))
	for decisions, want := range map[int64][]string{
		0: nil,
		decisionLimit - 1: {"decision: an ask is the task's last decision, and no decision would be left to act on " +
			"the answer"},
	} {
		task := store.EpicTask{Seq: 3, Status: taskOpen, Build: store.TaskBuild{Decisions: decisions}}
		if got := OrchestratorCheck(task, []Peer{{Task: task}})(ask); !slices.Equal(got, want) {
			t.Errorf("ask after %d decisions: check = %q, want %q", decisions, got, want)
		}
	}
}

func TestAnsweredFollowsReplacementsToTheirAnswer(t *testing.T) {
	t.Parallel()
	questions := []store.EpicQuestion{
		{Seq: 1, State: store.QuestionReplaced, ReplacedBy: 2},
		{Seq: 2, State: store.QuestionOpen},
		{Seq: 3, State: store.QuestionReplaced, ReplacedBy: 4},
		{Seq: 4, State: store.QuestionAnswered, Choice: "edit"},
		{Seq: 5, State: store.QuestionAnswered},
	}
	for seq, want := range map[int64]bool{1: false, 2: false, 3: true, 5: true, 9: true} {
		if got := answered(questions, seq); got != want {
			t.Errorf("answered(Q%d) = %v, want %v", seq, got, want)
		}
	}
}
