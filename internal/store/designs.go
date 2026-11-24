package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
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

// Design is one design as its record holds it: its id, its title, its working conversation and when it was made. The
// working conversation is the session of the design's newest design_conversations row, or else the sessions row that
// wrote the design, as DesignConversation names it.
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

// CreateDesign stores the design as a new design. It stores the design's ID, Title, Conversation and Created, and
// returns an error that matches ErrDesignExists, and stores nothing, when a design or a contract already has the
// design's ID.
func (s *Store) CreateDesign(ctx context.Context, design Design) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		held, err := designIDHeld(ctx, queries, design.ID)
		if err != nil {
			return err
		}
		if held {
			return fmt.Errorf("design %s: %w", design.ID, ErrDesignExists)
		}
		if err = queries.InsertDesign(ctx, sqlcgen.InsertDesignParams{
			ID: design.ID, Title: design.Title, Conversation: design.Conversation, Created: design.Created.Unix(),
		}); err != nil {
			return fmt.Errorf("insert design %s: %w", design.ID, err)
		}
		return nil
	})
}

// Design returns the design with the given id, with its contracts in position order and its State as DesignState
// derives it from them. A contract in no design reads as a design of one. It returns an error that matches ErrNoDesign
// when neither a design nor a contract in no design has the id.
func (s *Store) Design(ctx context.Context, id string) (DesignRecord, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return DesignRecord{}, fmt.Errorf("begin design read: %w", err)
	}
	record, err := readDesign(ctx, sqlcgen.New(tx), id)
	if err != nil {
		return DesignRecord{}, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return DesignRecord{}, fmt.Errorf("end design read: %w", err)
	}
	return record, nil
}

// Designs returns every design: the real designs by created and then id, then each contract in no design as a design
// of one, by the contract's created and then id. Each is read as Design returns it.
func (s *Store) Designs(ctx context.Context) ([]DesignRecord, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin designs read: %w", err)
	}
	records, err := readDesigns(ctx, sqlcgen.New(tx))
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end designs read: %w", err)
	}
	return records, nil
}

// Design returns the design with the given id as Store.Design does, read through the Reader.
func (r *Reader) Design(ctx context.Context, id string) (DesignRecord, error) {
	var record DesignRecord
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		record, readErr = readDesign(ctx, queries, id)
		return readErr
	})
	return record, err
}

// Designs returns every design as Store.Designs does, read through the Reader.
func (r *Reader) Designs(ctx context.Context) ([]DesignRecord, error) {
	var records []DesignRecord
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		records, readErr = readDesigns(ctx, queries)
		return readErr
	})
	return records, err
}

// ProposeContractInDesign proposes the contract id as ProposeContract does, and adds it to the design at the end of
// the design's order, at the position after the design's highest. It returns an error that matches ErrNoDesign when no
// design has the design id, and the error ProposeContract returns when it returns one. It stores nothing on an error.
func (s *Store) ProposeContractInDesign(ctx context.Context, designID, id string, proposal ContractVersion) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireDesign(ctx, queries, designID); err != nil {
			return err
		}
		if err := proposeContract(ctx, queries, id, proposal); err != nil {
			return err
		}
		highest, err := queries.MaxDesignPosition(ctx, designID)
		if err != nil {
			return fmt.Errorf("read highest position of design %s: %w", designID, err)
		}
		if err = queries.InsertDesignContract(ctx, sqlcgen.InsertDesignContractParams{
			Contract: id, Design: designID, Position: highest + 1, Added: proposal.At,
		}); err != nil {
			return fmt.Errorf("add contract %s to design %s: %w", id, designID, err)
		}
		return nil
	})
}

