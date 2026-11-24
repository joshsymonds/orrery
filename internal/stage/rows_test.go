package stage

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// rowsSession is the sessions row id of the design's conversation, which the row tests write from, and rowsContract is the
// contract they write to.
const (
	rowsSession  = "conversation-one"
	rowsContract = "contract-rows"
)

// seedRowsSession adds the sessions row rowsSession, the design's conversation, to the store of fx.
func seedRowsSession(t *testing.T, fx stageResearchFixture) {
	t.Helper()
	add := store.EngineAdd{ID: rowsSession, Dir: "/synthetic/dir"}
	if err := fx.store.ApplyEngine(t.Context(), []store.EngineOp{add}, stageResearchNow()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// conversationMaker returns the maker of a row the design's conversation writes.
func conversationMaker() *v1.StageMaker {
	return &v1.StageMaker{
		Kind:       store.StageMakerConversation,
		Reason:     "The owner decided",
		Delegation: "Decide the rows",
	}
}

// writeRowFrom writes msg through the service of fx, from the session header session, or from no session when session is
// "". A request that names no contract or design names rowsContract and stageDesign. It returns the write WriteRow
// replies with, or the error it returns.
func writeRowFrom(
	t *testing.T,
	fx stageResearchFixture,
	session string,
	msg *v1.WriteRowRequest,
) (*v1.StageRowWrite, error) {
	t.Helper()
	if msg.GetContract() == "" {
		msg.Contract = rowsContract
	}
	if msg.GetDesign() == "" {
		msg.Design = stageDesign
	}
	req := connect.NewRequest(msg)
	if session != "" {
		req.Header().Set(protocol.HeaderSession, session)
	}
	reply, err := fx.service.WriteRow(t.Context(), req)
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetWrite(), nil
}

// writeRowAs writes row of rowsContract from the design's conversation, as its maker, with value and words, and returns the
// write WriteRow replies with.
func writeRowAs(t *testing.T, fx stageResearchFixture, row, value, words string) *v1.StageRowWrite {
	t.Helper()
	write, err := writeRowFrom(t, fx, rowsSession, &v1.WriteRowRequest{
		Row: row, Value: value, Words: words, Maker: conversationMaker(),
	})
	if err != nil {
		t.Fatalf("WriteRow %s: %v", row, err)
	}
	return write
}

// decodedContract returns the EpicContract body holds, decoded from protobuf JSON.
func decodedContract(t *testing.T, body string) *v1.EpicContract {
	t.Helper()
	var contract v1.EpicContract
	if err := protojson.Unmarshal([]byte(body), &contract); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}
	return &contract
}

// draftContract returns the EpicContract body of the draft of rowsContract in the store of fx.
func draftContract(t *testing.T, fx stageResearchFixture) *v1.EpicContract {
	t.Helper()
	draft, err := fx.store.ContractDraft(t.Context(), rowsContract)
	if err != nil {
		t.Fatalf("ContractDraft: %v", err)
	}
	return decodedContract(t, draft.Body)
}

// proposeRowsContract proposes rowsContract into stageDesign with body as its first version.
func proposeRowsContract(t *testing.T, fx stageResearchFixture, body *v1.EpicContract) {
	t.Helper()
	encoded, err := protojson.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	if err = fx.store.ProposeContractInDesign(t.Context(), stageDesign, rowsContract, store.ContractVersion{
		Version: 1, Repository: "/repo", Title: body.GetTitle(), Care: body.GetCare(), Body: string(encoded),
		Words: "Propose the contract", Writer: rowsSession, At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("ProposeContractInDesign: %v", err)
	}
}

// additionValue returns the protobuf JSON of an addition whose what is what.
func additionValue(what string) string {
	return fmt.Sprintf(`{"location":"internal/stage","what":%q,"why":"Needed","cost":"None"}`, what)
}

// draftAdditionWhats returns what each addition of the draft of rowsContract says, in order.
func draftAdditionWhats(t *testing.T, fx stageResearchFixture) []string {
	t.Helper()
	additions := draftContract(t, fx).GetAdditions()
	whats := make([]string, 0, len(additions))
	for _, addition := range additions {
		whats = append(whats, addition.GetWhat())
	}
	return whats
}

func TestRecordsRowsWorkingDraftRowsWrittenSingly(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	at := stageResearchNow().Unix()

	failure := writeRowAs(t, fx, "F1",
		`{"text":"The page drew a stand-in","rating":"serious","response":"prevent"}`, "First failure")
	requirement := writeRowAs(t, fx, "R1",
		`{"text":"Rows are written singly","evidence":"Test passes","kind":"test"}`, "First requirement")

	wantFailure := &v1.StageRowWrite{
		Seq: 1, Row: "F1", Action: store.RowActionWrite, Words: "First failure", Maker: conversationMaker(),
		Session: rowsSession, At: at,
	}
	wantRequirement := &v1.StageRowWrite{
		Seq: 2, Row: "R1", Action: store.RowActionWrite, Words: "First requirement", Maker: conversationMaker(),
		Session: rowsSession, At: at,
	}
	if !proto.Equal(failure, wantFailure) || !proto.Equal(requirement, wantRequirement) {
		t.Fatalf("writes = %v and %v, want %v and %v", failure, requirement, wantFailure, wantRequirement)
	}
	want := &v1.EpicContract{
		Failures: []*v1.EpicFailure{{
			Id: "F1", Text: "The page drew a stand-in", Rating: "serious", Response: "prevent",
		}},
		Requirements: []*v1.EpicRequirement{{
			Id: "R1", Text: "Rows are written singly", Evidence: "Test passes", Kind: "test",
		}},
	}
	if got := draftContract(t, fx); !proto.Equal(got, want) {
		t.Fatalf("draft body = %v, want %v", got, want)
	}
	wantLog := []store.DesignLogLine{
		{
			Design: stageDesign, Seq: 1, At: at, Maker: store.LogMakerConversation, Session: rowsSession,
			What: "row written", IDs: []string{"F1"}, Words: "The owner decided",
		},
		{
			Design: stageDesign, Seq: 2, At: at, Maker: store.LogMakerConversation, Session: rowsSession,
			What: "row written", IDs: []string{"R1"}, Words: "The owner decided",
		},
	}
	if got := storedLog(t, fx); !reflect.DeepEqual(got, wantLog) {
		t.Fatalf("design log = %+v, want %+v", got, wantLog)
	}
}

func TestRecordsRowsWorkingProposedRowsWriteWorkingCopy(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	original := &v1.EpicContract{
		Title: "Contract one", Care: "serious", Intent: "Synthetic intent.",
		Requirements: []*v1.EpicRequirement{{Id: "R1", Text: "The first requirement", Kind: "test"}},
	}
	proposeRowsContract(t, fx, original)

	writeRowAs(t, fx, "title", `"Contract two"`, "Rename it")
	writeRowAs(t, fx, "R2", `{"text":"The second requirement","kind":"test"}`, "Second requirement")
	removed, err := writeRowFrom(t, fx, "", &v1.WriteRowRequest{Row: "R1", Remove: true, Words: "Drop the first"})
	if err != nil {
		t.Fatalf("WriteRow R1 from no session: %v", err)
	}
	if removed.GetAction() != store.RowActionRemove || removed.GetMaker().GetKind() != "" ||
		removed.GetSession() != "" {
		t.Fatalf("removal = %v, want a remove with no maker and no session", removed)
	}

	working, err := fx.store.ContractWorkingCopy(t.Context(), rowsContract)
	if err != nil {
		t.Fatalf("ContractWorkingCopy: %v", err)
	}
	wantBody := &v1.EpicContract{
		Title: "Contract two", Care: "serious", Intent: "Synthetic intent.",
		Requirements: []*v1.EpicRequirement{{Id: "R2", Text: "The second requirement", Kind: "test"}},
	}
	if working.Version != 1 || !proto.Equal(decodedContract(t, working.Body), wantBody) {
		t.Fatalf("working copy = %+v, want version 1 with body %v", working, wantBody)
	}
	if want := []string{"title", "R2", "R1"}; !reflect.DeepEqual(working.ChangedRows, want) {
		t.Fatalf("changed rows = %q, want %q", working.ChangedRows, want)
	}
	record, err := fx.store.Contract(t.Context(), rowsContract)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if len(record.Versions) != 1 || !proto.Equal(decodedContract(t, record.Versions[0].Body), original) {
		t.Fatalf("versions = %+v, want version 1 untouched", record.Versions)
	}
}

func TestRecordsRowsWorkingAdditionAppendedAsA2AndRemoved(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	writeRowAs(t, fx, "A1", additionValue("first"), "First addition")
	writeRowAs(t, fx, "A2", additionValue("second"), "Second addition")
	if got := draftAdditionWhats(t, fx); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("additions = %q, want A2 appended after A1", got)
	}

	removed, err := writeRowFrom(t, fx, rowsSession, &v1.WriteRowRequest{
		Row: "A2", Remove: true, Words: "Strike the second", Maker: conversationMaker(),
	})
	if err != nil {
		t.Fatalf("WriteRow remove A2: %v", err)
	}
	if removed.GetAction() != store.RowActionRemove || removed.GetRow() != "A2" {
		t.Fatalf("removal = %v, want a remove of A2", removed)
	}
	if got := draftAdditionWhats(t, fx); !reflect.DeepEqual(got, []string{"first"}) {
		t.Fatalf("additions after removing A2 = %q, want the first alone", got)
	}

	writeRowAs(t, fx, "A2", additionValue("third"), "Third addition")
	if _, err = writeRowFrom(t, fx, rowsSession, &v1.WriteRowRequest{
		Row: "A1", Remove: true, Words: "Strike the first", Maker: conversationMaker(),
	}); err != nil {
		t.Fatalf("WriteRow remove A1: %v", err)
	}
	if got := draftAdditionWhats(t, fx); !reflect.DeepEqual(got, []string{"third"}) {
		t.Fatalf("additions = %q, want the later addition moved down to A1", got)
	}
}

func TestRecordsRowsWorkingRefusesRowsItCannotWrite(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	cases := []struct {
		name, row, value string
		remove           bool
	}{
		{name: "unknown row", row: "Z1", value: `{}`},
		{name: "row number of zero", row: "F0", value: `{"text":"Zero"}`},
		{name: "unterminated value", row: "F1", value: `{"text":`},
		{name: "value that is not an object", row: "A1", value: `"Just text"`},
		{name: "value naming another id", row: "F1", value: `{"id":"F2","text":"Another"}`},
		{name: "section value naming another field", row: "title", value: `"Renamed","intent":"Injected"`},
		{name: "name that is no section", row: "decisionsList", value: `[]`},
		{name: "removal of an absent requirement", row: "R9", remove: true},
		{name: "removal past the last addition", row: "A5", remove: true},
	}
	for _, tc := range cases {
		_, err := writeRowFrom(t, fx, rowsSession, &v1.WriteRowRequest{
			Row: tc.row, Value: tc.value, Remove: tc.remove, Maker: conversationMaker(),
		})
		if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), tc.row) {
			t.Fatalf("%s: WriteRow = %v, want an invalid argument naming %s", tc.name, err, tc.row)
		}
	}
	if _, err := fx.store.ContractDraft(t.Context(), rowsContract); !errors.Is(err, store.ErrNoDraft) {
		t.Fatalf("refused writes left a draft: %v", err)
	}
}

