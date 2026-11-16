// Package workflow is the workflow file format: the nodes a workflow runs, the kinds they may be, where each sits, and
// the checks that refuse a file before anything runs it.
package workflow

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Workflow is one workflow: its name, the file it was read from, and its top-level nodes.
type Workflow struct {
	Name  string
	File  string
	Nodes []Node
}

// Node is one step of a workflow. Exactly one of Kind, Include and Loop is set: a kind node names a registered kind,
// holds its Body when the kind has one and its Settings when the kind is a check, an include node names another
// workflow, and a loop node holds Loop and its Body. File and Line locate the node in the file it was read from.
type Node struct {
	ID       string
	Kind     string
	Include  string
	Loop     *Loop
	Settings *Settings
	Body     []Node
	File     string
	Line     int
}

// Loop is a loop node's settings: Max rounds at most, the Until outcome that ends it, and Spent, what happens when
// the rounds run out: park or continue.
type Loop struct {
	Max   int
	Until Outcome
	Spent string
}

// Outcome names an outcome: Node is the id of a node and Name the outcome that node reports.
type Outcome struct {
	Node string
	Name string
}

const (
	keyName    = "name"
	keyNodes   = "nodes"
	keyID      = "id"
	keyKind    = "kind"
	keyLoop    = "loop"
	keyInclude = "include"
	keyBody    = "body"
	keyMax     = "max"
	keyUntil   = "until"
	keySpent   = "spent"
	keyAt      = "at"
	keyGate    = "gate"
	keyArgv    = "argv"
	keyLimit   = "limit"
	keyNeeds   = "needs"
	keyPaths   = "paths"
	keyAgent   = "agent"
	keyPrompt  = "prompt"
	keySchema  = "schema"

	// loopPrefix is put before a loop setting's key in a refusal, so the key reads loop.max.
	loopPrefix = "loop."

	// needsPrefix is put before a needs pool's key in a refusal, so the key reads needs.<pool>.
	needsPrefix = "needs."

	kindPlan      = "plan"
	kindBuild     = "build"
	kindIntegrate = "integrate"
	kindGate      = "gate"
	kindEvidence  = "evidence"
	kindOwned     = "owned"
	kindCommand   = "command"
	kindAgent     = "agent"
	kindReview    = "review"
	kindClosure   = "closure"
	kindConclude  = "conclude"
	kindRelease   = "release"

	outcomeComplete = "complete"
	outcomePassed   = "passed"
	outcomeFailed   = "failed"

	atBase = "base"
	atHead = "head"

	spentPark     = "park"
	spentContinue = "continue"
)

// namePattern is the shape of a workflow name, of a node id and of an include.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// scope is where a node sits: at the top level, in a loop body or in a plan body. scopeNone is the body of a kind
// that takes none.
type scope int

const (
	scopeNone scope = iota
	scopeTop
	scopeLoop
	scopePlan
)

// where names a scope in a refusal.
func (s scope) where() string {
	switch s {
	case scopeLoop:
		return "in a loop body"
	case scopePlan:
		return "in a plan body"
	default:
		return "at the top level"
	}
}

// outer is where a plan, a loop and an include sit: the top level and a loop body.
func outer() []scope {
	return []scope{scopeTop, scopeLoop}
}

// anywhere is every scope a node can sit in: the top level, a loop body and a plan body.
func anywhere() []scope {
	return []scope{scopeTop, scopeLoop, scopePlan}
}

// kindSpec is one registered kind: the scopes it sits in, the scope its body holds (scopeNone when it takes no body),
// the outcomes it reports, and, for a check kind, the keys of its settings and the reader of them.
type kindSpec struct {
	sits     []scope
	body     scope
	outcomes []string
	keys     []string
	read     settingsReader
}

// settingsReader reads a check kind's settings from the pairs of its node.
type settingsReader func(here site, pairs []pair) (*Settings, error)

