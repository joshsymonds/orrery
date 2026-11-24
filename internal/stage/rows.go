package stage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// rowKinds are the letters a numbered row id begins with. F, R and P rows and D decisions match by their own id, and the
// others are positions in their list.
const rowKinds = "FRPDANLS"

// rowError refuses a row write: row names the row, and reason says why the write is refused.
type rowError struct {
	row, reason string
}

// Error returns the refusal, which names the row.
func (e *rowError) Error() string {
	return "row " + e.row + ": " + e.reason
}

// WriteRow stores a write to a row of a contract's body, with the maker the request names, and returns the write as
// stored. The row is written into the contract's draft, or into its working copy when the contract is proposed, as
// Store.WriteContractRow stores it. The row is set to value, or removed when remove is set. The write is logged in the
// design's log as row written or row removed, with the row as its id. A contract in any state but proposed is refused.
func (s *Service) WriteRow(
	ctx context.Context,
	req *connect.Request[v1.WriteRowRequest],
) (*connect.Response[v1.WriteRowResponse], error) {
	contract, design, row := req.Msg.GetContract(), req.Msg.GetDesign(), req.Msg.GetRow()
	if err := requireField("contract", contract); err != nil {
		return nil, err
	}
	if err := requireField("row", row); err != nil {
		return nil, err
	}
	maker, session, err := s.rowMaker(ctx, req)
	if err != nil {
		return nil, err
	}
	if err = s.requireContractConversation(ctx, session, contract, design); err != nil {
		return nil, err
	}
	value, remove, words := req.Msg.GetValue(), req.Msg.GetRemove(), req.Msg.GetWords()
	change := store.ContractRowChange{
		Contract: contract, Design: design, Row: row, Action: store.RowActionWrite, Depends: req.Msg.GetDepends(),
		Words: words, Maker: maker, Session: session, At: s.cfg.Now().Unix(),
	}
	what := "row written"
	if remove {
		change.Action, what = store.RowActionRemove, "row removed"
	}
	stored, logged, err := s.cfg.Store.WriteContractRow(
		ctx,
		change,
		func(current store.ContractBody) (store.ContractBody, error) {
			return applyRow(current, row, value, remove)
		},
	)
	if err != nil {
		return nil, rowWriteError(err, contract, design)
	}
	err = s.logCall(ctx, logged, loggedCall{
		Maker: maker, Session: session, What: what, IDs: []string{row}, Words: words,
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.WriteRowResponse{Write: stageRowWriteOf(stored)}), nil
}

// rowMaker returns the maker a row write names, as checkMaker checks it, and the sessions row id the write is stored with.
// A request that names no maker writes on the caller's own account: the zero maker, with the caller's session.
func (s *Service) rowMaker(
	ctx context.Context,
	req *connect.Request[v1.WriteRowRequest],
) (store.StageMaker, string, error) {
	if req.Msg.GetMaker() != nil {
		return s.checkMaker(ctx, req.Header(), req.Msg.GetMaker())
	}
	session, err := s.callerSession(ctx, callerOf(req.Header()))
	return store.StageMaker{}, session, err
}

// rowWriteError returns the error a row write answers with for err, which the store returned for a write to contract in
// design. A refused row is an invalid argument naming the row, a contract that is not proposed is a failed precondition
// named state: not proposed, a design no record holds is not found, and an id an epic or a design holds is already
// taken. Any other error is internal.
func rowWriteError(err error, contract, design string) error {
	var refusal *rowError
	switch {
	case errors.As(err, &refusal):
		return refuse(connect.CodeInvalidArgument, refusal.Error())
	case errors.Is(err, store.ErrContractState):
		return refuse(connect.CodeFailedPrecondition, "state: not proposed")
	case errors.Is(err, store.ErrNoDesign):
		return refuse(connect.CodeNotFound, fmt.Sprintf("design: %s not found", design))
	case errors.Is(err, store.ErrContractExists):
		return refuse(connect.CodeAlreadyExists,
			fmt.Sprintf("contract: %s is already held by a contract, an epic or a design", contract))
	default:
		return internalError(fmt.Errorf("write row of contract %s: %w", contract, err))
	}
}

// applyRow applies the write of row to the body current holds, and returns the body the write leaves. A write sets the row
// to value: the row's own message in protobuf JSON for a numbered row, and the section's value in JSON for a section. A
// remove takes the row out, and value is unused. A row no section or numbered row names is refused, as is a value the row
// cannot hold, and a removal of a row the body does not hold.
func applyRow(current store.ContractBody, row, value string, remove bool) (store.ContractBody, error) {
	contract, err := decodeBody(current.Body)
	if err != nil {
		return store.ContractBody{}, err
	}
	decisions := current.Decisions
	kind, number, numbered := parseRow(row)
	switch {
	case numbered && kind == 'D':
		decisions, err = editDecision(decisions, row, value, remove)
	case numbered:
		err = editListRow(contract, kind, number, row, value, remove)
	default:
		err = editSection(contract, row, value, remove)
	}
	if err != nil {
		return store.ContractBody{}, err
	}
	body, err := protojson.Marshal(contract)
	if err != nil {
		return store.ContractBody{}, fmt.Errorf("encode body: %w", err)
	}
	return store.ContractBody{
		Body: string(body), Repository: contract.GetRepository(), Title: contract.GetTitle(), Decisions: decisions,
	}, nil
}

// decodeBody returns the EpicContract body holds, which is an empty contract when the body is "".
func decodeBody(body string) (*v1.EpicContract, error) {
	var contract v1.EpicContract
	if body == "" {
		return &contract, nil
	}
	if err := protojson.Unmarshal([]byte(body), &contract); err != nil {
		return nil, fmt.Errorf("decode body: %w", err)
	}
	return &contract, nil
}

// parseRow splits a numbered row id into its letter and its number: a letter of rowKinds, then a positive number with no
// leading zero. It reports false for any other name.
func parseRow(row string) (byte, int, bool) {
	if len(row) < 2 || strings.IndexByte(rowKinds, row[0]) < 0 {
		return 0, 0, false
	}
	number, err := strconv.Atoi(row[1:])
	if err != nil || number < 1 || strconv.Itoa(number) != row[1:] {
		return 0, 0, false
	}
	return row[0], number, true
}

// rowListName returns the protobuf JSON name of the EpicContract list the rows of kind are in.
func rowListName(kind byte) string {
	switch kind {
	case 'F':
		return "failures"
	case 'R':
		return "requirements"
	case 'P':
		return "premises"
	case 'A':
		return "additions"
	case 'N':
		return "exclusions"
	case 'L':
		return "alternatives"
	default:
		return "releaseSteps"
	}
}

// editSection sets the section named row, by its protobuf JSON name, to value, or clears it on a remove. A row that names
// no section of the contract is refused.
func editSection(contract *v1.EpicContract, row, value string, remove bool) error {
	message := contract.ProtoReflect()
	field := message.Descriptor().Fields().ByJSONName(row)
	if field == nil {
		return &rowError{row: row, reason: "is neither a row nor a section of the contract"}
	}
	if remove {
		message.Clear(field)
		return nil
	}
	decoded, err := decodeField(row, value, false)
	if err != nil {
		return malformed(row, err)
	}
	message.Set(field, decoded.ProtoReflect().Get(field))
	return nil
}

// editListRow sets or removes the row of kind and number in the list of contract that kind names. An F, R or P row is the
// element whose id is the row, and a write of one that is absent appends it. An A, N, L or S row is the element at its
// number, counted from 1: a write past the end appends, and a removal shifts the later elements down.
func editListRow(contract *v1.EpicContract, kind byte, number int, row, value string, remove bool) error {
	name := rowListName(kind)
	message := contract.ProtoReflect()
	list := message.Mutable(message.Descriptor().Fields().ByJSONName(name)).List()
	keyed := kind == 'F' || kind == 'R' || kind == 'P'
	index := number - 1
	if keyed {
		index = listIndexOf(list, row)
	}
	if remove {
		if index < 0 || index >= list.Len() {
			return &rowError{row: row, reason: "is not in the contract, so it cannot be removed"}
		}
		removeListElement(list, index)
		return nil
	}
	element, err := decodeListElement(name, row, value, keyed)
	if err != nil {
		return err
	}
	if index >= 0 && index < list.Len() {
		list.Set(index, element)
	} else {
		list.Append(element)
	}
	return nil
}

// listIndexOf returns the index of the element of list whose id is id, or -1 when no element has it.
func listIndexOf(list protoreflect.List, id string) int {
	for index := range list.Len() {
		if messageID(list.Get(index).Message()) == id {
			return index
		}
	}
	return -1
}

// removeListElement removes the element of list at index, and moves each element after it down one place.
func removeListElement(list protoreflect.List, index int) {
	last := list.Len() - 1
	for position := index; position < last; position++ {
		list.Set(position, list.Get(position+1))
	}
	list.Truncate(last)
}

// messageID returns the id a keyed row's message carries.
func messageID(message protoreflect.Message) string {
	return message.Get(message.Descriptor().Fields().ByName("id")).String()
}

// decodeListElement returns the element of the list named name that value holds: the element's own message in protobuf
// JSON, or a JSON string for a list of strings. For a keyed row the element's id must be the row's, and it takes the row's
// id when it names none.
func decodeListElement(name, row, value string, keyed bool) (protoreflect.Value, error) {
	decoded, err := decodeField(name, value, true)
	if err != nil {
		return protoreflect.Value{}, malformed(row, err)
	}
	message := decoded.ProtoReflect()
	elements := message.Get(message.Descriptor().Fields().ByJSONName(name)).List()
	if elements.Len() != 1 {
		return protoreflect.Value{}, malformed(row, errors.New("value is not one element"))
	}
	element := elements.Get(0)
	if !keyed {
		return element, nil
	}
	item := element.Message()
	idField := item.Descriptor().Fields().ByName("id")
	switch id := item.Get(idField).String(); {
	case id == "":
		item.Set(idField, protoreflect.ValueOfString(row))
	case id != row:
		return protoreflect.Value{}, &rowError{row: row, reason: "value's id is " + id}
	}
	return element, nil
}

// decodeField decodes value as the EpicContract field named name holds it: the field's value in JSON, or one element of its
// list when element is set. The value must be one JSON value, so that it cannot name a field of its own.
func decodeField(name, value string, element bool) (*v1.EpicContract, error) {
	if !json.Valid([]byte(value)) {
		return nil, errors.New("value is not one JSON value")
	}
	if element {
		value = "[" + value + "]"
	}
	var wrapper v1.EpicContract
	if err := protojson.Unmarshal([]byte(`{"`+name+`":`+value+`}`), &wrapper); err != nil {
		return nil, fmt.Errorf("decode value: %w", err)
	}
	return &wrapper, nil
}

// editDecision sets or removes the decision of row among decisions. A decision matches by its id, and a value is the
// ContractDecision's own message in protobuf JSON. It returns the decisions the write leaves, and leaves decisions alone.
func editDecision(
	decisions []store.ContractDecision,
	row, value string,
	remove bool,
) ([]store.ContractDecision, error) {
	index := slices.IndexFunc(decisions, func(decision store.ContractDecision) bool { return decision.ID == row })
	if remove {
		if index < 0 {
			return nil, &rowError{row: row, reason: "is not in the contract, so it cannot be removed"}
		}
		return slices.Delete(slices.Clone(decisions), index, index+1), nil
	}
	decision, err := decodeDecision(row, value)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(decisions)
	if index < 0 {
		return append(out, decision), nil
	}
	out[index] = decision
	return out, nil
}

// decodeDecision returns the decision value holds as the decision of row. Its id is the row's when it names none, and a
// decision naming another id is refused.
func decodeDecision(row, value string) (store.ContractDecision, error) {
	var message v1.ContractDecision
	if err := protojson.Unmarshal([]byte(value), &message); err != nil {
		return store.ContractDecision{}, malformed(row, err)
	}
	switch id := message.GetId(); {
	case id == "":
		message.Id = row
	case id != row:
		return store.ContractDecision{}, &rowError{row: row, reason: "value's id is " + id}
	}
	return storeDecisionOf(&message), nil
}

// malformed returns the refusal of a value row cannot hold, for the reason err gives.
func malformed(row string, err error) error {
	return &rowError{row: row, reason: "value is malformed: " + err.Error()}
}

// storeDecisionOf returns the store decision of message.
func storeDecisionOf(message *v1.ContractDecision) store.ContractDecision {
	return store.ContractDecision{
		ID: message.GetId(), Question: message.GetQuestion(), Options: message.GetOptions(),
		Recommendation: message.GetRecommendation(), Reason: message.GetReason(), Row: message.GetRow(),
		Answer: message.GetAnswer(),
	}
}

// stageRowWriteOf returns the StageRowWrite of write as the store holds it.
func stageRowWriteOf(write store.ContractRowWrite) *v1.StageRowWrite {
	return &v1.StageRowWrite{
		Seq: write.Seq, Version: write.Version, Row: write.Row, Action: write.Action, Depends: write.Depends,
		Words: write.Words,
		Maker: &v1.StageMaker{
			Kind: write.Maker, Quote: write.Quote, Reason: write.MakerReason, Delegation: write.Delegation,
		},
		Session: write.Session, At: write.At,
	}
}
