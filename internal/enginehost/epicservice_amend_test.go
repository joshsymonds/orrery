package enginehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/evidence"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// amendTestItem returns the item the amend tests ask R1 to hold: epicTestItem's, with its test moved to another package.
func amendTestItem() *v1.EvidenceItem {
	item := epicTestItem()
	item.Capture = []string{"scripts/check", "./cmd/synthetic/"}
	item.Tests = []string{"cmd/synthetic/check_test.go"}
	return item
}

// TestAmendShownEvidenceRPCAppliesAndShowsItOnTheEpic amends how R1 is shown as the owner, keeping its conclusion and
// kind. The reply carries the stored amendment, applied in the call with no red run, with R1's old item and the new one,
// and the epic read back, which lists the amendment and holds the new item; GetEpic shows the same.
func TestAmendShownEvidenceRPCAppliesAndShowsItOnTheEpic(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	created := createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	amended, err := client.AmendEvidence(ctx, connect.NewRequest(&v1.AmendEvidenceRequest{
		Id: epicServiceID, Requirement: "R1", Item: amendTestItem(), Reason: "the test moved",
	}))
	if err != nil {
		t.Fatalf("AmendEvidence: %v", err)
	}
	got := amended.Msg.GetAmendment()
	want := &v1.EpicAmendment{
		Seq: 1, Requirement: "R1", Amender: store.AmenderOwner, PriorItem: epicTestItem(), Item: amendTestItem(),
		Reason: "the test moved", Base: created.GetContract().GetBase(), State: store.AmendmentApplied,
		RedRun: store.AmendmentWorkID(epicServiceID, 1, store.AmendmentRed, 0), Created: got.GetCreated(),
		Settled: got.GetCreated(),
	}
	if !proto.Equal(got, want) {
		t.Errorf("amendment = %v, want %v", got, want)
	}
	if got.GetCreated() == 0 {
		t.Errorf("amendment created = 0, want the time it was stored")
	}
	epic := getEpicOK(t, client)
	if !proto.Equal(epic, amended.Msg.GetEpic()) {
		t.Errorf("GetEpic = %v, want the epic of the reply %v", epic, amended.Msg.GetEpic())
	}
	if len(epic.GetAmendments()) != 1 || !proto.Equal(epic.GetAmendments()[0], want) {
		t.Errorf("epic amendments = %v, want the one amendment", epic.GetAmendments())
	}
	if item := epic.GetContract().GetRequirements()[0].GetItem(); !proto.Equal(item, amendTestItem()) {
		t.Errorf("R1 item after the amendment = %v, want the new item", item)
	}
}

// TestAmendEvidenceRPCRefusals checks each refusal of AmendEvidence and that none stores an amendment.
func TestAmendEvidenceRPCRefusals(t *testing.T) {
	t.Parallel()
	noKind := amendTestItem()
	noKind.Kind = ""
	noKind.Judge = ""
	cases := []struct {
		name    string
		req     *v1.AmendEvidenceRequest
		code    connect.Code
		message string
	}{
		{
			"empty id", &v1.AmendEvidenceRequest{Requirement: "R1", Item: amendTestItem()},
			connect.CodeInvalidArgument, "id: empty",
		},
		{
			"empty requirement", &v1.AmendEvidenceRequest{Id: epicServiceID, Item: amendTestItem()},
			connect.CodeInvalidArgument, "requirement: empty",
		},
		{
			"no item", &v1.AmendEvidenceRequest{Id: epicServiceID, Requirement: "R1"},
			connect.CodeInvalidArgument, "item: missing",
		},
		{
			"an item evidence.Check refuses",
			&v1.AmendEvidenceRequest{Id: epicServiceID, Requirement: "R1", Item: noKind},
			connect.CodeInvalidArgument,
			"item.judge: empty or blank; item.kind: none, name test, capture or supplied",
		},
		{
			"an unknown epic", &v1.AmendEvidenceRequest{Id: "no-such-epic", Requirement: "R1", Item: amendTestItem()},
			connect.CodeNotFound, "id: no epic no-such-epic",
		},
		{
			"a Requirement the epic does not hold",
			&v1.AmendEvidenceRequest{Id: epicServiceID, Requirement: "R9", Item: amendTestItem()},
			connect.CodeFailedPrecondition, `epic ` + epicServiceID + ` has no Requirement "R9"`,
		},
		{
			"the item R1 holds",
			&v1.AmendEvidenceRequest{Id: epicServiceID, Requirement: "R1", Item: epicTestItem()},
			connect.CodeFailedPrecondition, "R1 already holds this item",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, _ := openPlannerStore(t)
			client := newEpicsClient(t, startEpicServer(t, st))
			createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
			_, err := client.AmendEvidence(t.Context(), connect.NewRequest(tc.req))
			wantConnectError(t, err, tc.code, tc.message)
			if amendments := getEpicOK(t, client).GetAmendments(); len(amendments) != 0 {
				t.Errorf("amendments after the refusal = %v, want none", amendments)
			}
		})
	}
}

