package planner

import (
	"context"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// Resolver answers whether a planned task's names resolve for its epic: Gate, whether a gate resolves to a command
// gate, and Judge, whether an agent name resolves to an agent claude would run.
type Resolver interface {
	Gate(ctx context.Context, epic store.EpicRow, name string) error
	Judge(epic store.EpicRow, name string) error
}

// EnvResolver is the Resolver orreryd uses. It reads its environment through getenv and checks agent folders under
// managed.
type EnvResolver struct {
	getenv  func(string) string
	managed string
}

// NewResolver returns an EnvResolver that reads its environment from environ. managed is the managed settings root,
// or "" when there is none.
func NewResolver(environ []string, managed string) EnvResolver {
	return EnvResolver{getenv: environLookup(environ), managed: managed}
}

// Gate returns nil when name resolves to a command gate for epic, from the epic's gates or else the repository's
// project file. A name that neither defines wraps gates.ErrUnknown. An agent gate is refused.
func (r EnvResolver) Gate(ctx context.Context, epic store.EpicRow, name string) error {
	defined, err := defineEpicGates(epic.Gates)
	if err != nil {
		return err
	}
	resolved, err := gates.ResolveDefined(ctx, name, defined, epic.Repository, r.getenv)
	if err != nil {
		return fmt.Errorf("resolve gate: %w", err)
	}
	if resolved.Agent != "" {
		return fmt.Errorf("gate %q is an agent gate, not a command gate", name)
	}
	return nil
}

// Judge returns nil when claude would resolve the agent name for epic's repository under the environment. An
// unresolved name wraps agents.ErrUnresolved.
func (r EnvResolver) Judge(epic store.EpicRow, name string) error {
	where := agents.Where{
		Dir:       epic.Repository,
		Home:      r.getenv("HOME"),
		ConfigDir: r.getenv("CLAUDE_CONFIG_DIR"),
		Managed:   r.managed,
	}
	if err := agents.Resolve(name, where); err != nil {
		return fmt.Errorf("resolve judge: %w", err)
	}
	return nil
}

// defineEpicGates builds each of an epic's gates with gates.Define. A gate that Define refuses is an error that names it.
func defineEpicGates(epicGates []store.EpicGate) ([]gates.Gate, error) {
	defined := make([]gates.Gate, 0, len(epicGates))
	for _, epicGate := range epicGates {
		needs := make([]gates.Need, 0, len(epicGate.Needs))
		for _, need := range epicGate.Needs {
			needs = append(needs, gates.Need{Pool: need.Pool, Count: int(need.Count)})
		}
		gate, err := gates.Define(
			epicGate.Name,
			epicGate.Argv,
			epicGate.Limit,
			needs,
			epicGate.Agent,
			epicGate.Must,
			epicGate.MustNot,
		)
		if err != nil {
			return nil, fmt.Errorf("define epic gate: %w", err)
		}
		defined = append(defined, gate)
	}
	return defined, nil
}

// environLookup returns a lookup of name in environ. The last entry for name wins, as it does in a process's environment.
func environLookup(environ []string) func(string) string {
	return func(name string) string {
		value := ""
		for _, entry := range environ {
			if key, val, found := strings.Cut(entry, "="); found && key == name {
				value = val
			}
		}
		return value
	}
}
