package enginehost

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// decisionByNoteID is the id of the note the DecisionBy tests answer with.
const decisionByNoteID = "D-by"

// decisionByAt is the time the DecisionBy tests record their rows at.
func decisionByAt() time.Time { return time.Unix(1700003000, 0) }

// decisionByCase is one caller of the DecisionBy tests: the rows the store holds before the call, the caller headers the
// call sends, and the by and session its decision names.
type decisionByCase struct {
	name    string
	seed    func(t *testing.T, st *store.Store)
	headers func(h http.Header)
	by      string
	session string
}

// findDecisionByID returns the decision of decisions with id, and fails the test when none has it.
func findDecisionByID(t *testing.T, decisions []*v1.EpicDecision, id string) *v1.EpicDecision {
	t.Helper()
	for _, decision := range decisions {
		if decision.GetId() == id {
			return decision
		}
	}
	t.Fatalf("epic has no decision %s", id)
	return nil
}

// answerNoteAs sends a note with decisionByNoteID to epicServiceID through client, with the caller headers the case
// sends, and returns the decision the reply holds.
func answerNoteAs(t *testing.T, client enginev1connect.EpicsClient, tc decisionByCase) *v1.EpicDecision {
	t.Helper()
	req := connect.NewRequest(
		&v1.AnswerEpicRequest{EpicId: epicServiceID, Id: decisionByNoteID, Text: "Synthetic note."},
	)
	tc.headers(req.Header())
	reply, err := client.AnswerEpic(t.Context(), req)
	if err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	return reply.Msg.GetDecision()
}

// TestDecisionByRecordsTheMakerOfEachCaller checks that a served epic_answer stores the maker its caller resolves to, and
// that the reply and GetEpic both carry it: a headless conversation and a pane with a Claude named by its session, a pane
// with no Claude as the owner, a call with no headers as the owner, and a step as orreryd.
func TestDecisionByRecordsTheMakerOfEachCaller(t *testing.T) {
	t.Parallel()
	const tmuxSocket = "/tmp/tmux-1000/orrery"
	cases := []decisionByCase{
		{
			name: "headless session is a conversation",
			seed: func(t *testing.T, st *store.Store) {
				t.Helper()
				engine := store.EngineAdd{ID: "engine-1", Dir: "/work/engine"}
				if err := st.ApplyEngine(t.Context(), []store.EngineOp{engine}, decisionByAt()); err != nil {
					t.Fatalf("ApplyEngine: %v", err)
				}
			},
			headers: func(h http.Header) {
				h.Set(protocol.HeaderSession, "engine-1")
			},
			by:      store.MakerConversation,
			session: "engine-1",
		},
		{
			name: "pane with state is a conversation",
			seed: func(t *testing.T, st *store.Store) {
				t.Helper()
				pane := store.Pane{ID: "orrery-1", Pane: "%7", Fields: store.Fields{State: "working"}}
				if err := st.SyncPane(t.Context(), "-S "+tmuxSocket, pane, decisionByAt()); err != nil {
					t.Fatalf("SyncPane: %v", err)
				}
			},
			headers: func(h http.Header) {
				h.Set(protocol.HeaderTmux, tmuxSocket+",4242,0")
				h.Set(protocol.HeaderTmuxPane, "%7")
			},
			by:      store.MakerConversation,
			session: "orrery-1",
		},
		{
			name: "pane with empty state is the owner",
			seed: func(t *testing.T, st *store.Store) {
				t.Helper()
				pane := store.Pane{ID: "orrery-2", Pane: "%8"}
				if err := st.SyncPane(t.Context(), "-S "+tmuxSocket, pane, decisionByAt()); err != nil {
					t.Fatalf("SyncPane: %v", err)
				}
			},
			headers: func(h http.Header) {
				h.Set(protocol.HeaderTmux, tmuxSocket+",4242,0")
				h.Set(protocol.HeaderTmuxPane, "%8")
			},
			by: store.MakerOwner,
		},
		{
			name:    "no headers is the owner",
			seed:    func(*testing.T, *store.Store) {},
			headers: func(http.Header) {},
			by:      store.MakerOwner,
		},
		{
			name: "step session is orreryd",
			seed: func(t *testing.T, st *store.Store) {
				t.Helper()
				step := store.EngineAdd{ID: "step-1", Dir: "/wt/s1", State: "idle", Kind: store.KindStep}
				if err := st.ApplyEngine(t.Context(), []store.EngineOp{step}, decisionByAt()); err != nil {
					t.Fatalf("ApplyEngine: %v", err)
				}
			},
			headers: func(h http.Header) {
				h.Set(protocol.HeaderSession, "step-1")
			},
			by: store.MakerOrreryd,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, _ := openPlannerStore(t)
			tc.seed(t, st)
			client := newEpicsClient(t, startEpicServer(t, st))
			// The epic is created by the case's caller, so a conversation caller steers it and may change it.
			caller := http.Header{}
			tc.headers(caller)
			if err := steererSetCreate(t, client, stepGitTestRepo(t), caller); err != nil {
				t.Fatalf("CreateEpic: %v", err)
			}

			reply := answerNoteAs(t, client, tc)
			if reply.GetBy() != tc.by || reply.GetSession() != tc.session {
				t.Errorf("reply decision by %q session %q, want by %q session %q",
					reply.GetBy(), reply.GetSession(), tc.by, tc.session)
			}
			stored := findDecisionByID(t, getEpicOK(t, client).GetDecisions(), decisionByNoteID)
			if stored.GetBy() != tc.by || stored.GetSession() != tc.session {
				t.Errorf("stored decision by %q session %q, want by %q session %q",
					stored.GetBy(), stored.GetSession(), tc.by, tc.session)
			}
		})
	}
}