func TestRecordsRowsWorkingRefusesRowsOnAcceptedContract(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	proposeRowsContract(t, fx, &v1.EpicContract{Title: "Settled", Care: "serious"})
	if err := fx.store.AcceptContract(t.Context(), rowsContract, store.ContractChange{
		Words: "Accept the contract", At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}

	_, err := writeRowFrom(t, fx, rowsSession, &v1.WriteRowRequest{
		Row: "title", Value: `"Changed"`, Maker: conversationMaker(),
	})
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "state: not proposed") {
		t.Fatalf("WriteRow on an accepted contract = %v, want failed precondition state: not proposed", err)
	}
	writes, err := fx.store.ContractRowWrites(t.Context(), rowsContract)
	if err != nil || len(writes) != 0 {
		t.Fatalf("row writes of the accepted contract = %+v, %v, want none", writes, err)
	}
}

func TestRecordsRowsWorkingNewDraftNamesUnknownDesign(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	_, err := writeRowFrom(t, fx, rowsSession, &v1.WriteRowRequest{
		Design: "design-missing", Row: "title", Value: `"Draft"`, Maker: conversationMaker(),
	})
	if connect.CodeOf(err) != connect.CodeNotFound || !strings.Contains(err.Error(), "design-missing") {
		t.Fatalf("WriteRow into an unknown design = %v, want not found naming the design", err)
	}
}
