package gates

import (
	"cmp"
	"fmt"
	"math"
	"slices"
)

// Define builds one gate from data the way a [gates.NAME] table of a gates file is checked: the same name pattern,
// the same refusals and the same defaults. An agent gate is one whose agent is non-empty; any other is an argv gate.
// Empty slices count as absent.
func Define(name string, argv []string, limit int64, needs []Need, agent string, must, mustNot []string) (Gate, error) {
	if !namePattern.MatchString(name) {
		return Gate{}, fmt.Errorf("gate %q: name must match [a-z][a-z0-9_]*", name)
	}
	if agent != "" {
		return defineAgent(name, argv, limit, needs, agent, must, mustNot)
	}
	return defineArgv(name, argv, limit, needs, must, mustNot)
}

// defineArgv checks an argv gate and returns it as a Gate.
func defineArgv(name string, argv []string, limit int64, needs []Need, must, mustNot []string) (Gate, error) {
	if len(must) > 0 {
		return Gate{}, fmt.Errorf("gate %q: %s cannot sit beside argv", name, keyMust)
	}
	if len(mustNot) > 0 {
		return Gate{}, fmt.Errorf("gate %q: %s cannot sit beside argv", name, keyMustNot)
	}
	if err := checkArgv(name, argv); err != nil {
		return Gate{}, err
	}
	duration, err := toLimit(name, limit)
	if err != nil {
		return Gate{}, err
	}
	checked, err := checkNeeds(name, needs)
	if err != nil {
		return Gate{}, err
	}
	return Gate{Name: name, Argv: argv, Limit: duration, Needs: checked}, nil
}

// defineAgent checks an agent gate and returns it as a Gate. It takes no argv and no needs.
func defineAgent(
	name string,
	argv []string,
	limit int64,
	needs []Need,
	agent string,
	must, mustNot []string,
) (Gate, error) {
	if len(argv) > 0 {
		return Gate{}, fmt.Errorf("gate %q: argv cannot sit beside agent", name)
	}
	if len(needs) > 0 {
		return Gate{}, fmt.Errorf("gate %q: needs cannot sit beside agent", name)
	}
	duration, err := toLimit(name, limit)
	if err != nil {
		return Gate{}, err
	}
	assertions, err := toAssertions(name, gateFile{Must: must, MustNot: mustNot})
	if err != nil {
		return Gate{}, err
	}
	return Gate{Name: name, Agent: agent, Limit: duration, Assertions: assertions}, nil
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
