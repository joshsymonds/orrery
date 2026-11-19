// Package planner runs an epic's planner turns. It claims an epic with pending triggers that is neither paused, parked
// nor running, checks the plan each turn returns against the epic's Requirements, gates and judges, and tells the owner
// when an epic needs them.
package planner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// Schema is the JSON schema of the planner turn's return. It uses only the keywords the step schema check accepts.
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
					"evidence": {
						"type": "object",
						"properties": {
							"capture": {"type": "array", "items": {"type": "string"}},
							"limit": {"type": "integer"},
							"artifacts": {"type": "array", "items": {"type": "string"}},
							"supplied": {"type": "array", "items": {"type": "string"}},
							"conclusion": {"type": "string"},
							"judge": {"type": "string"},
							"kind": {"type": "string"},
							"tests": {"type": "array", "items": {"type": "string"}}
						},
						"required": ["capture", "limit", "artifacts", "supplied", "conclusion", "judge", "kind", "tests"],
						"additionalProperties": false
					}
				},
				"required": ["title", "deliverable", "files", "requirements", "contributes", "test_changes", "gate", "evidence"],
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
		"outcome": {"type": "string", "enum": ["work", "wait", "complete"]}
	},
	"required": ["status", "decisions", "tasks", "outline", "questions", "actions", "outcome"],
	"additionalProperties": false
}`

// Return is the planner turn's return.
type Return struct {
	Status    string         `json:"status"`
	Decisions []string       `json:"decisions"`
	Tasks     []Task         `json:"tasks"`
	Outline   string         `json:"outline"`
	Questions []Question     `json:"questions"`
	Actions   []EngineAction `json:"actions"`
	Outcome   string         `json:"outcome"`
}

// EngineAction is one action the planner asks the engine to carry out: its kind, the node, task, close, replacing epic,
// Requirement, evidence item and deliverable its kind uses, the undo-none positions it names, and the planner's reason.
// check.go lists the fields each kind uses. The return carries the fields a kind uses as args, a JSON object written as
// a string, so that the schema the model is held to stays one small fixed shape however many kinds there are; Parse
// reads args into these fields. ArgsError is why args did not read, and Unknown lists the keys of args that no kind uses;
// check.go refuses either.
type EngineAction struct {
	Kind        string        `json:"kind"`
	Node        string        `json:"-"`
	Task        int64         `json:"-"`
	Close       string        `json:"-"`
	By          string        `json:"-"`
	Requirement string        `json:"-"`
	Item        evidence.Item `json:"-"`
	Deliverable string        `json:"-"`
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
	argUndoNone    = "undo_none"
)

// readArgs fills action's fields from args, a JSON object written as a string. Blank args reads as no fields. args that is
// not a JSON object, or holds a key whose value is not of its field's type, leaves the fields empty and sets ArgsError; an
// item with a key an evidence item does not have is such a value. Keys outside the eight above are listed in Unknown.
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
		argUndoNone: &read.UndoNone,
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

// Task is one task the planner proposes for the epic.
type Task struct {
	Title        string        `json:"title"`
	Deliverable  string        `json:"deliverable"`
	Files        []string      `json:"files"`
	Requirements []string      `json:"requirements"`
	Contributes  []string      `json:"contributes"`
	TestChanges  []string      `json:"-"` // read by Parse from the key test_changes
	Gate         string        `json:"gate"`
	Evidence     evidence.Item `json:"evidence"`
}

// Parse decodes a planner return from raw JSON. It refuses input that is not valid JSON. A task's test_changes is read by
// its key, as the schema spells it, since the package's JSON tags are camel case, and each action's fields are read from
// its args (readArgs).
func Parse(raw json.RawMessage) (Return, error) {
	var ret Return
	if err := json.Unmarshal(raw, &ret); err != nil {
		return Return{}, fmt.Errorf("planner return: %w", err)
	}
	var keyed struct {
		Tasks   []map[string]json.RawMessage `json:"tasks"`
		Actions []map[string]json.RawMessage `json:"actions"`
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
	for i, action := range keyed.Actions {
		var args string
		if value, ok := action["args"]; ok {
			if err := json.Unmarshal(value, &args); err != nil {
				return Return{}, fmt.Errorf("planner return: actions[%d] args: %w", i, err)
			}
		}
		readArgs(&ret.Actions[i], args)
	}
	return ret, nil
}
