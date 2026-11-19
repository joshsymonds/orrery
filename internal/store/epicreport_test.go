package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// epicReportID is the synthetic epic the report tests store.
const epicReportID = "orrery-5d6e7f8/epic-report"

// endedEpic returns the synthetic epic fullEpic makes for id, in phase.
func endedEpic(id, phase string) EpicRow {
	row := fullEpic(id)
	row.Phase = phase
	return row
}

// syntheticReport returns a report for the phase phase of the synthetic epic.
func syntheticReport(phase string) EpicReport {
	return EpicReport{
		Phase:      phase,
		Shipped:    []string{"R1 Round-trip test", "F2 Trigger guard"},
		NotShipped: []string{"R3 Append once per client id"},
		Why:        "Synthetic reason the work did not ship",
	}
}

// unreportedIDs returns the ids UnreportedEpics lists, in the order it lists them.
func unreportedIDs(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.UnreportedEpics(context.Background())
	if err != nil {
		t.Fatalf("UnreportedEpics: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

// moveEpicPhase sets the phase of epic to phase at a synthetic time later than its creation, as a phase write does.
func moveEpicPhase(t *testing.T, s *Store, id, phase string) {
	t.Helper()
	at := time.Unix(1700000500, 0)
	if err := s.fixtureQueries().SetEpicPhase(context.Background(), sqlcgen.SetEpicPhaseParams{
		Phase: phase, At: at.Unix(), ID: id,
	}); err != nil {
		t.Fatalf("SetEpicPhase %s to %s: %v", id, phase, err)
	}
}

func TestUnreportedEpicsListsEndedEpicsWithoutAReportInIDOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const epicA, epicB, epicC = "orrery-5d6e7f8/epic-a", "orrery-5d6e7f8/epic-b", "orrery-5d6e7f8/epic-c"
	createEpic(t, s, endedEpic(epicB, "released"))
	createEpic(t, s, endedEpic(epicA, "done"))
	createEpic(t, s, endedEpic(epicC, "ended_with_gaps"))
	createEpic(t, s, endedEpic("orrery-5d6e7f8/epic-d", "accepted"))
	createEpic(t, s, endedEpic("orrery-5d6e7f8/epic-e", "released"))
	if err := s.SetEpicReport(ctx, "orrery-5d6e7f8/epic-e", syntheticReport("released")); err != nil {
		t.Fatalf("SetEpicReport: %v", err)
	}

	if got, want := unreportedIDs(t, s), []string{epicA, epicB, epicC}; !slices.Equal(got, want) {
		t.Errorf("UnreportedEpics ids = %v, want %v", got, want)
	}
}

// TestUnreportedEpicReadsAnEndedEpicWithoutAReportAsEpicReads checks that UnreportedEpic reads an ended epic with no
// report as Epic reads it and says it is listed, and says an epic is not listed when it is live, when its report is for
// its ending, and when no epic has its id.
func TestUnreportedEpicReadsAnEndedEpicWithoutAReportAsEpicReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const live = "orrery-5d6e7f8/epic-live"
	createEpic(t, s, endedEpic(epicReportID, "released"))
	createEpic(t, s, endedEpic(live, "accepted"))

	row, listed, err := s.UnreportedEpic(ctx, epicReportID)
	if err != nil {
		t.Fatalf("UnreportedEpic: %v", err)
	}
	if !listed {
		t.Fatal("UnreportedEpic of an ended epic with no report: listed = false, want true")
	}
	want, err := s.Epic(ctx, epicReportID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !reflect.DeepEqual(row, want) {
		t.Errorf("UnreportedEpic row = %+v, want the row Epic reads %+v", row, want)
	}
	if _, listed, err = s.UnreportedEpic(ctx, live); err != nil || listed {
		t.Errorf("UnreportedEpic of a live epic = listed %v, err %v, want not listed and no error", listed, err)
	}
	if _, listed, err = s.UnreportedEpic(ctx, "orrery-5d6e7f8/missing"); err != nil || listed {
		t.Errorf("UnreportedEpic of an unknown epic = listed %v, err %v, want not listed and no error", listed, err)
	}

	if err = s.SetEpicReport(ctx, epicReportID, syntheticReport("released")); err != nil {
		t.Fatalf("SetEpicReport: %v", err)
	}
	if _, listed, err = s.UnreportedEpic(ctx, epicReportID); err != nil || listed {
		t.Errorf("UnreportedEpic after its report = listed %v, err %v, want not listed and no error", listed, err)
	}
}

func TestSetEpicReportStoresTheReportThatEpicAndEpicFlowRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, endedEpic(epicReportID, "released"))
	report := syntheticReport("released")
	if err := s.SetEpicReport(ctx, epicReportID, report); err != nil {
		t.Fatalf("SetEpicReport: %v", err)
	}

	got, err := s.Epic(ctx, epicReportID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !reflect.DeepEqual(got.Report, report) {
		t.Errorf("Epic Report = %+v, want %+v", got.Report, report)
	}
	if want := fullEpic(epicReportID).Changed; !got.Changed.Equal(want) {
		t.Errorf("Changed after SetEpicReport = %v, want %v, unchanged", got.Changed, want)
	}
	flow, err := s.EpicFlow(ctx, epicReportID)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if !reflect.DeepEqual(flow.Epic.Report, report) {
		t.Errorf("EpicFlow Epic Report = %+v, want %+v", flow.Epic.Report, report)
	}
	if ids := unreportedIDs(t, s); len(ids) != 0 {
		t.Errorf("UnreportedEpics after SetEpicReport = %v, want none", ids)
	}
}

func TestSecondSetEpicReportForTheSamePhaseChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, endedEpic(epicReportID, "done"))
	first := syntheticReport("done")
	if err := s.SetEpicReport(ctx, epicReportID, first); err != nil {
		t.Fatalf("SetEpicReport first: %v", err)
	}
	second := EpicReport{Phase: "done", Shipped: []string{"Other shipped"}, Why: "Other reason"}
	if err := s.SetEpicReport(ctx, epicReportID, second); err != nil {
		t.Fatalf("SetEpicReport second: %v", err)
	}

	got, err := s.Epic(ctx, epicReportID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !reflect.DeepEqual(got.Report, first) {
		t.Errorf("Epic Report after two SetEpicReport calls = %+v, want the first %+v", got.Report, first)
	}
}

