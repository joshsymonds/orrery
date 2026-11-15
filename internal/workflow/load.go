package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Reader reads the workflow file at a repository path. found is false when there is no such file.
type Reader func(path string) (text []byte, found bool, err error)

// Pinned is a workflow Load has expanded and checked: its name, its canonical text, the lowercase hex sha256 of that
// text, and the expanded workflow.
type Pinned struct {
	Name     string
	Text     string
	SHA256   string
	Workflow Workflow
}

// canonicalIndent is the indent of the canonical text's sequences and mappings.
const canonicalIndent = 2

// epicText is the built-in epic in canonical form: red, an evidence node captured at base before the first task, then
// plan holding build and integrate.
const epicText = "name: epic\nnodes:\n  - id: red\n    kind: evidence\n    at: base\n  - id: plan\n    kind: plan\n" +
	"    body:\n      - id: build\n        kind: build\n      - id: integrate\n        kind: integrate\n"

// canonDoc, canonNode, canonSettings and canonLoop are the shape of a canonical workflow. Their field order is the key
// order the text is written in, and spent is always written.
type canonDoc struct {
	Name  string      `yaml:"name"`
	Nodes []canonNode `yaml:"nodes"`
}

type canonNode struct {
	ID       string        `yaml:"id"`
	Kind     string        `yaml:"kind,omitempty"`
	Settings canonSettings `yaml:",inline"`
	Loop     *canonLoop    `yaml:"loop,omitempty"`
	Body     []canonNode   `yaml:"body,omitempty"`
}

// canonSettings is a check kind's settings in the order the text writes them. A setting the kind does not take is
// omitted.
type canonSettings struct {
	At     string         `yaml:"at,omitempty"`
	Gate   string         `yaml:"gate,omitempty"`
	Argv   []string       `yaml:"argv,omitempty"`
	Limit  int            `yaml:"limit,omitempty"`
	Needs  map[string]int `yaml:"needs,omitempty"`
	Paths  []string       `yaml:"paths,omitempty"`
	Agent  string         `yaml:"agent,omitempty"`
	Prompt string         `yaml:"prompt,omitempty"`
	Schema any            `yaml:"schema,omitempty"`
}

type canonLoop struct {
	Max   int    `yaml:"max"`
	Until string `yaml:"until"`
	Spent string `yaml:"spent"`
}

// Load returns the workflow name names, read through read: the repository file of that name, else the built-in of that
// name. Each include is expanded in place, and the expanded workflow is checked. Its canonical text and that text's
// sha256 are what an epic pins.
func Load(name string, read Reader) (Pinned, error) {
	if !namePattern.MatchString(name) {
		return Pinned{}, fmt.Errorf("workflow name %q must match %s", name, namePattern)
	}
	top, found, err := resolve(name, read)
	if err != nil {
		return Pinned{}, err
	}
	if !found {
		return Pinned{}, fmt.Errorf("workflow %q: no file %s and no built-in %q", name, repoFile(name), name)
	}
	if top.Nodes, err = expand(top.Nodes, read, []string{name}, map[string]bool{name: true}); err != nil {
		return Pinned{}, err
	}
	err = Check(top)
	if err != nil {
		return Pinned{}, err
	}
	text, err := canonical(top)
	if err != nil {
		return Pinned{}, err
	}
	sum := sha256.Sum256([]byte(text))
	return Pinned{Name: name, Text: text, SHA256: hex.EncodeToString(sum[:]), Workflow: top}, nil
}

// repoFile is the repository path of the workflow name.
func repoFile(name string) string {
	return ".orrery/workflows/" + name + ".yaml"
}

// builtinText returns the text of the built-in workflow name, and whether there is one.
func builtinText(name string) (string, bool) {
	switch name {
	case "epic":
		return epicText, true
	default:
		return "", false
	}
}

// resolve parses the workflow name names: the repository file read through read, else the built-in of that name. found
// is false when neither exists. A built-in is parsed as the file "built-in <name>".
func resolve(name string, read Reader) (Workflow, bool, error) {
	path := repoFile(name)
	text, found, err := read(path)
	if err != nil {
		return Workflow{}, false, fmt.Errorf("%s: %w", path, err)
	}
	if !found {
		builtin, ok := builtinText(name)
		if !ok {
			return Workflow{}, false, nil
		}
		path, text = "built-in "+name, []byte(builtin)
	}
	w, err := Parse(path, text)
	return w, true, err
}

