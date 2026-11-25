package build

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// taskLabelPattern matches the Task label of a build step: T<seq> for a task with a positive sequence number.
var taskLabelPattern = regexp.MustCompile(`^T[1-9][0-9]*$`)

// ReadoptCheck returns the StepsConfig.ReadoptCheck of the build's steps: the return check a re-adopted step row is
// restored with is VerifierCheck for a verifier step, OrchestratorCheck for an orchestrator step, rebuilt from the
// tasks of the row's epic as they stand now, and nil for any other row. An epic or node runs that do not read, and an
// orchestrator row naming no task of its epic, are errors. The tasks of an orchestrator row are read on the host its
// epic names in hosts, which a nil hosts resolves as hostexec.LocalHosts does.
func ReadoptCheck(
	epics interface {
		Epic(ctx context.Context, id string) (store.EpicRow, error)
		NodeRuns(ctx context.Context, epic string) ([]store.NodeRun, error)
	},
	hosts hostexec.Hosts,
) func(ctx context.Context, row store.StepRow) (func(json.RawMessage) []string, error) {
	if hosts == nil {
		hosts = hostexec.LocalHosts()
	}
	return func(ctx context.Context, row store.StepRow) (func(json.RawMessage) []string, error) {
		switch row.Schema {
		case VerifierSchema:
			return VerifierCheck, nil
		case OrchestratorSchema:
			return readoptOrchestratorCheck(ctx, epics, hosts, row)
		}
		return nil, nil
	}
}

// readoptOrchestratorCheck returns the return check of the orchestrator step row, rebuilt from its epic's tasks now and
// read on the epic's host.
func readoptOrchestratorCheck(
	ctx context.Context,
	epics interface {
		Epic(ctx context.Context, id string) (store.EpicRow, error)
		NodeRuns(ctx context.Context, epic string) ([]store.NodeRun, error)
	},
	hosts hostexec.Hosts,
	row store.StepRow,
) (func(json.RawMessage) []string, error) {
	epic, err := epics.Epic(ctx, row.Epic)
	if err != nil {
		return nil, fmt.Errorf("read epic %s: %w", row.Epic, err)
	}
	i := slices.IndexFunc(epic.Tasks, func(task store.EpicTask) bool { return taskLabel(task.Seq) == row.Task })
	if i < 0 {
		return nil, fmt.Errorf("epic %s has no task %s", row.Epic, row.Task)
	}
	host, err := hosts.Host(epic.Host)
	if err != nil {
		return nil, fmt.Errorf("host of epic %s: %w", row.Epic, err)
	}
	runs, err := epics.NodeRuns(ctx, row.Epic)
	if err != nil {
		return nil, fmt.Errorf("read node runs of epic %s: %w", row.Epic, err)
	}
	peers, err := peersOf(ctx, host, epic, runs)
	if err != nil {
		return nil, err
	}
	return OrchestratorCheck(epic.Tasks[i], peers), nil
}

// ReadoptGate returns the agent gate a re-adopted read-only task step row runs. A row of a task's reproduction judge
// step gets the reproduction judge's gate for the judge its task's evidence item names (enginehost.ReproJudgeGate). A
// row of a task's review step names a reviewer of its epic at the task stage, and the gate is that reviewer's gate. Any
// other row gets a nil gate. An epic that does not read, a reproduction judge row naming no task of its epic, and a
// review row naming no enabled task-stage reviewer of its epic, are errors.
func ReadoptGate(
	epics interface {
		Epic(ctx context.Context, id string) (store.EpicRow, error)
	},
) func(ctx context.Context, row store.StepRow) (*gates.Gate, error) {
	return func(ctx context.Context, row store.StepRow) (*gates.Gate, error) {
		if !row.ReadOnly || !taskLabelPattern.MatchString(row.Task) || row.GateName == "" || len(row.Gate) != 0 {
			return nil, nil
		}
		epic, err := epics.Epic(ctx, row.Epic)
		if err != nil {
			return nil, fmt.Errorf("read epic %s: %w", row.Epic, err)
		}
		if row.GateName == enginehost.ReproJudgeGateName {
			i := slices.IndexFunc(epic.Tasks, func(task store.EpicTask) bool { return taskLabel(task.Seq) == row.Task })
			if i < 0 {
				return nil, fmt.Errorf("epic %s has no task %s", row.Epic, row.Task)
			}
			gate := enginehost.ReproJudgeGate(epic.Tasks[i].Evidence.Judge)
			return &gate, nil
		}
		for _, rev := range reviewers.ForStage(epic.Reviewers, reviewers.StageTask) {
			if rev.Name == row.GateName {
				gate := rev.Gate()
				return &gate, nil
			}
		}
		return nil, fmt.Errorf("epic %s has no task-stage reviewer %s", row.Epic, row.GateName)
	}
}
