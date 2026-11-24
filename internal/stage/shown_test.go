package stage

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// shownBody returns the body the shown tests propose: a severe contract with two release steps and no additions.
func shownBody() *v1.EpicContract {
	return &v1.EpicContract{
		Title: "Shown contract", Care: "severe", Intent: "Synthetic intent.",
		ReleaseSteps: []*v1.EpicReleaseStep{
			{Step: "Ship the build", Target: "origin", Undo: "Revert the merge"},
			{Step: "Tag the release", Target: "origin", Undo: "Delete the tag"},
		},
	}
}

// shownFrom calls Shown with msg from session, or from no session when session is "", and returns the reply it gives,
// or the error it returns.
func shownFrom(
	t *testing.T,
	fx stageResearchFixture,
	session string,
	msg *v1.ShownRequest,
) (*v1.ShownResponse, error) {
	t.Helper()
	req := connect.NewRequest(msg)
	if session != "" {
		req.Header().Set(protocol.HeaderSession, session)
	}
	reply, err := fx.service.Shown(t.Context(), req)
	if err != nil {
		return nil, err
	}
	return reply.Msg, nil
}

// shownMarksOf returns the shown marks of rowsContract in the store of fx, in the order the store lists them.
func shownMarksOf(t *testing.T, fx stageResearchFixture) []store.ContractShown {
	t.Helper()
	marks, err := fx.store.ContractShown(t.Context(), rowsContract)
	if err != nil {
		t.Fatalf("ContractShown: %v", err)
	}
	return marks
}

// TestRecordsLeftShownStoresOneMarkPerRowAndAnswersThem checks that Shown from no session marks each row it names once,
// answers the rows it named, and keeps the first mark of a row a later call marks again.
func TestRecordsLeftShownStoresOneMarkPerRowAndAnswersThem(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	proposeRowsContract(t, fx, shownBody())
	at := stageResearchNow().Unix()

	reply, err := shownFrom(t, fx, "", &v1.ShownRequest{
		Contract: rowsContract, Version: 1, Rows: []string{"S1", "S1", "S2"},
	})
	if err != nil {
		t.Fatalf("Shown S1 S2: %v", err)
	}
	if want := []string{"S1", "S2"}; !slices.Equal(reply.GetRows(), want) {
		t.Fatalf("Shown answered %q, want %q: each row once, in the order named", reply.GetRows(), want)
	}
	want := []store.ContractShown{
		{Contract: rowsContract, Version: 1, Row: "S1", At: at},
		{Contract: rowsContract, Version: 1, Row: "S2", At: at},
	}
	if got := shownMarksOf(t, fx); !reflect.DeepEqual(got, want) {
		t.Fatalf("shown marks = %+v, want %+v", got, want)
	}

	// A later call marks S1 again at a later time: it is answered, and the store keeps the first mark.
	later := NewService(Config{
		Store: fx.store, Now: func() time.Time { return stageResearchNow().Add(time.Hour) },
	})
	again, err := later.Shown(t.Context(), connect.NewRequest(&v1.ShownRequest{
		Contract: rowsContract, Version: 1, Rows: []string{"S1"},
	}))
	if err != nil {
		t.Fatalf("Shown S1 again: %v", err)
	}
	if got := again.Msg.GetRows(); !slices.Equal(got, []string{"S1"}) {
		t.Fatalf("Shown again answered %q, want S1", got)
	}
	if got := shownMarksOf(t, fx); !reflect.DeepEqual(got, want) {
		t.Fatalf("shown marks after marking S1 again = %+v, want the first marks kept: %+v", got, want)
	}
}

// TestRecordsLeftShownRefusesASessionCaller checks that Shown from a session is refused as permission denied, and
// stores no mark, since only the owner through serve shows the owner a row.
func TestRecordsLeftShownRefusesASessionCaller(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	proposeRowsContract(t, fx, shownBody())

	_, err := shownFrom(t, fx, rowsSession, &v1.ShownRequest{
		Contract: rowsContract, Version: 1, Rows: []string{"S1"},
	})
	requireRefusal(t, "Shown from a session", err, connect.CodePermissionDenied, "session")
	if marks := shownMarksOf(t, fx); len(marks) != 0 {
		t.Fatalf("shown marks after a refused call = %+v, want none", marks)
	}
}

