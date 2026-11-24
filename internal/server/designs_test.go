package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// The ids and titles of the design of two the tests seed: a design holding two contracts, the second starting after
// the first.
const (
	designTwoID       = "synthetic-design"
	designTwoTitle    = "Synthetic design"
	designFirstID     = "synthetic-repo/first"
	designSecondID    = "synthetic-repo/second"
	designFirstTitle  = "Synthetic one"
	designSecondTitle = "Synthetic two"
)

// The id and title of the contract the design of one test seeds, which is in no design.
const (
	designLoneID    = "synthetic-repo/lone"
	designLoneTitle = "Synthetic lone"
)

// designSession is the conversation that works the seeded designs and writes the contracts of the design of two.
// designLoneWriter writes the contract of the design of one.
const (
	designSession    = "synthetic-designer"
	designLoneWriter = "synthetic-lone-writer"
)

// designTime is the unix seconds the tests store as the creation of each design and each proposal.
const designTime int64 = 1_700_000_200

// designsClient serves handler on an httptest server and returns a generated Designs client whose requests carry the
// allowed login and the client header.
func designsClient(t *testing.T, handler http.Handler) orreryv1connect.DesignsClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewDesignsClient(httpClient, srv.URL)
}

// seedDesignOfTwo creates the design of two in st: two proposed contracts, the second starting after the first.
func seedDesignOfTwo(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := t.Context()
	if err := st.CreateDesign(ctx, store.Design{
		ID: designTwoID, Title: designTwoTitle, Conversation: designSession, Created: time.Unix(designTime, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	first := contractTestVersion(t, contractTestBody(designFirstID, designFirstTitle, "serious"), 1,
		designSession, "Synthetic proposal.", designTime, nil)
	if err := st.ProposeContractInDesign(ctx, designTwoID, designFirstID, first); err != nil {
		t.Fatalf("ProposeContractInDesign first: %v", err)
	}
	second := contractTestVersion(t, contractTestBody(designSecondID, designSecondTitle, "limited"), 1,
		designSession, "Synthetic proposal.", designTime, nil)
	if err := st.ProposeContractInDesign(ctx, designTwoID, designSecondID, second); err != nil {
		t.Fatalf("ProposeContractInDesign second: %v", err)
	}
	if err := st.AddDesignEdge(ctx, designTwoID, designSecondID, designFirstID, time.Unix(designTime, 0)); err != nil {
		t.Fatalf("AddDesignEdge: %v", err)
	}
}

// seedDesignOfOne proposes the contract designLoneID in st, which is in no design.
func seedDesignOfOne(t *testing.T, st *store.Store) {
	t.Helper()
	proposal := contractTestVersion(t, contractTestBody(designLoneID, designLoneTitle, "limited"), 1,
		designLoneWriter, "Synthetic proposal.", designTime, nil)
	if err := st.ProposeContract(t.Context(), designLoneID, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
}

// wantDesignOfTwo is the public design of the design of two, its first contract at position 1 and its second, which
// starts after the first, at position 2.
func wantDesignOfTwo() *orreryv1.Design {
	return &orreryv1.Design{
		Id:           designTwoID,
		Title:        designTwoTitle,
		Conversation: designSession,
		Created:      designTime,
		State:        "drafting: 0 of 2 accepted",
		Contracts: []*orreryv1.DesignContract{
			{
				Id: designFirstID, Title: designFirstTitle, Repository: "/synthetic/repo", State: "proposed",
				Position: 1,
			},
			{
				Id: designSecondID, Title: designSecondTitle, Repository: "/synthetic/repo", State: "proposed",
				Position: 2, After: []string{designFirstID},
			},
		},
	}
}

// wantDesignOfOne is the public design of the contract designLoneID, a design of one at position 1.
func wantDesignOfOne() *orreryv1.Design {
	return &orreryv1.Design{
		Id:           designLoneID,
		Title:        designLoneTitle,
		Conversation: designLoneWriter,
		Created:      designTime,
		OfOne:        true,
		State:        "drafting: 0 of 1 accepted",
		Contracts: []*orreryv1.DesignContract{{
			Id: designLoneID, Title: designLoneTitle, Repository: "/synthetic/repo", State: "proposed", Position: 1,
		}},
	}
}

func TestDesignRoundTripServeListAndGet(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedDesignOfTwo(t, st)
	client := designsClient(t, contractsHandler(t, store.NewReader(path), nil))

	list, err := client.List(t.Context(), connect.NewRequest(&orreryv1.DesignsListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantList := &orreryv1.DesignsListResponse{Designs: []*orreryv1.Design{wantDesignOfTwo()}}
	if !proto.Equal(list.Msg, wantList) {
		t.Fatalf("List = %v, want %v", list.Msg, wantList)
	}

	get, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.DesignsGetRequest{Id: designTwoID}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantGet := &orreryv1.DesignsGetResponse{Design: wantDesignOfTwo()}
	if !proto.Equal(get.Msg, wantGet) {
		t.Fatalf("Get = %v, want %v", get.Msg, wantGet)
	}
}

func TestDesignOfOneServe(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedDesignOfOne(t, st)
	client := designsClient(t, contractsHandler(t, store.NewReader(path), nil))

	list, err := client.List(t.Context(), connect.NewRequest(&orreryv1.DesignsListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantList := &orreryv1.DesignsListResponse{Designs: []*orreryv1.Design{wantDesignOfOne()}}
	if !proto.Equal(list.Msg, wantList) {
		t.Fatalf("List = %v, want %v", list.Msg, wantList)
	}

	get, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.DesignsGetRequest{Id: designLoneID}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantGet := &orreryv1.DesignsGetResponse{Design: wantDesignOfOne()}
	if !proto.Equal(get.Msg, wantGet) {
		t.Fatalf("Get = %v, want %v", get.Msg, wantGet)
	}
}
