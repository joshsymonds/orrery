package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// defaultControlTimeout bounds each Control forward when Config.ControlTimeout is zero.
const defaultControlTimeout = 10 * time.Second

// maxControlBody is the largest Control request body the handler reads.
const maxControlBody = 1 << 20

// controlUnavailable is the fixed message for an orreryd that is away or has no store.
const controlUnavailable = "session service unavailable"

// controlFailed is the fixed message for an orreryd internal or unknown failure.
const controlFailed = "session command failed"

// controlTimedOut is the fixed message for a forward that outlives the control timeout.
const controlTimedOut = "session command timed out"

// controlStartRefused is the fixed message for a start whose directory is not an existing directory under the home.
const controlStartRefused = "session directory must be an existing directory under the home directory"

// controlServer answers orrery.v1.Control by forwarding each call to engine. A nil engine answers unavailable. Start
// forwards only a directory under home.
type controlServer struct {
	engine  enginev1connect.EngineClient
	timeout time.Duration
	home    string
}

// Start forwards a start to the engine and answers with the started session's id. The directory forwarded is the one
// resolveSessionDir admits, with its symlinks resolved.
func (c controlServer) Start(
	ctx context.Context,
	req *connect.Request[orreryv1.StartRequest],
) (*connect.Response[orreryv1.StartResponse], error) {
	dir, dirErr := resolveSessionDir(c.home, req.Msg.GetDir())
	if dirErr != nil {
		return nil, dirErr
	}
	var id string
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		resp, err := engine.Start(ctx, connect.NewRequest(&enginev1.StartRequest{
			Dir:    dir,
			Prompt: req.Msg.GetPrompt(),
			Model:  req.Msg.GetModel(),
			Id:     req.Msg.GetId(),
		}))
		if err != nil {
			return wrapEngine("start", err)
		}
		id = resp.Msg.GetId()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.StartResponse{Id: id}), nil
}

// Say forwards a message to the engine.
func (c controlServer) Say(
	ctx context.Context,
	req *connect.Request[orreryv1.SayRequest],
) (*connect.Response[orreryv1.SayResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Say(ctx, connect.NewRequest(&enginev1.SayRequest{
			Id:       req.Msg.GetId(),
			Text:     req.Msg.GetText(),
			PromptId: req.Msg.GetPromptId(),
		}))
		return wrapEngine("say", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.SayResponse{}), nil
}

// Answer forwards an answer to the engine.
func (c controlServer) Answer(
	ctx context.Context,
	req *connect.Request[orreryv1.AnswerRequest],
) (*connect.Response[orreryv1.AnswerResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Answer(ctx, connect.NewRequest(&enginev1.AnswerRequest{
			Id:        req.Msg.GetId(),
			RequestId: req.Msg.GetRequestId(),
			Answer:    req.Msg.GetAnswer(),
		}))
		return wrapEngine("answer", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.AnswerResponse{}), nil
}

// Interrupt forwards an interrupt to the engine.
func (c controlServer) Interrupt(
	ctx context.Context,
	req *connect.Request[orreryv1.InterruptRequest],
) (*connect.Response[orreryv1.InterruptResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Interrupt(ctx, connect.NewRequest(&enginev1.InterruptRequest{Id: req.Msg.GetId()}))
		return wrapEngine("interrupt", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.InterruptResponse{}), nil
}

// Stop forwards a stop to the engine.
func (c controlServer) Stop(
	ctx context.Context,
	req *connect.Request[orreryv1.StopRequest],
) (*connect.Response[orreryv1.StopResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Stop(ctx, connect.NewRequest(&enginev1.StopRequest{Id: req.Msg.GetId()}))
		return wrapEngine("stop", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.StopResponse{}), nil
}

// Seen forwards a seen mark to the engine.
func (c controlServer) Seen(
	ctx context.Context,
	req *connect.Request[orreryv1.SeenRequest],
) (*connect.Response[orreryv1.SeenResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Seen(ctx, connect.NewRequest(&enginev1.SeenRequest{Id: req.Msg.GetId()}))
		return wrapEngine("seen", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.SeenResponse{}), nil
}

// forward runs call against the engine under the control timeout. A nil engine answers unavailable. Any engine
// error answers as translateError does.
func (c controlServer) forward(
	ctx context.Context,
	call func(context.Context, enginev1connect.EngineClient) error,
) error {
	if c.engine == nil {
		return listError(connect.CodeUnavailable, controlUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := call(ctx, c.engine); err != nil {
		return translateError(err)
	}
	return nil
}

// resolveSessionDir returns dir with its symlinks resolved when dir is an absolute path to an existing directory equal
// to or under home, with its symlinks resolved. Any other dir answers invalid_argument, and so does any home that is
// empty, relative, the root directory, or missing.
func resolveSessionDir(home, dir string) (string, error) {
	refused := listError(connect.CodeInvalidArgument, controlStartRefused)
	root, ok := resolveHome(home)
	if !ok || !filepath.IsAbs(dir) {
		return "", refused
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", refused
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", refused
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return "", refused
	}
	return resolved, nil
}

// resolveHome returns home with its symlinks resolved. It reports ok false for an empty or relative home, for the
// root directory, and for a home that does not resolve.
func resolveHome(home string) (string, bool) {
	if !filepath.IsAbs(home) {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil || resolved == string(filepath.Separator) {
		return "", false
	}
	return resolved, true
}

// wrapEngine names the engine call op on err, keeping err reachable for translateError. A nil err stays nil.
func wrapEngine(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("server: control %s: %w", op, err)
}

// translateError returns the error a Control call answers with for err from the engine. A deadline_exceeded answers
// with the timeout message whichever side's deadline ended the call: the forward's own timer, or the deadline the
// client sent orreryd, which can reach the client first. unavailable, internal and unknown answer fixed messages,
// so orreryd's text never reaches the client. Other codes pass through with their messages.
func translateError(err error) error {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return listError(connect.CodeInternal, controlFailed)
	}
	switch connectErr.Code() {
	case connect.CodeDeadlineExceeded:
		return listError(connect.CodeDeadlineExceeded, controlTimedOut)
	case connect.CodeUnavailable:
		return listError(connect.CodeUnavailable, controlUnavailable)
	case connect.CodeInternal, connect.CodeUnknown:
		return listError(connect.CodeInternal, controlFailed)
	default:
		return listError(connectErr.Code(), connectErr.Message())
	}
}

// newControlHandler returns the path and handler that serve Control over engine, reading at most maxControlBody
// bytes of each request. A zero timeout is defaultControlTimeout. Start admits only directories under home.
func newControlHandler(engine enginev1connect.EngineClient, timeout time.Duration, home string) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	return orreryv1connect.NewControlHandler(
		controlServer{engine: engine, timeout: timeout, home: home},
		connect.WithReadMaxBytes(maxControlBody),
	)
}
