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
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/store"
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

// controlStartRefusedOnHost is the fixed message for a start on a host whose agent refuses its directory.
const controlStartRefusedOnHost = "session directory must be an existing directory under the host's root"

// controlServer answers orrery.v1.Control by forwarding each call to engine. A nil engine answers unavailable. Start
// forwards only a directory under home that resolveSessionDir admits on the controller's own host, and only a directory
// that the agent of a hosts.toml host admits on that host. controller is the controller's orrery name, and hosts holds
// the agent of each hosts.toml host by name.
type controlServer struct {
	engine     enginev1connect.EngineClient
	timeout    time.Duration
	home       string
	steps      StepClient
	controller string
	hosts      map[string]*node.Client
}

// Start forwards a start to the engine and answers with the started session's id. The host the start names decides
// where its directory is checked: startDir checks it under home for the controller's own host, and on the agent of the
// host otherwise. The directory forwarded is the one that check resolves, with its symlinks resolved.
func (c controlServer) Start(
	ctx context.Context,
	req *connect.Request[orreryv1.StartRequest],
) (*connect.Response[orreryv1.StartResponse], error) {
	dir, dirErr := c.startDir(ctx, req.Msg.GetHost(), req.Msg.GetDir())
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
			Client: req.Header().Get(HeaderClient),
			Host:   req.Msg.GetHost(),
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

// startDir returns the directory a start on host runs in. An empty host, or the controller's own name, checks dir under
// home as resolveSessionDir does. A host that one of hosts.toml names has its agent check dir under the agent's root,
// and answers the path the agent resolved. An agent that refuses dir answers invalid_argument, and any other failure
// to reach it answers unavailable. A host that no agent serves answers invalid_argument, and nothing is checked.
func (c controlServer) startDir(ctx context.Context, host, dir string) (string, error) {
	if host == "" || host == c.controller {
		return resolveSessionDir(c.home, dir)
	}
	agent, ok := c.hosts[host]
	if !ok {
		return "", listError(connect.CodeInvalidArgument, "unknown host "+host)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	resolved, err := agent.Folder(ctx, dir)
	switch {
	case err == nil:
		return resolved, nil
	case errors.Is(err, node.ErrRefused):
		return "", listError(connect.CodeInvalidArgument, controlStartRefusedOnHost)
	default:
		return "", listError(connect.CodeUnavailable, fmt.Sprintf("host %s is unreachable", host))
	}
}

// Say forwards a message to the engine, unless its id names a step.
func (c controlServer) Say(
	ctx context.Context,
	req *connect.Request[orreryv1.SayRequest],
) (*connect.Response[orreryv1.SayResponse], error) {
	if err := c.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Say(ctx, connect.NewRequest(&enginev1.SayRequest{
			Id:       req.Msg.GetId(),
			Text:     req.Msg.GetText(),
			PromptId: req.Msg.GetPromptId(),
			Client:   req.Header().Get(HeaderClient),
		}))
		return wrapEngine("say", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.SayResponse{}), nil
}

// Answer forwards an answer to the engine, unless its id names a step.
func (c controlServer) Answer(
	ctx context.Context,
	req *connect.Request[orreryv1.AnswerRequest],
) (*connect.Response[orreryv1.AnswerResponse], error) {
	if err := c.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
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

// Interrupt forwards an interrupt to the engine, unless its id names a step. It answers with the texts the engine
// cancelled from the session's queue at the interrupt, in send order.
func (c controlServer) Interrupt(
	ctx context.Context,
	req *connect.Request[orreryv1.InterruptRequest],
) (*connect.Response[orreryv1.InterruptResponse], error) {
	if err := c.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	var texts []string
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		resp, err := engine.Interrupt(ctx, connect.NewRequest(&enginev1.InterruptRequest{Id: req.Msg.GetId()}))
		if err != nil {
			return wrapEngine("interrupt", err)
		}
		texts = resp.Msg.GetTexts()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.InterruptResponse{Texts: texts}), nil
}

// Decline forwards a decline to the engine, unless it is a headless call whose id names a step. A pane's decline is not
// checked against steps, since a step is never a pane. It forwards the kind and question_hash unchanged, and answers
// with the texts the engine cancelled from the session's queue at the decline, in send order, which a pane's decline
// leaves empty.
func (c controlServer) Decline(
	ctx context.Context,
	req *connect.Request[orreryv1.DeclineRequest],
) (*connect.Response[orreryv1.DeclineResponse], error) {
	if req.Msg.GetKind() != store.PutOffPane {
		if err := c.refuseStep(ctx, req.Msg.GetId()); err != nil {
			return nil, err
		}
	}
	var resp *connect.Response[enginev1.DeclineResponse]
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		var callErr error
		resp, callErr = engine.Decline(ctx, connect.NewRequest(&enginev1.DeclineRequest{
			Id:           req.Msg.GetId(),
			RequestId:    req.Msg.GetRequestId(),
			Kind:         req.Msg.GetKind(),
			QuestionHash: req.Msg.GetQuestionHash(),
		}))
		return wrapEngine("decline", callErr)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.DeclineResponse{Texts: resp.Msg.GetTexts()}), nil
}

// Stop forwards a stop to the engine, unless its id names a step.
func (c controlServer) Stop(
	ctx context.Context,
	req *connect.Request[orreryv1.StopRequest],
) (*connect.Response[orreryv1.StopResponse], error) {
	if err := c.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
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

// Resume forwards a resume to the engine, unless its id names a step. orreryd sends the session its resume prompt, or
// refuses one that is not interrupted or has no Claude session, and the refusal's message passes through.
func (c controlServer) Resume(
	ctx context.Context,
	req *connect.Request[orreryv1.ResumeRequest],
) (*connect.Response[orreryv1.ResumeResponse], error) {
	if err := c.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.Resume(ctx, connect.NewRequest(&enginev1.ResumeRequest{Id: req.Msg.GetId()}))
		return wrapEngine("resume", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.ResumeResponse{}), nil
}

// Withdraw forwards a request to cancel one queued owner message to the engine, unless its id names a step. It answers
// with that message's text when the engine cancelled it, and none when Claude had already taken it in, when the session
// is not running, or when prompt_id is empty.
func (c controlServer) Withdraw(
	ctx context.Context,
	req *connect.Request[orreryv1.WithdrawRequest],
) (*connect.Response[orreryv1.WithdrawResponse], error) {
	if err := c.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	var texts []string
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		resp, err := engine.Withdraw(ctx, connect.NewRequest(&enginev1.WithdrawRequest{
			Id:       req.Msg.GetId(),
			PromptId: req.Msg.GetPromptId(),
		}))
		if err != nil {
			return wrapEngine("withdraw", err)
		}
		texts = resp.Msg.GetTexts()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.WithdrawResponse{Texts: texts}), nil
}

// NotNow forwards a not-now mark to the engine with every field unchanged, with no step check.
func (c controlServer) NotNow(
	ctx context.Context,
	req *connect.Request[orreryv1.NotNowRequest],
) (*connect.Response[orreryv1.NotNowResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.NotNow(ctx, connect.NewRequest(&enginev1.NotNowRequest{
			Kind:         req.Msg.GetKind(),
			Id:           req.Msg.GetId(),
			RequestId:    req.Msg.GetRequestId(),
			QuestionHash: req.Msg.GetQuestionHash(),
			Changed:      req.Msg.GetChanged(),
		}))
		return wrapEngine("not now", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.NotNowResponse{}), nil
}

// BringBack forwards a bring-back to the engine with every field unchanged, with no step check.
func (c controlServer) BringBack(
	ctx context.Context,
	req *connect.Request[orreryv1.BringBackRequest],
) (*connect.Response[orreryv1.BringBackResponse], error) {
	err := c.forward(ctx, func(ctx context.Context, engine enginev1connect.EngineClient) error {
		_, err := engine.BringBack(ctx, connect.NewRequest(&enginev1.BringBackRequest{
			Kind:         req.Msg.GetKind(),
			Id:           req.Msg.GetId(),
			RequestId:    req.Msg.GetRequestId(),
			QuestionHash: req.Msg.GetQuestionHash(),
			Changed:      req.Msg.GetChanged(),
		}))
		return wrapEngine("bring back", err)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.BringBackResponse{}), nil
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

// refuseStep answers failed_precondition when id names a step, so a session command cannot drive a step around its own
// loop. A nil steps client, or a StepStatus that answers not_found, returns nil and lets the command forward. Any other
// StepStatus error answers as translateError does. The lookup runs under the control timeout.
func (c controlServer) refuseStep(ctx context.Context, id string) error {
	if c.steps == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	_, err := c.steps.StepStatus(ctx, connect.NewRequest(&enginev1.StepStatusRequest{Id: id}))
	var connectErr *connect.Error
	switch {
	case err == nil:
		return listError(connect.CodeFailedPrecondition, fmt.Sprintf("%s is a step: drive it through Steps", id))
	case errors.As(err, &connectErr) && connectErr.Code() == connect.CodeNotFound:
		return nil
	default:
		return translateError(err)
	}
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
// bytes of each request. A zero timeout is defaultControlTimeout. Start forwards only directories under home that
// resolveSessionDir admits on the controller, controller being its orrery name, and only directories that the agent of
// one of hosts admits on that host.
func newControlHandler(
	engine enginev1connect.EngineClient,
	timeout time.Duration,
	home string,
	steps StepClient,
	controller string,
	hosts []*node.Client,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	options := append([]connect.HandlerOption{connect.WithReadMaxBytes(maxControlBody)}, opts...)
	return orreryv1connect.NewControlHandler(
		newControlServer(engine, timeout, home, steps, controller, hosts), options...,
	)
}

// newControlServer returns the Control service over engine and steps for home, each forward bounded by timeout, or by
// defaultControlTimeout when timeout is zero. controller is the controller's orrery name, and hosts holds the agent of
// each hosts.toml host, which Start reaches by the host's name.
func newControlServer(
	engine enginev1connect.EngineClient,
	timeout time.Duration,
	home string,
	steps StepClient,
	controller string,
	hosts []*node.Client,
) controlServer {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	agents := make(map[string]*node.Client, len(hosts))
	for _, agent := range hosts {
		agents[agent.Name()] = agent
	}
	return controlServer{
		engine: engine, timeout: timeout, home: home, steps: steps, controller: controller, hosts: agents,
	}
}
