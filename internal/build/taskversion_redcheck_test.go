package build

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// redCheckItem is the evidence item the red checks rerun task 1 with: a conclusion the planner's item does not hold.
func redCheckItem() evidence.Item {
	return evidence.Item{
		Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "thing.go:3 holds the field", Judge: "the judge",
		Kind: evidence.KindCapture,
	}
}

// carryRerun carries out the rerun of task 1 that the orchestrator returned as ret, with execute, so the decision is
// stored and no red run starts.
func (f *passFixture) carryRerun(ret string) {
	f.t.Helper()
	view := requestView(f, ret)
	if !f.runner.execute(f.ctx, f.epic(), view, Decide(view)) {
		f.t.Fatal("execute changed nothing, want the rerun carried out")
	}
}

// redCheckRerun is the orchestrator's rerun that changes task 1's evidence to redCheckItem.
func redCheckRerun(t *testing.T) string {
	t.Helper()
	return orchReturn(t, map[string]any{
		"decision": "rerun", "reason": "the evidence names the wrong capture", "version": versionWith(map[string]any{
			"changed": []string{"evidence"}, "evidence": redCheckItem(),
		}),
	})
}

// addCheckingRun stores the red run id as ended at the base, judged under redCheckItem with verdict and a capture that
// exited 0. The fixture's Evidence starts runs under the task's own item, so a red run judging a checking version is
// stored here.
func addCheckingRun(f *passFixture, id, verdict string) {
	f.t.Helper()
	at := passClock()
	if err := f.store.AddEvidenceRun(f.ctx, store.EvidenceRun{
		ID: id, Epic: passEpic, Item: "T1", Revision: f.base, Phase: evidence.PhaseRed, Source: evidence.SourceCaptured,
		Spec: redCheckItem(), Created: at,
	}); err != nil {
		f.t.Fatalf("AddEvidenceRun: %v", err)
	}
	if err := f.store.RecordEvidenceCapture(f.ctx, id, store.EvidenceCapture{
		Exit: 0, LogHash: "loghash", LogBytes: 3, At: at,
	}); err != nil {
		f.t.Fatalf("RecordEvidenceCapture: %v", err)
	}
	if err := f.store.RecordEvidenceVerdict(f.ctx, id, store.EvidenceVerdict{
		Verdict: verdict, Reason: "the judge read the log", Passed: evidence.Passed(evidence.PhaseRed, "", 0, verdict),
		At: at,
	}); err != nil {
		f.t.Fatalf("RecordEvidenceVerdict: %v", err)
	}
}

// TestTaskVersionEvidenceChangeRunsRedAtTheTaskBase: a carried rerun that changes the evidence stores version 2
// checking and moves the build to red, keeping its attempt. The next pass starts the red run at the task's base under
// the derived id of its attempt and rerun.
func TestTaskVersionEvidenceChangeRunsRedAtTheTaskBase(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.carryRerun(redCheckRerun(t))

	task := f.task()
	if task.Build.Stage != StageRed || task.Build.Attempt != 1 || task.Build.Rerun != 1 {
		t.Fatalf("build stage %q attempt %d rerun %d, want red at attempt 1 and rerun 1",
			task.Build.Stage, task.Build.Attempt, task.Build.Rerun)
	}
	if version, ok := task.Checking(); !ok || version.Version != 2 {
		t.Fatalf("Checking() = %+v, %t, want version 2 checking", version, ok)
	}

	f.pass()
	want := passRun{
		id: RunID(passEpic, 1, StageRed, 1, 1), epic: passEpic, seq: 1, revision: task.Build.Base, phase: StageRed,
	}
	if got := f.lastRun(); got != want {
		t.Errorf("last evidence run = %+v, want %+v", got, want)
	}
}

// TestTaskVersionEvidenceChangeAppliedWhenRedDoesNotHold: a red run whose conclusion does not hold at the base applies
// the checking version. The task reads the version's evidence at version 2, and the build is at implement with that run
// as its red run.
func TestTaskVersionEvidenceChangeAppliedWhenRedDoesNotHold(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.carryRerun(redCheckRerun(t))
	red := RunID(passEpic, 1, StageRed, 1, 1)
	addCheckingRun(f, red, evidence.VerdictFails)

	f.pass()
	task := f.task()
	if task.Version != 2 || task.Evidence.Conclusion != redCheckItem().Conclusion {
		t.Errorf("task at version %d with evidence %q, want version 2 with the version's evidence", task.Version,
			task.Evidence.Conclusion)
	}
	if task.Build.Stage != StageImplement || task.Build.RedRun != red {
		t.Errorf("build stage %q red run %q, want implement with red run %s", task.Build.Stage, task.Build.RedRun, red)
	}
	if len(task.Versions) != 1 || task.Versions[0].State != store.VersionApplied || task.Versions[0].RedRun != red {
		t.Errorf("Versions = %+v, want version 2 applied with red run %s", task.Versions, red)
	}
	if task.Status != taskOpen {
		t.Errorf("task status %q, want open", task.Status)
	}
}

// TestTaskVersionEvidenceChangeAppliesWhenRedHolds: a red run whose conclusion holds at the base does not refuse the
// checking version. Red is not a gate, so the version applies, and the red outcome is a note on the task's build: the
// note lands first, and the version settles on the next pass. The task stays open.
func TestTaskVersionEvidenceChangeAppliesWhenRedHolds(t *testing.T) {
	t.Parallel()
	f := newRequestFixture(t)
	f.carryRerun(redCheckRerun(t))
	red := RunID(passEpic, 1, StageRed, 1, 1)
	addCheckingRun(f, red, evidence.VerdictHolds)

	f.pass()
	f.pass()
	task := f.task()
	if want := redCheckItem().Conclusion; task.Version != 2 || task.Evidence.Conclusion != want {
		t.Errorf("task at version %d with evidence %q, want version 2 with %q", task.Version,
			task.Evidence.Conclusion, want)
	}
	if len(task.Versions) != 1 || task.Versions[0].State != store.VersionApplied {
		t.Errorf("Versions = %+v, want version 2 applied", task.Versions)
	}
	if task.Build.Stage != StageImplement || task.Build.RedRun != red {
		t.Errorf("build stage %q red run %q, want implement with the red run %s", task.Build.Stage,
			task.Build.RedRun, red)
	}
	if len(task.Notes) != 1 || !strings.HasPrefix(task.Notes[0], taskLabel(task.Seq)+" red: the conclusion holds") {
		t.Errorf("notes = %q, want the red run's note", task.Notes)
	}
	if task.Status != taskOpen {
		t.Errorf("task status %q, want open", task.Status)
	}
}

// TestTaskVersionGrantMayNotChangeTheEvidence: the orchestrator check refuses a grant whose version names evidence,
// with the one line that says a grant may not change it.
func TestTaskVersionGrantMayNotChangeTheEvidence(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 1, Files: []string{"a.go"}, Status: taskOpen}
	check := OrchestratorCheck(task, []Peer{{Task: task}})
	ret := orchReturn(t, map[string]any{
		"decision": "grant", "version": versionWith(map[string]any{
			"changed": []string{"files", "evidence"}, "files": []string{"b.go"}, "evidence": redCheckItem(),
		}),
	})
	want := []string{"version.evidence: not changed on a grant"}
	if got := check(json.RawMessage(ret)); !slices.Equal(got, want) {
		t.Errorf("check = %q, want %q", got, want)
	}
}
