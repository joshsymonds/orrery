// Package planner decides an epic's next tasks (O3c1).
package planner

import (
	"encoding/json"
	"fmt"

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
					"gate": {"type": "string"},
					"evidence": {
						"type": "object",
						"properties": {
							"capture": {"type": "array", "items": {"type": "string"}},
							"limit": {"type": "integer"},
							"artifacts": {"type": "array", "items": {"type": "string"}},
							"supplied": {"type": "array", "items": {"type": "string"}},
							"conclusion": {"type": "string"},
							"judge": {"type": "string"}
						},
						"required": ["capture", "limit", "artifacts", "supplied", "conclusion", "judge"],
						"additionalProperties": false
					}
				},
				"required": ["title", "deliverable", "files", "requirements", "gate", "evidence"],
				"additionalProperties": false
			}
		},
		"outline": {"type": "string"},
		"questions": {"type": "array", "items": {"type": "string"}},
		"outcome": {"type": "string", "enum": ["work", "wait", "complete"]}
	},
	"required": ["status", "decisions", "tasks", "outline", "questions", "outcome"],
	"additionalProperties": false
}`

// Return is the planner turn's return.
type Return struct {
	Status    string   `json:"status"`
	Decisions []string `json:"decisions"`
	Tasks     []Task   `json:"tasks"`
	Outline   string   `json:"outline"`
	Questions []string `json:"questions"`
	Outcome   string   `json:"outcome"`
}

// Task is one task the planner proposes for the epic.
type Task struct {
	Title        string        `json:"title"`
	Deliverable  string        `json:"deliverable"`
	Files        []string      `json:"files"`
	Requirements []string      `json:"requirements"`
	Gate         string        `json:"gate"`
	Evidence     evidence.Item `json:"evidence"`
}

// Parse decodes a planner return from raw JSON. It refuses input that is not valid JSON.
func Parse(raw json.RawMessage) (Return, error) {
	var ret Return
	if err := json.Unmarshal(raw, &ret); err != nil {
		return Return{}, fmt.Errorf("planner return: %w", err)
	}
	return ret, nil
}
