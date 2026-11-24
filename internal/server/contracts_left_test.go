package server_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// contractIDL is the proposed contract the left tests walk through, and leftDesign is the design it is proposed into.
const (
	contractIDL = "synthetic-repo/left"
	leftDesign  = "design-left"
)

// leftServeBody returns the body contractIDL is proposed with: a severe contract with one addition and two release
// steps.
func leftServeBody() *enginev1.EpicContract {
	body := contractTestBody(contractIDL, "Synthetic left", "severe")
	body.Additions = []*enginev1.EpicAddition{
		{Location: "internal/store", What: "first", Why: "Needed", Cost: "None"},
	}
	body.ReleaseSteps = []*enginev1.EpicReleaseStep{
		{Step: "Ship the build", Target: "origin", Undo: "Revert the merge"},
		{Step: "Tag the release", Target: "origin", Undo: "Delete the tag"},
	}
	return body
}

// serveLeftOf returns the ContractLeft message serve answers for kind and ref, with the phrase the server gives kind.
func serveLeftOf(kind, ref string) *orreryv1.ContractLeft {
	phrases := map[string]string{
		store.LeftQuestion: "design question not yet answered",
		store.LeftDecision: "decision not yet answered",
		store.LeftAddition: "addition neither kept nor struck",
		store.LeftRefused:  "write refused, and no version stored after it",
		store.LeftRevising: "working copy not yet proposed",
		store.LeftNotShown: "release step not yet shown to the owner",
	}
	return &orreryv1.ContractLeft{Kind: kind, Ref: ref, Text: phrases[kind]}
}

// assertServeLeftAndShown fails t unless the response holds exactly the left items and shown rows given.
func assertServeLeftAndShown(
	t *testing.T,
	got *orreryv1.ContractsGetResponse,
	left []*orreryv1.ContractLeft,
	shown []*orreryv1.ContractShown,
) {
	t.Helper()
	want := &orreryv1.ContractsGetResponse{Left: left, Shown: shown}
	if !proto.Equal(&orreryv1.ContractsGetResponse{Left: got.GetLeft(), Shown: got.GetShown()}, want) {
		t.Fatalf("left = %v and shown = %v, want left %v and shown %v", got.GetLeft(), got.GetShown(), left, shown)
	}
}

