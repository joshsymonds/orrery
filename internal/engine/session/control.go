package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

// controlErrorSubtype is the subtype of a control response that refuses its request.
const controlErrorSubtype = "error"

// errSessionEnded is the error a control call returns when the session ends before its reply arrives.
var errSessionEnded = errors.New("session: the session ended before its control reply")

// controlReply is what a waiting control call receives: the response its reply carried, or the error the reply
// refused it with.
type controlReply struct {
	response json.RawMessage
	err      error
}

// controlWaiters is the set of control calls waiting for a reply, by request id. The reader delivers replies and ends
// the set; each call registers and removes its own entry. Every access is guarded by mu.
type controlWaiters struct {
	mu    sync.Mutex
	byID  map[string]chan controlReply
	ended bool
}

func newControlWaiters() *controlWaiters {
	return &controlWaiters{byID: make(map[string]chan controlReply)}
}

// register adds a waiter for requestID and returns the channel its reply arrives on. It fails once the set has ended,
// so no call waits on a session that has ended.
func (w *controlWaiters) register(requestID string) (<-chan controlReply, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ended {
		return nil, errSessionEnded
	}
	waiter := make(chan controlReply, 1)
	w.byID[requestID] = waiter
	return waiter, nil
}

// remove drops the waiter for requestID, if it is still registered.
func (w *controlWaiters) remove(requestID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.byID, requestID)
}

// deliver hands reply to the waiter registered for requestID and removes that waiter. A reply for no waiter wakes
// nothing. The channel has room for its one reply, so the send never blocks.
func (w *controlWaiters) deliver(requestID string, reply controlReply) {
	w.mu.Lock()
	defer w.mu.Unlock()
	waiter, ok := w.byID[requestID]
	if !ok {
		return
	}
	delete(w.byID, requestID)
	waiter <- reply
}

// end hands every waiter errSessionEnded and refuses later registrations.
func (w *controlWaiters) end() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ended = true
	for requestID, waiter := range w.byID {
		waiter <- controlReply{err: errSessionEnded}
		delete(w.byID, requestID)
	}
}

// CancelQueued cancels the queued user message writeID, the uuid it was sent under, and reports whether Claude
// cancelled it. The call waits for Claude's reply until ctx is done or the session ends.
func (s *Session) CancelQueued(ctx context.Context, writeID string) (bool, error) {
	var body struct {
		Cancelled bool `json:"cancelled"`
	}
	err := s.controlCall(ctx, func(requestID string) error {
		return s.transport.SendCancelAsyncMessage(requestID, writeID)
	}, &body)
	if err != nil {
		return false, fmt.Errorf("session: cancel queued message %s: %w", writeID, err)
	}
	return body.Cancelled, nil
}

// InterruptCancelQueued interrupts the turn in progress and cancels the queued user messages. It returns the uuids of
// the messages Claude cancelled, in the order its reply lists them. The call waits as CancelQueued does.
func (s *Session) InterruptCancelQueued(ctx context.Context) ([]string, error) {
	var body struct {
		Cancelled []string `json:"cancelled"`
	}
	err := s.controlCall(ctx, s.transport.SendInterruptCancelQueued, &body)
	if err != nil {
		return nil, fmt.Errorf("session: interrupt canceling queued messages: %w", err)
	}
	return body.Cancelled, nil
}

// controlCall sends one control request under a fresh request id, through send, and waits for its reply. It decodes
// the reply's response into out, and returns the error a refusing reply carries. A call whose ctx is done returns
// ctx's error, and a call the session ends before its reply returns errSessionEnded. Either way it leaves no waiter
// behind.
func (s *Session) controlCall(ctx context.Context, send func(requestID string) error, out any) error {
	requestID, err := newRequestID()
	if err != nil {
		return err
	}
	waiter, err := s.waiters.register(requestID)
	if err != nil {
		return err
	}
	defer s.waiters.remove(requestID)
	if err = s.write(requestID, func() error { return send(requestID) }); err != nil {
		return fmt.Errorf("session: send control request %s: %w", requestID, err)
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("session: control request %s: %w", requestID, ctx.Err())
	case reply := <-waiter:
		if reply.err != nil {
			return reply.err
		}
		if err = json.Unmarshal(reply.response, out); err != nil {
			return fmt.Errorf("session: decode reply to control request %s: %w", requestID, err)
		}
		return nil
	}
}

// controlEnvelope is the part of a stdout line the reader routes by: a control response's type and, for its reply,
// the request id, subtype, error text and response. The request id is keyed by its wire spelling, as the wire
// package decodes control messages.
type controlEnvelope struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `wire:"request_id"`
		Error     string          `json:"error"`
		Body      json.RawMessage `json:"response"`
	} `json:"response"`
}

// routeControlReply hands the reply that line carries to the waiter registered under its request id. A line that is
// not a control response, or does not decode, is no reply: the translator reports such a line as Unknown.
func (s *Session) routeControlReply(line []byte) {
	var envelope controlEnvelope
	if err := wire.UnmarshalControlJSON(line, &envelope); err != nil || envelope.Type != controlResponse {
		return
	}
	reply := controlReply{response: envelope.Response.Body}
	if envelope.Response.Subtype == controlErrorSubtype {
		reply = controlReply{err: fmt.Errorf(
			"session: control request %s refused: %s", envelope.Response.RequestID, envelope.Response.Error,
		)}
	}
	s.waiters.deliver(envelope.Response.RequestID, reply)
}
