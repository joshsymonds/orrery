package main

import (
	"encoding/json"
	"path/filepath"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
)

// askUserQuestionTool is the tool name the state package stores for an AskUserQuestion question.
const askUserQuestionTool = "AskUserQuestion"

// storedAsk is the stored @orrery_question value of an AskUserQuestion. Only the fields the option labels need are
// decoded: the tool, and each question's multiSelect and option labels.
type storedAsk struct {
	Tool      string `json:"tool"`
	Questions []struct {
		MultiSelect bool `json:"multiSelect"`
		Options     []struct {
			Label string `json:"label"`
		} `json:"options"`
	} `json:"questions"`
}

// questionOptions returns the option labels, in order, of the stored @orrery_question value when it is an
// AskUserQuestion with exactly one question that is not multiSelect. It returns nil for any other value: a permission
// prompt, a truncated value, more than one question, a multiSelect question, or a value that does not decode.
func questionOptions(stored string) []string {
	var ask storedAsk
	if err := json.Unmarshal([]byte(stored), &ask); err != nil {
		return nil
	}
	if ask.Tool != askUserQuestionTool || len(ask.Questions) != 1 || ask.Questions[0].MultiSelect {
		return nil
	}
	var labels []string
	for _, option := range ask.Questions[0].Options {
		labels = append(labels, option.Label)
	}
	return labels
}

// paneNotification returns the Web Push notification for transition, given the text its pane stores. It carries the
// option labels of the pane's question only when the pane waits. Notices sets the host, pane, session and time.
func paneNotification(transition daemon.Transition, text paneText) push.Notification {
	note := push.Notification{
		Project:  filepath.Base(transition.Cwd),
		State:    transition.To,
		Summary:  text.summary,
		Label:    text.label,
		Question: text.question,
	}
	if transition.To == protocol.StateWaiting {
		note.Options = questionOptions(text.question)
	}
	return note
}
