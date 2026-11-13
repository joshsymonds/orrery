package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// refusalMessage is the error a refused request is answered with.
const refusalMessage = "orrery does not answer this request"

// ErrNoPendingRequest reports a command for a request that is not open.
var ErrNoPendingRequest = errors.New("session: no open request with that id")

// ErrNotQuestion reports AnswerQuestion for a request that is not a question.
var ErrNotQuestion = errors.New("session: request is not a question")

// ErrUnverifiedAnswer reports an answer that does not match the recorded question shape.
var ErrUnverifiedAnswer = errors.New("session: answer does not match the recorded question shape")

// openRequest is a request the host may answer: its kind, tool use id and the input it asked about.
type openRequest struct {
	kind      RequestKind
	toolUseID string
	input     json.RawMessage
}

// openRequests is the set of requests the host may answer, by request id. The reader adds and cancels
// entries and commands claim them, so every access is guarded by mu. No caller holds mu across a write.
type openRequests struct {
	mu   sync.Mutex
	byID map[string]openRequest
}

func newOpenRequests() *openRequests {
	return &openRequests{byID: make(map[string]openRequest)}
}

// open adds request to the set. A request with the tool use id of an open one replaces it.
func (o *openRequests) open(request Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if request.ToolUseID != "" {
		for id, held := range o.byID {
			if held.toolUseID == request.ToolUseID {
				delete(o.byID, id)
			}
		}
	}
	o.byID[request.RequestID] = openRequest{
		kind:      request.Kind,
		toolUseID: request.ToolUseID,
		input:     request.Input,
	}
}

// lookup returns the open request with requestID, and leaves it open.
func (o *openRequests) lookup(requestID string) (openRequest, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	request, ok := o.byID[requestID]
	return request, ok
}

// claim removes the open request with requestID and returns it. It reports false when none is open,
// so a request is claimed by at most one caller.
func (o *openRequests) claim(requestID string) (openRequest, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	request, ok := o.byID[requestID]
	if ok {
		delete(o.byID, requestID)
	}
	return request, ok
}

// observeLine records the request state of each event of a line that starts at start, in order, before the line is
// sent. A request is opened before the host sees the line that holds it, so a host that answers on receipt finds it
// open. Each request takes the write id "event:" with start and its index in the line. Each event is replaced by its
// observed form.
func (s *Session) observeLine(start int64, events []Event) {
	for i, event := range events {
		switch event := event.(type) {
		case Request:
			event.WriteID = fmt.Sprintf("event:%d.%d", start, i)
			events[i] = s.observeRequest(event)
		case RequestCancelled:
			events[i] = s.observeCancel(event)
		}
	}
}

// observeCancel gives a cancellation the tool use id of the open request it withdraws.
func (s *Session) observeCancel(canceled RequestCancelled) RequestCancelled {
	if request, ok := s.pending.claim(canceled.RequestID); ok {
		canceled.ToolUseID = request.toolUseID
	}
	return canceled
}

// observeRequest opens every request the host has not answered and refuses a request the package cannot answer. A
// request the host already answered is neither opened nor refused, so it is never answered twice. A replayed request
// is not refused: the session writes nothing for a replayed line. A refusal that cannot be written leaves the request
// unrefused, so the host still sees it.
func (s *Session) observeRequest(request Request) Request {
	request.Answered = s.written[responseWriteID(request.RequestID)]
	if request.Answered {
		return request
	}
	if !request.Refused {
		s.pending.open(request)
		return request
	}
	if s.replaying {
		return request
	}
	err := s.write(request.WriteID, func() error {
		return s.transport.SendControlError(request.RequestID, refusalMessage)
	})
	if err != nil {
		request.Refused = false
	}
	return request
}

// responseWriteID returns the write id of a host's answer to the control request requestID of claude: "response:"
// and the request id. When that passes maxWriteIDBytes, it is "response:sha256:" and the hex SHA-256 of the request id.
func responseWriteID(requestID string) string {
	plain := "response:" + requestID
	if len(plain) <= maxWriteIDBytes {
		return plain
	}
	sum := sha256.Sum256([]byte(requestID))
	return "response:sha256:" + hex.EncodeToString(sum[:])
}

// AnswerQuestion answers the open question requestID with the option labeled label. It accepts only
// the recorded shape: one question, not multiSelect, and a label equal to one of its options. Any other
// shape returns ErrUnverifiedAnswer and writes nothing, and the request stays open.
func (s *Session) AnswerQuestion(requestID, label string) error {
	request, ok := s.pending.lookup(requestID)
	if !ok {
		return ErrNoPendingRequest
	}
	if request.kind != RequestQuestion {
		return ErrNotQuestion
	}
	updated, err := recordedAnswer(request.input, label)
	if err != nil {
		return err
	}
	if _, claimed := s.pending.claim(requestID); !claimed {
		return ErrNoPendingRequest
	}
	err = s.write(responseWriteID(requestID), func() error {
		return s.transport.SendPermissionAllow(requestID, updated)
	})
	if err != nil {
		return fmt.Errorf("session: answer question %s: %w", requestID, err)
	}
	return nil
}

