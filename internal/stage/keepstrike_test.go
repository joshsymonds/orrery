package stage

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// keepStrikeQuote is the owner's words a strike quotes in the tests that strike from the conversation.
const keepStrikeQuote = "Strike it."

// keepStrikeProposal proposes rowsContract into stageDesign with the additions A1 and A2 and no rows.
func keepStrikeProposal(t *testing.T, fx stageResearchFixture) {
	t.Helper()
	proposeRowsContract(t, fx, keepStrikeBody())
}

// keepStrikeBody returns the body keepStrikeProposal proposes: two additions and no rows.
func keepStrikeBody() *v1.EpicContract {
	return &v1.EpicContract{
		Title: "Contract one", Care: "serious", Intent: "Synthetic intent.",
		Additions: []*v1.EpicAddition{
			{Location: "internal/stage", What: "first", Why: "Needed", Cost: "None"},
			{Location: "internal/store", What: "second", Why: "Needed", Cost: "None"},
		},
	}
}

// keepFrom keeps msg from rowsSession, the design's conversation, and returns the write Keep replies with, or the error
// it returns.
func keepFrom(t *testing.T, fx stageResearchFixture, msg *v1.KeepRequest) (*v1.StageRowWrite, error) {
	t.Helper()
	req := connect.NewRequest(msg)
	req.Header().Set(protocol.HeaderSession, rowsSession)
	reply, err := fx.service.Keep(t.Context(), req)
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetWrite(), nil
}

// strikeFrom strikes msg from rowsSession, the design's conversation, and returns the reply Strike gives, or the error
// it returns.
func strikeFrom(t *testing.T, fx stageResearchFixture, msg *v1.StrikeRequest) (*v1.StrikeResponse, error) {
	t.Helper()
	req := connect.NewRequest(msg)
	req.Header().Set(protocol.HeaderSession, rowsSession)
	reply, err := fx.service.Strike(t.Context(), req)
	if err != nil {
		return nil, err
	}
	return reply.Msg, nil
}

// keepStrikeOwnerMaker returns the maker of a strike the owner made in the conversation, quoting keepStrikeQuote.
func keepStrikeOwnerMaker() *v1.StageMaker {
	return &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: keepStrikeQuote}
}

// mustWriteRow writes msg from rowsSession, the design's conversation, and fails the test when the write is refused.
func mustWriteRow(t *testing.T, fx stageResearchFixture, msg *v1.WriteRowRequest) {
	t.Helper()
	if _, err := writeRowFrom(t, fx, rowsSession, msg); err != nil {
		t.Fatalf("WriteRow %s: %v", msg.GetRow(), err)
	}
}

// workingCopyOf returns the working copy of rowsContract in the store of fx.
func workingCopyOf(t *testing.T, fx stageResearchFixture) store.ContractWorkingCopy {
	t.Helper()
	working, err := fx.store.ContractWorkingCopy(t.Context(), rowsContract)
	if err != nil {
		t.Fatalf("ContractWorkingCopy: %v", err)
	}
	return working
}

// rowWritesOf returns the row writes of rowsContract in the store of fx, oldest first.
func rowWritesOf(t *testing.T, fx stageResearchFixture) []store.ContractRowWrite {
	t.Helper()
	writes, err := fx.store.ContractRowWrites(t.Context(), rowsContract)
	if err != nil {
		t.Fatalf("ContractRowWrites: %v", err)
	}
	return writes
}

// requireRefusal fails the test unless err is refused with code and its text contains each of the words.
func requireRefusal(t *testing.T, what string, err error, code connect.Code, words ...string) {
	t.Helper()
	if got := connect.CodeOf(err); got != code {
		t.Fatalf("%s = %v, want code %v", what, err, code)
	}
	for _, word := range words {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("%s = %v, want its text to name %q", what, err, word)
		}
	}
}

