package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Settings are a check kind's settings. A kind takes only the settings its rule names, and the rest stay zero. At is
// base or head. Gate names a gate. Argv is a command's argument vector and Limit its time limit in seconds. Needs holds
// the pools a command takes, sorted by pool. Paths are the repository paths an owned node owns. Agent and Prompt name
// an agent and what it is asked. Schema is the compact JSON of the schema its output takes, with sorted keys.
type Settings struct {
	At     string
	Gate   string
	Argv   []string
	Limit  int
	Needs  []Need
	Paths  []string
	Agent  string
	Prompt string
	Schema string
}

// Need is a pool a command holds, and the units of it the command takes.
type Need struct {
	Pool  string
	Count int
}

// snakePattern is the shape of a gate's name and of a pool's name.
var snakePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// readGateSettings reads a gate node's settings: at, and gate, which is a gate's name.
func readGateSettings(here site, pairs []pair) (*Settings, error) {
	settings := &Settings{}
	var err error
	if settings.At, err = readAt(here, pairs); err != nil {
		return nil, err
	}
	pr, err := requireKey(here, pairs, keyGate)
	if err != nil {
		return nil, err
	}
	if settings.Gate, err = snakeOf(here, pr); err != nil {
		return nil, err
	}
	return settings, nil
}

// readEvidenceSettings reads an evidence node's settings: at.
func readEvidenceSettings(here site, pairs []pair) (*Settings, error) {
	at, err := readAt(here, pairs)
	if err != nil {
		return nil, err
	}
	return &Settings{At: at}, nil
}

// readOwnedSettings reads an owned node's settings: paths.
func readOwnedSettings(here site, pairs []pair) (*Settings, error) {
	paths, err := readPaths(here, pairs)
	if err != nil {
		return nil, err
	}
	return &Settings{Paths: paths}, nil
}

// readCommandSettings reads a command node's settings: at, argv, limit and needs.
func readCommandSettings(here site, pairs []pair) (*Settings, error) {
	settings := &Settings{}
	var err error
	if settings.At, err = readAt(here, pairs); err != nil {
		return nil, err
	}
	if settings.Argv, err = readArgv(here, pairs); err != nil {
		return nil, err
	}
	if settings.Limit, err = readLimit(here, pairs); err != nil {
		return nil, err
	}
	if settings.Needs, err = readNeeds(here, pairs); err != nil {
		return nil, err
	}
	return settings, nil
}

// readAgentSettings reads an agent node's settings: at, agent, prompt and schema.
func readAgentSettings(here site, pairs []pair) (*Settings, error) {
	settings := &Settings{}
	var err error
	if settings.At, err = readAt(here, pairs); err != nil {
		return nil, err
	}
	if settings.Agent, err = readText(here, pairs, keyAgent); err != nil {
		return nil, err
	}
	if settings.Prompt, err = readText(here, pairs, keyPrompt); err != nil {
		return nil, err
	}
	if settings.Schema, err = readSchema(here, pairs); err != nil {
		return nil, err
	}
	return settings, nil
}

// readAt reads at: base or head, required.
func readAt(here site, pairs []pair) (string, error) {
	pr, err := requireKey(here, pairs, keyAt)
	if err != nil {
		return "", err
	}
	val, err := text(here, pr)
	if err != nil {
		return "", err
	}
	if val != atBase && val != atHead {
		return "", here.refusef(pr.line, pr.path, "%q must be base or head", val)
	}
	return val, nil
}

// snakeOf reads a value that must match snakePattern: a gate's name.
func snakeOf(here site, pr pair) (string, error) {
	val, err := text(here, pr)
	if err != nil {
		return "", err
	}
	if !snakePattern.MatchString(val) {
		return "", here.refusef(pr.line, pr.path, "%q must match %s", val, snakePattern)
	}
	return val, nil
}

// readText reads the single value under key: required, and not blank.
func readText(here site, pairs []pair, key string) (string, error) {
	pr, err := requireKey(here, pairs, key)
	if err != nil {
		return "", err
	}
	val, err := text(here, pr)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(val) == "" {
		return "", here.refusef(pr.line, pr.path, "must not be blank")
	}
	return val, nil
}

