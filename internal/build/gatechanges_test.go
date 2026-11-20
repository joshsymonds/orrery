package build

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// guardDoneDeclaring is the kept return of an implementer that finished with commit and declared one gate-needed change
// of guardTestFile for reason.
func guardDoneDeclaring(commit, reason string) string {
	return `{"summary":"done","commit":"` + commit + `","gate_changes":[{"path":"` + guardTestFile + `","reason":"` +
		reason + `"}]}`
}

// TestImplementerSchemaDeclaresGateChangesByPathAndReason: the implementer's return may carry gate_changes, a list of
// objects that each name a path and a reason and nothing else.
func TestImplementerSchemaDeclaresGateChangesByPathAndReason(t *testing.T) {
	t.Parallel()
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type  string `json:"type"`
			Items struct {
				Type                 string              `json:"type"`
				AdditionalProperties bool                `json:"additionalProperties"`
				Required             []string            `json:"required"`
				Properties           map[string]struct{} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(ImplementerSchema), &schema); err != nil {
		t.Fatalf("ImplementerSchema is not JSON: %v", err)
	}
	changes, ok := schema.Properties["gate_changes"]
	if !ok || changes.Type != "array" || changes.Items.Type != "object" || changes.Items.AdditionalProperties {
		t.Fatalf("gate_changes = %+v, want an array of closed objects", changes)
	}
	if want := []string{
		"path",
		"reason",
	}; !slices.Equal(changes.Items.Required, want) ||
		len(changes.Items.Properties) != 2 {
		t.Errorf("gate_changes items require %v with %d properties, want exactly %v", changes.Items.Required,
			len(changes.Items.Properties), want)
	}
	if slices.Contains(schema.Required, "gate_changes") {
		t.Errorf("required = %v, want gate_changes optional", schema.Required)
	}
}

// TestGuardPassStoresTheDeclaredGateChangesAndMovesToGreen: a commit the guard passes, whose return declares a
// gate-needed change, moves the build to green at the commit, and the task holds the declared change.
func TestGuardPassStoresTheDeclaredGateChangesAndMovesToGreen(t *testing.T) {
	t.Parallel()
	f := newGuardFixture(t)
	guardStartImplementer(f)
	worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
	moved := guardCommitIn(t, worktree, strings.Replace(guardTwoAsserts, `t.Error("b")`, `t.Error("c")`, 1))
	const reason = "the change moves the message TestThing expects"
	f.setStep(passStep(StageImplement, 1, 0), "done", guardDoneDeclaring(moved, reason))
	f.pass()
	f.expectGreenAt(moved)
	want := []store.GateChange{{Path: guardTestFile, Reason: reason}}
	if got := f.task().GateChanges; !reflect.DeepEqual(got, want) {
		t.Errorf("gate changes = %v, want %v", got, want)
	}
}

// TestGuardRefusalStoresNoDeclaredGateChange: a declared gate-needed change does not lift the guard. A commit that changes
// the capture file is refused though its return declares a file it changed, and the task holds no declared change.
func TestGuardRefusalStoresNoDeclaredGateChange(t *testing.T) {
	t.Parallel()
	f := newGuardFixture(t)
	guardStartImplementer(f)
	worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
	changed := guardCommitFiles(t, worktree, map[string]string{
		guardCapture: guardCaptureChanged, guardTestFile: guardTwoAsserts,
	})
	f.setStep(passStep(StageImplement, 1, 0), "done", guardDoneDeclaring(changed, "the gate fails on the capture"))
	f.pass()
	task := f.task()
	if task.Build.Stage != StageImplement || task.Build.Attempt != 2 || task.Build.Guard != guardRefusal {
		t.Errorf("build stage %q attempt %d guard %q, want implement at attempt 2 with the refusal",
			task.Build.Stage, task.Build.Attempt, task.Build.Guard)
	}
	if task.GateChanges != nil {
		t.Errorf("gate changes = %v, want none", task.GateChanges)
	}
}

