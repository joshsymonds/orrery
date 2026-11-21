package enginehost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// EscalateEpic marks the question its request names escalated, with the reason it gives, and returns the question read
// back. An empty id, an empty question, a question that is not Q<n> with a positive n, and a reason empty after trimming
// are InvalidArgument. A call from a step of the epic, the planner's turn included, is FailedPrecondition and marks
// nothing (see ownerCaller). A conversation that does not steer the epic is FailedPrecondition (see controlMaker). An
// unknown epic is NotFound, and an unknown question is NotFound on question. A question the store refuses, one answered,
// replaced or already escalated, is FailedPrecondition with the store's text (see store.Store.EscalateQuestion).
func (s *epicService) EscalateEpic(
	ctx context.Context,
	req *connect.Request[v1.EscalateEpicRequest],
) (*connect.Response[v1.EscalateEpicResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	seq, err := checkEscalateEpic(req.Msg)
	if err != nil {
		return nil, err
	}
	id := req.Msg.GetId()
	err = s.ownerCaller(ctx, id, req.Header())
	if err != nil {
		return nil, err
	}
	maker, err := s.controlMaker(ctx, id, req.Header())
	if err != nil {
		return nil, err
	}
	escalated, err := s.epics.EscalateQuestion(ctx, id, seq, req.Msg.GetReason(), maker, time.Now())
	if err != nil {
		return nil, escalateError(id, req.Msg.GetQuestion(), err)
	}
	return connect.NewResponse(&v1.EscalateEpicResponse{Question: questionMessage(escalated)}), nil
}

// checkEscalateEpic refuses a request with an empty id, an empty question, a question that is not Q<n> with a positive n,
// or a reason empty after trimming, as EscalateEpic gives. It returns the seq n of the question when the request is valid.
func checkEscalateEpic(req *v1.EscalateEpicRequest) (int64, error) {
	switch {
	case req.GetId() == "":
		return 0, refuseEpic(connect.CodeInvalidArgument, "id: empty")
	case req.GetQuestion() == "":
		return 0, refuseEpic(connect.CodeInvalidArgument, "question: empty")
	}
	seq, ok := questionSeq(req.GetQuestion())
	if !ok {
		return 0, refuseEpic(connect.CodeInvalidArgument, "question: "+req.GetQuestion()+" is not Q<n>")
	}
	if strings.TrimSpace(req.GetReason()) == "" {
		return 0, refuseEpic(connect.CodeInvalidArgument, "reason: empty")
	}
	return seq, nil
}

// escalateError maps an error from escalating question of epic to its Connect error: an unknown question is NotFound on
// question, and any other error goes through epicControlError.
func escalateError(epic, question string, err error) error {
	if errors.Is(err, store.ErrNoQuestion) {
		return refuseEpic(connect.CodeNotFound, fmt.Sprintf("question: no question %s on epic %s", question, epic))
	}
	return epicControlError(epic, err)
}