// expand returns nodes with each include replaced, in place, by the nodes of the workflow it names, expanded in turn.
// chain names the workflows being expanded, outermost first: an include of one of them is a cycle. included holds
// every name the workflow has included so far: a second include of one is refused before it is expanded again, since
// it would repeat every id it holds, and so expansion stays linear in the files read.
func expand(nodes []Node, read Reader, chain []string, included map[string]bool) ([]Node, error) {
	out := make([]Node, 0, len(nodes))
	for i, node := range nodes {
		if node.Include == "" {
			body, err := expandBody(node, read, chain, included)
			if err != nil {
				return nil, err
			}
			out = append(out, body)
			continue
		}
		sub, err := expandInclude(node, i+1, read, chain, included)
		if err != nil {
			return nil, err
		}
		out = append(out, sub...)
	}
	return out, nil
}

// expandBody returns node with the includes in its body expanded. A node with no body is returned as it is.
func expandBody(node Node, read Reader, chain []string, included map[string]bool) (Node, error) {
	if len(node.Body) == 0 {
		return node, nil
	}
	body, err := expand(node.Body, read, chain, included)
	if err != nil {
		return Node{}, err
	}
	node.Body = body
	return node, nil
}

// expandInclude returns the nodes of the workflow that the include node, at position pos in its list, names. The
// chain and the refusals are checked before the workflow is read.
func expandInclude(node Node, pos int, read Reader, chain []string, included map[string]bool) ([]Node, error) {
	here := site{file: node.File, line: node.Line, node: node.label(pos)}
	next := slices.Concat(chain, []string{node.Include})
	if slices.Contains(chain, node.Include) {
		return nil, here.refusef(node.Line, keyInclude, "include cycle %s", strings.Join(next, " -> "))
	}
	if included[node.Include] {
		return nil, here.refusef(node.Line, keyInclude, "%q is included more than once", node.Include)
	}
	included[node.Include] = true
	inc, found, err := resolve(node.Include, read)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, here.refusef(node.Line, keyInclude, "no file %s and no built-in %q",
			repoFile(node.Include), node.Include)
	}
	return expand(inc.Nodes, read, next, included)
}

// canonical returns the canonical YAML text of an expanded workflow: its keys in a fixed order, indented by
// canonicalIndent.
func canonical(flow Workflow) (string, error) {
	nodes, err := canonNodes(flow.Nodes)
	if err != nil {
		return "", fmt.Errorf("encode workflow %s: %w", flow.Name, err)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(canonicalIndent)
	if err = enc.Encode(canonDoc{Name: flow.Name, Nodes: nodes}); err != nil {
		return "", fmt.Errorf("encode workflow %s: %w", flow.Name, err)
	}
	if err = enc.Close(); err != nil {
		return "", fmt.Errorf("encode workflow %s: %w", flow.Name, err)
	}
	return buf.String(), nil
}

// canonNodes returns nodes in their canonical shape.
func canonNodes(nodes []Node) ([]canonNode, error) {
	out := make([]canonNode, 0, len(nodes))
	for _, node := range nodes {
		canon := canonNode{ID: node.ID, Kind: node.Kind}
		if node.Settings != nil {
			settings, err := canonSettingsOf(*node.Settings)
			if err != nil {
				return nil, err
			}
			canon.Settings = settings
		}
		if node.Loop != nil {
			until := node.Loop.Until.Node + "." + node.Loop.Until.Name
			canon.Loop = &canonLoop{Max: node.Loop.Max, Until: until, Spent: node.Loop.Spent}
		}
		if len(node.Body) > 0 {
			body, err := canonNodes(node.Body)
			if err != nil {
				return nil, err
			}
			canon.Body = body
		}
		out = append(out, canon)
	}
	return out, nil
}

// canonSettingsOf returns settings in their canonical shape: needs as a mapping from pool to units, and schema as the
// mapping its JSON holds.
func canonSettingsOf(settings Settings) (canonSettings, error) {
	out := canonSettings{
		At: settings.At, Gate: settings.Gate, Argv: settings.Argv, Limit: settings.Limit, Paths: settings.Paths,
		Agent: settings.Agent, Prompt: settings.Prompt,
	}
	if len(settings.Needs) > 0 {
		out.Needs = make(map[string]int, len(settings.Needs))
		for _, need := range settings.Needs {
			out.Needs[need.Pool] = need.Count
		}
	}
	if settings.Schema != "" {
		if err := yaml.Unmarshal([]byte(settings.Schema), &out.Schema); err != nil {
			return canonSettings{}, fmt.Errorf("schema: %w", err)
		}
	}
	return out, nil
}
