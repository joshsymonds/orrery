package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Session row ids, epic ids and contract ids of the steering tests, with invented values. The cases of
// TestInboxStatesSteers share one store, so each case has a pane and a headless session of its own. The headless ids
// are 32 hex digits, as session ids are.
const (
	steersLivePane     = "orrery-steers-live"
	steersLiveHead     = "3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d"
	steersProposedPane = "orrery-steers-proposed"
	steersProposedHead = "3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e"
	steersFinalPane    = "orrery-steers-final"
	steersFinalHead    = "3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f3f"
	steersAfterPane    = "orrery-steers-after"
	steersAfterHead    = "40404040404040404040404040404040"
	steersFailPane     = "orrery-steers-failing"
	steersFailHead     = "41414141414141414141414141414141"
	steersUnimplPane   = "orrery-steers-unimplemented"
	steersUnimplHead   = "42424242424242424242424242424242"

	steersPaneEpic = "repoa/steers-pane"
	steersHeadEpic = "repoa/steers-headless"

	steersProposedPaneContract = "repoa/steers-proposed-pane-contract"
	steersProposedHeadContract = "repoa/steers-proposed-headless-contract"
	steersFinalHeadContract    = "repoa/steers-final-headless-contract"
	steersAfterHeadContract    = "repoa/steers-after-headless-contract"
)

// steersStart is the fixed clock of the steering tests.
func steersStart() time.Time {
	return time.Unix(1_700_000_000, 0)
}

// steersChangedAt is the change time of the steering rows, ten minutes before steersStart.
func steersChangedAt() time.Time {
	return steersStart().Add(-10 * time.Minute)
}

// steersEpics is the EpicStoreClient of the steering tests. ListEpics lists summaries, or fails with listErr when it is
// set. The list reads never call GetEpic, which answers unimplemented.
type steersEpics struct {
	summaries []*enginev1.EpicSummary
	listErr   error
}

func (f *steersEpics) ListEpics(
	context.Context,
	*connect.Request[enginev1.ListEpicsRequest],
) (*connect.Response[enginev1.ListEpicsResponse], error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return connect.NewResponse(&enginev1.ListEpicsResponse{Epics: f.summaries}), nil
}

