// Package donejudge asks Haiku whether a plain done needs the user.
package donejudge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/route"
)

const instruction = "Judge whether a coding agent's final message at the end of its turn needs its user. Answer needed true when it asks anything, offers choices, reports a failure, error, blocker or unfinished work, or asks for a review, decision or action, and whenever unsure. Answer needed false only when it reports finished work that needs nothing. Answer why in one short sentence."

// schema is the structured output the judge must return.
const schema = `{"type":"object","additionalProperties":false,"required":["needed","why"],"properties":{"needed":{"type":"boolean"},"why":{"type":"string"}}}`

// Caller runs one structured Haiku call against baseURL with model. *enginehost.Haiku satisfies it.
type Caller interface {
	CallVia(ctx context.Context, baseURL, model, schema, input string) (json.RawMessage, error)
}

// Judge asks a Caller whether a plain done needs the user.
type Judge struct {
	caller Caller
	models map[string]string
}

// verdictReply is the structured output the judge decodes. Needed is a pointer so a missing needed is told apart
// from false.
type verdictReply struct {
	Needed *bool  `json:"needed"`
	Why    string `json:"why"`
}

// New returns a Judge that calls caller and picks each pane's model from models.
func New(caller Caller, models map[string]string) *Judge {
	return &Judge{caller: caller, models: models}
}

// Done returns the verdict on message, the final message of a pane whose base URL is baseURL.
func (j *Judge) Done(ctx context.Context, baseURL, message string) (route.Verdict, error) {
	if strings.TrimSpace(message) == "" {
		return route.Verdict{}, errors.New("done judge: empty message")
	}
	model := protocol.SummaryModel(baseURL, j.models)
	raw, err := j.caller.CallVia(ctx, baseURL, model, schema, instruction+"\n\n"+message)
	if err != nil {
		return route.Verdict{}, fmt.Errorf("done judge: %w", err)
	}
	var reply verdictReply
	if err = json.Unmarshal(raw, &reply); err != nil {
		return route.Verdict{}, fmt.Errorf("done judge: decode verdict: %w", err)
	}
	if reply.Needed == nil {
		return route.Verdict{}, errors.New("done judge: verdict has no boolean needed")
	}
	why := strings.TrimSpace(strings.ReplaceAll(reply.Why, "\n", " "))
	return route.Verdict{Needed: *reply.Needed, Why: why}, nil
}
