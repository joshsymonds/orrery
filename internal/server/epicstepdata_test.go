package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// stepDataEvidence is the EvidenceClient of the role test. ListEvidence answers its runs. GetEvidenceArtifact is unused.
type stepDataEvidence struct {
	runs []*enginev1.EvidenceRun
}

func (f *stepDataEvidence) ListEvidence(
	context.Context,
	*connect.Request[enginev1.ListEvidenceRequest],
) (*connect.Response[enginev1.ListEvidenceResponse], error) {
	return connect.NewResponse(&enginev1.ListEvidenceResponse{Runs: f.runs}), nil
}

func (f *stepDataEvidence) GetEvidenceArtifact(
	context.Context,
	*connect.Request[enginev1.GetEvidenceArtifactRequest],
) (*connect.Response[enginev1.GetEvidenceArtifactResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

// stepDataServe serves newEpicsHandler over root, steps, store and evidence with the fixed clock, and returns a
// generated Epics client.
func stepDataServe(
	t *testing.T,
	root string,
	steps StepClient,
	store EpicStoreClient,
	evidence EvidenceClient,
) orreryv1connect.EpicsClient {
	t.Helper()
	path, handler := newEpicsHandler(root, steps, store, evidence, 0, epicsNow, newRetryClock())
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewEpicsClient(srv.Client(), srv.URL)
}

// stepDataRoles maps each step's id to its role.
func stepDataRoles(steps []*orreryv1.EpicStep) map[string]string {
	roles := make(map[string]string, len(steps))
	for _, step := range steps {
		roles[step.GetId()] = step.GetRole()
	}
	return roles
}

func TestEpicStepMessageCopiesStageGateLimitAndReadOnly(t *testing.T) {
	t.Parallel()
	step := &enginev1.Step{
		Id:               "s1",
		ReadOnly:         true,
		Stage:            "gate",
		StageStarted:     1_700_000_100,
		GateName:         "synthetic-gate",
		GateLimitSeconds: 600,
		Limited:          true,
		LimitKind:        "synthetic-kind",
		LimitResets:      1_700_003_600,
	}
	got := epicStepMessage(step)
	if got.GetStage() != "gate" || got.GetStageStarted() != 1_700_000_100 ||
		got.GetGateName() != "synthetic-gate" || got.GetGateLimit() != 600 ||
		!got.GetLimited() || got.GetLimitKind() != "synthetic-kind" || got.GetLimitResets() != 1_700_003_600 ||
		!got.GetReadOnly() {
		t.Errorf("step = stage %q stage_started %d gate_name %q gate_limit %d limited %v limit_kind %q "+
			"limit_resets %d read_only %v, want gate, 1700000100, synthetic-gate, 600, true, synthetic-kind, "+
			"1700003600, true",
			got.GetStage(), got.GetStageStarted(), got.GetGateName(), got.GetGateLimit(), got.GetLimited(),
			got.GetLimitKind(), got.GetLimitResets(), got.GetReadOnly())
	}
}

func TestEpicStepMessageRedactsNewFreeText(t *testing.T) {
	t.Parallel()
	const credential = "sk-EXAMPLEKEY0123456789abcd"
	step := &enginev1.Step{
		Id:     "s1",
		Result: `{"status":"done","summary":"synthetic summary ` + credential + `"}`,
		Checks: []*enginev1.StepCheck{
			{
				Round: 1, Name: "gate", Ok: true,
				Detail: "synthetic detail " + credential,
				Stdout: "synthetic passed stdout " + credential,
				Stderr: "synthetic passed stderr " + credential,
			},
		},
		Verdicts: []*enginev1.StepVerdict{
			{Round: 1, Kind: "judge", Verdict: "unmet", Detail: "synthetic reason " + credential},
		},
	}
	got := epicStepMessage(step)
	if got.GetSummary() != "synthetic summary [redacted]" {
		t.Errorf("summary = %q, want %q", got.GetSummary(), "synthetic summary [redacted]")
	}
	check := got.GetChecks()[0]
	if check.GetDetail() != "synthetic detail [redacted]" {
		t.Errorf("check detail = %q, want %q", check.GetDetail(), "synthetic detail [redacted]")
	}
	if check.GetTailStdout() != "synthetic passed stdout [redacted]" ||
		check.GetTailStderr() != "synthetic passed stderr [redacted]" {
		t.Errorf("passing gate tails = stdout %q stderr %q, want the placeholder in each",
			check.GetTailStdout(), check.GetTailStderr())
	}
	if check.GetStdout() != "" || check.GetStderr() != "" {
		t.Errorf("passing gate stdout %q stderr %q, want none: stdout and stderr are for a failed gate",
			check.GetStdout(), check.GetStderr())
	}
	verdicts := got.GetVerdicts()
	if len(verdicts) != 1 || verdicts[0].GetReason() != "synthetic reason [redacted]" {
		t.Errorf("verdicts = %v, want one judge verdict with reason %q", verdicts, "synthetic reason [redacted]")
	}
}

func TestEpicStepMessageSummaryIsTheResultsStringSummary(t *testing.T) {
	t.Parallel()
	withSummary := epicStepMessage(&enginev1.Step{Id: "s1", Result: `{"summary":"synthetic summary"}`})
	if withSummary.GetSummary() != "synthetic summary" {
		t.Errorf("summary = %q, want %q", withSummary.GetSummary(), "synthetic summary")
	}
	nonString := epicStepMessage(&enginev1.Step{Id: "s2", Result: `{"summary":3}`})
	if nonString.GetSummary() != "" {
		t.Errorf("summary of a non-string summary = %q, want empty", nonString.GetSummary())
	}
}

func TestEpicStepMessageVerdictsAreJudgeOnly(t *testing.T) {
	t.Parallel()
	step := &enginev1.Step{
		Id: "s1",
		Verdicts: []*enginev1.StepVerdict{
			{Round: 1, Kind: "monitor", Verdict: "met", Detail: "synthetic monitor detail"},
			{Round: 1, Kind: "judge", Verdict: "met", Detail: "synthetic judge reason one"},
			{Round: 2, Kind: "judge", Verdict: "", Detail: "synthetic judge reason two"},
		},
	}
	want := []struct {
		round   uint32
		verdict string
		reason  string
	}{
		{1, "met", "synthetic judge reason one"},
		{2, "", "synthetic judge reason two"},
	}
	verdicts := epicStepMessage(step).GetVerdicts()
	if len(verdicts) != len(want) {
		t.Fatalf("verdicts = %d, want %d judge verdicts", len(verdicts), len(want))
	}
	for i, w := range want {
		got := verdicts[i]
		if got.GetRound() != w.round || got.GetVerdict() != w.verdict || got.GetReason() != w.reason {
			t.Errorf("verdict %d = round %d verdict %q reason %q, want round %d verdict %q reason %q",
				i, got.GetRound(), got.GetVerdict(), got.GetReason(), w.round, w.verdict, w.reason)
		}
	}
}

func TestEpicStepRolesOnGetAndList(t *testing.T) {
	root := t.TempDir()
	stored := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	stored.Turns = []*enginev1.EpicTurn{{Seq: 1, Step: "turn"}}
	stored.Tasks = []*enginev1.EpicTask{{Id: "T1", Reviews: []*enginev1.EpicTaskReview{{Step: "review"}}}}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	steps := &epicsFake{steps: []*enginev1.Step{
		{Id: "impl", Epic: "repoa/shared", Started: epicsAgo(5 * time.Hour)},
		{Id: "review", Epic: "repoa/shared", ReadOnly: true, Started: epicsAgo(4 * time.Hour)},
		{Id: "turn", Epic: "repoa/shared", ReadOnly: true, Started: epicsAgo(3 * time.Hour)},
		{Id: "judge", Epic: "repoa/shared", ReadOnly: true, Started: epicsAgo(2 * time.Hour)},
		{Id: "idle", Epic: "repoa/shared", ReadOnly: true, Started: epicsAgo(time.Hour)},
	}}
	evidence := &stepDataEvidence{runs: []*enginev1.EvidenceRun{{Id: "run-1", JudgeStep: "judge"}}}
	client := stepDataServe(t, root, steps, store, evidence)

	getWant := map[string]string{
		"impl": "implementer", "review": "reviewer", "turn": "planner", "judge": "evidence", "idle": "",
	}
	resp := epicsGet(t, client, "repoa/shared")
	if got := stepDataRoles(resp.GetSteps()); !reflect.DeepEqual(got, getWant) {
		t.Errorf("Get steps roles = %v, want %v", got, getWant)
	}
	if got := stepDataRoles(resp.GetEpic().GetRunningSteps()); !reflect.DeepEqual(got, getWant) {
		t.Errorf("Get running_steps roles = %v, want %v", got, getWant)
	}

	epics := epicsList(t, client)
	if len(epics) != 1 {
		t.Fatalf("List epics = %d, want 1", len(epics))
	}
	listWant := map[string]string{"impl": "implementer", "review": "", "turn": "", "judge": "", "idle": ""}
	if got := stepDataRoles(epics[0].GetRunningSteps()); !reflect.DeepEqual(got, listWant) {
		t.Errorf("List running_steps roles = %v, want %v", got, listWant)
	}
}
