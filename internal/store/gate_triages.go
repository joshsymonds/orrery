package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// GateTriage is the triage of a gate check that ran and failed: the check with id Check, of the node run at Path, as
// the read-only step Step judged it. Verdict is environment, flake or change. Evidence is the output an environment or
// flake verdict quotes, Citation the failing test or lint line a change verdict cites, and Reason what the judge, or the
// refusal of its answer, says. Created is the second the triage was recorded at.
type GateTriage struct {
	Check    string
	Path     string
	Step     string
	Verdict  string
	Evidence string
	Citation string
	Reason   string
	Created  time.Time
}

// RecordGateTriage stores triage of epic in one write, created at the second at. The triage's Created is not stored: at
// is. A triage already stored for its check writes nothing, so a repeat leaves the first record as it is.
func (s *Store) RecordGateTriage(ctx context.Context, epic string, triage GateTriage, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.InsertGateTriage(ctx, sqlcgen.InsertGateTriageParams{
			Epic: epic, CheckID: triage.Check, Path: triage.Path, Step: triage.Step, Verdict: triage.Verdict,
			Evidence: triage.Evidence, Citation: triage.Citation, Reason: triage.Reason, Created: at.Unix(),
		}); err != nil {
			return fmt.Errorf("record triage of check %s of node run %s of epic %s: %w",
				triage.Check, triage.Path, epic, err)
		}
		return nil
	})
}

// GateTriages returns the gate triages of epic in the order they were recorded, then by check id.
func (s *Store) GateTriages(ctx context.Context, epic string) ([]GateTriage, error) {
	return readGateTriages(ctx, sqlcgen.New(s.read), epic)
}

// readGateTriages returns the gate triages of epic as queries read them, in the order they were recorded, then by check
// id.
func readGateTriages(ctx context.Context, queries *sqlcgen.Queries, epic string) ([]GateTriage, error) {
	rows, err := queries.ListEpicGateTriages(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("list gate triages of epic %s: %w", epic, err)
	}
	return mapInOrder(rows, func(row sqlcgen.GateTriage) GateTriage {
		return GateTriage{
			Check: row.CheckID, Path: row.Path, Step: row.Step, Verdict: row.Verdict, Evidence: row.Evidence,
			Citation: row.Citation, Reason: row.Reason, Created: time.Unix(row.Created, 0),
		}
	}), nil
}
