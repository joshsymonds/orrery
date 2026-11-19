package server

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// syntheticSteerSession is the session id the steer tests move an epic to, or the session orreryd starts for it.
const syntheticSteerSession = "synthetic-steer-session"

func TestSteerMoveForwardsOneMoveWithoutCallerHeaders(t *testing.T) {
	t.Parallel()
	fake := &epicCommandsFake{}
	client := epicCommandServe(t, fake)
	req := connect.NewRequest(&orreryv1.EpicsMoveRequest{Id: syntheticEpicID, Session: syntheticSteerSession})
	req.Header().Set(protocol.HeaderSession, "synthetic-caller-session")
	if _, err := client.Move(t.Context(), req); err != nil {
		t.Fatalf("Move: %v", err)
	}
	assertForwarded(t, fake.recorded(), &enginev1.MoveEpicRequest{Id: syntheticEpicID, Session: syntheticSteerSession})
	if sessions := fake.recordedMoveSessions(); len(sessions) != 1 || sessions[0] != "" {
		t.Fatalf("engine Orrery-Session headers = %q, want one empty header", sessions)
	}
}

func TestSteerMovePassesRefusalThrough(t *testing.T) {
	t.Parallel()
	const message = "session: synthetic-steer-session is not a conversation"
	refused := connect.NewError(connect.CodeFailedPrecondition, errors.New(message))
	fake := &epicCommandsFake{failures: []error{refused}}
	client := epicCommandServe(t, fake)
	_, err := client.Move(t.Context(), connect.NewRequest(&orreryv1.EpicsMoveRequest{
		Id: syntheticEpicID, Session: syntheticSteerSession,
	}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Move: error = %v, want a Connect error", err)
	}
	if connectErr.Code() != connect.CodeFailedPrecondition || connectErr.Message() != message {
		t.Fatalf("Move: error = %v %q, want %v %q",
			connectErr.Code(), connectErr.Message(), connect.CodeFailedPrecondition, message)
	}
	if n := len(fake.recorded()); n != 1 {
		t.Fatalf("engine calls = %d, want one: a refusal is not retried", n)
	}
}

func TestSteerContinueForwardsOneContinueAndAnswersSession(t *testing.T) {
	t.Parallel()
	fake := &epicCommandsFake{continueSession: syntheticSteerSession}
	client := epicCommandServe(t, fake)
	reply, err := client.Continue(t.Context(), connect.NewRequest(&orreryv1.EpicsContinueRequest{
		Id: syntheticEpicID,
	}))
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	assertForwarded(t, fake.recorded(), &enginev1.ContinueEpicRequest{Id: syntheticEpicID})
	want := &orreryv1.EpicsContinueResponse{Session: syntheticSteerSession}
	if !proto.Equal(reply.Msg, want) {
		t.Fatalf("Continue = %v, want %v", reply.Msg, want)
	}
}

func TestSteerContinuePassesRefusalThrough(t *testing.T) {
	t.Parallel()
	const message = "epic_id: no epic " + syntheticEpicID
	refused := connect.NewError(connect.CodeNotFound, errors.New(message))
	fake := &epicCommandsFake{failures: []error{refused}}
	client := epicCommandServe(t, fake)
	_, err := client.Continue(t.Context(), connect.NewRequest(&orreryv1.EpicsContinueRequest{Id: syntheticEpicID}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Continue: error = %v, want a Connect error", err)
	}
	if connectErr.Code() != connect.CodeNotFound || connectErr.Message() != message {
		t.Fatalf("Continue: error = %v %q, want %v %q",
			connectErr.Code(), connectErr.Message(), connect.CodeNotFound, message)
	}
	if n := len(fake.recorded()); n != 1 {
		t.Fatalf("engine calls = %d, want one: a refusal is not retried", n)
	}
}
