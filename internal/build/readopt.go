package build

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// taskLabelPattern matches the Task label of a build step: T<seq> for a task with a positive sequence number.
var taskLabelPattern = regexp.MustCompile(`^T[1-9][0-9]*$`)

// ReadoptCheck returns the StepsConfig.ReadoptCheck of the build's steps: the return check a re-adopted step row is
// restored with is VerifierCheck for a verifier step, and nil for any other row.
func ReadoptCheck() func(ctx context.Context, row store.StepRow) (func(json.RawMessage) []string, error) {
	return func(_ context.Context, row store.StepRow) (func(json.RawMessage) []string, error) {
		if row.Schema != VerifierSchema {
			return nil, nil
		}
		return VerifierCheck, nil
	}
}

// ReadoptGate returns the agent gate a re-adopted read-only task step row runs. A row of a task's review step names a
// review gate that its epic defines, and the gate is rebuilt from that epic. Any other row gets a nil gate. An epic that
// does not read, a gate that does not resolve, and a gate that runs no agent are errors.
func ReadoptGate(
	epics interface {
		Epic(ctx context.Context, id string) (store.EpicRow, error)
	},
	getenv func(string) string,
) func(ctx context.Context, row store.StepRow) (*gates.Gate, error) {
	return func(ctx context.Context, row store.StepRow) (*gates.Gate, error) {
		if !row.ReadOnly || !taskLabelPattern.MatchString(row.Task) || row.GateName == "" || len(row.Gate) != 0 {
			return nil, nil
		}
		epic, err := epics.Epic(ctx, row.Epic)
		if err != nil {
			return nil, fmt.Errorf("read epic %s: %w", row.Epic, err)
		}
		gate, err := ResolveGate(ctx, epic, row.GateName, getenv)
		if err != nil {
			return nil, err
		}
		if gate.Agent == "" {
			return nil, fmt.Errorf("gate %s of epic %s runs no agent", row.GateName, row.Epic)
		}
		return &gate, nil
	}
}
