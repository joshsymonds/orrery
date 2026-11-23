package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// designListTime is the unix seconds the design list tests store as the creation of each design and each proposal.
// designListClock is the fixed clock the lists date their retention window by.
const designListTime int64 = 1_700_000_200

func designListClock() time.Time {
	return time.Unix(1_700_000_000, 0)
}

// The ids of the three headless sessions the headless test seeds. Alpha and beta each write a design, and gamma none.
const (
	designListAlphaID = "11111111111111111111111111111111"
	designListBetaID  = "22222222222222222222222222222222"
	designListGammaID = "33333333333333333333333333333333"
)

// openDesignListStore opens a seeded store at a temporary path, closed when the test ends, and returns it with its
// path.
func openDesignListStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	return st, path
}

// designListProposal returns version 1 of the contract id, titled title, proposed by writer at unix seconds at.
func designListProposal(t *testing.T, id, title, writer string, at int64) store.ContractVersion {
	t.Helper()
	body := &enginev1.EpicContract{
		Id:         id,
		Repository: "/synthetic/repo",
		Title:      title,
		Care:       "serious",
		Intent:     "Synthetic intent.",
		Failures: []*enginev1.EpicFailure{{
			Id: "F1", Text: "Synthetic failure.", Rating: "serious", Response: "prevent",
		}},
		Requirements: []*enginev1.EpicRequirement{{
			Id: "R1", Text: "Synthetic requirement.", Evidence: "Synthetic evidence.", Kind: "test",
		}},
	}
	encoded, err := protojson.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	return store.ContractVersion{
		Version: 1, Repository: body.GetRepository(), Title: title, Care: body.GetCare(),
		Body: string(encoded), Words: "Synthetic proposal.", Writer: writer, At: at,
	}
}

// seedDesignListOne creates the design designID in st for conversation, with the one contract id proposed as its only
// contract, and returns the public design the lists should carry for it.
func seedDesignListOne(t *testing.T, st *store.Store, designID, conversation, id, title string) *orreryv1.Design {
	t.Helper()
	ctx := t.Context()
	if err := st.CreateDesign(ctx, store.Design{
		ID: designID, Title: title, Conversation: conversation, Created: time.Unix(designListTime, 0),
	}); err != nil {
		t.Fatalf("CreateDesign %s: %v", designID, err)
	}
	proposal := designListProposal(t, id, title, conversation, designListTime)
	if err := st.ProposeContractInDesign(ctx, designID, id, proposal); err != nil {
		t.Fatalf("ProposeContractInDesign %s: %v", id, err)
	}
	return &orreryv1.Design{
		Id:           designID,
		Title:        title,
		Conversation: conversation,
		Created:      designListTime,
		OfOne:        true,
		State:        "drafting: 0 of 1 accepted",
		Contracts: []*orreryv1.DesignContract{{
			Id: id, Title: title, Repository: "/synthetic/repo", State: "proposed", Position: 1,
		}},
	}
}

func TestDesignListsHeadlessCarriesDesigns(t *testing.T) {
	t.Parallel()
	st, path := openDesignListStore(t)
	at := designListClock()
	for _, id := range []string{designListAlphaID, designListBetaID, designListGammaID} {
		applyHeadless(t, st, at, store.EngineAdd{ID: id, Dir: "/work/" + id, State: "idle"})
	}
	alpha := seedDesignListOne(t, st, "synthetic-design-alpha", designListAlphaID,
		"synthetic-repo/alpha-first", "Synthetic alpha")
	beta := seedDesignListOne(t, st, "synthetic-design-beta", designListBetaID,
		"synthetic-repo/beta-first", "Synthetic beta")
	want := map[string][]*orreryv1.Design{
		designListAlphaID: {alpha},
		designListBetaID:  {beta},
		designListGammaID: nil,
	}

	client := sessionsClientAt(t, store.NewReader(path), designListClock)
	got, err := client.ListHeadless(t.Context(), connect.NewRequest(&orreryv1.ListHeadlessRequest{}))
	if err != nil {
		t.Fatalf("ListHeadless: %v", err)
	}
	if len(got.Msg.GetSessions()) != len(want) {
		t.Fatalf("ListHeadless sessions = %d, want %d", len(got.Msg.GetSessions()), len(want))
	}
	for _, session := range got.Msg.GetSessions() {
		wantDesigns, ok := want[session.GetId()]
		if !ok {
			t.Fatalf("ListHeadless lists unexpected session %s", session.GetId())
		}
		gotDesigns := &orreryv1.DesignsListResponse{Designs: session.GetDesigns()}
		if !proto.Equal(gotDesigns, &orreryv1.DesignsListResponse{Designs: wantDesigns}) {
			t.Errorf("session %s designs = %v, want %v", session.GetId(), session.GetDesigns(), wantDesigns)
		}
	}
}