// AddDesignEdge records at the given time that a contract starts after another, both contracts of one design. It
// returns an error that matches ErrNoDesign when no design has the design id, ErrDesignMember when the contract is not
// in the design or the after contract is in no design, ErrDesignAcross when the after contract is in another design,
// and ErrDesignCycle when the edge would close a cycle of starts-after edges, a self edge included. An edge already
// stored writes nothing and returns nil. It stores nothing on an error.
func (s *Store) AddDesignEdge(ctx context.Context, designID, contract, after string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := requireDesign(ctx, queries, designID); err != nil {
			return err
		}
		own, err := contractDesignOf(ctx, queries, contract)
		if err != nil {
			return err
		}
		if own != designID {
			return fmt.Errorf("contract %s is not in design %s: %w", contract, designID, ErrDesignMember)
		}
		owner, err := contractDesignOf(ctx, queries, after)
		if err != nil {
			return err
		}
		if owner == "" {
			return fmt.Errorf("contract %s is not in design %s: %w", after, designID, ErrDesignMember)
		}
		if owner != designID {
			return fmt.Errorf(
				"contract %s of design %s starts after contract %s of design %s: %w",
				contract, designID, after, owner, ErrDesignAcross,
			)
		}
		edges, err := queries.ListDesignEdges(ctx, designID)
		if err != nil {
			return fmt.Errorf("list edges of design %s: %w", designID, err)
		}
		if hasDesignEdge(edges, contract, after) {
			return nil
		}
		if path := startsAfterPath(edges, after, contract); path != nil {
			cycle := append([]string{contract}, path...)
			return fmt.Errorf("design %s: %s: %w", designID, strings.Join(cycle, " -> "), ErrDesignCycle)
		}
		if err = queries.InsertDesignEdge(ctx, sqlcgen.InsertDesignEdgeParams{
			Design: designID, Contract: contract, After: after, Created: at.Unix(),
		}); err != nil {
			return fmt.Errorf("insert edge of contract %s in design %s: %w", contract, designID, err)
		}
		return nil
	})
}

// designIDHeld reports whether a design or a contract already holds id, read with queries.
func designIDHeld(ctx context.Context, queries *sqlcgen.Queries, id string) (bool, error) {
	designs, err := queries.CountDesign(ctx, id)
	if err != nil {
		return false, fmt.Errorf("count design %s: %w", id, err)
	}
	contracts, err := queries.CountContract(ctx, id)
	if err != nil {
		return false, fmt.Errorf("count contract %s: %w", id, err)
	}
	return designs > 0 || contracts > 0, nil
}

// requireDesign returns an error that matches ErrNoDesign when no design has id, read with queries.
func requireDesign(ctx context.Context, queries *sqlcgen.Queries, id string) error {
	count, err := queries.CountDesign(ctx, id)
	if err != nil {
		return fmt.Errorf("count design %s: %w", id, err)
	}
	if count == 0 {
		return fmt.Errorf("design %s: %w", id, ErrNoDesign)
	}
	return nil
}

// contractDesignOf returns the id of the design the contract is in, or "" when it is in none, read with queries.
func contractDesignOf(ctx context.Context, queries *sqlcgen.Queries, contract string) (string, error) {
	design, err := queries.GetContractDesign(ctx, contract)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read design of contract %s: %w", contract, err)
	}
	return design, nil
}

// hasDesignEdge reports whether edges holds the edge that contract starts after after.
func hasDesignEdge(edges []sqlcgen.DesignEdge, contract, after string) bool {
	for _, edge := range edges {
		if edge.Contract == contract && edge.After == after {
			return true
		}
	}
	return false
}

// startsAfterShipped reports whether every contract the contract starts after has an epic in a phase that ships. A
// predecessor with no epic reads as phase "", which no shipped phase is, so it does not ship. Read with queries.
func startsAfterShipped(ctx context.Context, queries *sqlcgen.Queries, contract string) (bool, error) {
	phases, err := queries.ListContractPredecessors(ctx, contract)
	if err != nil {
		return false, fmt.Errorf("list predecessors of contract %s: %w", contract, err)
	}
	for _, phase := range phases {
		if !shippedPhase(phase) {
			return false, nil
		}
	}
	return true, nil
}

// shippedPhase reports whether phase is one a predecessor of a held epic ships in: released, done or closed_shipped.
func shippedPhase(phase string) bool {
	switch phase {
	case phaseReleased, phaseDone, PhaseClosedShipped:
		return true
	}
	return false
}

// startsAfterPath returns the contracts of a path of starts-after edges from the contract from to the contract to, both
// ends included, or nil when no path joins them. A contract from reaches to when from is to, so its path is [from].
func startsAfterPath(edges []sqlcgen.DesignEdge, from, to string) []string {
	afters := make(map[string][]string)
	for _, edge := range edges {
		afters[edge.Contract] = append(afters[edge.Contract], edge.After)
	}
	parent := map[string]string{from: from}
	queue := []string{from}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if node == to {
			var path []string
			for at := to; ; at = parent[at] {
				path = append([]string{at}, path...)
				if at == from {
					return path
				}
			}
		}
		for _, after := range afters[node] {
			if _, seen := parent[after]; seen {
				continue
			}
			parent[after] = node
			queue = append(queue, after)
		}
	}
	return nil
}