// kinds is the table of registered kinds. A plan sits at the top level and in a loop body, holds a build and an
// integrate and reports complete. A build sits only in a plan body and takes no settings and reports no outcome. An
// integrate sits only in a plan body, takes no settings and no body, and reports passed or failed. A check kind sits
// anywhere, takes the settings its reader reads and reports passed or failed. The epic kinds take no settings and no
// body: a review, a conclude and a release sit at the top level, and a closure sits in a loop body. A review and a
// conclude report passed, and a closure and a release report passed or failed. The interpreter always registers the
// release kind, and registers the review, closure and conclude kinds when it has reviewers and an ending; without them
// a node of one of those waits.
func kinds() map[string]kindSpec {
	checkOutcomes := []string{outcomePassed, outcomeFailed}
	passed := []string{outcomePassed}
	return map[string]kindSpec{
		kindPlan:      {sits: outer(), body: scopePlan, outcomes: []string{outcomeComplete}},
		kindBuild:     {sits: []scope{scopePlan}},
		kindIntegrate: {sits: []scope{scopePlan}, outcomes: checkOutcomes},
		kindGate: {
			sits: anywhere(), outcomes: checkOutcomes, keys: []string{keyAt, keyGate}, read: readGateSettings,
		},
		kindEvidence: {
			sits: anywhere(), outcomes: checkOutcomes, keys: []string{keyAt}, read: readEvidenceSettings,
		},
		kindOwned: {
			sits: anywhere(), outcomes: checkOutcomes, keys: []string{keyPaths}, read: readOwnedSettings,
		},
		kindCommand: {
			sits: anywhere(), outcomes: checkOutcomes, keys: []string{keyAt, keyArgv, keyLimit, keyNeeds},
			read: readCommandSettings,
		},
		kindAgent: {
			sits: anywhere(), outcomes: checkOutcomes, keys: []string{keyAt, keyAgent, keyPrompt, keySchema},
			read: readAgentSettings,
		},
		kindReview:   {sits: []scope{scopeTop}, outcomes: passed},
		kindClosure:  {sits: []scope{scopeLoop}, outcomes: checkOutcomes},
		kindConclude: {sits: []scope{scopeTop}, outcomes: passed},
		kindRelease:  {sits: []scope{scopeTop}, outcomes: checkOutcomes},
	}
}

// site is where a refusal starts: a file, a line, and the node it names. An empty node names the workflow itself.
type site struct {
	file string
	line int
	node string
}

// refusef is the refusal for one key at line: `<file>:<line>: node <node>: key <key>: <reason>`, with no node part at
// the workflow level.
func (here site) refusef(line int, key, format string, args ...any) error {
	where := fmt.Sprintf("%s:%d: ", here.file, line)
	if here.node != "" {
		where += "node " + here.node + ": "
	}
	return fmt.Errorf("%skey %s: %s", where, key, fmt.Sprintf(format, args...))
}

// pair is one key and value of a mapping. key is the key as the file spells it, path names it in a refusal (a loop
// setting's path carries loopPrefix), and line is the line of the key.
type pair struct {
	key   string
	path  string
	line  int
	value *yaml.Node
}

// Parse reads one workflow file and returns it. file names the file in every refusal, and the workflow's name must be
// the file's name: its base name without .yaml, and without the "built-in " prefix a built-in carries. Parse leaves
// includes unresolved; Check checks the workflow once they are expanded.
func Parse(file string, text []byte) (Workflow, error) {
	root, err := document(file, text)
	if err != nil {
		return Workflow{}, err
	}
	return readWorkflow(file, root)
}

// Check checks an expanded workflow: no include is left, every id is unique across the whole workflow, and each
// loop's until names a direct child of its body whose kind reports that outcome. It returns the first refusal.
func Check(w Workflow) error {
	return checkNodes(w.Nodes, map[string]Node{})
}