// epicsDesignListClient serves the Epics handler over reader, with the fixed clock, and returns a generated client.
func epicsDesignListClient(t *testing.T, reader *store.Reader) orreryv1connect.EpicsClient {
	t.Helper()
	path, handler := newEpicsHandler(t.TempDir(), &epicsFake{}, nil, nil, nil, reader, 0, epicsNow, newRetryClock())
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewEpicsClient(srv.Client(), srv.URL)
}

func TestDesignListsEpicsCarriesDesigns(t *testing.T) {
	t.Parallel()
	st, path := openDesignListStore(t)
	ctx := t.Context()
	at := time.Unix(designListTime, 0)
	const (
		designID    = "synthetic-design-epics"
		writer      = "synthetic-designer-epics"
		first       = "synthetic-repo/epics-first"
		second      = "synthetic-repo/epics-second"
		lone        = "synthetic-repo/epics-lone"
		firstTitle  = "Synthetic first"
		secondTitle = "Synthetic second"
		loneTitle   = "Synthetic lone"
	)
	if err := st.CreateDesign(ctx, store.Design{
		ID: designID, Title: "Synthetic epics design", Conversation: writer, Created: at,
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	proposals := map[string]string{first: firstTitle, second: secondTitle}
	for _, id := range []string{first, second} {
		proposal := designListProposal(t, id, proposals[id], writer, designListTime)
		if err := st.ProposeContractInDesign(ctx, designID, id, proposal); err != nil {
			t.Fatalf("ProposeContractInDesign %s: %v", id, err)
		}
	}
	if err := st.AddDesignEdge(ctx, designID, second, first, at); err != nil {
		t.Fatalf("AddDesignEdge: %v", err)
	}
	for _, id := range []string{first, second} {
		accept := store.ContractChange{Words: "Synthetic accept.", At: designListTime}
		if err := st.AcceptContract(ctx, id, accept); err != nil {
			t.Fatalf("AcceptContract %s: %v", id, err)
		}
	}
	firstEpic := store.EpicRow{
		ID: first, Repository: "/work/synthetic-repo", Title: firstTitle, Care: "serious", Phase: "accepted",
		Created: at, Changed: at, PhaseAt: at,
	}
	if err := st.CreateContractEpic(ctx, firstEpic, 1); err != nil {
		t.Fatalf("CreateContractEpic: %v", err)
	}
	// lone is a contract in no design, which the list carries as a design of one, after the real designs.
	loneProposal := designListProposal(t, lone, loneTitle, writer, designListTime)
	if err := st.ProposeContract(ctx, lone, loneProposal); err != nil {
		t.Fatalf("ProposeContract %s: %v", lone, err)
	}

	want := []*orreryv1.Design{
		{
			Id:           designID,
			Title:        "Synthetic epics design",
			Conversation: writer,
			Created:      designListTime,
			State:        "building: 0 of 2 released",
			Contracts: []*orreryv1.DesignContract{
				{
					Id: first, Title: firstTitle, Repository: "/synthetic/repo", State: "accepted",
					Position: 1, Epic: first, EpicPhase: "accepted",
				},
				{
					Id: second, Title: secondTitle, Repository: "/synthetic/repo", State: "accepted",
					Position: 2, After: []string{first},
				},
			},
		},
		{
			Id:           lone,
			Title:        loneTitle,
			Conversation: writer,
			Created:      designListTime,
			OfOne:        true,
			State:        "drafting: 0 of 1 accepted",
			Contracts: []*orreryv1.DesignContract{{
				Id: lone, Title: loneTitle, Repository: "/synthetic/repo", State: "proposed", Position: 1,
			}},
		},
	}

	client := epicsDesignListClient(t, store.NewReader(path))
	got, err := client.List(ctx, connect.NewRequest(&orreryv1.EpicsListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !proto.Equal(
		&orreryv1.DesignsListResponse{Designs: got.Msg.GetDesigns()},
		&orreryv1.DesignsListResponse{Designs: want},
	) {
		t.Fatalf("List designs = %v, want %v", got.Msg.GetDesigns(), want)
	}
}