func (f *steersEpics) GetEpic(
	context.Context,
	*connect.Request[enginev1.GetEpicRequest],
) (*connect.Response[enginev1.GetEpicResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unimplemented"))
}

// steersEpic returns the summary of an orreryd epic id, in phase, steered by the session steerer.
func steersEpic(id, phase, steerer string) *enginev1.EpicSummary {
	return &enginev1.EpicSummary{Id: id, Phase: phase, Steerer: steerer}
}

// seedSteersCases writes the rows of the cases of TestInboxStatesSteers into the store at path. The headless sessions
// are added in one write at steersChangedAt. Each pane is synced at that time, and each pane and headless session is
// closed for that change time by a verdict stamped at steersStart. The proposed contracts name their conversations.
func seedSteersCases(t *testing.T, path string) {
	t.Helper()
	changed := steersChangedAt()
	panes := []string{
		steersLivePane, steersProposedPane, steersFinalPane, steersAfterPane, steersFailPane, steersUnimplPane,
	}
	heads := []string{
		steersLiveHead, steersProposedHead, steersFinalHead, steersAfterHead, steersFailHead, steersUnimplHead,
	}
	adds := make([]store.EngineOp, 0, len(heads))
	for _, id := range heads {
		adds = append(adds, store.EngineAdd{ID: id, Dir: "/work/steers", State: "idle"})
	}
	applyHeadlessAt(t, path, changed, adds...)
	for _, pane := range panes {
		syncEndedPane(t, path, pane, "idle", changed)
	}
	for _, id := range slices.Concat(panes, heads) {
		setClosureVerdict(t, path, closureVerdictOf(id, changed, true), steersStart())
	}
	proposeSteersContract(t, path, steersProposedPaneContract, steersProposedPane, changed)
	proposeSteersContract(t, path, steersProposedHeadContract, steersProposedHead, changed)
	proposeSteersContract(t, path, steersFinalHeadContract, steersFinalHead, changed)
	proposeSteersContract(t, path, steersAfterHeadContract, steersAfterHead, changed)
}

// proposeSteersContract proposes the contract id with writer as its conversation, at at, in the store at path, and
// closes the store.
func proposeSteersContract(t *testing.T, path, id, writer string, at time.Time) {
	t.Helper()
	s := openHeadlessStore(t, path)
	proposal := store.ContractVersion{
		Repository: "/work/steers", Title: "steers contract", Body: "{}",
		Words: "a proposal", Writer: writer, At: at.Unix(),
	}
	if err := s.ProposeContract(t.Context(), id, proposal); err != nil {
		t.Fatalf("ProposeContract %s: %v", id, err)
	}
	closeStore(t, s)
}

// setAsideSteersContract sets the proposed contract id aside at at in the store at path, and closes the store.
func setAsideSteersContract(t *testing.T, path, id string, at time.Time) {
	t.Helper()
	s := openHeadlessStore(t, path)
	if err := s.SetAsideContract(t.Context(), id, store.ContractChange{At: at.Unix()}); err != nil {
		t.Fatalf("SetAsideContract %s: %v", id, err)
	}
	closeStore(t, s)
}

// steersClient serves Sessions over the store at path, with the clock steersStart and epics as the epic store, and
// returns a client for it.
func steersClient(t *testing.T, path string, epics EpicStoreClient) orreryv1connect.SessionsClient {
	t.Helper()
	handlerPath, handler := newSessionsHandler(
		store.NewReader(path), "", steersStart, nil, epics, newChangeHub(nil, nil), nil, 0, 0, nil,
	)
	mux := http.NewServeMux()
	mux.Handle(handlerPath, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewSessionsClient(srv.Client(), srv.URL)
}

// pickStates returns the entries of states whose ids are among ids. A store holds the rows of every case, so a case
// reads only its own rows, and a row it names that is absent is absent from the result.
func pickStates[V any](states map[string]V, ids ...string) map[string]V {
	picked := make(map[string]V, len(ids))
	for _, id := range ids {
		if state, ok := states[id]; ok {
			picked[id] = state
		}
	}
	return picked
}

// expectSteersEnded lists the store through sessions, by Sessions.List and ListHeadless, and fails the test unless the
// pane and the headless session of one case have the ended states wantPane and wantHeadless.
func expectSteersEnded(
	t *testing.T, step string, sessions orreryv1connect.SessionsClient, pane, headless string,
	wantPane, wantHeadless endedRowState,
) {
	t.Helper()
	panes := pickStates(endedStates(listSessionRows(t, sessions)), pane)
	expectEnded(t, step+" Sessions.List", panes, map[string]endedRowState{pane: wantPane})
	headlessStates := pickStates(endedStates(listHeadlessRows(t, sessions)), headless)
	expectEnded(t, step+" ListHeadless", headlessStates, map[string]endedRowState{headless: wantHeadless})
}

// TestInboxStatesSteers holds the cases of the steering rule: a conversation that steers a live epic, or that a proposed
// contract names, is never ended. The cases share the store seedSteersCases writes. The case that sets its contract
// aside runs in order, before the cases that only read, and those run in parallel on their own rows.
func TestInboxStatesSteers(t *testing.T) {
	t.Parallel()
	path := archiveStorePath(t)
	seedSteersCases(t, path)
	none := endedRowState{}
	ended := endedRowState{ended: true, endedAt: steersChangedAt().Unix()}

	t.Run("LiveEpicHoldsRowsOpen", func(t *testing.T) {
		t.Parallel()
		epics := &steersEpics{summaries: []*enginev1.EpicSummary{
			steersEpic(steersPaneEpic, "accepted", steersLivePane),
			steersEpic(steersHeadEpic, "accepted", steersLiveHead),
		}}
		expectSteersEnded(t, "live epics", steersClient(t, path, epics), steersLivePane, steersLiveHead, none, none)
	})

	t.Run("ProposedContractHoldsRowsOpen", func(t *testing.T) {
		t.Parallel()
		sessions := steersClient(t, path, &steersEpics{})
		expectSteersEnded(t, "proposed contracts", sessions, steersProposedPane, steersProposedHead, none, none)
	})

	t.Run("EndedOnceEpicFinalAndContractSetAside", func(t *testing.T) {
		epics := &steersEpics{summaries: []*enginev1.EpicSummary{
			steersEpic(steersPaneEpic, "accepted", steersFinalPane),
		}}
		sessions := steersClient(t, path, epics)
		expectSteersEnded(t, "while live and proposed", sessions, steersFinalPane, steersFinalHead, none, none)

		epics.summaries = []*enginev1.EpicSummary{steersEpic(steersPaneEpic, "released", steersFinalPane)}
		setAsideSteersContract(t, path, steersFinalHeadContract, steersStart())
		expectSteersEnded(t, "once final and set aside", sessions, steersFinalPane, steersFinalHead, ended, ended)
	})

	t.Run("ProposedContractHoldsAfterEpicFinal", func(t *testing.T) {
		t.Parallel()
		epics := &steersEpics{summaries: []*enginev1.EpicSummary{
			steersEpic(steersPaneEpic, "released", steersAfterPane),
		}}
		expectSteersEnded(t, "epic final, contract proposed", steersClient(t, path, epics),
			steersAfterPane, steersAfterHead, ended, none)
	})

	t.Run("EpicStoreFailureAnswersUnavailable", func(t *testing.T) {
		t.Parallel()
		epics := &steersEpics{listErr: connect.NewError(connect.CodeInternal, errors.New("synthetic failure"))}
		sessions := steersClient(t, path, epics)
		_, err := sessions.List(t.Context(), connect.NewRequest(&orreryv1.ListRequest{}))
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("List error = %v, want code %v", err, connect.CodeUnavailable)
		}
		expectHeadlessCode(t, sessions, connect.CodeUnavailable)
	})

	t.Run("UnimplementedEpicStoreReadsNone", func(t *testing.T) {
		t.Parallel()
		epics := &steersEpics{
			listErr: connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unimplemented")),
		}
		expectSteersEnded(t, "unimplemented epic store", steersClient(t, path, epics),
			steersUnimplPane, steersUnimplHead, ended, ended)
	})
}
