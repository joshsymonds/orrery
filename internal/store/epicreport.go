package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EpicReport is the report of an ending: the phase it reports, the Requirement ids that shipped and that did not, in
// contract order, and why.
type EpicReport struct {
	Phase      string
	Shipped    []string
	NotShipped []string
	Why        string
}

// UnreportedEpics returns the epics that need a report, ordered by id, each read as Epic reads it. An epic needs one
// when its phase is released, done or ended_with_gaps, or is a closed phase its planner closed, and its stored report is
// for another phase. An epic the owner closed needs none.
func (s *Store) UnreportedEpics(ctx context.Context) ([]EpicRow, error) {
	queries := sqlcgen.New(s.db)
	heads, err := queries.ListUnreportedEpics(ctx)
	if err != nil {
		return nil, fmt.Errorf("list unreported epics: %w", err)
	}
	var rows []EpicRow
	for _, head := range heads {
		var row EpicRow
		if row, err = readEpicOf(ctx, queries, head); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// UnreportedEpic returns the epic with id, read as Epic reads it, and whether UnreportedEpics lists it. An epic that is
// not listed, or not stored, is false with no error.
func (s *Store) UnreportedEpic(ctx context.Context, id string) (EpicRow, bool, error) {
	queries := sqlcgen.New(s.db)
	head, err := queries.GetUnreportedEpic(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EpicRow{}, false, nil
	}
	if err != nil {
		return EpicRow{}, false, fmt.Errorf("get unreported epic %s: %w", id, err)
	}
	row, err := readEpicOf(ctx, queries, head)
	if err != nil {
		return EpicRow{}, false, err
	}
	return row, true, nil
}

// SetEpicReport stores report as the report of epic when the epic's phase is report's phase and its stored report is
// for another phase; otherwise it changes nothing. It does not move the epic's changed. An unknown epic is ErrNoEpic.
func (s *Store) SetEpicReport(ctx context.Context, id string, report EpicReport) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireEpic(ctx, queries, id); err != nil {
			return err
		}
		shipped, err := encodeList(report.Shipped)
		if err != nil {
			return fmt.Errorf("encode shipped of epic %s: %w", id, err)
		}
		notShipped, err := encodeList(report.NotShipped)
		if err != nil {
			return fmt.Errorf("encode not shipped of epic %s: %w", id, err)
		}
		if err = queries.SetEpicReport(ctx, sqlcgen.SetEpicReportParams{
			ReportPhase: report.Phase, Shipped: shipped, NotShipped: notShipped, Why: report.Why, ID: id,
		}); err != nil {
			return fmt.Errorf("set report of epic %s: %w", id, err)
		}
		return nil
	})
}

// epicReportOf returns the report stored on head, which is the zero EpicReport until one is written.
func epicReportOf(head sqlcgen.Epic) (EpicReport, error) {
	shipped, err := decodeList[string](head.ReportShipped)
	if err != nil {
		return EpicReport{}, fmt.Errorf("report shipped: %w", err)
	}
	notShipped, err := decodeList[string](head.ReportNotShipped)
	if err != nil {
		return EpicReport{}, fmt.Errorf("report not shipped: %w", err)
	}
	return EpicReport{Phase: head.ReportPhase, Shipped: shipped, NotShipped: notShipped, Why: head.ReportWhy}, nil
}
