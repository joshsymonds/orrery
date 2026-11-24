package enginehost

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// designTestSecondID is the id of the second contract the Designs tests propose, in a repository other than the
// first's.
const designTestSecondID = "synthetic-repo-1b2c3d4/epic-second"

// designTestAt is the time the fake clock of the Designs tests reads.
func designTestAt() time.Time { return time.Unix(1700005000, 0) }

// designTestServer is a sessions socket serving the Contracts and Designs services, with the clients that dial it.
type designTestServer struct {
	designs   enginev1connect.DesignsClient
	contracts enginev1connect.ContractsClient
}

// startDesignServer serves the Contracts service over st with contractTestContracts and the Designs service over st
// with the fake clock, on a sessions socket in a temporary directory. The server stops at cleanup.
func startDesignServer(t *testing.T, st *store.Store) designTestServer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	cfg := ServeConfig{
		Contracts: contractTestContracts(st),
		Designs:   &Designs{Store: st, Now: designTestAt},
	}
	go func() { done <- ServeWith(ctx, ln, cfg) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith after cleanup: %v", err)
		}
	})
	client := unixHTTPClient(t, path)
	return designTestServer{
		designs:   enginev1connect.NewDesignsClient(client, "http://designs.invalid"),
		contracts: enginev1connect.NewContractsClient(client, "http://contracts.invalid"),
	}
}

// designCreate returns a CreateDesign request for the design id titled title, from the headless session.
func designCreate(id, title string) *connect.Request[v1.CreateDesignRequest] {
	req := connect.NewRequest(&v1.CreateDesignRequest{Id: id, Title: title})
	headlessCaller(req.Header())
	return req
}

// createDesign creates the design id titled title from the headless session and returns the reply's design. It fails
// the test on error.
func createDesign(t *testing.T, client enginev1connect.DesignsClient, id, title string) *v1.StoredDesign {
	t.Helper()
	reply, err := client.CreateDesign(t.Context(), designCreate(id, title))
	if err != nil {
		t.Fatalf("CreateDesign %s: %v", id, err)
	}
	return reply.Msg.GetDesign()
}

// storeDesignTestContract stores the contract with id as version 1, written by the headless session. It joins design
// when design is not empty, and is in no design otherwise. It fails the test on error.
func storeDesignTestContract(t *testing.T, st *store.Store, design, id string) {
	t.Helper()
	body := contractTestBody(stepGitTestRepo(t))
	body.Id = id
	version, err := contractVersionOf(id, body, nil, contractTestWords, contractTestHeadless, designTestAt().Unix())
	if err != nil {
		t.Fatalf("contractVersionOf %s: %v", id, err)
	}
	if design == "" {
		err = st.ProposeContract(t.Context(), id, version)
	} else {
		err = st.ProposeContractInDesign(t.Context(), design, id, version)
	}
	if err != nil {
		t.Fatalf("propose %s into %q: %v", id, design, err)
	}
}

// TestDesignServiceCreateGetList checks that CreateDesign stores a design of the caller's conversation at the clock's
// time and replies with it with its state, that GetDesign and ListDesigns read it back, that ListDesigns lists a
// contract in no design as a design of one, and that CreateDesign refuses a blank id or title and an id already held.
func TestDesignServiceCreateGetList(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startDesignServer(t, st)

	created := createDesign(t, server.designs, "design-one", "Design one")
	record, err := st.Design(t.Context(), "design-one")
	if err != nil {
		t.Fatalf("Design: %v", err)
	}
	want := &v1.StoredDesign{
		Id:           "design-one",
		Title:        "Design one",
		Conversation: contractTestHeadless,
		Created:      designTestAt().Unix(),
		State:        record.State,
	}
	if !proto.Equal(created, want) {
		t.Errorf("CreateDesign reply = %v, want %v", created, want)
	}

	got, err := server.designs.GetDesign(t.Context(), connect.NewRequest(&v1.GetDesignRequest{Id: "design-one"}))
	if err != nil {
		t.Fatalf("GetDesign: %v", err)
	}
	if !proto.Equal(got.Msg.GetDesign(), want) {
		t.Errorf("GetDesign reply = %v, want %v", got.Msg.GetDesign(), want)
	}

	_, err = server.designs.GetDesign(t.Context(), connect.NewRequest(&v1.GetDesignRequest{Id: "design-missing"}))
	wantContractRefusal(t, err, connect.CodeNotFound, "design not found")

	_, err = server.designs.CreateDesign(t.Context(), designCreate("design-one", "Design one again"))
	wantContractRefusal(t, err, connect.CodeAlreadyExists, "id: design-one is already held by a design or a contract")
	_, err = server.designs.CreateDesign(t.Context(), designCreate(" ", "Blank id"))
	wantContractRefusal(t, err, connect.CodeInvalidArgument, "id: empty")
	_, err = server.designs.CreateDesign(t.Context(), designCreate("design-blank", " "))
	wantContractRefusal(t, err, connect.CodeInvalidArgument, "title: empty")

	createDesign(t, server.designs, "design-two", "Design two")
	storeDesignTestContract(t, st, "", "synthetic-repo-2c3d4e5/lone-contract")
	listed, err := server.designs.ListDesigns(t.Context(), connect.NewRequest(&v1.ListDesignsRequest{}))
	if err != nil {
		t.Fatalf("ListDesigns: %v", err)
	}
	ids := make([]string, 0, len(listed.Msg.GetDesigns()))
	for _, design := range listed.Msg.GetDesigns() {
		ids = append(ids, design.GetId())
	}
	wantIDs := []string{"design-one", "design-two", "synthetic-repo-2c3d4e5/lone-contract"}
	if !slices.Equal(ids, wantIDs) {
		t.Fatalf("ListDesigns ids = %v, want %v", ids, wantIDs)
	}
	if lone := listed.Msg.GetDesigns()[2]; !lone.GetOfOne() || len(lone.GetContracts()) != 1 {
		t.Errorf("listed contract in no design = %v, want a design of one with one contract", lone)
	}
}