func TestMergeGateChanges(t *testing.T) {
	t.Parallel()
	held := []store.GateChange{{Path: "a.go", Reason: "first"}, {Path: "b_test.go", Reason: "second"}}
	cases := []struct {
		name     string
		before   []store.GateChange
		declared []store.GateChange
		want     []store.GateChange
	}{
		{name: "nothing declared changes nothing", before: held, want: nil},
		{
			name: "a first declaration is the whole list", declared: []store.GateChange{{Path: "a.go", Reason: "r"}},
			want: []store.GateChange{{Path: "a.go", Reason: "r"}},
		},
		{
			name: "a new file is added after the held ones", before: held,
			declared: []store.GateChange{{Path: "c.go", Reason: "third"}},
			want:     append(append([]store.GateChange{}, held...), store.GateChange{Path: "c.go", Reason: "third"}),
		},
		{
			name: "a held file keeps its place and takes the new reason", before: held,
			declared: []store.GateChange{{Path: "a.go", Reason: "now this"}},
			want:     []store.GateChange{{Path: "a.go", Reason: "now this"}, {Path: "b_test.go", Reason: "second"}},
		},
		{
			name: "a held file declared again with its reason changes nothing", before: held,
			declared: []store.GateChange{{Path: " a.go ", Reason: "first "}}, want: nil,
		},
		{
			name: "a blank path or reason declares nothing", before: held,
			declared: []store.GateChange{{Path: "", Reason: "why"}, {Path: "d.go", Reason: "  "}}, want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mergeGateChanges(tc.before, tc.declared); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mergeGateChanges = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestOrchestratorReadsTheDeclaredGateChangesAndNeverFailsATaskForOne: the orchestrator's Task section lists the
// changes the task's implementers declared, after its test changes, and its role says a task whose gate needs such a
// change is rerun or granted, not failed.
func TestOrchestratorReadsTheDeclaredGateChangesAndNeverFailsATaskForOne(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{
		Seq: 13, Title: "Keep the step open", Deliverable: "the step stays open", Status: taskOpen,
		Files: []string{"internal/enginehost/record.go"}, Build: store.TaskBuild{Base: "b0se"},
		GateChanges: []store.GateChange{
			{Path: "internal/enginehost/record.go", Reason: "check_changed fails on a -race report in stoppedTasks"},
		},
	}
	runner := &Runner{getenv: func(string) string { return "" }}
	got, err := runner.requestTaskLines(context.Background(), store.EpicRow{Repository: t.TempDir()}, task)
	if err != nil {
		t.Fatalf("requestTaskLines: %v", err)
	}
	want := []string{
		"Test changes: None.",
		"Gate-needed changes declared: internal/enginehost/record.go: check_changed fails on a -race report in " +
			"stoppedTasks",
		"Requirements: None.",
	}
	at := slices.Index(got, want[0])
	if at < 0 || at+len(want) > len(got) || !slices.Equal(got[at:at+len(want)], want) {
		t.Errorf("Task lines = %q, want %q in a row", got, want)
	}
	rule := "So never fail a task because its gate needs such a change, or because a reviewer could read it as " +
		"work nobody asked for: rerun, or grant when it is in a file outside the task's own"
	if !strings.Contains(OrchestratorRole, rule) {
		t.Errorf("orchestrator role does not say %q", rule)
	}
}

// TestVerifierRoleJudgesADeclaredGateChangeByTheGatesNeed: the verifier drops a finding that a declared gate-needed
// change is work nobody asked for when the gate needs it, and confirms it when the change is not needed or is larger.
func TestVerifierRoleJudgesADeclaredGateChangeByTheGatesNeed(t *testing.T) {
	t.Parallel()
	want := "A change the prompt lists as a declared gate-needed change is in scope when the gate or evidence it names " +
		"could not pass without it and it is no larger than that need. So a finding that such a change is work the " +
		"task or the contract did not ask for is dropped when fresh evidence shows that need, and confirmed when it " +
		"shows the change is not needed or is larger than the need."
	if !strings.Contains(VerifierRole(), want) {
		t.Fatalf("verifier role does not say %q", want)
	}
}