// amendSteps is the step commands of the amend tests: Status answers the rows it holds by id, and ErrNoStep for any
// other id.
type amendSteps struct {
	StepCommands

	rows map[string]store.StepRow
}

// Status returns the row with id, or an error matching store.ErrNoStep.
func (a amendSteps) Status(_ context.Context, id string) (store.StepRow, error) {
	row, ok := a.rows[id]
	if !ok {
		return store.StepRow{}, fmt.Errorf("step %s: %w", id, store.ErrNoStep)
	}
	return row, nil
}

// TestAmendEvidenceRPCIsTheOwnersAlone amends R1 with a new conclusion from the step of the epic's running turn, from an
// implementer step labeled with the epic, from a step of another epic and from no session. The two steps of the epic are
// refused and store nothing, since the planner amends through its action; the other two are the owner's and are stored.
func TestAmendEvidenceRPCIsTheOwnersAlone(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openPlannerStore(t)
	steps := amendSteps{rows: map[string]store.StepRow{
		"step-1":     {ID: "step-1", Epic: epicServiceID},
		"step-impl":  {ID: "step-impl", Epic: epicServiceID, Task: "T1"},
		"step-other": {ID: "step-other", Epic: "synthetic-repo-0a1b2c3/epic-other", Task: "T1"},
	}}
	client := newEpicsClient(t, startControlServer(t, st, steps))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = st.SetTurnStep(ctx, epicServiceID, turn.Seq, "step-1"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	item := amendTestItem()
	item.Conclusion = "Another conclusion."
	amend := func(session string) (*connect.Response[v1.AmendEvidenceResponse], error) {
		req := connect.NewRequest(&v1.AmendEvidenceRequest{Id: epicServiceID, Requirement: "R1", Item: item})
		if session != "" {
			req.Header().Set(protocol.HeaderSession, session)
		}
		return client.AmendEvidence(ctx, req)
	}

	for _, session := range []string{"step-1", "step-impl"} {
		_, err = amend(session)
		wantConnectError(t, err, connect.CodeFailedPrecondition, "step "+session+" of epic "+epicServiceID+
			" may not amend its evidence; the planner amends through its amend_evidence action, and the owner "+
			"through this call")
	}
	if amendments := getEpicOK(t, client).GetAmendments(); len(amendments) != 0 {
		t.Fatalf("amendments after the steps' calls = %v, want none", amendments)
	}

	owner, err := amend("step-other")
	if err != nil {
		t.Fatalf("AmendEvidence from a step of another epic: %v", err)
	}
	if got := owner.Msg.GetAmendment(); got.GetAmender() != store.AmenderOwner ||
		got.GetItem().GetConclusion() != "Another conclusion." {
		t.Errorf("amendment = %v, want the owner's with the new conclusion", got)
	}
	if _, err = amend(""); err == nil {
		t.Errorf("a second owner amendment while the first checks = nil, want refused")
	}
}

func TestAmendEvidenceRPCIsRecordedAsEpicAmend(t *testing.T) {
	t.Parallel()
	rec := &fakeEpicRecorder{}
	_, client := epicCallFixture(t, rec)
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	req := connect.NewRequest(&v1.AmendEvidenceRequest{Id: epicServiceID, Requirement: "R1", Item: amendTestItem()})
	req.Header().Set(protocol.HeaderSession, epicCallSession)
	if _, err := client.AmendEvidence(t.Context(), req); err != nil {
		t.Fatalf("AmendEvidence: %v", err)
	}
	wantEpicCall(t, rec.recorded(), epicCall{
		epic: epicServiceID, caller: store.EpicCaller{Session: epicCallSession}, call: "epic_amend", at: epicCallAt(),
	})
}

// TestEvidenceRunAmendmentRunsTheAmendedItemOnceUnderID runs the item an amendment asks R1 to hold, at a revision, under
// a caller id. The run keeps R1 as its item and the amended item as its spec, and a second run under the id returns the
// stored run and starts no judge.
func TestEvidenceRunAmendmentRunsTheAmendedItemOnceUnderID(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)
	spec := evidenceRunItem()
	spec.Conclusion = "the amended build passes"

	started, err := ev.RunAmendment(t.Context(), evidenceTaskRunID, evidenceRunEpic, "R1", spec, head,
		evidence.PhaseRed)
	if err != nil {
		t.Fatalf("RunAmendment: %v", err)
	}
	evidenceRunWantStarted(t, started, "R1", head, evidence.PhaseRed, evidence.SourceCaptured)
	if !reflect.DeepEqual(started.Spec, spec) {
		t.Fatalf("started run spec = %+v, want the amended item %+v", started.Spec, spec)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, evidenceTaskRunID)
	again, err := ev.RunAmendment(t.Context(), evidenceTaskRunID, evidenceRunEpic, "R1", spec, head,
		evidence.PhaseRed)
	if err != nil {
		t.Fatalf("second RunAmendment: %v", err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("second RunAmendment = %+v, want the stored run %+v", again, got)
	}
	if n := steps.startCount(); n != 1 {
		t.Fatalf("%d judge steps started, want 1", n)
	}
}

// TestEvidenceRunAmendmentRefusesABadRequest asks for an amendment run of a Requirement the epic does not hold and of an
// empty item. Each is refused with an error matching ErrEvidenceRefused and stores no run.
func TestEvidenceRunAmendmentRefusesABadRequest(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	ev := evidenceRunNew(t, st, &evidenceRunSteps{})
	cases := []struct {
		name, requirement string
		spec              evidence.Item
	}{
		{"unknown requirement", "R9", evidenceRunItem()},
		{"empty item", "R1", evidence.Item{}},
	}
	for _, tc := range cases {
		_, err := ev.RunAmendment(t.Context(), evidenceTaskRunID, evidenceRunEpic, tc.requirement, tc.spec, head,
			evidence.PhaseRed)
		if !errors.Is(err, ErrEvidenceRefused) {
			t.Errorf("%s: RunAmendment = %v, want an error matching ErrEvidenceRefused", tc.name, err)
		}
	}
	evidenceRunWantNoRuns(t, st)
}

// TestEvidenceResumeDrivesAnAmendmentRunByItsOwnItem resumes an amendment's red run that a restart left capturing before
// its capture launched. The run is captured by the item it keeps, the amendment's, not the item its Requirement holds,
// and is judged once.
func TestEvidenceResumeDrivesAnAmendmentRunByItsOwnItem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	held, amended := filepath.Join(dir, "held"), filepath.Join(dir, "amended")
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{
		"capture.sh": "echo run >> " + held + "\nexit 1\n",
		"amended.sh": "echo run >> " + amended + "\nexit 1\n",
	})
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	spec := evidenceRunItem()
	spec.Capture, spec.Conclusion = []string{"/bin/sh", "amended.sh"}, "the amended build passes"
	evidenceReadoptAdd(t, st, "amendment-red", head, evidence.PhaseRed, spec)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)

	if err := ev.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	got := evidenceRunSettled(t, engineOf(ev), st, "amendment-red")
	if !reflect.DeepEqual(got.Spec, spec) || got.Judges != 1 {
		t.Fatalf("run = spec %+v judges %d, want the amended item judged once", got.Spec, got.Judges)
	}
	if n := evidenceReadoptLines(t, amended); n != 1 {
		t.Errorf("amended capture ran %d times, want once", n)
	}
	if n := evidenceReadoptLines(t, held); n != 0 {
		t.Errorf("the Requirement's own capture ran %d times, want never", n)
	}
}
