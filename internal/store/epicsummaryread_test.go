package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// summaryReadTextSize is the size of the intent and the workflow text each epic of seedSummaryReadEpics holds.
const summaryReadTextSize = 64 << 10

// summaryReadEpics is the number of epics seedSummaryReadEpics stores.
const summaryReadEpics = 4

// seedSummaryReadEpics stores summaryReadEpics epics, each with a summaryReadTextSize intent and workflow text and its
// own created and changed times. Every even epic is seen, at a time of its own, and every odd epic is never seen.
func seedSummaryReadEpics(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	text := strings.Repeat("x", summaryReadTextSize)
	for n := range summaryReadEpics {
		row := fullEpic(fmt.Sprintf("orrery-1a2b3c4/summary-read-%d", n))
		row.Intent = text
		row.Created = time.Unix(1700000000+int64(10*n), 0)
		row.Changed = time.Unix(1700000060+int64(10*n), 0)
		row.Workflow = EpicWorkflow{
			Name: "summary", Text: text, SHA256: "sum",
			Nodes: []EpicWorkflowNode{{ID: fmt.Sprintf("node-%d", n), Kind: "plan"}},
		}
		createEpic(t, s, row)
		if n%2 == 0 {
			if err := s.SeeEpic(ctx, row.ID, time.Unix(int64(1700000100+n), 0)); err != nil {
				t.Fatalf("SeeEpic: %v", err)
			}
		}
	}
}

func TestEpicSummaryReadRunsOneQueryWithoutTextColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	seedSummaryReadEpics(t, s)
	recorder := &planRecorder{db: s.db.DB}
	if _, err := readEpicListings(ctx, sqlcgen.New(recorder)); err != nil {
		t.Fatalf("readEpicListings: %v", err)
	}
	if len(recorder.texts) != 1 {
		t.Fatalf("listing issued %d queries, want 1", len(recorder.texts))
	}
	query := strings.ToLower(recorder.texts[0])
	if strings.Contains(query, "select *") {
		t.Errorf("listing query selects every column: %q", recorder.texts[0])
	}
	for _, column := range []string{
		"intent", "approach", "checks", "workflow_text", "summary", "release_then", "approved", "care", "report_",
	} {
		if strings.Contains(query, column) {
			t.Errorf("listing query names %s: %q", column, recorder.texts[0])
		}
	}
}

func TestEpicSummaryReadRowsMatchEpicsRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	seedSummaryReadEpics(t, s)
	listed, err := s.EpicListings(ctx)
	if err != nil {
		t.Fatalf("EpicListings: %v", err)
	}
	full, err := s.Epics(ctx)
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	if len(listed) != summaryReadEpics || len(full) != summaryReadEpics {
		t.Fatalf("EpicListings lists %d epics and Epics %d, want %d each", len(listed), len(full), summaryReadEpics)
	}
	for i, row := range listed {
		want := full[i]
		if row.ID != want.ID || row.Repository != want.Repository || row.Title != want.Title ||
			row.Phase != want.Phase || row.Revision != want.Revision ||
			!row.PhaseAt.Equal(want.PhaseAt) || !row.Created.Equal(want.Created) ||
			!row.Changed.Equal(want.Changed) || !row.Seen.Equal(want.Seen) {
			t.Errorf("listing of epic %s = %+v, want the fields of its epic row %+v", want.ID, row, want)
		}
		if row.Intent != "" || row.Workflow.Text != "" {
			t.Errorf("listing of epic %s holds intent or workflow text, want none", row.ID)
		}
	}
}