// TestDesignServiceOrderRefused checks that OrderDesign refuses a starts-after edge across designs, an edge that closes
// a cycle, and an edge to a contract outside the design, each as a failed precondition, and logs the message the
// refusal carries.
func TestDesignServiceOrderRefused(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startDesignServer(t, st)
	createDesign(t, server.designs, "design-a", "Design a")
	createDesign(t, server.designs, "design-b", "Design b")
	const (
		aOne     = "synthetic-repo-3d4e5f6/a-one"
		aTwo     = "synthetic-repo-3d4e5f6/a-two"
		bOne     = "synthetic-repo-4e5f6a7/b-one"
		outsider = "synthetic-repo-5f6a7b8/outsider"
	)
	storeDesignTestContract(t, st, "design-a", aOne)
	storeDesignTestContract(t, st, "design-a", aTwo)
	storeDesignTestContract(t, st, "design-b", bOne)
	storeDesignTestContract(t, st, "", outsider)

	// aTwo starts after aOne, so the edge from aOne back to aTwo would close a cycle.
	_, err := server.designs.OrderDesign(t.Context(), connect.NewRequest(&v1.OrderDesignRequest{
		Design: "design-a", Contract: aTwo, After: []string{aOne},
	}))
	if err != nil {
		t.Fatalf("OrderDesign %s after %s: %v", aTwo, aOne, err)
	}

	cases := []struct {
		name     string
		contract string
		after    string
	}{
		{name: "across designs", contract: aOne, after: bOne},
		{name: "cycle", contract: aOne, after: aTwo},
		{name: "non-member", contract: aOne, after: outsider},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := server.designs.OrderDesign(t.Context(), connect.NewRequest(&v1.OrderDesignRequest{
				Design: "design-a", Contract: tc.contract, After: []string{tc.after},
			}))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("OrderDesign error = %v, want a Connect error", err)
			}
			if connectErr.Code() != connect.CodeFailedPrecondition {
				t.Errorf("code = %v, want %v", connectErr.Code(), connect.CodeFailedPrecondition)
			}
			t.Logf("refusal message: %s", connectErr.Message())
		})
	}
}

// TestDesignServiceProposeInDesign checks that two contracts in different repositories, proposed into one design, join
// it in position order, and that a propose into a design no one holds is refused as not found and stores no contract.
func TestDesignServiceProposeInDesign(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	server := startDesignServer(t, st)
	createDesign(t, server.designs, "design-one", "Design one")

	repoA, repoB := stepGitTestRepo(t), stepGitTestRepo(t)
	first := contractTestBody(repoA)
	second := contractTestBody(repoB)
	second.Id = designTestSecondID
	for _, body := range []*v1.EpicContract{first, second} {
		if err := proposeIntoDesign(t, server.contracts, "design-one", body); err != nil {
			t.Fatalf("ProposeContract %s into design-one: %v", body.GetId(), err)
		}
	}

	got, err := server.designs.GetDesign(t.Context(), connect.NewRequest(&v1.GetDesignRequest{Id: "design-one"}))
	if err != nil {
		t.Fatalf("GetDesign: %v", err)
	}
	want := []*v1.StoredDesignContract{
		{Id: first.GetId(), Title: first.GetTitle(), Repository: repoA, State: "proposed", Position: 1},
		{Id: second.GetId(), Title: second.GetTitle(), Repository: repoB, State: "proposed", Position: 2},
	}
	if !slices.EqualFunc(got.Msg.GetDesign().GetContracts(), want, func(a, b *v1.StoredDesignContract) bool {
		return proto.Equal(a, b)
	}) {
		t.Errorf("design-one contracts = %v, want %v", got.Msg.GetDesign().GetContracts(), want)
	}

	missing := contractTestBody(repoA)
	missing.Id = "synthetic-repo-6a7b8c9/epic-missing"
	err = proposeIntoDesign(t, server.contracts, "no-such-design", missing)
	wantContractRefusal(t, err, connect.CodeNotFound, "design: no-such-design not found")
	_, err = server.contracts.GetContract(
		t.Context(), connect.NewRequest(&v1.GetContractRequest{Id: missing.GetId()}))
	wantContractRefusal(t, err, connect.CodeNotFound, "contract "+missing.GetId()+" not found")
}

// proposeIntoDesign proposes body from the headless session into design, and returns the error the propose gets.
func proposeIntoDesign(
	t *testing.T,
	client enginev1connect.ContractsClient,
	design string,
	body *v1.EpicContract,
) error {
	t.Helper()
	req := connect.NewRequest(&v1.ProposeContractRequest{
		Id:       body.GetId(),
		Contract: body,
		Words:    contractTestWords,
		Design:   design,
	})
	headlessCaller(req.Header())
	_, err := client.ProposeContract(t.Context(), req)
	return err
}