// document returns the root of the file's one YAML document. An empty file, a second document and a syntax error are
// refused.
func document(file string, text []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(text))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: empty file: a workflow is one YAML document", file)
		}
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return nil, fmt.Errorf("%s: %w", file, err)
	default:
		return nil, fmt.Errorf("%s:%d: a workflow is one YAML document", file, extra.Line)
	}
	return doc.Content[0], nil
}

// readWorkflow reads the top-level mapping, which holds exactly name and nodes.
func readWorkflow(file string, root *yaml.Node) (Workflow, error) {
	if root.Kind != yaml.MappingNode {
		return Workflow{}, fmt.Errorf("%s:%d: a workflow is a mapping of name and nodes", file, root.Line)
	}
	here := site{file: file, line: root.Line}
	pairs, err := pairsOf(here, root, "")
	if err != nil {
		return Workflow{}, err
	}
	return readTop(here, pairs)
}

// readTop reads the top-level keys, name and nodes, which are both required.
func readTop(here site, pairs []pair) (Workflow, error) {
	if err := checkKeys(here, pairs, keyName, keyNodes); err != nil {
		return Workflow{}, err
	}
	namePr, ok := find(pairs, keyName)
	if !ok {
		return Workflow{}, here.refusef(here.line, keyName, "required")
	}
	nodesPr, ok := find(pairs, keyNodes)
	if !ok {
		return Workflow{}, here.refusef(here.line, keyNodes, "required")
	}
	name, err := workflowName(here, namePr)
	if err != nil {
		return Workflow{}, err
	}
	nodes, err := readList(here, nodesPr, scopeTop)
	if err != nil {
		return Workflow{}, err
	}
	return Workflow{Name: name, File: here.file, Nodes: nodes}, nil
}

// workflowName reads the name: a name that matches namePattern and equals the file's name.
func workflowName(here site, pr pair) (string, error) {
	name, err := nameOf(here, pr)
	if err != nil {
		return "", err
	}
	if want := fileName(here.file); name != want {
		return "", here.refusef(pr.line, pr.path, "%q differs from the file's name %q", name, want)
	}
	return name, nil
}

// fileName is the name a workflow file carries: its base name without .yaml, and without the "built-in " prefix a
// built-in file carries.
func fileName(file string) string {
	return strings.TrimPrefix(strings.TrimSuffix(filepath.Base(file), ".yaml"), "built-in ")
}

// pairsOf reads a mapping's keys and values in file order. A key that is not a single value, or that appears twice, is
// refused. prefix is put before each key's path.
func pairsOf(here site, m *yaml.Node, prefix string) ([]pair, error) {
	pairs := make([]pair, 0, len(m.Content)/2)
	seen := make(map[string]bool, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, value := m.Content[i], m.Content[i+1]
		if key.Kind != yaml.ScalarNode {
			return nil, here.refusef(key.Line, "#"+strconv.Itoa(i/2+1), "must be a single value")
		}
		if seen[key.Value] {
			return nil, here.refusef(key.Line, prefix+key.Value, "appears twice")
		}
		seen[key.Value] = true
		pairs = append(pairs, pair{key: key.Value, path: prefix + key.Value, line: key.Line, value: value})
	}
	return pairs, nil
}

// checkKeys refuses a key that is not among allowed.
func checkKeys(here site, pairs []pair, allowed ...string) error {
	for _, pr := range pairs {
		if !slices.Contains(allowed, pr.key) {
			return here.refusef(pr.line, pr.path, "unknown key")
		}
	}
	return nil
}

// find returns the pair under key, and whether the mapping holds one.
func find(pairs []pair, key string) (pair, bool) {
	i := slices.IndexFunc(pairs, func(pr pair) bool { return pr.key == key })
	if i < 0 {
		return pair{}, false
	}
	return pairs[i], true
}