// readArgv reads argv: a sequence of non-empty strings, required, whose first item names a path with a /.
func readArgv(here site, pairs []pair) ([]string, error) {
	pr, err := requireKey(here, pairs, keyArgv)
	if err != nil {
		return nil, err
	}
	if pr.value.Kind != yaml.SequenceNode {
		return nil, here.refusef(pr.line, pr.path, "must be a sequence of strings")
	}
	if len(pr.value.Content) == 0 {
		return nil, here.refusef(pr.line, pr.path, "must hold at least one string")
	}
	argv := make([]string, 0, len(pr.value.Content))
	for i, item := range pr.value.Content {
		if item.Kind != yaml.ScalarNode || item.Value == "" {
			return nil, here.refusef(item.Line, pr.path, "item %d must be a non-empty string", i+1)
		}
		argv = append(argv, item.Value)
	}
	if !strings.Contains(argv[0], "/") {
		return nil, here.refusef(pr.value.Content[0].Line, pr.path, "item 1 %q must contain a /", argv[0])
	}
	return argv, nil
}

// readLimit reads limit: an integer of 1 or more seconds, required.
func readLimit(here site, pairs []pair) (int, error) {
	pr, err := requireKey(here, pairs, keyLimit)
	if err != nil {
		return 0, err
	}
	return countOf(here, pr)
}

// readNeeds reads needs: a mapping of pool to the units of it a command takes, optional. Needs are sorted by pool, and
// nil when the mapping holds none.
func readNeeds(here site, pairs []pair) ([]Need, error) {
	pr, ok := find(pairs, keyNeeds)
	if !ok {
		return nil, nil
	}
	if pr.value.Kind != yaml.MappingNode {
		return nil, here.refusef(pr.line, pr.path, "must be a mapping")
	}
	pools, err := pairsOf(here, pr.value, needsPrefix)
	if err != nil {
		return nil, err
	}
	var needs []Need
	for _, pool := range pools {
		if !snakePattern.MatchString(pool.key) {
			return nil, here.refusef(pool.line, pool.path, "%q must match %s", pool.key, snakePattern)
		}
		var count int
		if count, err = countOf(here, pool); err != nil {
			return nil, err
		}
		needs = append(needs, Need{Pool: pool.key, Count: count})
	}
	slices.SortFunc(needs, func(a, b Need) int { return strings.Compare(a.Pool, b.Pool) })
	return needs, nil
}

// readPaths reads paths: a sequence of repository-relative paths, required and not empty.
func readPaths(here site, pairs []pair) ([]string, error) {
	pr, err := requireKey(here, pairs, keyPaths)
	if err != nil {
		return nil, err
	}
	if pr.value.Kind != yaml.SequenceNode {
		return nil, here.refusef(pr.line, pr.path, "must be a sequence of paths")
	}
	if len(pr.value.Content) == 0 {
		return nil, here.refusef(pr.line, pr.path, "must hold at least one path")
	}
	paths := make([]string, 0, len(pr.value.Content))
	for i, item := range pr.value.Content {
		if item.Kind != yaml.ScalarNode {
			return nil, here.refusef(item.Line, pr.path, "item %d must be a single value", i+1)
		}
		if !isRepoPath(item.Value) {
			return nil, here.refusef(item.Line, pr.path, "item %d %q must be a repository-relative path",
				i+1, item.Value)
		}
		paths = append(paths, item.Value)
	}
	return paths, nil
}

// isRepoPath reports whether v is a repository-relative path: no element is empty, "." or "..", and no leading /. A
// trailing / is allowed, since it names a directory.
func isRepoPath(v string) bool {
	rel := strings.TrimSuffix(v, "/")
	if rel == "" {
		return false
	}
	for el := range strings.SplitSeq(rel, "/") {
		if el == "" || el == "." || el == ".." {
			return false
		}
	}
	return true
}

// readSchema reads schema: a mapping, required, held as its compact JSON with sorted keys.
func readSchema(here site, pairs []pair) (string, error) {
	pr, err := requireKey(here, pairs, keySchema)
	if err != nil {
		return "", err
	}
	if pr.value.Kind != yaml.MappingNode {
		return "", here.refusef(pr.line, pr.path, "must be a mapping")
	}
	raw, err := jsonOf(pr.value)
	if err != nil {
		return "", here.refusef(pr.line, pr.path, "must be a mapping of JSON values")
	}
	return raw, nil
}

// jsonOf returns the compact JSON of a YAML node's value, with sorted keys. JSON holds no key that is not text and no
// NaN, so either is an error.
func jsonOf(n *yaml.Node) (string, error) {
	var v any
	if err := n.Decode(&v); err != nil {
		return "", fmt.Errorf("decode schema: %w", err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode schema: %w", err)
	}
	return string(raw), nil
}
