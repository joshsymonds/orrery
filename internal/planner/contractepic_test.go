package planner

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The contract, its conversation and the session that changes it are invented.
const (
	contractEventID           = "orrery-1a2b3c4/contract-events"
	contractEventConversation = "creator"
	contractEventReader       = "reader"
	contractEventAt           = 1700000600
)

// errContractEventRead is the read error the failing store of the read-error test returns for the contract events.
var errContractEventRead = errors.New("synthetic contract event read")

// errDesignEventRead is the read error the failing store of the read-error test returns for the design events.
var errDesignEventRead = errors.New("synthetic design event read")

// failingContractEventsStore is the store of the read-error test: the real store, except that its ContractEvents and
// its DesignEvents fail.
type failingContractEventsStore struct {
	*store.Store
}

func (failingContractEventsStore) ContractEvents(context.Context) ([]store.ContractEvent, error) {
	return nil, errContractEventRead
}

func (failingContractEventsStore) DesignEvents(context.Context) ([]store.DesignEvent, error) {
	return nil, errDesignEventRead
}

// contractEventVersion returns a version of contractEventID, written by writer at at, with its one decision answered so
// the contract can be accepted.
func contractEventVersion(version int64, writer, words string, at int64) store.ContractVersion {
	return store.ContractVersion{
		Version: version, Repository: "/work/synthetic-repo", Title: "Synthetic contract", Care: "serious",
		Body: `{"id":"synthetic-contract"}`,
		Decisions: []store.ContractDecision{
			{
				ID: "D1", Question: "Which store?", Options: []string{"sqlite", "postgres"},
				Recommendation: "sqlite", Reason: "Already here", Row: "R1", Answer: "sqlite",
			},
		},
		Words: words, Writer: writer, At: at,
	}
}

// proposeContractEvents proposes contractEventID by the conversation and revises it to version 2 with its decision
// answered, so the owner can accept it.
func proposeContractEvents(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := t.Context()
	proposal := contractEventVersion(1, contractEventConversation, "Propose the synthetic contract", contractEventAt)
	if err := s.ProposeContract(ctx, contractEventID, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	revision := contractEventVersion(2, contractEventConversation, "Answer the synthetic contract", contractEventAt+30)
	if err := s.ReviseContract(ctx, contractEventID, revision); err != nil {
		t.Fatalf("ReviseContract: %v", err)
	}
}

func TestContractEpicOwnerAndOtherChangesReachConversationOnce(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	addCallerEngine(t, s, contractEventConversation, store.KindEngine)
	addCallerEngine(t, s, contractEventReader, store.KindEngine)
	proposeContractEvents(t, s)
	deliver := &fakeCallerDeliver{}
	pass := callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t)))

	owner := store.ContractChange{Session: "", Words: "Accept the synthetic contract", At: contractEventAt + 100}
	if err := s.AcceptContract(ctx, contractEventID, owner); err != nil {
		t.Fatalf("AcceptContract by the owner: %v", err)
	}
	passEpics(ctx, t, pass)
	accept := enginehost.DeliverRequest{
		ID: contractEventConversation, PromptID: store.ContractStateEvent(contractEventID, 2),
		Text: `<orrery-event kind="action" id="contract:orrery-1a2b3c4/contract-events:2">
Contract orrery-1a2b3c4/contract-events (Synthetic contract): the owner accepted it at version 2.
words: Accept the synthetic contract
epic_create with its id and a base makes its epic.
</orrery-event>`,
	}
	if want := []enginehost.DeliverRequest{accept}; !reflect.DeepEqual(deliver.delivered, want) {
		t.Fatalf("delivered after the accept = %+v, want %+v", deliver.delivered, want)
	}

	if err := s.SetAsideContract(
		ctx,
		contractEventID,
		store.ContractChange{
			Session: contractEventReader,
			Words:   "Set it aside",
			At:      contractEventAt + 110,
		},
	); err != nil {
		t.Fatalf("SetAsideContract by another session: %v", err)
	}
	passEpics(ctx, t, pass)
	setAside := enginehost.DeliverRequest{
		ID: contractEventConversation, PromptID: store.ContractStateEvent(contractEventID, 3),
		Text: `<orrery-event kind="action" id="contract:orrery-1a2b3c4/contract-events:3">
Contract orrery-1a2b3c4/contract-events (Synthetic contract): session reader set it aside at version 2.
words: Set it aside
</orrery-event>`,
	}
	if want := []enginehost.DeliverRequest{accept, setAside}; !reflect.DeepEqual(deliver.delivered, want) {
		t.Fatalf("delivered after the set aside = %+v, want %+v", deliver.delivered, want)
	}

	if err := s.ReopenContract(
		ctx,
		contractEventID,
		store.ContractChange{
			Session: "",
			Words:   "Reopen the synthetic contract",
			At:      contractEventAt + 120,
		},
	); err != nil {
		t.Fatalf("ReopenContract by the owner: %v", err)
	}
	passEpics(ctx, t, pass)
	reopen := enginehost.DeliverRequest{
		ID: contractEventConversation, PromptID: store.ContractStateEvent(contractEventID, 4),
		Text: `<orrery-event kind="action" id="contract:orrery-1a2b3c4/contract-events:4">
Contract orrery-1a2b3c4/contract-events (Synthetic contract): the owner reopened it at version 2.
words: Reopen the synthetic contract
</orrery-event>`,
	}
	want := []enginehost.DeliverRequest{accept, setAside, reopen}
	if !reflect.DeepEqual(deliver.delivered, want) {
		t.Fatalf("delivered after the reopen = %+v, want %+v", deliver.delivered, want)
	}

	passEpics(ctx, t, pass)
	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t))))
	if !reflect.DeepEqual(deliver.delivered, want) {
		t.Fatalf("delivered after later passes = %+v, want %+v", deliver.delivered, want)
	}
}

