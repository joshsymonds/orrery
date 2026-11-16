package store_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// checkEpic and checkOther are the epics the node check tests write to. checkOther is a second epic whose checks
// checkEpic's reads must not return.
const (
	checkEpic  = "orrery-5e6f7a8/epic-checks"
	checkOther = "orrery-5e6f7a8/epic-checks-other"
)

// checkTime returns the second offset seconds after the base the node check tests stamp their writes with.
func checkTime(offset int64) time.Time {
	return time.Unix(1700000400+offset, 0)
}

// checkItem is the evidence item of Requirement R1 of the check epics, which the red run of R1 keeps as its spec.
func checkItem() evidence.Item {
	return evidence.Item{
		Capture: []string{"make", "check"}, Limit: 60, Artifacts: []string{"out/log.txt"},
		Conclusion: "The check runs", Judge: "Read the output",
	}
}

// openCheckStore opens a migrated store with checkEpic and checkOther created, each with Requirement R1 holding
// checkItem.
func openCheckStore(t *testing.T) *store.Store {
	t.Helper()
	s, _ := openNodeStore(t)
	for _, id := range []string{checkEpic, checkOther} {
		row := loopEpicRow(id)
		row.Requirements = []store.EpicRequirement{{
			ID: "R1", Text: "The check runs", Evidence: "make check", Kind: "check", Item: checkItem(),
		}}
		if err := s.CreateEpic(context.Background(), row); err != nil {
			t.Fatalf("CreateEpic %s: %v", id, err)
		}
	}
	return s
}

// runningCheck returns a check of checkEpic at path gate-tests under revision rev-1 that runs make check with a 60
// second limit and needs one slot of pool build. It has no grant, end or outcome.
func runningCheck(id string) store.NodeCheck {
	return store.NodeCheck{
		ID: id, Epic: checkEpic, Path: "gate-tests", Revision: "rev-1",
		Argv: []string{"/usr/bin/make", "check"}, Limit: 60,
		Needs: []store.EpicGateNeed{{Pool: "build", Count: 1}},
	}
}

// runningWant is what NodeCheck returns for runningCheck(id) added at checkTime(0).
func runningWant(id string) store.NodeCheck {
	check := runningCheck(id)
	check.State = "running"
	check.Created = checkTime(0)
	return check
}

// requireNodeCheck fails unless the stored check with id is want.
func requireNodeCheck(t *testing.T, s *store.Store, id string, want store.NodeCheck) {
	t.Helper()
	got, err := s.NodeCheck(context.Background(), id)
	if err != nil {
		t.Fatalf("NodeCheck %s: %v", id, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NodeCheck %s = %+v, want %+v", id, got, want)
	}
}

// TestAddNodeCheckStoresRunningCheckOnce checks that AddNodeCheck stores a check running at the second it is added,
// and that a second add under the same id, carrying other fields, changes nothing.
func TestAddNodeCheckStoresRunningCheckOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openCheckStore(t)
	const id = "id-check-once"
	if err := s.AddNodeCheck(ctx, runningCheck(id), checkTime(0)); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	want := runningWant(id)
	requireNodeCheck(t, s, id, want)

	second := store.NodeCheck{
		ID: id, Epic: checkEpic, Path: "gate-other", Revision: "rev-2", Argv: []string{"/bin/false"}, Limit: 5,
		NotRun: "a second add that must not be stored",
	}
	if err := s.AddNodeCheck(ctx, second, checkTime(1)); err != nil {
		t.Fatalf("second AddNodeCheck: %v", err)
	}
	requireNodeCheck(t, s, id, want)
}

// TestAddNodeCheckWithNotRunStoresItEnded checks that a check added with NotRun set is stored ended at the second it
// is added, with that reason and no exit or output.
func TestAddNodeCheckWithNotRunStoresItEnded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openCheckStore(t)
	const id = "id-check-not-run"
	check := runningCheck(id)
	check.NotRun = "the gate names no workflow check"
	if err := s.AddNodeCheck(ctx, check, checkTime(0)); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	want := runningWant(id)
	want.State = "ended"
	want.NotRun = check.NotRun
	want.Ended = checkTime(0)
	requireNodeCheck(t, s, id, want)
}

// TestSetNodeCheckGrantRecordsGrantAndHolderOfRunningCheck checks that SetNodeCheckGrant records the grant and holder
// of a running check, and that a grant set once the check has ended changes nothing.
func TestSetNodeCheckGrantRecordsGrantAndHolderOfRunningCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openCheckStore(t)
	const id = "id-check-grant"
	if err := s.AddNodeCheck(ctx, runningCheck(id), checkTime(0)); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	if err := s.SetNodeCheckGrant(ctx, id, "grant-7", "holder-a"); err != nil {
		t.Fatalf("SetNodeCheckGrant: %v", err)
	}
	want := runningWant(id)
	want.Grant, want.Holder = "grant-7", "holder-a"
	requireNodeCheck(t, s, id, want)

	if err := s.EndNodeCheck(ctx, id, store.NodeCheckEnd{Exit: 0}, checkTime(2)); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
	if err := s.SetNodeCheckGrant(ctx, id, "grant-9", "holder-b"); err != nil {
		t.Fatalf("SetNodeCheckGrant after end: %v", err)
	}
	want.State = "ended"
	want.Ended = checkTime(2)
	requireNodeCheck(t, s, id, want)
}