// requireKey returns the pair under key, and refuses the node when the mapping holds none.
func requireKey(here site, pairs []pair, key string) (pair, error) {
	pr, ok := find(pairs, key)
	if !ok {
		return pair{}, here.refusef(here.line, key, "required")
	}
	return pr, nil
}

// text reads a pair's value, which must be a single value.
func text(here site, pr pair) (string, error) {
	if pr.value.Kind != yaml.ScalarNode {
		return "", here.refusef(pr.line, pr.path, "must be a single value")
	}
	return pr.value.Value, nil
}

// nameOf reads a value that must match namePattern: a workflow name, a node id or an include.
func nameOf(here site, pr pair) (string, error) {
	val, err := text(here, pr)
	if err != nil {
		return "", err
	}
	if !namePattern.MatchString(val) {
		return "", here.refusef(pr.line, pr.path, "%q must match %s", val, namePattern)
	}
	return val, nil
}

// idOf reads a kind or loop node's id, which is required.
func idOf(here site, pairs []pair) (string, error) {
	pr, ok := find(pairs, keyID)
	if !ok {
		return "", here.refusef(here.line, keyID, "required")
	}
	return nameOf(here, pr)
}

// readList reads the sequence under pr, whose nodes sit at scope at. The sequence holds at least one node, and each
// item is a mapping.
func readList(here site, pr pair, at scope) ([]Node, error) {
	seq := pr.value
	if seq.Kind != yaml.SequenceNode {
		return nil, here.refusef(pr.line, pr.path, "must be a sequence of nodes")
	}
	if len(seq.Content) == 0 {
		return nil, here.refusef(pr.line, pr.path, "must hold at least one node")
	}
	nodes := make([]Node, 0, len(seq.Content))
	for i, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			return nil, here.refusef(item.Line, pr.path, "item %d must be a mapping", i+1)
		}
		node, err := readNode(here.file, item, i+1, at)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// readNode reads the node at position pos of its list, which sits at scope at.
func readNode(file string, item *yaml.Node, pos int, at scope) (Node, error) {
	here := site{file: file, line: item.Line, node: nodeLabel(item, pos)}
	pairs, err := pairsOf(here, item, "")
	if err != nil {
		return Node{}, err
	}
	form, err := formOf(here, pairs)
	if err != nil {
		return Node{}, err
	}
	switch form {
	case keyKind:
		return readKind(here, pairs, at)
	case keyLoop:
		return readLoop(here, pairs, at)
	default:
		return readInclude(here, pairs, at)
	}
}

// nodeLabel names a node by its id, or as #pos, its 1-based position in its list, when it has none.
func nodeLabel(item *yaml.Node, pos int) string {
	for i := 0; i+1 < len(item.Content); i += 2 {
		key, value := item.Content[i], item.Content[i+1]
		if key.Value == keyID && value.Kind == yaml.ScalarNode && value.Value != "" {
			return value.Value
		}
	}
	return "#" + strconv.Itoa(pos)
}

// formOf returns the form key a node carries: kind, loop or include. A node carries exactly one.
func formOf(here site, pairs []pair) (string, error) {
	forms := []string{keyKind, keyLoop, keyInclude}
	form := ""
	for _, pr := range pairs {
		if !slices.Contains(forms, pr.key) {
			continue
		}
		if form != "" {
			return "", here.refusef(pr.line, pr.key, "cannot sit beside %s", form)
		}
		form = pr.key
	}
	if form == "" {
		return "", here.refusef(here.line, keyKind, "one of %s, %s or %s is required", keyKind, keyLoop, keyInclude)
	}
	return form, nil
}

// readKind reads a node that names a registered kind. It takes an id, its kind and, when the kind has one, a body.
func readKind(here site, pairs []pair, at scope) (Node, error) {
	kindPr, _ := find(pairs, keyKind)
	kind, err := text(here, kindPr)
	if err != nil {
		return Node{}, err
	}
	spec, ok := kinds()[kind]
	if !ok {
		return Node{}, here.refusef(kindPr.line, keyKind, "unknown kind %q", kind)
	}
	if !slices.Contains(spec.sits, at) {
		return Node{}, here.refusef(kindPr.line, keyKind, "%s cannot sit %s", kind, at.where())
	}
	return readKindBody(here, pairs, kind, spec)
}