// readDesign returns the design with id, read with queries. A contract in no design with id reads as a design of one.
// It returns an error that matches ErrNoDesign when neither has id.
func readDesign(ctx context.Context, queries *sqlcgen.Queries, id string) (DesignRecord, error) {
	row, err := queries.GetDesign(ctx, id)
	if err == nil {
		return readDesignRows(ctx, queries, row)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DesignRecord{}, fmt.Errorf("read design %s: %w", id, err)
	}
	lone, err := queries.GetLoneContract(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return DesignRecord{}, fmt.Errorf("design %s: %w", id, ErrNoDesign)
	}
	if err != nil {
		return DesignRecord{}, fmt.Errorf("read contract %s: %w", id, err)
	}
	conversation, err := workingConversation(ctx, queries, lone.ID, lone.Conversation)
	if err != nil {
		return DesignRecord{}, err
	}
	return designOfOne(
		Design{ID: lone.ID, Title: lone.Title, Conversation: conversation, Created: time.Unix(lone.Created, 0)},
		DesignContract{
			ID: lone.ID, Title: lone.Title, Repository: lone.Repository, State: lone.State, Position: 1,
			Epic: lone.Epic, EpicPhase: lone.EpicPhase,
		},
	), nil
}

// workingConversation returns the working conversation of design: the session of its newest design_conversations row,
// or else own, the conversation its record holds. Read with queries.
func workingConversation(ctx context.Context, queries *sqlcgen.Queries, design, own string) (string, error) {
	newest, err := queries.NewestDesignConversation(ctx, design)
	if errors.Is(err, sql.ErrNoRows) {
		return own, nil
	}
	if err != nil {
		return "", fmt.Errorf("read newest conversation of design %s: %w", design, err)
	}
	return newest.Session, nil
}

// readDesigns returns every design as Store.Designs does, read with queries.
func readDesigns(ctx context.Context, queries *sqlcgen.Queries) ([]DesignRecord, error) {
	rows, err := queries.ListDesigns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list designs: %w", err)
	}
	records := make([]DesignRecord, 0, len(rows))
	for _, row := range rows {
		record, readErr := readDesignRows(ctx, queries, row)
		if readErr != nil {
			return nil, readErr
		}
		records = append(records, record)
	}
	lone, err := queries.ListLoneContracts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list contracts in no design: %w", err)
	}
	for _, row := range lone {
		conversation, convErr := workingConversation(ctx, queries, row.ID, row.Conversation)
		if convErr != nil {
			return nil, convErr
		}
		records = append(records, designOfOne(
			Design{ID: row.ID, Title: row.Title, Conversation: conversation, Created: time.Unix(row.Created, 0)},
			DesignContract{
				ID: row.ID, Title: row.Title, Repository: row.Repository, State: row.State, Position: 1,
				Epic: row.Epic, EpicPhase: row.EpicPhase,
			},
		))
	}
	return records, nil
}

// readDesignRows returns the design row with its contracts in position order, each with the contracts it starts after,
// and its State as DesignState derives it, read with queries.
func readDesignRows(ctx context.Context, queries *sqlcgen.Queries, row sqlcgen.Design) (DesignRecord, error) {
	members, err := queries.ListDesignContracts(ctx, row.ID)
	if err != nil {
		return DesignRecord{}, fmt.Errorf("list contracts of design %s: %w", row.ID, err)
	}
	edges, err := queries.ListDesignEdges(ctx, row.ID)
	if err != nil {
		return DesignRecord{}, fmt.Errorf("list edges of design %s: %w", row.ID, err)
	}
	afters := make(map[string][]string)
	for _, edge := range edges {
		afters[edge.Contract] = append(afters[edge.Contract], edge.After)
	}
	conversation, err := workingConversation(ctx, queries, row.ID, row.Conversation)
	if err != nil {
		return DesignRecord{}, err
	}
	record := DesignRecord{
		Design: Design{
			ID: row.ID, Title: row.Title, Conversation: conversation, Created: time.Unix(row.Created, 0),
		},
		OfOne: len(members) == 1,
	}
	for _, member := range members {
		record.Contracts = append(record.Contracts, DesignContract{
			ID: member.Contract, Title: member.Title, Repository: member.Repository, State: member.State,
			Position: member.Position, After: afters[member.Contract], Epic: member.Epic, EpicPhase: member.EpicPhase,
		})
	}
	record.State = DesignState(record)
	return record, nil
}