func TestRecordsKeepStrikeKeepByConversationStoresMaker(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	keepStrikeProposal(t, fx)
	at := stageResearchNow().Unix()

	write, err := keepFrom(t, fx, &v1.KeepRequest{
		Contract: rowsContract, Addition: "A1", Words: "Keep the first", Maker: conversationMaker(),
	})
	if err != nil {
		t.Fatalf("Keep A1: %v", err)
	}
	wantWrite := &v1.StageRowWrite{
		Seq: 1, Version: 1, Row: "A1", Action: store.RowActionKeep, Words: "Keep the first", Maker: conversationMaker(),
		Session: rowsSession, At: at,
	}
	if !proto.Equal(write, wantWrite) {
		t.Fatalf("keep = %v, want %v", write, wantWrite)
	}
	wantStored := []store.ContractRowWrite{{
		Contract: rowsContract, Seq: 1, Version: 1, Row: "A1", Action: store.RowActionKeep, Words: "Keep the first",
		Maker: store.StageMakerConversation, MakerReason: "The owner decided", Delegation: "Decide the rows",
		Session: rowsSession, At: at,
	}}
	if got := rowWritesOf(t, fx); !reflect.DeepEqual(got, wantStored) {
		t.Fatalf("row writes = %+v, want %+v", got, wantStored)
	}
	wantLog := []store.DesignLogLine{{
		Design: stageDesign, Seq: 1, At: at, Maker: store.LogMakerConversation, Session: rowsSession,
		What: "kept", IDs: []string{"A1"}, Words: "The owner decided",
	}}
	if got := storedLog(t, fx); !reflect.DeepEqual(got, wantLog) {
		t.Fatalf("design log = %+v, want %+v", got, wantLog)
	}
	if _, err = fx.store.ContractWorkingCopy(t.Context(), rowsContract); !errors.Is(err, store.ErrNoWorking) {
		t.Fatalf("ContractWorkingCopy after a keep = %v, want none: a keep writes the newest version", err)
	}
}

func TestRecordsKeepStrikeStrikeByOwnerConversationStoresMaker(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	keepStrikeProposal(t, fx)
	at := stageResearchNow().Unix()

	reply, err := strikeFrom(t, fx, &v1.StrikeRequest{
		Contract: rowsContract, Addition: "A2", Words: "Strike the second", Maker: keepStrikeOwnerMaker(),
	})
	if err != nil {
		t.Fatalf("Strike A2: %v", err)
	}
	wantWrite := &v1.StageRowWrite{
		Seq: 1, Version: 1, Row: "A2", Action: store.RowActionStrike, Words: "Strike the second",
		Maker: keepStrikeOwnerMaker(), Session: rowsSession, At: at,
	}
	if !proto.Equal(reply.GetWrite(), wantWrite) || len(reply.GetRemoved()) != 0 {
		t.Fatalf("strike = %v removing %v, want %v removing none", reply.GetWrite(), reply.GetRemoved(), wantWrite)
	}
	wantStored := []store.ContractRowWrite{{
		Contract: rowsContract, Seq: 1, Version: 1, Row: "A2", Action: store.RowActionStrike,
		Words: "Strike the second", Maker: store.StageMakerOwnerConversation, Quote: keepStrikeQuote,
		Session: rowsSession, At: at,
	}}
	if got := rowWritesOf(t, fx); !reflect.DeepEqual(got, wantStored) {
		t.Fatalf("row writes = %+v, want %+v", got, wantStored)
	}
	working := workingCopyOf(t, fx)
	wantBody := keepStrikeBody()
	wantBody.Additions = wantBody.Additions[:1]
	if !proto.Equal(decodedContract(t, working.Body), wantBody) || !slices.Equal(working.ChangedRows, []string{"A2"}) {
		t.Fatalf("working copy = %+v, want the first addition alone and A2 changed", working)
	}
	wantLog := []store.DesignLogLine{{
		Design: stageDesign, Seq: 1, At: at, Maker: store.LogMakerOwner, Session: rowsSession,
		What: "struck", IDs: []string{"A2"}, Words: "Strike the second",
	}}
	if got := storedLog(t, fx); !reflect.DeepEqual(got, wantLog) {
		t.Fatalf("design log = %+v, want %+v", got, wantLog)
	}
}

func TestRecordsKeepStrikeStrikeRemovesRowsThatDependOnAddition(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	keepStrikeProposal(t, fx)
	mustWriteRow(t, fx, &v1.WriteRowRequest{
		Row: "R1", Value: `{"text":"Depends on the first","evidence":"Test passes","kind":"test"}`, Depends: "A1",
		Words: "Depends on the first", Maker: conversationMaker(),
	})
	writeRowAs(t, fx, "R2", `{"text":"Stands alone","evidence":"Test passes","kind":"test"}`, "Stands alone")

	reply, err := strikeFrom(t, fx, &v1.StrikeRequest{
		Contract: rowsContract, Addition: "A1", Words: "Strike the first", Maker: keepStrikeOwnerMaker(),
	})
	if err != nil {
		t.Fatalf("Strike A1: %v", err)
	}
	if got := reply.GetRemoved(); !slices.Equal(got, []string{"R1"}) {
		t.Fatalf("Strike removed %q, want the row that depends on A1", got)
	}
	wantBody := &v1.EpicContract{
		Title: "Contract one", Care: "serious", Intent: "Synthetic intent.",
		Additions:    []*v1.EpicAddition{{Location: "internal/store", What: "second", Why: "Needed", Cost: "None"}},
		Requirements: []*v1.EpicRequirement{{Id: "R2", Text: "Stands alone", Evidence: "Test passes", Kind: "test"}},
	}
	if got := decodedContract(t, workingCopyOf(t, fx).Body); !proto.Equal(got, wantBody) {
		t.Fatalf("working copy body = %v, want %v: A1 and R1 gone, A2 moved down, R2 kept", got, wantBody)
	}
	lines := storedLog(t, fx)
	if len(lines) == 0 || lines[len(lines)-1].What != "struck" ||
		!slices.Equal(lines[len(lines)-1].IDs, []string{"A1", "R1"}) {
		t.Fatalf("design log = %+v, want its last line to strike A1 and R1", lines)
	}
	writes := rowWritesOf(t, fx)
	if len(writes) == 0 || writes[len(writes)-1].Row != "R1" || writes[len(writes)-1].Action != store.RowActionRemove {
		t.Fatalf("row writes = %+v, want a last remove of R1", writes)
	}
}

