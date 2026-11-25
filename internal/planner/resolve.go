package planner

import (
	"context"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

// Resolver answers whether a planned task's names resolve for its epic: Gate, whether a gate resolves to a command
// gate, and Judge, whether an agent name resolves to an agent claude would run.
type Resolver interface {
	Gate(ctx context.Context, epic store.EpicRow, name string) error
	Judge(epic store.EpicRow, name string) error
}

// EnvResolver is the Resolver orreryd uses. It reads its environment through getenv, checks agent folders under
// managed, and reads each epic's project file on the host the epic names in hosts.
type EnvResolver struct {
	getenv  func(string) string
	managed string
	hosts   hostexec.Hosts
}

// NewResolver returns an EnvResolver that reads its environment from environ. managed is the managed settings root,
// or "" when there is none. hosts resolves each epic's host name to the Host its project file is read on; nil means
// hostexec.LocalHosts.
func NewResolver(environ []string, managed string, hosts hostexec.Hosts) EnvResolver {
	if hosts == nil {
		hosts = hostexec.LocalHosts()
	}
	return EnvResolver{getenv: environLookup(environ), managed: managed, hosts: hosts}
}

// Gate returns nil when name resolves to a command gate for epic, from the epic's gates or else the repository's
// project file, which is read on the epic's host. A name that neither defines wraps gates.ErrUnknown. An agent gate is
// refused.
func (r EnvResolver) Gate(ctx context.Context, epic store.EpicRow, name string) error {
	defined, err := DefineEpicGates(epic.Gates)
	if err != nil {
		return err
	}
	host, err := r.hosts.Host(epic.Host)
	if err != nil {
		return fmt.Errorf("resolve gate: %w", err)
	}
	resolved, err := gates.ResolveDefined(ctx, host, name, defined, epic.Repository, r.getenv)
	if err != nil {
		return fmt.Errorf("resolve gate: %w", err)
	}
	if resolved.Agent != "" {
		return fmt.Errorf("gate %q is an agent gate, not a command gate", name)
	}
	return nil
}

// Judge returns nil when claude would resolve the agent name for epic's repository under the environment, reading its
// agent files on the epic's host. An unresolved name wraps agents.ErrUnresolved.
func (r EnvResolver) Judge(epic store.EpicRow, name string) error {
	host, err := r.hosts.Host(epic.Host)
	if err != nil {
		return fmt.Errorf("resolve judge: %w", err)
	}
	where := agents.Where{
		Dir:       epic.Repository,
		Home:      r.getenv("HOME"),
		ConfigDir: r.getenv("CLAUDE_CONFIG_DIR"),
		Managed:   r.managed,
		Files:     host,
	}
	if err = agents.Resolve(context.Background(), name, where); err != nil {
		return fmt.Errorf("resolve judge: %w", err)
	}
	return nil
}

// DefineEpicGates builds each of an epic's gates with gates.Define. A gate that Define refuses is an error that names it.
func DefineEpicGates(epicGates []store.EpicGate) ([]gates.Gate, error) {
	defined := make([]gates.Gate, 0, len(epicGates))
	for _, epicGate := range epicGates {
		needs := make([]gates.Need, 0, len(epicGate.Needs))
		for _, need := range epicGate.Needs {
			needs = append(needs, gates.Need{Pool: need.Pool, Count: int(need.Count)})
		}
		gate, err := gates.Define(gates.Definition{
			Name:    epicGate.Name,
			Argv:    epicGate.Argv,
			Limit:   epicGate.Limit,
			Needs:   needs,
			Agent:   epicGate.Agent,
			Must:    epicGate.Must,
			MustNot: epicGate.MustNot,
			Always:  epicGate.Always,
			Note:    epicGate.Note,
		})
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