// designOfOne returns the design of one of the design and its only contract, at position 1, with its State as
// DesignState derives it.
func designOfOne(design Design, contract DesignContract) DesignRecord {
	record := DesignRecord{Design: design, OfOne: true, Contracts: []DesignContract{contract}}
	record.State = DesignState(record)
	return record
}

// HeldEpics returns every held epic, oldest first and ties by id, each with its repository, its host and each
// predecessor's contract state and epic phase. Question is 0 and Answer "" until a held question is asked.
func (s *Store) HeldEpics(ctx context.Context) ([]HeldEpic, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin held epics read: %w", err)
	}
	held, err := readHeldEpics(ctx, sqlcgen.New(tx))
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end held epics read: %w", err)
	}
	return held, nil
}

// readHeldEpics returns every held epic with its predecessors, read with queries.
func readHeldEpics(ctx context.Context, queries *sqlcgen.Queries) ([]HeldEpic, error) {
	rows, err := queries.ListHeldEpics(ctx)
	if err != nil {
		return nil, fmt.Errorf("list held epics: %w", err)
	}
	held := make([]HeldEpic, 0, len(rows))
	for _, row := range rows {
		predecessors, listErr := queries.ListPredecessorStates(ctx, row.ID)
		if listErr != nil {
			return nil, fmt.Errorf("list predecessors of held epic %s: %w", row.ID, listErr)
		}
		epic := HeldEpic{
			ID: row.ID, Repository: row.Repository, Host: row.Host, Question: row.Question, Answer: row.Answer,
		}
		for _, predecessor := range predecessors {
			epic.Predecessors = append(epic.Predecessors, HeldPredecessor{
				Contract: predecessor.Contract, ContractState: predecessor.State, EpicPhase: predecessor.EpicPhase,
			})
		}
		held = append(held, epic)
	}
	return held, nil
}

// heldStartDecision is the id of the decision StartHeldEpic appends to a held epic it starts.
const heldStartDecision = "held-start"

// StartHeldEpic starts the held epic id at base, at at, in one write: its base and stored base become base, its phase
// becomes accepted at at, and orreryd appends a decision, held-start, whose text names base as origin main, the commit
// the epic starts from once its last predecessor shipped. It returns an error that matches ErrNoEpic when no epic has
// the id, and ErrNotHeld when the epic is not held. It stores nothing on an error.
func (s *Store) StartHeldEpic(ctx context.Context, id, base string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		started, err := queries.StartHeldEpic(ctx, sqlcgen.StartHeldEpicParams{Base: base, At: at.Unix(), ID: id})
		if err != nil {
			return fmt.Errorf("start held epic %s: %w", id, err)
		}
		if started == 0 {
			return heldEpicRefusal(ctx, queries, id)
		}
		text := fmt.Sprintf("Started at base %s, origin main, once its last predecessor shipped.", base)
		if _, err = appendDecision(
			ctx, queries, id, heldStartDecision, text, 0, Maker{By: MakerOrreryd}, "", at,
		); err != nil {
			return fmt.Errorf("record start of held epic %s: %w", id, err)
		}
		return nil
	})
}

// heldEpicRefusal returns the error StartHeldEpic returns for id when it started nothing: one that matches ErrNoEpic
// when no epic has id, and ErrNotHeld otherwise. Read with queries.
func heldEpicRefusal(ctx context.Context, queries *sqlcgen.Queries, id string) error {
	count, err := queries.CountEpic(ctx, id)
	if err != nil {
		return fmt.Errorf("count epic %s: %w", id, err)
	}
	if count == 0 {
		return fmt.Errorf("epic %s: %w", id, ErrNoEpic)
	}
	return fmt.Errorf("epic %s: %w", id, ErrNotHeld)
}