// TestEndNodeCheckEndsRunningCheckOnce checks that EndNodeCheck ends a running check with its exit, timeout and
// output, and that a second end under the same id changes nothing.
func TestEndNodeCheckEndsRunningCheckOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openCheckStore(t)
	const id = "id-check-end"
	if err := s.AddNodeCheck(ctx, runningCheck(id), checkTime(0)); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	if err := s.EndNodeCheck(ctx, id, store.NodeCheckEnd{
		Exit: 2, TimedOut: true, Stdout: "out\n", Stderr: "err\n",
	}, checkTime(100)); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
	want := runningWant(id)
	want.State = "ended"
	want.Exit = 2
	want.TimedOut = true
	want.Stdout = "out\n"
	want.Stderr = "err\n"
	want.Ended = checkTime(100)
	requireNodeCheck(t, s, id, want)

	if err := s.EndNodeCheck(ctx, id, store.NodeCheckEnd{
		Exit: 0, NotRun: "a second end", Stdout: "later", Stderr: "later",
	}, checkTime(200)); err != nil {
		t.Fatalf("second EndNodeCheck: %v", err)
	}
	requireNodeCheck(t, s, id, want)
}

// TestEndNodeCheckKeepsNewestOutputBytesAtRuneStart checks that EndNodeCheck keeps the newest 4096 bytes of stdout and
// of stderr. The stdout is 1400 three-byte runes, 4200 bytes, so the cut 4096 bytes from its end falls inside a rune
// and the kept tail starts at the next rune start, 4095 bytes long. The stderr is 5000 bytes and ends in END, so its
// tail is 4093 bytes of a followed by END.
func TestEndNodeCheckKeepsNewestOutputBytesAtRuneStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openCheckStore(t)
	const id = "id-check-tails"
	if err := s.AddNodeCheck(ctx, runningCheck(id), checkTime(0)); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	if err := s.EndNodeCheck(ctx, id, store.NodeCheckEnd{
		Exit: 1, Stdout: strings.Repeat("€", 1400), Stderr: strings.Repeat("a", 5000) + "END",
	}, checkTime(100)); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
	want := runningWant(id)
	want.State = "ended"
	want.Exit = 1
	want.Stdout = strings.Repeat("€", 1365)
	want.Stderr = strings.Repeat("a", 4093) + "END"
	want.Ended = checkTime(100)
	requireNodeCheck(t, s, id, want)
}

// TestNodeCheckOfUnknownIDMatchesErrNoNodeCheck checks that NodeCheck returns an error matching ErrNoNodeCheck for an
// id no check has.
func TestNodeCheckOfUnknownIDMatchesErrNoNodeCheck(t *testing.T) {
	t.Parallel()
	s := openCheckStore(t)
	_, err := s.NodeCheck(context.Background(), "id-check-missing")
	if !errors.Is(err, store.ErrNoNodeCheck) {
		t.Fatalf("NodeCheck of an unknown id: err = %v, want one matching ErrNoNodeCheck", err)
	}
}

// TestNodeChecksAndOpenNodeChecksListInAddOrder checks that NodeChecks lists one epic's checks, ended or running, in
// the order they were added, and that OpenNodeChecks lists the running checks of every epic and no ended one.
func TestNodeChecksAndOpenNodeChecksListInAddOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openCheckStore(t)

	first := runningCheck("id-check-first")
	second := runningCheck("id-check-second")
	notRun := runningCheck("id-check-not-run")
	notRun.NotRun = "the gate names no workflow check"
	other := runningCheck("id-check-other")
	other.Epic = checkOther
	for _, add := range []struct {
		check store.NodeCheck
		at    time.Time
	}{
		{first, checkTime(0)}, {second, checkTime(1)}, {notRun, checkTime(2)}, {other, checkTime(3)},
	} {
		if err := s.AddNodeCheck(ctx, add.check, add.at); err != nil {
			t.Fatalf("AddNodeCheck %s: %v", add.check.ID, err)
		}
	}
	if err := s.EndNodeCheck(ctx, second.ID, store.NodeCheckEnd{Exit: 0}, checkTime(4)); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}

	firstWant := runningWant(first.ID)
	secondWant := runningWant(second.ID)
	secondWant.Created = checkTime(1)
	secondWant.State = "ended"
	secondWant.Ended = checkTime(4)
	notRunWant := runningWant(notRun.ID)
	notRunWant.Created = checkTime(2)
	notRunWant.State = "ended"
	notRunWant.NotRun = notRun.NotRun
	notRunWant.Ended = checkTime(2)
	otherWant := runningWant(other.ID)
	otherWant.Epic = checkOther
	otherWant.Created = checkTime(3)

	checks, err := s.NodeChecks(ctx, checkEpic)
	if err != nil {
		t.Fatalf("NodeChecks: %v", err)
	}
	if want := []store.NodeCheck{firstWant, secondWant, notRunWant}; !reflect.DeepEqual(checks, want) {
		t.Errorf("NodeChecks = %+v, want %+v", checks, want)
	}

	open, err := s.OpenNodeChecks(ctx)
	if err != nil {
		t.Fatalf("OpenNodeChecks: %v", err)
	}
	if want := []store.NodeCheck{firstWant, otherWant}; !reflect.DeepEqual(open, want) {
		t.Errorf("OpenNodeChecks = %+v, want %+v", open, want)
	}
}

