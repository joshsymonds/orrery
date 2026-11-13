// Package gates reads gate definitions: the [gates] tables of an epic's gates file and of a project file. Each
// [gates.NAME] table names one gate: either an argv that runs it with the pool members it needs, or an agent that
// checks its must and must_not assertions. Either way the table names the longest the gate may run.
package gates

import (
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// DefaultPool is the pool a gate takes its member from when it names no needs.
const DefaultPool = "gate"

// Need is a count of members a gate takes from one pool.
type Need struct {
	Pool  string
	Count int
}

// Kinds of assertion: a must is confirmed when it holds, a must not when it does not.
const (
	KindMust    = "must"
	KindMustNot = "must not"
)

// Assertion is one statement an agent gate checks, with its Kind.
type Assertion struct {
	Text string
	Kind string
}

// Gate is one named gate as a file defines it. Argv names the program and its arguments; Limit bounds how long
// the program may run; Needs names the pool members the gate holds while it runs. An agent gate instead names
// Agent, which checks Assertions, and holds no argv or needs.
type Gate struct {
	Name       string
	Argv       []string
	Limit      time.Duration
	Needs      []Need
	Agent      string
	Assertions []Assertion
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// maxLimitSeconds is the largest limit whose duration fits in a time.Duration.
const maxLimitSeconds = math.MaxInt64 / int64(time.Second)

const (
	// sectionGates is the top-level table that holds every gate.
	sectionGates = "gates"
	// sectionPools is the top-level table of a project file that internal/pools reads.
	sectionPools = "pools"
	// keyAgent is the gate key that names the agent an agent gate runs.
	keyAgent = "agent"
	// keyNeeds is the gate key that names the pool members a gate takes.
	keyNeeds = "needs"
	// keyMust and keyMustNot are the gate keys that name the assertions of an agent gate.
	keyMust    = "must"
	keyMustNot = "must_not"
)

// gateFile is one [gates.NAME] table as the file spells it. Whether a key is present is read from the metadata,
// since a zero value cannot tell a missing key from one set to zero.
type gateFile struct {
	Argv    []string         `toml:"argv"`
	Limit   int64            `toml:"limit"`
	Needs   map[string]int64 `toml:"needs"`
	Agent   string           `toml:"agent"`
	Must    []string         `toml:"must"`
	MustNot []string         `toml:"must_not"`
}

type gateDocument struct {
	Gates map[string]gateFile `toml:"gates"`
}

// definedKeys lists the keys a [gates.NAME] table may hold.
func definedKeys() []string {
	return []string{"argv", keyAgent, "limit", keyNeeds, keyMust, keyMustNot}
}

// ParseFile reads an epic gates file. path names the file in errors; data is its contents. The [gates] table must
// exist, though it may hold no gates. The gates come back sorted by name.
func ParseFile(path string, data []byte) ([]Gate, error) {
	md, doc, err := decode(path, data, false)
	if err != nil {
		return nil, err
	}
	if !md.IsDefined(sectionGates) {
		return nil, fmt.Errorf("%s: missing [gates] table", path)
	}
	return toGates(path, md, doc)
}

// ParseProject reads a project file. path names the file in errors; data is its contents. The [gates] table may be
// absent. The pools table and every key under it are left to internal/pools. The gates come back sorted by name.
func ParseProject(path string, data []byte) ([]Gate, error) {
	md, doc, err := decode(path, data, true)
	if err != nil {
		return nil, err
	}
	return toGates(path, md, doc)
}

// decode reads the file and refuses any key it does not define. project admits the pools table of a project file.
func decode(path string, data []byte, project bool) (toml.MetaData, gateDocument, error) {
	var doc gateDocument
	md, err := toml.Decode(string(data), &doc)
	if err != nil {
		return toml.MetaData{}, gateDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	if err = checkKeys(md, project); err != nil {
		return toml.MetaData{}, gateDocument{}, fmt.Errorf("%s: %w", path, err)
	}
	return md, doc, nil
}

// checkKeys refuses any key the format does not define. Keys match exactly, so ARGV is refused rather than read as
// argv. The keys of a needs table are pool names, checked with the needs.
func checkKeys(md toml.MetaData, project bool) error {
	for _, key := range md.Keys() {
		switch {
		case len(key) == 1 && key[0] == sectionGates:
		case len(key) == 2 && key[0] == sectionGates:
			if !namePattern.MatchString(key[1]) {
				return fmt.Errorf("gate %q: name must match [a-z][a-z0-9_]*", key[1])
			}
		case len(key) == 3 && key[0] == sectionGates && slices.Contains(definedKeys(), key[2]):
		case len(key) == 4 && key[0] == sectionGates && key[2] == keyNeeds:
		case project && key[0] == sectionPools:
		default:
			return fmt.Errorf("unknown key %s", key)
		}
	}
	return nil
}

// toGates checks every gate and returns them sorted by name.
func toGates(path string, md toml.MetaData, doc gateDocument) ([]Gate, error) {
	var out []Gate
	for _, name := range slices.Sorted(maps.Keys(doc.Gates)) {
		g, err := toGate(md, name, doc.Gates[name])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, g)
	}
	return out, nil
}

// toGate checks one gate's table and returns it as a Gate. A table with an agent key is an agent gate; any other is
// an argv gate.
func toGate(md toml.MetaData, name string, raw gateFile) (Gate, error) {
	if md.IsDefined(sectionGates, name, keyAgent) {
		return toAgentGate(md, name, raw)
	}
	return toArgvGate(md, name, raw)
}

// toArgvGate checks an argv gate's table and returns it as a Gate.
func toArgvGate(md toml.MetaData, name string, raw gateFile) (Gate, error) {
	if err := requireKeys(md, name, "argv", "limit"); err != nil {
		return Gate{}, err
	}
	for _, key := range []string{keyMust, keyMustNot} {
		if md.IsDefined(sectionGates, name, key) {
			return Gate{}, fmt.Errorf("gate %q: %s cannot sit beside argv", name, key)
		}
	}
	if err := checkArgv(name, raw.Argv); err != nil {
		return Gate{}, err
	}
	limit, err := toLimit(name, raw.Limit)
	if err != nil {
		return Gate{}, err
	}
	needs, err := toNeeds(md, name, raw.Needs)
	if err != nil {
		return Gate{}, err
	}
	return Gate{Name: name, Argv: raw.Argv, Limit: limit, Needs: needs}, nil
}

// toAgentGate checks an agent gate's table and returns it as a Gate. It takes no argv and no needs.
func toAgentGate(md toml.MetaData, name string, raw gateFile) (Gate, error) {
	for _, key := range []string{"argv", keyNeeds} {
		if md.IsDefined(sectionGates, name, key) {
			return Gate{}, fmt.Errorf("gate %q: %s cannot sit beside agent", name, key)
		}
	}
	if err := requireKeys(md, name, keyAgent, "limit"); err != nil {
		return Gate{}, err
	}
	if raw.Agent == "" {
		return Gate{}, fmt.Errorf("gate %q: agent is empty", name)
	}
	limit, err := toLimit(name, raw.Limit)
	if err != nil {
		return Gate{}, err
	}
	assertions, err := toAssertions(name, raw)
	if err != nil {
		return Gate{}, err
	}
	return Gate{Name: name, Agent: raw.Agent, Limit: limit, Assertions: assertions}, nil
}

// requireKeys refuses the first of keys that the [gates.NAME] table does not define.
func requireKeys(md toml.MetaData, name string, keys ...string) error {
	for _, key := range keys {
		if !md.IsDefined(sectionGates, name, key) {
			return fmt.Errorf("gate %q: missing key %q", name, key)
		}
	}
	return nil
}

// toAssertions checks an agent gate's must and must_not entries and returns them as Assertions: the musts first, then
// the must_nots, each in its file order. A gate needs at least one entry between them.
func toAssertions(name string, raw gateFile) ([]Assertion, error) {
	if len(raw.Must)+len(raw.MustNot) == 0 {
		return nil, fmt.Errorf("gate %q: an agent gate needs a must or must_not assertion", name)
	}
	var out []Assertion
	for _, list := range []struct {
		key   string
		kind  string
		texts []string
	}{{keyMust, KindMust, raw.Must}, {keyMustNot, KindMustNot, raw.MustNot}} {
		if slices.Contains(list.texts, "") {
			return nil, fmt.Errorf("gate %q: %s has an empty element", name, list.key)
		}
		for _, text := range list.texts {
			out = append(out, Assertion{Text: text, Kind: list.kind})
		}
	}
	return out, nil
}

// checkArgv refuses an empty argv, an empty element of one, and a program name without a slash. argv[0] is a path:
// the gate never looks it up on PATH.
func checkArgv(name string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("gate %q: argv is empty", name)
	}
	if slices.Contains(argv, "") {
		return fmt.Errorf("gate %q: argv has an empty element", name)
	}
	if !strings.Contains(argv[0], "/") {
		return fmt.Errorf("gate %q: argv[0] %q must be a path with a / in it, never looked up on PATH", name, argv[0])
	}
	return nil
}

// toLimit converts a limit in whole seconds to a duration, refusing one below 1 second or too large for a duration.
func toLimit(name string, seconds int64) (time.Duration, error) {
	if seconds < 1 || seconds > maxLimitSeconds {
		return 0, fmt.Errorf("gate %q: limit %d is outside 1 to %d seconds", name, seconds, maxLimitSeconds)
	}
	return time.Duration(seconds) * time.Second, nil
}

// toNeeds checks one gate's needs and returns them sorted by pool. A gate with no needs key takes one member of
// DefaultPool.
func toNeeds(md toml.MetaData, name string, raw map[string]int64) ([]Need, error) {
	if !md.IsDefined(sectionGates, name, keyNeeds) {
		return []Need{{Pool: DefaultPool, Count: 1}}, nil
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("gate %q: needs is empty", name)
	}
	var out []Need
	for _, pool := range slices.Sorted(maps.Keys(raw)) {
		if !namePattern.MatchString(pool) {
			return nil, fmt.Errorf("gate %q: pool %q in needs must match [a-z][a-z0-9_]*", name, pool)
		}
		count := raw[pool]
		if count < 1 || count > math.MaxInt32 {
			return nil, fmt.Errorf("gate %q: needs %s count %d is outside 1 to %d", name, pool, count, math.MaxInt32)
		}
		out = append(out, Need{Pool: pool, Count: int(count)})
	}
	return out, nil
}