// TestRecordsKeepStrikeStrikeRemovesPositionalDependentsHighestFirst checks that a strike removes an addition that
// another addition depends on along with it, and leaves the addition after them in place. The additions are positions,
// so the dependent A2 is removed before A1, and the third addition is the one left.
func TestRecordsKeepStrikeStrikeRemovesPositionalDependentsHighestFirst(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	keepStrikeProposal(t, fx)
	writeRowAs(t, fx, "A3", additionValue("third"), "Third addition")
	mustWriteRow(t, fx, &v1.WriteRowRequest{
		Row: "A2", Value: additionValue("second"), Depends: "A1", Words: "Second depends on the first",
		Maker: conversationMaker(),
	})

	reply, err := strikeFrom(t, fx, &v1.StrikeRequest{
		Contract: rowsContract, Addition: "A1", Words: "Strike the first", Maker: keepStrikeOwnerMaker(),
	})
	if err != nil {
		t.Fatalf("Strike A1: %v", err)
	}
	if got := reply.GetRemoved(); !slices.Equal(got, []string{"A2"}) {
		t.Fatalf("Strike removed %q, want the addition that depends on A1", got)
	}
	additions := decodedContract(t, workingCopyOf(t, fx).Body).GetAdditions()
	whats := make([]string, 0, len(additions))
	for _, addition := range additions {
		whats = append(whats, addition.GetWhat())
	}
	if want := []string{"third"}; !reflect.DeepEqual(whats, want) {
		t.Fatalf("working additions = %q, want %q", whats, want)
	}
}

func TestRecordsKeepStrikeStrikeLeavesRowsWhoseNewestWriteDoesNotDepend(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	keepStrikeProposal(t, fx)
	// R3 named A1 once and then named nothing, so its newest write does not depend on A1. R4 named A1 and was removed, so
	// no row of the body depends on A1 when it is struck.
	mustWriteRow(t, fx, &v1.WriteRowRequest{
		Row: "R3", Value: `{"text":"Once depended","evidence":"Test passes","kind":"test"}`, Depends: "A1",
		Words: "Once depended", Maker: conversationMaker(),
	})
	mustWriteRow(t, fx, &v1.WriteRowRequest{
		Row: "R3", Value: `{"text":"Once depended","evidence":"Test passes","kind":"test"}`,
		Words: "Depends on nothing now", Maker: conversationMaker(),
	})
	mustWriteRow(t, fx, &v1.WriteRowRequest{
		Row: "R4", Value: `{"text":"Removed","evidence":"Test passes","kind":"test"}`, Depends: "A1",
		Words: "Depends on the first", Maker: conversationMaker(),
	})
	mustWriteRow(t, fx, &v1.WriteRowRequest{
		Row: "R4", Remove: true, Words: "Remove it", Maker: conversationMaker(),
	})

	reply, err := strikeFrom(t, fx, &v1.StrikeRequest{
		Contract: rowsContract, Addition: "A1", Words: "Strike the first", Maker: keepStrikeOwnerMaker(),
	})
	if err != nil {
		t.Fatalf("Strike A1: %v", err)
	}
	if got := reply.GetRemoved(); len(got) != 0 {
		t.Fatalf("Strike removed %q, want no row: none's newest write depends on A1", got)
	}
	body := decodedContract(t, workingCopyOf(t, fx).Body)
	if len(body.GetRequirements()) != 1 || body.GetRequirements()[0].GetId() != "R3" {
		t.Fatalf("requirements = %v, want R3 alone", body.GetRequirements())
	}
}

func TestRecordsKeepStrikeKeepRefusedWithoutItsQuote(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	keepStrikeProposal(t, fx)

	_, err := keepFrom(t, fx, &v1.KeepRequest{
		Contract: rowsContract, Addition: "A1", Words: "Keep the first",
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation},
	})
	requireRefusal(t, "Keep without a quote", err, connect.CodeInvalidArgument, "quote")
	if writes := rowWritesOf(t, fx); len(writes) != 0 {
		t.Fatalf("row writes after a refused keep = %+v, want none", writes)
	}
	if lines := storedLog(t, fx); len(lines) != 0 {
		t.Fatalf("design log after a refused keep = %+v, want none", lines)
	}
}