// readKindBody reads a registered kind's node: its id, the settings the kind takes, and the body the kind holds when it
// has one.
func readKindBody(here site, pairs []pair, kind string, spec kindSpec) (Node, error) {
	allowed := append([]string{keyID, keyKind}, spec.keys...)
	if spec.body != scopeNone {
		allowed = append(allowed, keyBody)
	}
	if err := checkKeys(here, pairs, allowed...); err != nil {
		return Node{}, err
	}
	id, err := idOf(here, pairs)
	if err != nil {
		return Node{}, err
	}
	node := Node{ID: id, Kind: kind, File: here.file, Line: here.line}
	if spec.read != nil {
		if node.Settings, err = spec.read(here, pairs); err != nil {
			return Node{}, err
		}
	}
	if spec.body == scopeNone {
		return node, nil
	}
	bodyPr, ok := find(pairs, keyBody)
	if !ok {
		return Node{}, here.refusef(here.line, keyBody, "required")
	}
	if node.Body, err = readList(here, bodyPr, spec.body); err != nil {
		return Node{}, err
	}
	return node, nil
}

// readLoop reads a loop node: an id, its loop settings, and a body that holds what the top level holds.
func readLoop(here site, pairs []pair, at scope) (Node, error) {
	loopPr, _ := find(pairs, keyLoop)
	if !slices.Contains(outer(), at) {
		return Node{}, here.refusef(loopPr.line, keyLoop, "cannot sit %s", at.where())
	}
	if err := checkKeys(here, pairs, keyID, keyLoop, keyBody); err != nil {
		return Node{}, err
	}
	id, err := idOf(here, pairs)
	if err != nil {
		return Node{}, err
	}
	loop, err := readLoopSettings(here, loopPr)
	if err != nil {
		return Node{}, err
	}
	bodyPr, ok := find(pairs, keyBody)
	if !ok {
		return Node{}, here.refusef(here.line, keyBody, "required")
	}
	body, err := readList(here, bodyPr, scopeLoop)
	if err != nil {
		return Node{}, err
	}
	return Node{ID: id, Loop: &loop, Body: body, File: here.file, Line: here.line}, nil
}

// readLoopSettings reads a loop node's loop mapping. Its keys are named loop.<key> in refusals.
func readLoopSettings(here site, loopPr pair) (Loop, error) {
	if loopPr.value.Kind != yaml.MappingNode {
		return Loop{}, here.refusef(loopPr.line, keyLoop, "must be a mapping")
	}
	inner := site{file: here.file, line: loopPr.value.Line, node: here.node}
	pairs, err := pairsOf(inner, loopPr.value, loopPrefix)
	if err != nil {
		return Loop{}, err
	}
	return readLoopFields(inner, pairs)
}

// readLoopFields reads max, until and spent, and refuses any other key.
func readLoopFields(here site, pairs []pair) (Loop, error) {
	if err := checkKeys(here, pairs, keyMax, keyUntil, keySpent); err != nil {
		return Loop{}, err
	}
	rounds, err := readMax(here, pairs)
	if err != nil {
		return Loop{}, err
	}
	until, err := readUntil(here, pairs)
	if err != nil {
		return Loop{}, err
	}
	spent, err := readSpent(here, pairs)
	if err != nil {
		return Loop{}, err
	}
	return Loop{Max: rounds, Until: until, Spent: spent}, nil
}

// readMax reads max: an integer of 1 or more, required.
func readMax(here site, pairs []pair) (int, error) {
	pr, ok := find(pairs, keyMax)
	if !ok {
		return 0, here.refusef(here.line, loopPrefix+keyMax, "required")
	}
	return countOf(here, pr)
}

