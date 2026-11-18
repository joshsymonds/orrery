package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// callResult is the outcome of a control call that ran on its own goroutine.
type callResult[T any] struct {
	value T
	err   error
}

// startCall runs call on its own goroutine and returns the channel its outcome arrives on. The goroutine ends when
// call returns, so each test receives from the channel of every call it starts.
func startCall[T any](call func() (T, error)) <-chan callResult[T] {
	result := make(chan callResult[T], 1)
	go func() {
		value, err := call()
		result <- callResult[T]{value: value, err: err}
	}()
	return result
}

// awaitStdinLine returns the n-th line the session wrote to its process's stdin, the initialize request being the
// first. It waits for that write, a state, and has no deadline of its own.
func awaitStdinLine(process *fakeProcess, n int) string {
	for {
		if lines := stdinLines(process); len(lines) >= n {
			return lines[n-1]
		}
		runtime.Gosched()
	}
}

// controlRequestEnvelope is the part of a control request the tests read.
type controlRequestEnvelope struct {
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype      string `json:"subtype"`
		MessageUUID  string `json:"message_uuid"`
		CancelQueued bool   `json:"cancel_queued"`
	} `json:"request"`
}

// requireControlRequest fails the test unless line is a control request, and returns it decoded.
func requireControlRequest(t *testing.T, line string) controlRequestEnvelope {
	t.Helper()
	var envelope controlRequestEnvelope
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("decode control request %q: %v", line, err)
	}
	return envelope
}

// successReply returns a control response that answers requestID with the JSON object response.
func successReply(requestID, response string) string {
	return `{"type":"control_response","response":{"subtype":"success","request_id":"` + requestID +
		`","response":` + response + `}}`
}

// errorReply returns a control response that refuses requestID with message.
func errorReply(requestID, message string) string {
	return `{"type":"control_response","response":{"subtype":"error","request_id":"` + requestID +
		`","error":"` + message + `"}}`
}

// answer emits reply on the process's stdout, then reads the line the session delivers for it. The session routes a
// reply before it sends its line, so the line's arrival means the reply has reached its waiter. A control response
// yields no event.
func answer(t *testing.T, session *Session, process *fakeProcess, reply string) {
	t.Helper()
	emit(t, process, []byte(reply))
	if line := nextLine(t, session); len(line.Events) != 0 {
		t.Fatalf("control response line carries events %#v, want none", line.Events)
	}
}

func TestControlReplyCancelQueuedReturnsReplyCancelled(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	for i, cancelled := range []bool{true, false} {
		call := startCall(func() (bool, error) {
			return session.CancelQueued(context.Background(), "write-1")
		})
		request := requireControlRequest(t, awaitStdinLine(process, i+2))
		if request.Request.Subtype != "cancel_async_message" || request.Request.MessageUUID != "write-1" {
			t.Fatalf("request = %#v, want cancel_async_message for write-1", request)
		}
		answer(t, session, process, successReply(request.RequestID, fmt.Sprintf(`{"cancelled":%t}`, cancelled)))
		if got := <-call; got.err != nil || got.value != cancelled {
			t.Fatalf("CancelQueued = %v, %v; want %v, nil", got.value, got.err, cancelled)
		}
	}
}

func TestControlReplyInterruptCancelQueuedReturnsUUIDsInReplyOrder(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	call := startCall(func() ([]string, error) {
		return session.InterruptCancelQueued(context.Background())
	})
	request := requireControlRequest(t, awaitStdinLine(process, 2))
	if request.Request.Subtype != "interrupt" || !request.Request.CancelQueued {
		t.Fatalf("request = %#v, want an interrupt with cancel_queued", request)
	}
	want := []string{"uuid-c", "uuid-a", "uuid-b"}
	answer(t, session, process, successReply(request.RequestID, `{"cancelled":["uuid-c","uuid-a","uuid-b"]}`))
	if got := <-call; got.err != nil || !slices.Equal(got.value, want) {
		t.Fatalf("InterruptCancelQueued = %q, %v; want %q, nil", got.value, got.err, want)
	}
}

// TestControlReplyOtherRequestIDWakesNoWaiter answers first with a reply to another request id, which says the
// message was not cancelled. The waiter must not take it: the call returns the cancelled reply that follows.
func TestControlReplyOtherRequestIDWakesNoWaiter(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	call := startCall(func() (bool, error) {
		return session.CancelQueued(context.Background(), "write-1")
	})
	request := requireControlRequest(t, awaitStdinLine(process, 2))
	answer(t, session, process, successReply("orrery-other", `{"cancelled":false}`))
	answer(t, session, process, successReply(request.RequestID, `{"cancelled":true}`))
	if got := <-call; got.err != nil || !got.value {
		t.Fatalf("CancelQueued = %v, %v; want true, nil from its own reply", got.value, got.err)
	}
}

func TestControlReplyErrorReplyReturnsError(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	call := startCall(func() (bool, error) {
		return session.CancelQueued(context.Background(), "write-1")
	})
	request := requireControlRequest(t, awaitStdinLine(process, 2))
	answer(t, session, process, errorReply(request.RequestID, "no such queued message"))
	if got := <-call; got.err == nil || !strings.Contains(got.err.Error(), "no such queued message") {
		t.Fatalf("CancelQueued = %v, %v; want an error carrying the reply's text", got.value, got.err)
	}
}

func TestControlReplySessionExitEndsWaitingCall(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	call := startCall(func() (bool, error) {
		return session.CancelQueued(context.Background(), "write-1")
	})
	awaitStdinLine(process, 2)
	process.exit(nil, "")
	if got := <-call; !errors.Is(got.err, errSessionEnded) {
		t.Fatalf("CancelQueued after the session exited = %v, %v; want errSessionEnded", got.value, got.err)
	}
}

// TestControlReplyCancelledContextReturnsAndLeavesNoWaiter cancels ctx before the call. The request is still written,
// but no reply comes, so the call returns ctx's error and the waiter is gone once it returns.
func TestControlReplyCancelledContextReturnsAndLeavesNoWaiter(t *testing.T) {
	t.Parallel()
	session, _ := startSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.CancelQueued(ctx, "write-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("CancelQueued with a done ctx = %v, want context.Canceled", err)
	}
	session.waiters.mu.Lock()
	defer session.waiters.mu.Unlock()
	if n := len(session.waiters.byID); n != 0 {
		t.Fatalf("%d waiters remain after the call returned, want none", n)
	}
}