// askQuestion is one entry of an AskUserQuestion questions array, reduced to the fields an answer reads.
type askQuestion struct {
	Question    string      `json:"question"`
	MultiSelect bool        `json:"multiSelect"`
	Options     []askOption `json:"options"`
}

// askOption is one option of an AskUserQuestion question.
type askOption struct {
	Label string `json:"label"`
}

// recordedAnswer returns the updated input that answers the one question in input with label. The
// answers object, keyed by the question text and valued by the label, is added beside the original keys.
func recordedAnswer(input json.RawMessage, label string) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(input, &fields); err != nil {
		return nil, fmt.Errorf("%w: decode question input: %w", ErrUnverifiedAnswer, err)
	}
	var questions []askQuestion
	if err := json.Unmarshal(fields["questions"], &questions); err != nil {
		return nil, fmt.Errorf("%w: decode questions: %w", ErrUnverifiedAnswer, err)
	}
	if len(questions) != 1 {
		return nil, fmt.Errorf("%w: %d questions, want one", ErrUnverifiedAnswer, len(questions))
	}
	question := questions[0]
	if question.MultiSelect {
		return nil, fmt.Errorf("%w: question allows several answers", ErrUnverifiedAnswer)
	}
	if !slices.ContainsFunc(question.Options, func(option askOption) bool { return option.Label == label }) {
		return nil, fmt.Errorf("%w: %q is not an option", ErrUnverifiedAnswer, label)
	}
	answers, err := json.Marshal(map[string]string{question.Question: label})
	if err != nil {
		return nil, fmt.Errorf("session: encode answers: %w", err)
	}
	fields["answers"] = answers
	updated, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("session: encode answered input: %w", err)
	}
	return updated, nil
}

// Allow approves the open request requestID. A nil updatedInput approves it with the request's own input.
// An updatedInput that is not JSON is rejected before the request is claimed, so it stays open.
func (s *Session) Allow(requestID string, updatedInput json.RawMessage) error {
	if updatedInput != nil && !json.Valid(updatedInput) {
		return fmt.Errorf("session: allow %s: updated input is not JSON", requestID)
	}
	request, ok := s.pending.claim(requestID)
	if !ok {
		return ErrNoPendingRequest
	}
	if updatedInput == nil {
		updatedInput = request.input
	}
	if err := s.write(responseWriteID(requestID), func() error {
		return s.transport.SendPermissionAllow(requestID, updatedInput)
	}); err != nil {
		return fmt.Errorf("session: allow %s: %w", requestID, err)
	}
	return nil
}

// Deny rejects the open request requestID with message for the user.
func (s *Session) Deny(requestID, message string) error {
	return s.deny(requestID, responseWriteID(requestID), message)
}

// DenyEvent rejects the open request of request, which the session delivered in an event, with message for the
// user. It writes under request.WriteID, the write id of the event.
func (s *Session) DenyEvent(request Request, message string) error {
	return s.deny(request.RequestID, request.WriteID, message)
}

// deny claims the open request requestID and writes its denial with message under writeID.
func (s *Session) deny(requestID, writeID, message string) error {
	if _, ok := s.pending.claim(requestID); !ok {
		return ErrNoPendingRequest
	}
	if err := s.write(writeID, func() error {
		return s.transport.SendPermissionDeny(requestID, message)
	}); err != nil {
		return fmt.Errorf("session: deny %s: %w", requestID, err)
	}
	return nil
}

// Refuse answers the open request requestID with an error response carrying message.
func (s *Session) Refuse(requestID, message string) error {
	if _, ok := s.pending.claim(requestID); !ok {
		return ErrNoPendingRequest
	}
	if err := s.write(responseWriteID(requestID), func() error {
		return s.transport.SendControlError(requestID, message)
	}); err != nil {
		return fmt.Errorf("session: refuse %s: %w", requestID, err)
	}
	return nil
}

// SetPermissionMode asks the session to switch to mode, under a fresh random id.
func (s *Session) SetPermissionMode(mode string) error {
	id, err := newRequestID()
	if err == nil {
		err = s.write(id, func() error { return s.transport.SendSetPermissionMode(id, mode) })
	}
	if err != nil {
		return fmt.Errorf("session: set permission mode: %w", err)
	}
	return nil
}