// TestRecordsLeftShownRefusesAVersionThatIsNotTheNewest checks that Shown of a version before the newest is refused as
// a failed precondition that names the newest, that the newest version is marked, and that nothing is marked for the
// other.
func TestRecordsLeftShownRefusesAVersionThatIsNotTheNewest(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	proposeRowsContract(t, fx, shownBody())
	encoded, err := protojson.Marshal(shownBody())
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	if err = fx.store.ReviseContract(t.Context(), rowsContract, store.ContractVersion{
		Version: 2, Repository: "/repo", Title: "Shown contract", Care: "severe", Body: string(encoded),
		Words: "Revise the contract", Writer: rowsSession, At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}

	_, err = shownFrom(t, fx, "", &v1.ShownRequest{Contract: rowsContract, Version: 1, Rows: []string{"S1"}})
	requireRefusal(t, "Shown of version 1", err, connect.CodeFailedPrecondition, "is not the newest, which is 2")
	if marks := shownMarksOf(t, fx); len(marks) != 0 {
		t.Fatalf("shown marks after a refused version = %+v, want none", marks)
	}

	reply, err := shownFrom(t, fx, "", &v1.ShownRequest{Contract: rowsContract, Version: 2, Rows: []string{"S1"}})
	if err != nil {
		t.Fatalf("Shown of version 2: %v", err)
	}
	if got := reply.GetRows(); !slices.Equal(got, []string{"S1"}) {
		t.Fatalf("Shown of version 2 answered %q, want S1", got)
	}
	want := []store.ContractShown{{Contract: rowsContract, Version: 2, Row: "S1", At: stageResearchNow().Unix()}}
	if got := shownMarksOf(t, fx); !reflect.DeepEqual(got, want) {
		t.Fatalf("shown marks = %+v, want %+v", got, want)
	}
}

// TestRecordsLeftShownRefusesRowsThatAreNotReleaseSteps checks that a row that is not a release step the version holds
// is refused as an invalid argument that names it, and that a call with one such row marks none of its other rows.
func TestRecordsLeftShownRefusesRowsThatAreNotReleaseSteps(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	proposeRowsContract(t, fx, shownBody())

	_, err := shownFrom(t, fx, "", &v1.ShownRequest{
		Contract: rowsContract, Version: 1, Rows: []string{"S1", "S3"},
	})
	requireRefusal(t, "Shown of S3", err, connect.CodeInvalidArgument, "row S3")
	_, err = shownFrom(t, fx, "", &v1.ShownRequest{Contract: rowsContract, Version: 1, Rows: []string{"R1"}})
	requireRefusal(t, "Shown of R1", err, connect.CodeInvalidArgument, "row R1")
	if marks := shownMarksOf(t, fx); len(marks) != 0 {
		t.Fatalf("shown marks after refused rows = %+v, want none, S1 included", marks)
	}
}

// TestRecordsLeftShownRefusesAContractThatIsNotProposed checks that Shown of an accepted contract and of a draft is
// refused as a failed precondition named state, and that Shown of an id no one holds is not found.
func TestRecordsLeftShownRefusesAContractThatIsNotProposed(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedRowsSession(t, fx)
	proposeRowsContract(t, fx, shownBody())
	if err := fx.store.AcceptContract(t.Context(), rowsContract, store.ContractChange{
		Words: "Accept the contract", At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}
	_, err := shownFrom(t, fx, "", &v1.ShownRequest{Contract: rowsContract, Version: 1, Rows: []string{"S1"}})
	requireRefusal(t, "Shown of an accepted contract", err, connect.CodeFailedPrecondition, "state: not proposed")

	const draft = "contract-shown-draft"
	if _, err = writeRowFrom(t, fx, rowsSession, &v1.WriteRowRequest{
		Contract: draft, Row: "R1", Value: `{"text":"Draft requirement","evidence":"Test passes","kind":"test"}`,
		Words: "Draft requirement", Maker: conversationMaker(),
	}); err != nil {
		t.Fatalf("WriteRow of the draft: %v", err)
	}
	_, err = shownFrom(t, fx, "", &v1.ShownRequest{Contract: draft, Version: 1, Rows: []string{"S1"}})
	requireRefusal(t, "Shown of a draft", err, connect.CodeFailedPrecondition, "state: not proposed")

	_, err = shownFrom(t, fx, "", &v1.ShownRequest{
		Contract: "contract-shown-missing", Version: 1, Rows: []string{"S1"},
	})
	requireRefusal(t, "Shown of an unknown contract", err, connect.CodeNotFound, "contract-shown-missing")
}
