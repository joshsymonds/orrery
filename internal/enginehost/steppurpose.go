package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// stepPurposeSchema is the structured output a purpose call must return.
const stepPurposeSchema = `{"type":"object","additionalProperties":false,"required":["purpose"],"properties":{"purpose":{"type":"string"}}}`

// stepPurposeInstruction asks for what the step in the call's input is for, in at most ten words. The step's prompt
// follows it there.
const stepPurposeInstruction = "Say what the step below is for, in at most ten words. Answer with the purpose field " +
	"only."

// stepPurposeReply is the structured output a purpose call decodes.
type stepPurposeReply struct {
	Purpose string `json:"purpose"`
}

// askStepPurpose asks haiku for the purpose of the step id that start started, from its prompt, and stores the answer,
// in a goroutine the caller does not wait on. A start that names a purpose, or a step with no haiku caller, asks
// nothing. The goroutine is counted in s.calls from here until it returns, so the work is seen as under way until then.
// Its context is detached from cancellation, since the call and its write outlive the start that made them.
func (s *Steps) askStepPurpose(ctx context.Context, id string, start StepStart, haiku haikuCaller) {
	if start.Purpose != "" || haiku == nil {
		return
	}
	done := s.calls.enter()
	go func() {
		defer done()
		s.storeStepPurpose(context.WithoutCancel(ctx), id, start.Prompt, haiku)
	}()
}

// storeStepPurpose calls haiku for the purpose of step id from prompt and stores the purpose the reply holds. A failed
// call, reply or write is logged and leaves the purpose empty.
func (s *Steps) storeStepPurpose(ctx context.Context, id, prompt string, haiku haikuCaller) {
	raw, err := haiku.Call(ctx, stepPurposeSchema, stepPurposeInstruction+"\n\n"+prompt)
	if err != nil {
		s.logger.ErrorContext(ctx, "ask for step purpose", "step", id, "error", err)
		return
	}
	purpose, err := stepPurposeOf(raw)
	if err != nil {
		s.logger.ErrorContext(ctx, "invalid step purpose", "step", id, "error", err)
		return
	}
	if err = s.store.SetStepPurpose(ctx, id, purpose); err != nil {
		s.logger.ErrorContext(ctx, "store step purpose", "step", id, "error", err)
	}
}

// stepPurposeOf returns the trimmed purpose raw holds. It is an error when raw is not an object with a purpose, or the
// purpose is blank.
func stepPurposeOf(raw json.RawMessage) (string, error) {
	var reply stepPurposeReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "", fmt.Errorf("decode purpose: %w", err)
	}
	purpose := strings.TrimSpace(reply.Purpose)
	if purpose == "" {
		return "", errors.New("purpose is blank")
	}
	return purpose, nil
}
