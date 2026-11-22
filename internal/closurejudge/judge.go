// Package closurejudge asks Haiku whether a conversation is closed, so its inbox entry can be archived.
package closurejudge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Instruction asks whether a conversation is closed. It answers closed true only when the user decisively ended the
// conversation or its work is permanently finished and the final reply needs nothing from the user.
const Instruction = "Judge whether a conversation is closed. Answer closed true only when the user decisively ended " +
	"the conversation, closing the book on it, or its work is permanently finished and the final reply needs nothing " +
	"from the user. Answer closed false whenever unsure, and whenever the final reply asks anything, offers choices, " +
	"reports a failure, error, blocker or unfinished work, or awaits a decision. Answer why in one short sentence."

// schema is the structured output the judge must return.
const schema = `{"type":"object","additionalProperties":false,"required":["closed","why"],"properties":{"closed":{"type":"boolean"},"why":{"type":"string"}}}`

// Caller runs one structured Haiku call with model, its process env's base URL set to baseURL and its --settings env
// set to env. *enginehost.Haiku satisfies it.
type Caller interface {
	CallVia(
		ctx context.Context, baseURL, model string, env map[string]string, schema, input string,
	) (json.RawMessage, error)
}

// Verdict is the closure judge's answer: whether the conversation is closed, and why.
type Verdict struct {
	Closed bool
	Why    string
}

// Judge asks a Caller whether a conversation is closed.
type Judge struct {
	caller Caller
	models map[string]string
}

// verdictReply is the structured output the judge decodes. Closed is a pointer so a missing closed is told apart
// from false.
type verdictReply struct {
	Closed *bool  `json:"closed"`
	Why    string `json:"why"`
}

// New returns a Judge that calls caller and picks each pane's model from models.
func New(caller Caller, models map[string]string) *Judge {
	return &Judge{caller: caller, models: models}
}

// Closed returns the verdict on recent, the recent text of a conversation. The call runs against baseURL, with env in
// its --settings value, and its model is the one baseURL maps to.
func (j *Judge) Closed(
	ctx context.Context, baseURL string, env map[string]string, recent string,
) (Verdict, error) {
	if strings.TrimSpace(recent) == "" {
		return Verdict{}, errors.New("closure judge: empty recent text")
	}
	model := protocol.SummaryModel(baseURL, j.models)
	raw, err := j.caller.CallVia(ctx, baseURL, model, env, schema, Instruction+"\n\n"+recent)
	if err != nil {
		return Verdict{}, fmt.Errorf("closure judge: %w", err)
	}
	var reply verdictReply
	if err = json.Unmarshal(raw, &reply); err != nil {
		return Verdict{}, fmt.Errorf("closure judge: decode verdict: %w", err)
	}
	if reply.Closed == nil {
		return Verdict{}, errors.New("closure judge: verdict has no boolean closed")
	}
	why := strings.TrimSpace(strings.ReplaceAll(reply.Why, "\n", " "))
	return Verdict{Closed: *reply.Closed, Why: why}, nil
}