// countOf reads the value under pr, which must be an integer of 1 or more. The value must resolve to a YAML integer
// before it is decoded, because decoding a float such as 2.5 into an int truncates it without an error.
func countOf(here site, pr pair) (int, error) {
	var n int
	if pr.value.ShortTag() != "!!int" || pr.value.Decode(&n) != nil || n < 1 {
		return 0, here.refusef(pr.line, pr.path, "must be an integer of 1 or more")
	}
	return n, nil
}

// readUntil reads until: a node id and an outcome joined by a dot, required.
func readUntil(here site, pairs []pair) (Outcome, error) {
	pr, ok := find(pairs, keyUntil)
	if !ok {
		return Outcome{}, here.refusef(here.line, loopPrefix+keyUntil, "required")
	}
	val, err := text(here, pr)
	if err != nil {
		return Outcome{}, err
	}
	node, outcome, found := strings.Cut(val, ".")
	if !found || node == "" || outcome == "" {
		return Outcome{}, here.refusef(pr.line, pr.path, "must be <node id>.<outcome>")
	}
	return Outcome{Node: node, Name: outcome}, nil
}

// readSpent reads spent: park or continue, park when absent.
func readSpent(here site, pairs []pair) (string, error) {
	pr, ok := find(pairs, keySpent)
	if !ok {
		return spentPark, nil
	}
	spent, err := text(here, pr)
	if err != nil {
		return "", err
	}
	if spent != spentPark && spent != spentContinue {
		return "", here.refusef(pr.line, pr.path, "must be park or continue")
	}
	return spent, nil
}

// readInclude reads an include node, whose only key is include: a workflow name.
func readInclude(here site, pairs []pair, at scope) (Node, error) {
	incPr, _ := find(pairs, keyInclude)
	if !slices.Contains(outer(), at) {
		return Node{}, here.refusef(incPr.line, keyInclude, "cannot sit %s", at.where())
	}
	if err := checkKeys(here, pairs, keyInclude); err != nil {
		return Node{}, err
	}
	name, err := nameOf(here, incPr)
	if err != nil {
		return Node{}, err
	}
	return Node{Include: name, File: here.file, Line: here.line}, nil
}

// checkNodes checks one list of nodes and every node under it. seen holds the first node to use each id.
func checkNodes(nodes []Node, seen map[string]Node) error {
	for i, node := range nodes {
		here := site{file: node.File, line: node.Line, node: node.label(i + 1)}
		if node.Include != "" {
			return here.refusef(node.Line, keyInclude, "unresolved include %q", node.Include)
		}
		if node.ID != "" {
			if first, dup := seen[node.ID]; dup {
				return here.refusef(node.Line, keyID, "already used at %s:%d", first.File, first.Line)
			}
			seen[node.ID] = node
		}
		if node.Loop != nil {
			if err := checkUntil(here, node); err != nil {
				return err
			}
		}
		if err := checkNodes(node.Body, seen); err != nil {
			return err
		}
	}
	return nil
}

// checkUntil checks that a loop's until names a direct child of the loop's body whose kind reports the outcome.
func checkUntil(here site, loop Node) error {
	until := loop.Loop.Until
	for _, child := range loop.Body {
		if child.ID != until.Node {
			continue
		}
		if spec, ok := kinds()[child.Kind]; ok && slices.Contains(spec.outcomes, until.Name) {
			return nil
		}
		return here.refusef(loop.Line, loopPrefix+keyUntil, "%s has no outcome %q", until.Node, until.Name)
	}
	return here.refusef(loop.Line, loopPrefix+keyUntil, "%q is not a node in the loop body", until.Node)
}

// label names a node by its id, or as #pos, its 1-based position in its list, when it has none.
func (n Node) label(pos int) string {
	if n.ID != "" {
		return n.ID
	}
	return "#" + strconv.Itoa(pos)
}
