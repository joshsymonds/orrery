package enginehost

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// epicActionLogSeedAt is the time the EpicActionLog tests seed their sessions at.
func epicActionLogSeedAt() time.Time { return time.Unix(1700004000, 0) }

// epicActionLogCase is one caller of the EpicActionLog pause test: the sessions rows the store holds before the call, the
// caller headers the call sends, and the maker its pause action records.
type epicActionLogCase struct {
	name    string
	seed    func(t *testing.T, st *store.Store)
	headers func(h http.Header)
	maker   store.Maker
}

// TestEpicActionLogPauseRecordsItsCaller checks that a served PauseEpic appends one pause action that records the maker
// its caller resolves to: the owner with no headers, the conversation of a steering session by its id, and orreryd for a
// step session.
func TestEpicActionLogPauseRecordsItsCaller(t *testing.T) {
	t.Parallel()
	cases := []epicActionLogCase{
		{
			name:    "no headers is the owner",
			seed:    func(*testing.T, *store.Store) {},
			headers: func(http.Header) {},
			maker:   store.Maker{By: store.MakerOwner},
		},
		{
			name: "steering conversation is a conversation",
			seed: func(t *testing.T, st *store.Store) {
				t.Helper()
				engine := store.EngineAdd{ID: "engine-1", Dir: "/work/engine"}
				if err := st.ApplyEngine(t.Context(), []store.EngineOp{engine}, epicActionLogSeedAt()); err != nil {
					t.Fatalf("ApplyEngine: %v", err)
				}
			},
			headers: func(h http.Header) {
				h.Set(protocol.HeaderSession, "engine-1")
			},
			maker: store.Maker{By: store.MakerConversation, Session: "engine-1"},
		},
		{
			name: "step session is orreryd",
			seed: func(t *testing.T, st *store.Store) {
				t.Helper()
				step := store.EngineAdd{ID: "step-1", Dir: "/wt/s1", State: "idle", Kind: store.KindStep}
				if err := st.ApplyEngine(t.Context(), []store.EngineOp{step}, epicActionLogSeedAt()); err != nil {
					t.Fatalf("ApplyEngine: %v", err)
				}
			},
			headers: func(h http.Header) {
				h.Set(protocol.HeaderSession, "step-1")
			},
			maker: store.Maker{By: store.MakerOrreryd},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			tc.seed(t, st)
			client := newEpicsClient(t, startEpicServer(t, st))
			// The epic is created by the case's caller, so a conversation caller steers it and may change it.
			caller := http.Header{}
			tc.headers(caller)
			if err := steererSetCreate(t, client, stepGitTestRepo(t), caller); err != nil {
				t.Fatalf("CreateEpic: %v", err)
			}

			req := connect.NewRequest(&v1.PauseEpicRequest{Id: epicServiceID})
			tc.headers(req.Header())
			if _, err := client.PauseEpic(t.Context(), req); err != nil {
				t.Fatalf("PauseEpic: %v", err)
			}

			history, err := st.EpicHistory(t.Context(), epicServiceID)
			if err != nil {
				t.Fatalf("EpicHistory: %v", err)
			}
			if len(history.Actions) != 1 || history.Actions[0].Kind != store.ActionPause ||
				history.Actions[0].Maker != tc.maker {
				t.Fatalf("actions = %+v, want one pause made by %+v", history.Actions, tc.maker)
			}
		})
	}
}