func TestSetEpicReportForAPhaseTheEpicIsNotInChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, endedEpic(epicReportID, "released"))
	if err := s.SetEpicReport(ctx, epicReportID, syntheticReport("done")); err != nil {
		t.Fatalf("SetEpicReport: %v", err)
	}

	got, err := s.Epic(ctx, epicReportID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !reflect.DeepEqual(got.Report, EpicReport{}) {
		t.Errorf("Epic Report after a report for another phase = %+v, want zero", got.Report)
	}
	if ids := unreportedIDs(t, s); !slices.Equal(ids, []string{epicReportID}) {
		t.Errorf("UnreportedEpics = %v, want the epic still listed", ids)
	}
}

func TestEpicIsListedAgainAfterItMovesToAnotherEnding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, endedEpic(epicReportID, "released"))
	if err := s.SetEpicReport(ctx, epicReportID, syntheticReport("released")); err != nil {
		t.Fatalf("SetEpicReport released: %v", err)
	}
	if ids := unreportedIDs(t, s); len(ids) != 0 {
		t.Fatalf("UnreportedEpics before the move = %v, want none", ids)
	}

	moveEpicPhase(t, s, epicReportID, "done")
	if ids := unreportedIDs(t, s); !slices.Equal(ids, []string{epicReportID}) {
		t.Errorf("UnreportedEpics after the move to done = %v, want the epic", ids)
	}
	if err := s.SetEpicReport(ctx, epicReportID, syntheticReport("released")); err != nil {
		t.Fatalf("SetEpicReport released after the move: %v", err)
	}
	if got, err := s.Epic(ctx, epicReportID); err != nil {
		t.Fatalf("Epic: %v", err)
	} else if got.Report.Phase != "released" {
		t.Errorf("Report phase after a report for the phase it left = %q, want the stored released", got.Report.Phase)
	}

	if err := s.SetEpicReport(ctx, epicReportID, syntheticReport("done")); err != nil {
		t.Fatalf("SetEpicReport done: %v", err)
	}
	got, err := s.Epic(ctx, epicReportID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !reflect.DeepEqual(got.Report, syntheticReport("done")) {
		t.Errorf("Epic Report after a report for done = %+v, want the done report", got.Report)
	}
	if ids := unreportedIDs(t, s); len(ids) != 0 {
		t.Errorf("UnreportedEpics after the done report = %v, want none", ids)
	}
}

func TestSetEpicReportOfAnUnknownEpicIsErrNoEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)

	if err := s.SetEpicReport(ctx, "orrery-5d6e7f8/missing", syntheticReport("released")); !errors.Is(err, ErrNoEpic) {
		t.Errorf("SetEpicReport of an unknown epic err = %v, want ErrNoEpic", err)
	}
}
