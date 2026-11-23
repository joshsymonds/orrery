package enginehost

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/evidence"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// steerAmendWorld returns the store and a client of the epic epicServiceID, created by the conversation steerOnlySteerer.
// It is steerOnlyWorld with the store kept: the same engines, steerOnlySteerer, steerOnlyOther and steerOnlyStep, and the
// pane with empty state, so a test can record the evidence run of an amendment in the store.
func steerAmendWorld(t *testing.T) (*store.Store, enginev1connect.EpicsClient) {
	t.Helper()
	st := epicTestStore(t)
	engines := []store.EngineOp{
		store.EngineAdd{ID: steerOnlySteerer, Dir: "/work/steerer"},
		store.EngineAdd{ID: steerOnlyOther, Dir: "/work/other"},
		store.EngineAdd{ID: steerOnlyStep, Dir: "/wt/step", State: "idle", Kind: store.KindStep},
	}
	if err := st.ApplyEngine(t.Context(), engines, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	pane := store.Pane{ID: steerOnlyPaneID, Pane: steerOnlyPane}
	if err := st.SyncPane(t.Context(), "-S "+steerSocket, pane, fixedClock()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	client := newEpicsClient(t, serveEpicsWith(t, st, fixedSteering{}))
	if err := steererSetCreate(t, st, client, stepGitTestRepo(t), sessionHeader(steerOnlySteerer)); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	return st, client
}

// TestAmendSteererChangesConclusionAndKind checks that the conversation that steers the epic, through the Epics RPC,
// amends R1 to a new conclusion and a new kind. The amendment is stored checking with the conversation as its amender,
// R1 still holds its old item, and the red run of the new item at the epic's base fails, which applies the amendment:
// GetEpic then reads it applied, by the conversation, with R1 holding the new item.
func TestAmendSteererChangesConclusionAndKind(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, client := steerAmendWorld(t)
	base := getEpicOK(t, client).GetContract().GetBase()

	item := epicTestItem()
	item.Conclusion = "Synthetic amended conclusion."
	item.Kind = "capture"
	item.Tests = nil
	reply, err := client.AmendEvidence(ctx, steerOnlyRequest(sessionHeader(steerOnlySteerer), &v1.AmendEvidenceRequest{
		Id: epicServiceID, Requirement: "R1", Item: item, Reason: "Synthetic reason.",
	}))
	if err != nil {
		t.Fatalf("AmendEvidence by the steering conversation: %v", err)
	}
	amendment := reply.Msg.GetAmendment()
	if got := amendment.GetAmender(); got != store.AmenderConversation {
		t.Errorf("amender of the steering conversation's amendment = %q, want %q", got, store.AmenderConversation)
	}
	if got := amendment.GetState(); got != store.AmendmentChecking {
		t.Errorf("state of the amendment = %q, want %q", got, store.AmendmentChecking)
	}
	if got := amendment.GetBase(); got != base {
		t.Errorf("base of the amendment = %q, want the epic's base %q", got, base)
	}
	old := getEpicOK(t, client).GetContract().GetRequirements()[0].GetItem()
	if !proto.Equal(old, epicTestItem()) {
		t.Errorf("R1 item while checking = %v, want the old item %v", old, epicTestItem())
	}

	row, err := st.Epic(ctx, epicServiceID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(row.Amendments) != 1 {
		t.Fatalf("amendments stored = %d, want the one the steering conversation made", len(row.Amendments))
	}
	spec := row.Amendments[0].Item

	// The evidence runner records the red run of the new item at the base: it captures a non-zero exit, and the judge
	// finds the item fails there.
	at := fixedClock()
	red := amendment.GetRedRun()
	if err := st.AddEvidenceRun(ctx, store.EvidenceRun{
		ID: red, Epic: epicServiceID, Item: "R1", Revision: base, Phase: evidence.PhaseRed,
		Source: evidence.SourceCaptured, Spec: spec, Created: at,
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", red, err)
	}
	if err := st.RecordEvidenceCapture(ctx, red, store.EvidenceCapture{Exit: 1, LogHash: "log", At: at}); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", red, err)
	}
	if err := st.RecordEvidenceVerdict(ctx, red, store.EvidenceVerdict{
		Verdict: evidence.VerdictFails, Reason: "the judge read the log",
		Passed: evidence.Passed(evidence.PhaseRed, "", 1, evidence.VerdictFails), At: at,
	}); err != nil {
		t.Fatalf("RecordEvidenceVerdict %s: %v", red, err)
	}
	if err := st.SettleAmendment(ctx, epicServiceID, amendment.GetSeq(), at); err != nil {
		t.Fatalf("SettleAmendment: %v", err)
	}

	epic := getEpicOK(t, client)
	if len(epic.GetAmendments()) != 1 {
		t.Fatalf("amendments after the settle = %d, want the one amendment", len(epic.GetAmendments()))
	}
	settled := epic.GetAmendments()[0]
	if got := settled.GetState(); got != store.AmendmentApplied {
		t.Errorf("state of the amendment after its red fails = %q, want %q", got, store.AmendmentApplied)
	}
	if got := settled.GetAmender(); got != store.AmenderConversation {
		t.Errorf("amender of the settled amendment = %q, want %q", got, store.AmenderConversation)
	}
	if got := epic.GetContract().GetRequirements()[0].GetItem(); !proto.Equal(got, item) {
		t.Errorf("R1 item after the amendment = %v, want the new item %v", got, item)
	}
}
