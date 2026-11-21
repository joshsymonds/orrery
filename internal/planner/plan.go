// Package planner runs an epic's planner turns. It claims an epic with pending triggers that is neither paused, parked
// nor running, checks the plan each turn returns against the epic's Requirements, gates and judges, and tells the owner
// when an epic needs them.
package planner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// Schema is the JSON schema of the planner turn's return. It uses only the keywords the step schema check accepts. A
// task's evidence and depends_on are strings the engine reads as JSON, as args is, because the API refuses a schema
// whose compiled grammar is too large: with evidence as an object, a depends_on property is refused
// (TestPlannerSchemaIsTheOneTheAPIAccepted).
const Schema = `{
	"type": "object",
	"properties": {
		"status": {"type": "string", "enum": ["done"]},
		"decisions": {"type": "array", "items": {"type": "string"}},
		"tasks": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"title": {"type": "string"},
					"deliverable": {"type": "string"},
					"files": {"type": "array", "items": {"type": "string"}},
					"requirements": {"type": "array", "items": {"type": "string"}},
					"contributes": {"type": "array", "items": {"type": "string"}},
					"test_changes": {"type": "array", "items": {"type": "string"}},
					"gate": {"type": "string"},
					"evidence": {"type": "string"},
					"depends_on": {"type": "string"}
				},
				"required": ["title", "deliverable", "files", "requirements", "contributes", "test_changes", "gate", "evidence", "depends_on"],
				"additionalProperties": false
			}
		},
		"outline": {"type": "string"},
		"questions": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"label": {"type": "string"},
					"text": {"type": "string"},
					"choices": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"id": {"type": "string"},
								"does": {"type": "string"},
								"closes": {"type": "string"}
							},
							"required": ["id", "does", "closes"],
							"additionalProperties": false
						}
					},
					"recommended": {"type": "string"},
					"why": {"type": "string"},
					"catastrophe": {"type": "string", "enum": ["irreversible-step", "contract-cannot-hold", "premise-false", "cannot-proceed"]},
					"confidence": {"type": "string", "enum": ["low", "medium", "high"]},
					"replaces": {"type": "string"}
				},
				"required": ["label", "text", "choices", "recommended", "why", "catastrophe", "confidence", "replaces"],
				"additionalProperties": false
			}
		},
		"actions": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"kind": {"type": "string"},
					"args": {"type": "string"},
					"reason": {"type": "string"}
				},
				"required": ["kind", "args", "reason"],
				"additionalProperties": false
			}
		},
		"unresolvable": {"type": "string"},
		"outcome": {"type": "string", "enum": ["work", "wait", "complete"]}
	},
	"required": ["status", "decisions", "tasks", "outline", "questions", "actions", "outcome"],
	"additionalProperties": false
}`

// Return is the planner turn's return. Unresolvable is read by Parse from the key unresolvable, a JSON array written as a
// string, as args is; UnresolvableError is why that string did not read as an array, and check.go refuses it.
type Return struct {
	Status            string         `json:"status"`
	Decisions         []string       `json:"decisions"`
	Tasks             []Task         `json:"tasks"`
	Outline           string         `json:"outline"`
	Questions         []Question     `json:"questions"`
	Actions           []EngineAction `json:"actions"`
	Unresolvable      []Unresolvable `json:"-"`
	UnresolvableError string         `json:"-"`
	Outcome           string         `json:"outcome"`
}

// Unresolvable is one gap of a gap trigger the planner cannot resolve without the owner: the gap's key, the reason the
// planner gives, and the recommendation, release or correct.
type Unresolvable struct {
	Gap         string `json:"gap"`
	Reason      string `json:"reason"`
	Recommended string `json:"recommended"`
}

// EngineAction is one action the planner asks the engine to carry out: its kind, the node, task, close, replacing epic,
// Requirement, evidence item, deliverable, files and reviewer its kind uses, the undo-none positions it names, and the
// planner's reason. check.go lists the fields each kind uses. The return carries the fields a kind uses as args, a JSON
// object written as a string, so that the schema the model is held to stays one small fixed shape however many kinds
// there are; Parse reads args into these fields. ArgsError is why args did not read, and Unknown lists the keys of args
// that no kind uses; check.go refuses either.
type EngineAction struct {
	Kind        string        `json:"kind"`
	Node        string        `json:"-"`
	Task        int64         `json:"-"`
	Close       string        `json:"-"`
	By          string        `json:"-"`
	Requirement string        `json:"-"`
	Item        evidence.Item `json:"-"`
	Deliverable string        `json:"-"`
	Files       []string      `json:"-"`
	Remove      []string      `json:"-"`
	Reviewer    string        `json:"-"`
	UndoNone    []int64       `json:"-"`
	Reason      string        `json:"reason"`
	ArgsError   string        `json:"-"`
	Unknown     []string      `json:"-"`
}