// TestContractRecordsLeftServeGetWalksAsTheOwnerSettlesIt checks that a read through serve lists what is left of a
// severe contract as the owner settles it through serve: the question is answered, the working copy proposed, the
// addition kept and the first release step shown. Each step leaves the list as it should, and the shown row holds on a
// second read.
func TestContractRecordsLeftServeGetWalksAsTheOwnerSettlesIt(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openContractStore(t)
	if err := st.CreateDesign(ctx, store.Design{
		ID: leftDesign, Title: "Left design", Conversation: contractWriterA, Created: time.Unix(contractTimeA, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	proposal := contractTestVersion(t, leftServeBody(), 1, contractWriterA, "Synthetic proposal.", contractTimeA, nil)
	if err := st.ProposeContractInDesign(ctx, leftDesign, contractIDL, proposal); err != nil {
		t.Fatalf("ProposeContractInDesign: %v", err)
	}
	if _, err := st.AskDesignQuestion(ctx, store.DesignQuestion{
		Design: leftDesign, Contract: contractIDL, Text: "Which path?",
		Options:        []string{"Synthetic option one.", "Synthetic option two."},
		Recommendation: "Synthetic option one.", Asker: contractWriterA, Asked: time.Unix(contractTimeA, 0),
	}); err != nil {
		t.Fatalf("AskDesignQuestion: %v", err)
	}
	if _, _, err := st.WriteContractRow(ctx, store.ContractRowChange{
		Contract: contractIDL, Row: "R2", Action: store.RowActionWrite, Words: "Add a requirement",
		Session: contractWriterA, At: contractTimeA + 5,
	}, func(current store.ContractBody) (store.ContractBody, error) {
		return current, nil
	}); err != nil {
		t.Fatalf("WriteContractRow R2: %v", err)
	}
	handler := designsAnswerHandler(t, store.NewReader(path), serveStageEngine(t, st))
	contracts := contractsClient(t, handler)
	designs := designsClient(t, handler)
	get := func() *orreryv1.ContractsGetResponse {
		t.Helper()
		got, err := contracts.Get(ctx, connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDL}))
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		return got.Msg
	}

	assertServeLeftAndShown(t, get(), []*orreryv1.ContractLeft{
		serveLeftOf(store.LeftQuestion, "Q1"),
		serveLeftOf(store.LeftAddition, "A1"),
		serveLeftOf(store.LeftRevising, contractIDL),
		serveLeftOf(store.LeftNotShown, "S1"),
		serveLeftOf(store.LeftNotShown, "S2"),
	}, nil)

	if _, err := designs.Answer(ctx, connect.NewRequest(&orreryv1.DesignsAnswerRequest{
		Design: leftDesign, Question: 1, Answer: "Synthetic option one.",
	})); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	assertServeLeftAndShown(t, get(), []*orreryv1.ContractLeft{
		serveLeftOf(store.LeftAddition, "A1"),
		serveLeftOf(store.LeftRevising, contractIDL),
		serveLeftOf(store.LeftNotShown, "S1"),
		serveLeftOf(store.LeftNotShown, "S2"),
	}, nil)

	// The working copy is proposed as version 2, which leaves the addition undecided and the release steps unshown.
	working, err := st.ContractWorkingCopy(ctx, contractIDL)
	if err != nil {
		t.Fatalf("ContractWorkingCopy: %v", err)
	}
	if err = st.ReviseContract(ctx, contractIDL, store.ContractVersion{
		Version: 2, Repository: "/synthetic/repo", Title: "Synthetic left", Care: "severe", Body: working.Body,
		Decisions: working.Decisions, Words: "Propose the working copy.", Writer: contractWriterA,
		At: contractTimeA + 10,
	}); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
	assertServeLeftAndShown(t, get(), []*orreryv1.ContractLeft{
		serveLeftOf(store.LeftAddition, "A1"),
		serveLeftOf(store.LeftNotShown, "S1"),
		serveLeftOf(store.LeftNotShown, "S2"),
	}, nil)

	if _, err = contracts.Keep(ctx, connect.NewRequest(&orreryv1.ContractsKeepRequest{
		Id: contractIDL, Addition: "A1", Words: "Keep the first.",
	})); err != nil {
		t.Fatalf("Keep: %v", err)
	}
	assertServeLeftAndShown(t, get(), []*orreryv1.ContractLeft{
		serveLeftOf(store.LeftNotShown, "S1"),
		serveLeftOf(store.LeftNotShown, "S2"),
	}, nil)

	shownAt := designAnswerTime
	if _, err = contracts.Shown(ctx, connect.NewRequest(&orreryv1.ContractsShownRequest{
		Id: contractIDL, Version: 2, Rows: []string{"S1"},
	})); err != nil {
		t.Fatalf("Shown: %v", err)
	}
	shown := []*orreryv1.ContractShown{{Version: 2, Row: "S1", Time: shownAt}}
	assertServeLeftAndShown(t, get(), []*orreryv1.ContractLeft{
		serveLeftOf(store.LeftNotShown, "S2"),
	}, shown)
	assertServeLeftAndShown(t, get(), []*orreryv1.ContractLeft{
		serveLeftOf(store.LeftNotShown, "S2"),
	}, shown)
}

// TestContractRecordsLeftEngineGetCarriesLeftAndShown checks that orreryd's GetContract answers what is left before a
// severe contract is accepted, and the rows the owner was shown, as the store holds them.
func TestContractRecordsLeftEngineGetCarriesLeftAndShown(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, _ := openContractStore(t)
	proposal := contractTestVersion(t, leftServeBody(), 1, contractWriterA, "Synthetic proposal.", contractTimeA, nil)
	if err := st.ProposeContract(ctx, contractIDL, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	if err := st.MarkContractShown(ctx, contractIDL, 1, []string{"S1"}, contractTimeA+20); err != nil {
		t.Fatalf("MarkContractShown: %v", err)
	}
	client := serveContractEngine(t, st)

	got, err := client.GetContract(ctx, connect.NewRequest(&enginev1.GetContractRequest{Id: contractIDL}))
	if err != nil {
		t.Fatalf("GetContract: %v", err)
	}
	want := &enginev1.StoredContract{
		Left: []*enginev1.ContractLeft{
			{Kind: store.LeftAddition, Ref: "A1", Text: "addition neither kept nor struck"},
			{Kind: store.LeftNotShown, Ref: "S2", Text: "release step not yet shown to the owner"},
		},
		Shown: []*enginev1.ContractShown{{Version: 1, Row: "S1", Time: contractTimeA + 20}},
	}
	gotStored := &enginev1.StoredContract{
		Left:  got.Msg.GetContract().GetLeft(),
		Shown: got.Msg.GetContract().GetShown(),
	}
	if !proto.Equal(gotStored, want) {
		t.Fatalf("GetContract left and shown = %v, want %v", gotStored, want)
	}
}