func TestContractEpicConversationsOwnChangeSendsNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	addCallerEngine(t, s, contractEventConversation, store.KindEngine)
	proposeContractEvents(t, s)
	if err := s.SetAsideContract(ctx, contractEventID, store.ContractChange{
		Session: contractEventConversation, Words: "Set it aside", At: contractEventAt + 100,
	}); err != nil {
		t.Fatalf("SetAsideContract by the conversation: %v", err)
	}
	if err := s.ReopenContract(ctx, contractEventID, store.ContractChange{
		Session: contractEventConversation, Words: "Reopen it", At: contractEventAt + 110,
	}); err != nil {
		t.Fatalf("ReopenContract by the conversation: %v", err)
	}

	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, noDeliver(t), noWake(t))))
}

func TestContractEpicContractReadErrorKeepsPassGoing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	plannedCallerEpic(t, s, contractEventConversation, store.EpicPlan{Outcome: "wait"})
	addCallerEngine(t, s, contractEventConversation, store.KindEngine)
	if err := s.PauseEpic(ctx, runnerEpicID, store.Maker{By: store.MakerOwner}, claimedAt()); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	deliver := &fakeCallerDeliver{}
	pass := NewEpicCallerEvents(EpicCallerEventsConfig{
		Store:  failingContractEventsStore{Store: s},
		Sender: callerSenderOver(s, deliver.deliver, noWake(t)),
	})

	err := pass.Pass(ctx)
	if !errors.Is(err, errContractEventRead) || !errors.Is(err, errDesignEventRead) {
		t.Fatalf("Pass = %v, want the contract and design event read errors", err)
	}
	want := []enginehost.DeliverRequest{{
		ID: contractEventConversation, PromptID: "epic:orrery-1a2b3c4/planner-runner:A1",
		Text: `<orrery-event kind="action" id="epic:orrery-1a2b3c4/planner-runner:A1">
Epic orrery-1a2b3c4/planner-runner (Plan the runner): the owner paused the epic.
</orrery-event>`,
	}}
	if !reflect.DeepEqual(deliver.delivered, want) {
		t.Fatalf("delivered past the failed contract read = %+v, want %+v", deliver.delivered, want)
	}
}
