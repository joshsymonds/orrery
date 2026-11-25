package gates

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Definition is one gate as data, the fields a [gates.NAME] table holds: its name, its argv, its limit in seconds and
// the pool members it needs, or the agent that checks its must and must_not assertions, and whether it is always run
// and what it covers. Empty slices count as absent, and so does a blank note.
type Definition struct {
	Name    string
	Argv    []string
	Limit   int64
	Needs   []Need
	Agent   string
	Must    []string
	MustNot []string
	Always  bool
	Note    string
}

// Define builds one gate from def the way a [gates.NAME] table of a gates file is checked: the same name pattern, the
// same refusals and the same defaults. An agent gate is one whose agent is non-empty; any other is an argv gate. Its note
// is trimmed of surrounding white space.
func Define(def Definition) (Gate, error) {
	if !namePattern.MatchString(def.Name) {
		return Gate{}, fmt.Errorf("gate %q: name must match [a-z][a-z0-9_]*", def.Name)
	}
	if def.Agent != "" {
		return defineAgent(def)
	}
	return defineArgv(def)
}

// defineArgv checks an argv gate and returns it as a Gate.
func defineArgv(def Definition) (Gate, error) {
	if len(def.Must) > 0 {
		return Gate{}, fmt.Errorf("gate %q: %s cannot sit beside argv", def.Name, keyMust)
	}
	if len(def.MustNot) > 0 {
		return Gate{}, fmt.Errorf("gate %q: %s cannot sit beside argv", def.Name, keyMustNot)
	}
	if err := checkArgv(def.Name, def.Argv); err != nil {
		return Gate{}, err
	}
	duration, err := toLimit(def.Name, def.Limit)
	if err != nil {
		return Gate{}, err
	}
	checked, err := checkNeeds(def.Name, def.Needs)
	if err != nil {
		return Gate{}, err
	}
	return Gate{
		Name: def.Name, Argv: def.Argv, Limit: duration, Needs: checked, Always: def.Always,
		Note: strings.TrimSpace(def.Note),
	}, nil
}

// defineAgent checks an agent gate and returns it as a Gate. It takes no argv, no needs and no always, since an agent
// gate is never a Done gate.
func defineAgent(def Definition) (Gate, error) {
	if len(def.Argv) > 0 {
		return Gate{}, fmt.Errorf("gate %q: argv cannot sit beside agent", def.Name)
	}
	if len(def.Needs) > 0 {
		return Gate{}, fmt.Errorf("gate %q: needs cannot sit beside agent", def.Name)
	}
	if def.Always {
		return Gate{}, fmt.Errorf("gate %q: %s cannot sit beside agent", def.Name, keyAlways)
	}
	duration, err := toLimit(def.Name, def.Limit)
	if err != nil {
		return Gate{}, err
	}
	assertions, err := toAssertions(def.Name, gateFile{Must: def.Must, MustNot: def.MustNot})
	if err != nil {
		return Gate{}, err
	}
	return Gate{
		Name: def.Name, Agent: def.Agent, Limit: duration, Assertions: assertions, Note: strings.TrimSpace(def.Note),
	}, nil
}

// checkNeeds checks an argv gate's needs and returns them sorted by pool. Empty needs take one member of DefaultPool.
// A pool named twice is refused.
func checkNeeds(name string, needs []Need) ([]Need, error) {
	if len(needs) == 0 {
		return []Need{{Pool: DefaultPool, Count: 1}}, nil
	}
	out := slices.Clone(needs)
	slices.SortFunc(out, func(a, b Need) int { return cmp.Compare(a.Pool, b.Pool) })
	for i, need := range out {
		if !namePattern.MatchString(need.Pool) {
			return nil, fmt.Errorf("gate %q: pool %q in needs must match [a-z][a-z0-9_]*", name, need.Pool)
		}
		if need.Count < 1 || need.Count > math.MaxInt32 {
			return nil, fmt.Errorf(
				"gate %q: needs %s count %d is outside 1 to %d",
				name,
				need.Pool,
				need.Count,
				math.MaxInt32,
			)
		}
		if i > 0 && out[i-1].Pool == need.Pool {
			return nil, fmt.Errorf("gate %q: pool %q named twice in needs", name, need.Pool)
		}
	}
	return out, nil
}
