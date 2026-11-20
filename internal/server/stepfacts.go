package server

import (
	"context"
	"fmt"
	"math"
	"strings"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepPromptPrefix starts the prompt id of a step's end: the id is this prefix followed by the step's id. promptKind is
// the kind of a prompt item.
const (
	stepPromptPrefix = "step:"
	promptKind       = "prompt"
)

// stepFactsReader is the store read withStepFacts asks for the facts of steps. *store.Reader satisfies it.
type stepFactsReader interface {
	StepFacts(ctx context.Context, ids []string) (map[string]store.StepFacts, error)
}

// stepFactsProto returns the StepFacts message of facts. The files, added and removed counts are set only when the
// step's lines were counted.
func stepFactsProto(facts store.StepFacts) *orreryv1.StepFacts {
	msg := &orreryv1.StepFacts{
		Rounds:  roundCount(facts.Rounds),
		Seconds: facts.Seconds,
		Counted: facts.Lines.Counted,
	}
	if facts.Lines.Counted {
		msg.Files = facts.Lines.Files
		msg.Added = facts.Lines.Added
		msg.Removed = facts.Lines.Removed
	}
	return msg
}

// roundCount returns the loop round n as the uint32 a StepFacts message carries. A round below zero is 0, and a round
// above the uint32 range is the largest uint32.
func roundCount(n int) uint32 {
	if n < 0 {
		return 0
	}
	if int64(n) > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(n)
}

// withStepFacts sets the StepFacts of each step-end item of items whose step the store has a row for. A step-end item
// is a prompt item whose prompt id starts with stepPromptPrefix, and its step is the rest of that id. The facts of
// every such step are read in one StepFacts call, and no call is made when there is no step-end item. An item whose
// step has no row is left without StepFacts, and no other field of any item is changed.
func withStepFacts(ctx context.Context, reader stepFactsReader, items []*orreryv1.ConversationItem) error {
	var ids []string
	asked := make(map[string]bool)
	for _, item := range items {
		id, ok := stepEndID(item)
		if !ok || asked[id] {
			continue
		}
		asked[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	facts, err := reader.StepFacts(ctx, ids)
	if err != nil {
		return fmt.Errorf("read step facts: %w", err)
	}
	for _, item := range items {
		id, ok := stepEndID(item)
		if !ok {
			continue
		}
		if step, has := facts[id]; has {
			item.StepFacts = stepFactsProto(step)
		}
	}
	return nil
}

// stepEndID returns the step id that a step-end item names, and false for any other item.
func stepEndID(item *orreryv1.ConversationItem) (string, bool) {
	if item.GetKind() != promptKind {
		return "", false
	}
	return strings.CutPrefix(item.GetPromptId(), stepPromptPrefix)
}