// TestEpicFlowReadsChecksEvidenceAndAgentSteps checks that EpicFlow returns the checks and evidence runs of its epic,
// and for each agent node run the step under NodeWorkID of that run when one exists. A gate run's step is not read,
// and an agent run with no step has none.
func TestEpicFlowReadsChecksEvidenceAndAgentSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openCheckStore(t)
	runs := []store.NodeRun{
		{Path: "agent-review", ID: "id-agent-review", Node: "review", Kind: "agent", Attempt: 2},
		{Path: "agent-idle", ID: "id-agent-idle", Node: "idle", Kind: "agent", Attempt: 1},
		{Path: "gate-tests", ID: "id-gate-tests", Node: "tests", Kind: "gate", Attempt: 1},
	}
	if err := s.StartNodes(ctx, checkEpic, runs, nil, checkTime(0)); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	reviewStep := store.NodeWorkID("id-agent-review", 2, "step")
	gateStep := store.NodeWorkID("id-gate-tests", 1, "step")
	for _, id := range []string{reviewStep, gateStep} {
		if err := s.ApplyEngine(ctx, []store.EngineOp{checkStepAdd(id)}, checkTime(0)); err != nil {
			t.Fatalf("ApplyEngine adding step %s: %v", id, err)
		}
	}

	if err := s.AddNodeCheck(ctx, runningCheck("id-check-tests"), checkTime(0)); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	elsewhere := runningCheck("id-check-elsewhere")
	elsewhere.Epic = checkOther
	if err := s.AddNodeCheck(ctx, elsewhere, checkTime(0)); err != nil {
		t.Fatalf("AddNodeCheck of the other epic: %v", err)
	}
	if err := s.AddEvidenceRun(ctx, store.EvidenceRun{
		ID: "run-r1-red", Epic: checkEpic, Item: "R1", Revision: "rev-1", Phase: "red", Source: "captured",
		Spec: checkItem(), Created: checkTime(0),
	}); err != nil {
		t.Fatalf("AddEvidenceRun: %v", err)
	}

	flow, err := s.EpicFlow(ctx, checkEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	checks, err := s.NodeChecks(ctx, checkEpic)
	if err != nil {
		t.Fatalf("NodeChecks: %v", err)
	}
	if len(checks) != 1 || !reflect.DeepEqual(flow.Checks, checks) {
		t.Errorf("EpicFlow checks = %+v, want the one check of the epic %+v", flow.Checks, checks)
	}
	evidenceRuns, err := s.EvidenceRuns(ctx, checkEpic)
	if err != nil {
		t.Fatalf("EvidenceRuns: %v", err)
	}
	if len(evidenceRuns) != 1 || !reflect.DeepEqual(flow.Evidence, evidenceRuns) {
		t.Errorf("EpicFlow evidence = %+v, want the runs EvidenceRuns returns %+v", flow.Evidence, evidenceRuns)
	}
	review, err := s.Step(ctx, reviewStep)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if want := []store.StepRow{review}; !reflect.DeepEqual(flow.Steps, want) {
		t.Errorf("EpicFlow steps = %+v, want only the agent run's step %+v", flow.Steps, want)
	}
}

// checkStepAdd returns the add of a step with id, the step an agent node run records under NodeWorkID.
func checkStepAdd(id string) store.StepAdd {
	return store.StepAdd{
		ID: id, Agent: "reviewer", Prompt: "review it", Schema: `{"type":"object"}`, Workspace: "/ws/" + id,
		Revision: "rev-1", Worktree: "/wt/" + id, DoneValue: "DONE", ReadOnly: true,
		GateLimit: 90 * time.Second, ModelLimit: 60 * time.Minute,
	}
}
