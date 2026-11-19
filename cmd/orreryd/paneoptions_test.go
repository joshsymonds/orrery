package main

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// optionsAsk is a stored AskUserQuestion with one question that is not multiSelect and three invented options.
const optionsAsk = `{"tool":"AskUserQuestion","questions":[{"question":"Which fruit?","header":"Fruit",` +
	`"options":[{"label":"quince","description":"tart"},{"label":"fig","description":"sweet"},` +
	`{"label":"lime","description":"sour"}],"multiSelect":false}]}`

// TestPushOptionsWaitingAskPushesLabelsInOrder checks that a waiting pane whose question is a one-question, non
// multiSelect AskUserQuestion pushes its option labels in order, and no request.
func TestPushOptionsWaitingAskPushesLabelsInOrder(t *testing.T) {
	transition := paneTransition(tmuxctl.Server{}, "%7", protocol.StateWaiting, "/work/invented", "{}")
	note := paneNotification(transition, paneText{question: optionsAsk})
	if want := []string{"quince", "fig", "lime"}; !slices.Equal(note.Options, want) {
		t.Errorf("options = %q, want %q", note.Options, want)
	}
	if note.Request != "" {
		t.Errorf("request = %q, want none for a pane", note.Request)
	}
}

// TestPushOptionsNoOptionsOutsideOneChoiceAsk checks that a waiting pane whose question is not a one-question,
// non multiSelect AskUserQuestion pushes no options.
func TestPushOptionsNoOptionsOutsideOneChoiceAsk(t *testing.T) {
	cases := map[string]string{
		"two questions": `{"tool":"AskUserQuestion","questions":[` +
			`{"question":"Which fruit?","header":"Fruit","options":[{"label":"fig","description":"sweet"}],"multiSelect":false},` +
			`{"question":"Which tree?","header":"Tree","options":[{"label":"oak","description":"tall"}],"multiSelect":false}]}`,
		"multiSelect ask": `{"tool":"AskUserQuestion","questions":[{"question":"Which fruits?","header":"Fruit",` +
			`"options":[{"label":"fig","description":"sweet"},{"label":"lime","description":"sour"}],"multiSelect":true}]}`,
		"truncated question": `{"tool":"AskUserQuestion","truncated":true,"sha256":"deadbeef"}`,
		"permission prompt":  `{"tool":"Write","input":{"file_path":"notes.txt","content":"hi"}}`,
		"does not decode":    `not a question`,
	}
	for name, stored := range cases {
		t.Run(name, func(t *testing.T) {
			transition := paneTransition(tmuxctl.Server{}, "%7", protocol.StateWaiting, "/work/invented", "{}")
			note := paneNotification(transition, paneText{question: stored})
			if note.Options != nil {
				t.Errorf("options = %q, want none", note.Options)
			}
		})
	}
}

// TestPushOptionsDoneCarriesNoOptions checks that a done pane pushes no options, even with an AskUserQuestion stored.
func TestPushOptionsDoneCarriesNoOptions(t *testing.T) {
	transition := paneTransition(tmuxctl.Server{}, "%7", protocol.StateDone, "/work/invented", "{}")
	note := paneNotification(transition, paneText{question: optionsAsk, summary: "invented summary"})
	if note.Options != nil {
		t.Errorf("options = %q, want none for a done pane", note.Options)
	}
}

// TestPushOptionsEpicQuestionHasNoOptions checks that the push of an epic's notice, rebuilt by planner.RestoreNotice
// from its stored JSON, carries no options.
func TestPushOptionsEpicQuestionHasNoOptions(t *testing.T) {
	data := []byte(`{"id":"notice-1","epic":"orrery-invented","title":"Invented epic","body":"Which route?"}`)
	incoming, err := planner.RestoreNotice(t.Context(), data, 0)
	if err != nil {
		t.Fatalf("RestoreNotice: %v", err)
	}
	note, err := incoming.Push(t.Context())
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if note.Options != nil {
		t.Errorf("options = %q, want none for an epic", note.Options)
	}
}