// The keys an action's args may hold, by the names the role text gives them.
const (
	argNode        = "node"
	argTask        = "task"
	argClose       = "close"
	argBy          = "by"
	argRequirement = "requirement"
	argItem        = "item"
	argDeliverable = "deliverable"
	argFiles       = "files"
	argRemove      = "remove"
	argReviewer    = "reviewer"
	argUndoNone    = "undo_none"
)

// readArgs fills action's fields from args, a JSON object written as a string. Blank args reads as no fields. args that is
// not a JSON object, or holds a key whose value is not of its field's type, leaves the fields empty and sets ArgsError; an
// item with a key an evidence item does not have is such a value. Keys outside the eleven above are listed in Unknown.
func readArgs(action *EngineAction, args string) {
	if strings.TrimSpace(args) == "" {
		return
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &keys); err != nil {
		action.ArgsError = "not a JSON object: " + err.Error()
		return
	}
	var read EngineAction
	fields := map[string]any{
		argNode: &read.Node, argTask: &read.Task, argClose: &read.Close, argBy: &read.By,
		argRequirement: &read.Requirement, argItem: &read.Item, argDeliverable: &read.Deliverable,
		argFiles: &read.Files, argRemove: &read.Remove, argReviewer: &read.Reviewer, argUndoNone: &read.UndoNone,
	}
	for key, value := range keys {
		field, known := fields[key]
		if !known {
			action.Unknown = append(action.Unknown, key)
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(field); err != nil {
			action.ArgsError = key + ": " + err.Error()
			return
		}
	}
	slices.Sort(action.Unknown)
	action.Node, action.Task, action.Close, action.By, action.UndoNone =
		read.Node, read.Task, read.Close, read.By, read.UndoNone
	action.Requirement, action.Item, action.Deliverable = read.Requirement, read.Item, read.Deliverable
	action.Files, action.Remove = read.Files, read.Remove
	action.Reviewer = read.Reviewer
}

// Question is one question the planner asks the owner: its label, its text, the choices it declares, the choice it
// recommends with why and how confidently, the catastrophe it names, one of the four kinds check.go lists, and the
// question it replaces, "" or Q<n>.
type Question struct {
	Label       string   `json:"label"`
	Text        string   `json:"text"`
	Choices     []Choice `json:"choices"`
	Recommended string   `json:"recommended"`
	Why         string   `json:"why"`
	Catastrophe string   `json:"catastrophe"`
	Confidence  string   `json:"confidence"`
	Replaces    string   `json:"replaces"`
}

// Choice is one choice a question declares: its id, what choosing it does, and closes, the ids of the open ledger
// findings that choosing it settles with no code change, separated by commas, or blank when it closes none. The closure
// round closes each of them on that answer. closes is one string the engine splits (closesOf), as an action's args is,
// so that the schema keeps the size the API compiles (TestPlannerSchemaIsTheOneTheAPIAccepted).
type Choice struct {
	ID     string `json:"id"`
	Does   string `json:"does"`
	Closes string `json:"closes"`
}

// closesOf returns the finding ids a choice's closes names, in order, each with its surrounding spaces trimmed. A blank
// closes names none. An id left empty between commas stays, as "", for the store to refuse.
func closesOf(closes string) []string {
	if strings.TrimSpace(closes) == "" {
		return nil
	}
	ids := strings.Split(closes, ",")
	for i, id := range ids {
		ids[i] = strings.TrimSpace(id)
	}
	return ids
}

// Task is one task the planner proposes for the epic. Its evidence is an object, or a string holding one, which
// UnmarshalJSON reads; EvidenceError is why a string did not read, and check.go refuses it. DependsOn is the tasks
// this one depends on, each a T<n> of the epic or the title of a task of the return. Parse reads it from the key
// depends_on, a JSON array of strings written as a string; DependsOnError is why that string did not read, and
// check.go refuses it.
type Task struct {
	Title          string        `json:"title"`
	Deliverable    string        `json:"deliverable"`
	Files          []string      `json:"files"`
	Requirements   []string      `json:"requirements"`
	Contributes    []string      `json:"contributes"`
	TestChanges    []string      `json:"-"` // read by Parse from the key test_changes
	DependsOn      []string      `json:"-"` // read by Parse from the key depends_on
	DependsOnError string        `json:"-"`
	Gate           string        `json:"gate"`
	Evidence       evidence.Item `json:"evidence"`
	EvidenceError  string        `json:"-"`
}

// UnmarshalJSON decodes a task. Its key evidence is read as an object, or as a string holding one, which decodeEvidence
// reads; every other key is read as the struct's tags say. A value of the evidence key that is neither a string nor an
// object is an error of the whole return.
func (task *Task) UnmarshalJSON(raw []byte) error {
	type plain Task
	var shadow struct {
		plain
		EvidenceValue json.RawMessage `json:"evidence"`
	}
	if err := json.Unmarshal(raw, &shadow); err != nil {
		return fmt.Errorf("task: %w", err)
	}
	*task = Task(shadow.plain)
	if len(shadow.EvidenceValue) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(shadow.EvidenceValue, &text); err == nil {
		task.Evidence, task.EvidenceError = decodeEvidence(text)
		return nil
	}
	if err := json.Unmarshal(shadow.EvidenceValue, &task.Evidence); err != nil {
		return fmt.Errorf("task evidence: %w", err)
	}
	return nil
}

// decodeEvidence reads text as one evidence object whose keys are the item's, as readArgs reads an action's item, so
// an unknown key is refused. Blank text reads as an empty item. Text that is not such an object gives an empty item and
// the reason it did not read.
func decodeEvidence(text string) (evidence.Item, string) {
	var item evidence.Item
	if strings.TrimSpace(text) == "" {
		return item, ""
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&item); err != nil {
		return evidence.Item{}, err.Error()
	}
	if _, err := decoder.Token(); err != io.EOF {
		return evidence.Item{}, "data after the JSON object"
	}
	return item, ""
}

// Parse decodes a planner return from raw JSON. It refuses input that is not valid JSON, an unresolvable that is not a
// string, a task's depends_on that is not a string, and a task's evidence that is neither a string nor an object. A
// task's test_changes is read by its key, as the schema spells it, since the package's JSON tags are camel case. Its
// depends_on is read by its key and then by readDependsOn, and its evidence by Task.UnmarshalJSON. Each action's fields
// are read from its args (readArgs), and the unresolvable list from its string (readUnresolvable).
func Parse(raw json.RawMessage) (Return, error) {
	var ret Return
	if err := json.Unmarshal(raw, &ret); err != nil {
		return Return{}, fmt.Errorf("planner return: %w", err)
	}
	var keyed struct {
		Tasks        []map[string]json.RawMessage `json:"tasks"`
		Actions      []map[string]json.RawMessage `json:"actions"`
		Unresolvable json.RawMessage              `json:"unresolvable"`
	}
	if err := json.Unmarshal(raw, &keyed); err != nil {
		return Return{}, fmt.Errorf("planner return: %w", err)
	}
	for i, task := range keyed.Tasks {
		value, ok := task["test_changes"]
		if !ok {
			continue
		}
		if err := json.Unmarshal(value, &ret.Tasks[i].TestChanges); err != nil {
			return Return{}, fmt.Errorf("planner return: tasks[%d] test_changes: %w", i, err)
		}
	}
	if err := readDependsOnKeys(&ret, keyed.Tasks); err != nil {
		return Return{}, err
	}
	for i, action := range keyed.Actions {
		var args string
		if value, ok := action["args"]; ok {
			if err := json.Unmarshal(value, &args); err != nil {
				return Return{}, fmt.Errorf("planner return: actions[%d] args: %w", i, err)
			}
		}
		readArgs(&ret.Actions[i], args)
	}
	if len(keyed.Unresolvable) > 0 {
		var text string
		if err := json.Unmarshal(keyed.Unresolvable, &text); err != nil {
			return Return{}, fmt.Errorf("planner return: unresolvable: %w", err)
		}
		readUnresolvable(&ret, text)
	}
	return ret, nil
}

// readUnresolvable fills ret's Unresolvable from text, the unresolvable string of a return, a JSON array of objects with
// gap, reason and recommended. Blank text reads as an empty list. Text that is not such an array leaves the list empty and
// sets UnresolvableError, why it did not read.
func readUnresolvable(ret *Return, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	entries, err := decodeUnresolvable(text)
	if err != nil {
		ret.UnresolvableError = err.Error()
		return
	}
	ret.Unresolvable = entries
}

// readDependsOnKeys reads the depends_on key of each task of ret, whose keys are tasks, as readDependsOn reads its string.
// A key that is not a string is an error of the whole return.
func readDependsOnKeys(ret *Return, tasks []map[string]json.RawMessage) error {
	for i, task := range tasks {
		value, ok := task["depends_on"]
		if !ok {
			continue
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return fmt.Errorf("planner return: tasks[%d] depends_on: %w", i, err)
		}
		readDependsOn(&ret.Tasks[i], text)
	}
	return nil
}

// readDependsOn fills task's DependsOn from text, the depends_on string of a task: a JSON array of strings. Blank text
// and an empty array read as none. Text that is not such an array, a null included, leaves the list empty and sets
// DependsOnError.
func readDependsOn(task *Task, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	var names []string
	if err := json.Unmarshal([]byte(text), &names); err != nil {
		task.DependsOnError = err.Error()
		return
	}
	if names == nil {
		task.DependsOnError = "null is not an array"
		return
	}
	if len(names) > 0 {
		task.DependsOn = names
	}
}

// decodeUnresolvable decodes text as one JSON array of objects whose only keys are gap, reason and recommended, each
// holding a string. A null, or any value that is not an array, is refused.
func decodeUnresolvable(text string) ([]Unresolvable, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var entries []Unresolvable
	if err := decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if entries == nil {
		return nil, errors.New("not a JSON array")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("data after the JSON array")
	}
	return entries, nil
}
