package store

import (
	"context"
	"errors"
	"time"
)

// PhaseHeld is the phase of an epic that holds. A held epic waits for the contracts it starts after to end, and it
// starts only when StartHeldEpic starts it. An epic with no predecessor never holds.
const PhaseHeld = "held"

// HeldLabel is the label of the question AskHeldEpic opens on a held epic, as GapsLabel labels the gaps question.
const HeldLabel = "held"

// ChoiceStart is the choice a held epic's question offers to start the epic, beside ChoiceClose to close it.
const ChoiceStart = "start"

// ErrNoDesign reports that no design has the id asked for.
var ErrNoDesign = errors.New("no design")

// ErrDesignExists reports that a design already has the id CreateDesign was given.
var ErrDesignExists = errors.New("design id already held")

// ErrDesignMember reports that a contract a design call names is not a contract of that design.
var ErrDesignMember = errors.New("contract not in design")

// ErrDesignAcross reports that a starts-after edge names a contract of another design. No edge crosses designs.
var ErrDesignAcross = errors.New("starts-after edge across designs")

// ErrDesignCycle reports that a starts-after edge would close a cycle of starts-after edges.
var ErrDesignCycle = errors.New("starts-after edges form a cycle")

// ErrNotHeld reports that an epic a held-epic call names is not held.
var ErrNotHeld = errors.New("epic not held")

// errNotImplemented is what each design stub returns until its implementing task fills it in.
var errNotImplemented = errors.New("not implemented")

// Design is one design as its record holds it: its id, its title, the sessions row that wrote it and when it was made.
type Design struct {
	ID, Title, Conversation string
	Created                 time.Time
}

// DesignContract is one contract of a design as the design's record shows it. Position is its place in the design's
// order, and After names the contracts it starts after. Epic and EpicPhase name the epic the contract has, and are ""
// when it has none.
type DesignContract struct {
	ID, Title, Repository, State string
	Position                     int64
	After                        []string
	Epic, EpicPhase              string
}

// DesignRecord is a design with its contracts in position order. OfOne reports whether the design has exactly one
// contract, and State is the design's state as DesignState derives it from the record.
type DesignRecord struct {
	Design
	OfOne     bool
	State     string
	Contracts []DesignContract
}

// HeldPredecessor is a contract that a held epic starts after: its id, its state, and the phase of the epic it has,
// "" when it has none.
type HeldPredecessor struct {
	Contract, ContractState, EpicPhase string
}

// HeldEpic is a held epic as the held list shows it: its id, its repository and host, the contracts it starts after,
// and its held question. Question is the seq of that question, 0 when there is none, and Answer is the choice it was
// answered with, "" while it is open.
type HeldEpic struct {
	ID, Repository, Host string
	Predecessors         []HeldPredecessor
	Question             int64
	Answer               string
}

// CreateDesign stores the design as a new design. This is a stub: the task that implements designs fills it in to this
// contract. It stores the design's ID, Title, Conversation and Created, and returns an error that matches
// ErrDesignExists, and stores nothing, when a design already has the design's ID.
func (s *Store) CreateDesign(_ context.Context, _ Design) error {
	return errNotImplemented
}

// Design returns the design with the given id, with its contracts in position order and its state. This is a stub: the
// task that implements designs fills it in to this contract. It returns an error that matches ErrNoDesign when no
// design has the id.
func (s *Store) Design(_ context.Context, _ string) (DesignRecord, error) {
	return DesignRecord{}, errNotImplemented
}

// Designs returns every design, oldest first and ties by id, each as Design returns it. This is a stub: the task that
// implements designs fills it in to this contract.
func (s *Store) Designs(_ context.Context) ([]DesignRecord, error) {
	return nil, errNotImplemented
}

// Design returns the design with the given id as Store.Design does, read through the Reader. This is a stub: the task
// that implements designs fills it in to this contract.
func (r *Reader) Design(_ context.Context, _ string) (DesignRecord, error) {
	return DesignRecord{}, errNotImplemented
}

// Designs returns every design as Store.Designs does, read through the Reader. This is a stub: the task that
// implements designs fills it in to this contract.
func (r *Reader) Designs(_ context.Context) ([]DesignRecord, error) {
	return nil, errNotImplemented
}

// ProposeContractInDesign proposes the contract id as ProposeContract does, and adds it to the design at the end of
// the design's order. This is a stub: the task that implements designs fills it in to this contract. It returns an
// error that matches ErrNoDesign when no design has the design id, and the error ProposeContract returns when it
// returns one. It stores nothing on an error.
func (s *Store) ProposeContractInDesign(_ context.Context, _, _ string, _ ContractVersion) error {
	return errNotImplemented
}

// AddDesignEdge records at the given time that a contract starts after another, both contracts of one design. This is
// a stub: the task that implements designs fills it in to this contract. It returns an error that matches ErrNoDesign
// when no design has the design id, ErrDesignAcross when the after contract belongs to another design,
// ErrDesignMember when the contract or the after contract is not a contract of the design, and ErrDesignCycle when the
// edge would close a cycle of starts-after edges. It stores nothing on an error.
func (s *Store) AddDesignEdge(_ context.Context, _, _, _ string, _ time.Time) error {
	return errNotImplemented
}

// HeldEpics returns every held epic, oldest first and ties by id, each with its predecessors and its held question.
// This is a stub: the task that implements held epics fills it in to this contract.
func (s *Store) HeldEpics(_ context.Context) ([]HeldEpic, error) {
	return nil, errNotImplemented
}

// StartHeldEpic starts the held epic at the given base, at the given time, so that it no longer holds. This is a stub:
// the task that implements held epics fills it in to this contract. It starts the epic by the path an epic with no
// predecessor starts by, and changes nothing in that path. It returns an error that matches ErrNoEpic when no epic has
// the id, and ErrNotHeld when the epic is not held.
func (s *Store) StartHeldEpic(_ context.Context, _, _ string, _ time.Time) error {
	return errNotImplemented
}

// AskHeldEpic opens, at the given time, a question of the held epic with the given text as its text, labeled HeldLabel
// and offering ChoiceStart and ChoiceClose. This is a stub: the task that implements held epics fills it in to this
// contract. It returns an error that matches ErrNoEpic when no epic has the id, and ErrNotHeld when the epic is not
// held.
func (s *Store) AskHeldEpic(_ context.Context, _, _ string, _ time.Time) error {
	return errNotImplemented
}
