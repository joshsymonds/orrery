package enginehost

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// EpicCallRecorder records a call of an epic by the session that made it. *store.Store satisfies it.
type EpicCallRecorder interface {
	RecordEpicCall(ctx context.Context, epic string, caller store.EpicCaller, call string, at time.Time) (bool, error)
}

// EpicCallInterceptor records each unary call of the Epics service that names an epic and returns without error, when
// its request carries caller headers. The caller is the session in protocol.HeaderSession, or else the pane that
// protocol.HeaderTmux and protocol.HeaderTmuxPane name; a request with neither records nothing. The call is "epic_"
// and the lowercased verb of its method, so CreateEpic records epic_create. Each record takes now() as its time. A
// record error is logged and the call's answer is returned unchanged. Other services and streaming calls pass through.
func EpicCallInterceptor(rec EpicCallRecorder, now func() time.Time, logger *slog.Logger) connect.UnaryInterceptorFunc {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			res, err := next(ctx, req)
			if err != nil || req.Spec().IsClient {
				return res, err
			}
			epic := epicNamedBy(req.Any())
			caller, ok := epicCallerOf(req.Header())
			if epic == "" || !ok {
				return res, nil
			}
			call := epicCallName(req.Spec().Procedure)
			if _, recordErr := rec.RecordEpicCall(ctx, epic, caller, call, now()); recordErr != nil {
				logger.ErrorContext(ctx, "record epic call", "epic", epic, "call", call, "error", recordErr)
			}
			return res, nil
		}
	})
}

// epicNamedBy returns the id of the epic msg names. It returns "" for a message that names none: ListEpics, FollowEpics
// and every message of another service.
func epicNamedBy(msg any) string {
	switch req := msg.(type) {
	case *v1.CreateEpicRequest:
		return req.GetContract().GetId()
	case *v1.AnswerEpicRequest:
		return req.GetEpicId()
	case *v1.GetEpicRequest:
		return req.GetId()
	case *v1.PauseEpicRequest:
		return req.GetId()
	case *v1.ResumeEpicRequest:
		return req.GetId()
	case *v1.StopEpicRequest:
		return req.GetId()
	case *v1.RetryEpicRequest:
		return req.GetId()
	case *v1.KickEpicRequest:
		return req.GetId()
	case *v1.ReleaseEpicRequest:
		return req.GetId()
	case *v1.CloseEpicRequest:
		return req.GetId()
	}
	return ""
}

// epicCallerOf returns the caller header holds, and false when it holds none. A session names itself. A pane names the
// tmux socket, the part of TMUX before its first comma, and its TMUX_PANE, as the server column of a pane row does.
func epicCallerOf(header http.Header) (store.EpicCaller, bool) {
	if session := header.Get(protocol.HeaderSession); session != "" {
		return store.EpicCaller{Session: session}, true
	}
	tmux, pane := header.Get(protocol.HeaderTmux), header.Get(protocol.HeaderTmuxPane)
	if tmux == "" || pane == "" {
		return store.EpicCaller{}, false
	}
	socket, _, _ := strings.Cut(tmux, ",")
	return store.EpicCaller{Server: "-S " + socket, Pane: pane}, true
}

// epicCallName returns the name a call of procedure records: "epic_" and the lowercased verb of its method, so
// "/orrery.engine.v1.Epics/CreateEpic" records "epic_create".
func epicCallName(procedure string) string {
	method := procedure[strings.LastIndex(procedure, "/")+1:]
	return "epic_" + strings.ToLower(strings.TrimSuffix(method, "Epic"))
}