func TestRecordsKeepStrikeRefusesAdditionsTheContractDoesNotHold(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	keepStrikeProposal(t, fx)

	_, err := keepFrom(t, fx, &v1.KeepRequest{
		Contract: rowsContract, Addition: "A5", Words: "Keep the fifth", Maker: conversationMaker(),
	})
	requireRefusal(t, "Keep A5", err, connect.CodeInvalidArgument, "A5")
	_, err = keepFrom(t, fx, &v1.KeepRequest{
		Contract: rowsContract, Addition: "F1", Words: "Keep a failure", Maker: conversationMaker(),
	})
	requireRefusal(t, "Keep F1", err, connect.CodeInvalidArgument, "F1")
	_, err = strikeFrom(t, fx, &v1.StrikeRequest{
		Contract: rowsContract, Addition: "A5", Words: "Strike the fifth", Maker: keepStrikeOwnerMaker(),
	})
	requireRefusal(t, "Strike A5", err, connect.CodeInvalidArgument, "A5")
	if _, err = fx.store.ContractWorkingCopy(t.Context(), rowsContract); !errors.Is(err, store.ErrNoWorking) {
		t.Fatalf("refused strike left a working copy: %v", err)
	}
	if writes := rowWritesOf(t, fx); len(writes) != 0 {
		t.Fatalf("row writes after refused calls = %+v, want none", writes)
	}
}

func TestRecordsKeepStrikeKeepsAdditionAddedInWorkingCopy(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	keepStrikeProposal(t, fx)
	// A3 is added to the working copy only, so the newest version does not hold it but the working copy does.
	writeRowAs(t, fx, "A3", additionValue("third"), "Third addition")

	write, err := keepFrom(t, fx, &v1.KeepRequest{
		Contract: rowsContract, Addition: "A3", Words: "Keep the third", Maker: conversationMaker(),
	})
	if err != nil {
		t.Fatalf("Keep A3: %v", err)
	}
	if write.GetRow() != "A3" || write.GetAction() != store.RowActionKeep || write.GetVersion() != 1 {
		t.Fatalf("keep = %v, want a keep of A3 at version 1", write)
	}
}

func TestRecordsKeepStrikeRefusesContractNotProposed(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	keepStrikeProposal(t, fx)
	if err := fx.store.AcceptContract(t.Context(), rowsContract, store.ContractChange{
		Words: "Accept the contract", At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}

	_, err := keepFrom(t, fx, &v1.KeepRequest{
		Contract: rowsContract, Addition: "A1", Words: "Keep the first", Maker: conversationMaker(),
	})
	requireRefusal(t, "Keep on an accepted contract", err, connect.CodeFailedPrecondition, "state: not proposed")
	_, err = strikeFrom(t, fx, &v1.StrikeRequest{
		Contract: rowsContract, Addition: "A1", Words: "Strike the first", Maker: keepStrikeOwnerMaker(),
	})
	requireRefusal(t, "Strike on an accepted contract", err, connect.CodeFailedPrecondition, "state: not proposed")
	if writes := rowWritesOf(t, fx); len(writes) != 0 {
		t.Fatalf("row writes of the accepted contract = %+v, want none", writes)
	}
}

func TestRecordsKeepStrikeRefusesDraftAndUnknownContract(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	writeRowAs(t, fx, "A1", additionValue("first"), "First addition")

	_, err := keepFrom(t, fx, &v1.KeepRequest{
		Contract: rowsContract, Addition: "A1", Words: "Keep the first", Maker: conversationMaker(),
	})
	requireRefusal(t, "Keep on a draft", err, connect.CodeFailedPrecondition, "state: not proposed")
	_, err = keepFrom(t, fx, &v1.KeepRequest{
		Contract: "contract-missing", Addition: "A1", Words: "Keep the first", Maker: conversationMaker(),
	})
	requireRefusal(t, "Keep on an unknown contract", err, connect.CodeNotFound, "contract-missing")
}

func TestRecordsKeepStrikeRefusesMissingFields(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	keepStrikeProposal(t, fx)

	_, err := keepFrom(
		t,
		fx,
		&v1.KeepRequest{Contract: rowsContract, Words: "Keep nothing", Maker: conversationMaker()},
	)
	requireRefusal(t, "Keep with no addition", err, connect.CodeInvalidArgument, "addition")
	_, err = strikeFrom(t, fx, &v1.StrikeRequest{
		Contract: rowsContract, Addition: "A1", Words: "Strike the first",
		Maker: &v1.StageMaker{Kind: store.StageMakerConversation, Reason: "The owner left it"},
	})
	requireRefusal(t, "Strike without a delegation", err, connect.CodeInvalidArgument, "delegation")
}
