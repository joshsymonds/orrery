package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Session row ids, epic ids and contract ids of the steering tests, with invented values. The headless id is 32 hex
// digits, as session ids are.
const (
	steersPaneID       = "orrery-steers-pane"
	steersHeadlessID   = "3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d"
	steersPaneEpic     = "repoa/steers-pane"
	steersHeadEpic     = "repoa/steers-headless"
	steersPaneContract = "repoa/steers-pane-contract"
	steersHeadContract = "repoa/steers-headless-contract"
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

// steersStore returns the path of a store holding the pane steersPaneID and the headless session steersHeadlessID, each
// changed at steersChangedAt and closed for that change time.
func steersStore(t *testing.T) string {
	t.Helper()
	path := archiveStorePath(t)
	changed := steersChangedAt()
	applyHeadlessAt(t, path, changed, store.EngineAdd{ID: steersHeadlessID, Dir: "/work/steers", State: "idle"})
	syncEndedPane(t, path, steersPaneID, "idle", changed)
	setClosureVerdict(t, path, closureVerdictOf(steersPaneID, changed, true), steersStart())
	setClosureVerdict(t, path, closureVerdictOf(steersHeadlessID, changed, true), steersStart())
	return path
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

// expectSteersEnded lists the store at path through Sessions.List and ListHeadless over epics, and fails the test unless
// the pane steersPaneID has the ended state wantPane and the headless session has wantHeadless.
func expectSteersEnded(
	t *testing.T, step, path string, epics EpicStoreClient, wantPane, wantHeadless endedRowState,
) {
	t.Helper()
	sessions := steersClient(t, path, epics)
	expectEnded(t, step+" Sessions.List", endedStates(listSessionRows(t, sessions)),
		map[string]endedRowState{steersPaneID: wantPane})
	expectEnded(t, step+" ListHeadless", endedStates(listHeadlessRows(t, sessions)),
		map[string]endedRowState{steersHeadlessID: wantHeadless})
}

func TestInboxStatesSteersLiveEpicHoldsRowsOpen(t *testing.T) {
	t.Parallel()
	path := steersStore(t)
	epics := &steersEpics{summaries: []*enginev1.EpicSummary{
		steersEpic(steersPaneEpic, "accepted", steersPaneID),
		steersEpic(steersHeadEpic, "accepted", steersHeadlessID),
	}}
	none := endedRowState{}
	expectSteersEnded(t, "live epics", path, epics, none, none)
}

func TestInboxStatesSteersProposedContractHoldsRowsOpen(t *testing.T) {
	t.Parallel()
	path := steersStore(t)
	proposeSteersContract(t, path, steersPaneContract, steersPaneID, steersChangedAt())
	proposeSteersContract(t, path, steersHeadContract, steersHeadlessID, steersChangedAt())
	none := endedRowState{}
	expectSteersEnded(t, "proposed contracts", path, &steersEpics{}, none, none)
}

func TestInboxStatesSteersEndedOnceEpicFinalAndContractSetAside(t *testing.T) {
	t.Parallel()
	path := steersStore(t)
	epics := &steersEpics{summaries: []*enginev1.EpicSummary{steersEpic(steersPaneEpic, "accepted", steersPaneID)}}
	proposeSteersContract(t, path, steersHeadContract, steersHeadlessID, steersChangedAt())
	none := endedRowState{}
	expectSteersEnded(t, "while live and proposed", path, epics, none, none)

	epics.summaries = []*enginev1.EpicSummary{steersEpic(steersPaneEpic, "released", steersPaneID)}
	setAsideSteersContract(t, path, steersHeadContract, steersStart())
	want := endedRowState{ended: true, endedAt: steersChangedAt().Unix()}
	expectSteersEnded(t, "once final and set aside", path, epics, want, want)
}

func TestInboxStatesSteersProposedContractHoldsAfterEpicFinal(t *testing.T) {
	t.Parallel()
	path := steersStore(t)
	proposeSteersContract(t, path, steersHeadContract, steersHeadlessID, steersChangedAt())
	epics := &steersEpics{summaries: []*enginev1.EpicSummary{steersEpic(steersPaneEpic, "released", steersPaneID)}}
	expectSteersEnded(t, "epic final, contract proposed", path, epics,
		endedRowState{ended: true, endedAt: steersChangedAt().Unix()}, endedRowState{})
}

func TestInboxStatesSteersEpicStoreFailureAnswersUnavailable(t *testing.T) {
	t.Parallel()
	path := steersStore(t)
	epics := &steersEpics{listErr: connect.NewError(connect.CodeInternal, errors.New("synthetic failure"))}
	sessions := steersClient(t, path, epics)
	_, err := sessions.List(t.Context(), connect.NewRequest(&orreryv1.ListRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("List error = %v, want code %v", err, connect.CodeUnavailable)
	}
	expectHeadlessCode(t, sessions, connect.CodeUnavailable)
}

func TestInboxStatesSteersUnimplementedEpicStoreReadsNone(t *testing.T) {
	t.Parallel()
	path := steersStore(t)
	epics := &steersEpics{listErr: connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unimplemented"))}
	want := endedRowState{ended: true, endedAt: steersChangedAt().Unix()}
	expectSteersEnded(t, "unimplemented epic store", path, epics, want, want)
}
