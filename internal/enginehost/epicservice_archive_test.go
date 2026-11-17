package enginehost

import (
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// archiveMarksOf returns the archive marks the store at path lists, as a store.Reader reads them.
func archiveMarksOf(t *testing.T, path string) []store.ArchiveMark {
	t.Helper()
	marks, err := store.NewReader(path).ArchivedRows(t.Context())
	if err != nil {
		t.Fatalf("ArchivedRows: %v", err)
	}
	return marks
}

func TestEpicArchiveRowsReaderShowsArchivedSession(t *testing.T) {
	t.Parallel()
	st, path := openStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))

	_, err := client.ArchiveRow(t.Context(), connect.NewRequest(&v1.ArchiveRowRequest{
		Kind: "session", Id: "synthetic-session",
	}))
	if err != nil {
		t.Fatalf("ArchiveRow: %v", err)
	}
	marks := archiveMarksOf(t, path)
	if len(marks) != 1 || marks[0].Kind != "session" || marks[0].ID != "synthetic-session" {
		t.Fatalf("archived rows = %v, want the one session synthetic-session", marks)
	}
	if marks[0].ArchivedAt.IsZero() || !marks[0].UnarchivedAt.IsZero() {
		t.Fatalf("mark = %v, want archived and not unarchived", marks[0])
	}
}

func TestEpicArchiveRowsUnarchiveClearsMarkAndRecordsTime(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))

	if _, err := client.ArchiveRow(ctx, connect.NewRequest(&v1.ArchiveRowRequest{
		Kind: "epic", Id: epicServiceID,
	})); err != nil {
		t.Fatalf("ArchiveRow: %v", err)
	}
	if _, err := client.UnarchiveRow(ctx, connect.NewRequest(&v1.UnarchiveRowRequest{
		Kind: "epic", Id: epicServiceID,
	})); err != nil {
		t.Fatalf("UnarchiveRow: %v", err)
	}
	marks := archiveMarksOf(t, path)
	if len(marks) != 1 || marks[0].ID != epicServiceID {
		t.Fatalf("archived rows = %v, want the one epic %s", marks, epicServiceID)
	}
	if !marks[0].ArchivedAt.IsZero() || marks[0].UnarchivedAt.IsZero() {
		t.Fatalf("mark = %v, want not archived and an unarchive time", marks[0])
	}
}

func TestEpicArchiveRowsRecordOnlyEpicIsArchived(t *testing.T) {
	t.Parallel()
	const recordOnly = "synthetic-repo/record-only"
	st, path := openStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))

	if _, err := client.ArchiveRow(t.Context(), connect.NewRequest(&v1.ArchiveRowRequest{
		Kind: "epic", Id: recordOnly,
	})); err != nil {
		t.Fatalf("ArchiveRow of a record-only epic: %v", err)
	}
	marks := archiveMarksOf(t, path)
	if len(marks) != 1 || marks[0].Kind != "epic" || marks[0].ID != recordOnly || marks[0].ArchivedAt.IsZero() {
		t.Fatalf("archived rows = %v, want the record-only epic %s archived", marks, recordOnly)
	}
}

func TestEpicArchiveRowsRefusesUnknownKindAndEmptyID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st, path := openStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))

	_, err := client.ArchiveRow(ctx, connect.NewRequest(&v1.ArchiveRowRequest{Kind: "pane", Id: "synthetic-pane"}))
	wantConnectError(t, err, connect.CodeInvalidArgument, `kind: "pane" is not epic, session or headless`)
	_, err = client.ArchiveRow(ctx, connect.NewRequest(&v1.ArchiveRowRequest{Kind: "session"}))
	wantConnectError(t, err, connect.CodeInvalidArgument, "id: empty")
	_, err = client.UnarchiveRow(ctx, connect.NewRequest(&v1.UnarchiveRowRequest{Kind: "pane", Id: "synthetic-pane"}))
	wantConnectError(t, err, connect.CodeInvalidArgument, `kind: "pane" is not epic, session or headless`)
	_, err = client.UnarchiveRow(ctx, connect.NewRequest(&v1.UnarchiveRowRequest{Kind: "session"}))
	wantConnectError(t, err, connect.CodeInvalidArgument, "id: empty")

	if marks := archiveMarksOf(t, path); len(marks) != 0 {
		t.Fatalf("archived rows = %v after refused calls, want none", marks)
	}
}
