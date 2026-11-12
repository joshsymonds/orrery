package enginehost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/engine/session"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// shutdownWait bounds how long Serve waits for in-flight requests once its context ends.
const shutdownWait = 5 * time.Second

// readHeaderTimeout bounds how long a client may take to send its request headers.
const readHeaderTimeout = 5 * time.Second

// unavailableMessage is the message every RPC is refused with when there is no session store.
const unavailableMessage = "session store unavailable"

// Commands is the set of session operations the engine service answers with. Manager satisfies it.
type Commands interface {
	Start(ctx context.Context, req StartRequest) (string, error)
	Say(ctx context.Context, req SayRequest) error
	Answer(ctx context.Context, id, requestID, answer string) error
	Interrupt(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	Seen(ctx context.Context, id string) error
}

// Listen returns a listener on the engine socket at socketPath. daemon.Listen creates the socket directory and
// makes the socket file mode 0600 so only its owner can send commands.
func Listen(socketPath string) (net.Listener, error) {
	ln, err := daemon.Listen(socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen engine: %w", err)
	}
	return ln, nil
}

// Serve answers the engine service on ln with commands, the steps service with steps and the pools service with pools
// until ctx ends. It then shuts down within shutdownWait and returns nil. A nil commands refuses every engine RPC as
// unavailable, a nil steps refuses every step RPC as unavailable, and a nil pools refuses every Acquire as unavailable.
// With steps served, the engine refuses Say, Answer, Interrupt and Stop on a step id, since a step is driven only
// through the Steps service.
func Serve(ctx context.Context, ln net.Listener, commands Commands, steps StepCommands, pools PoolCommands) error {
	path, handler := enginev1connect.NewEngineHandler(&service{commands: commands, steps: steps})
	stepsPath, stepsHandler := enginev1connect.NewStepsHandler(&stepService{steps: steps})
	poolsPath, poolsHandler := enginev1connect.NewPoolsHandler(&poolService{pools: pools})
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	mux.Handle(stepsPath, stepsHandler)
	mux.Handle(poolsPath, poolsHandler)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		return fmt.Errorf("serve engine: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownWait)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// Shutdown ran out of time with requests still in flight. Close drops them.
		if closeErr := srv.Close(); closeErr != nil {
			return fmt.Errorf("close engine server: %w", closeErr)
		}
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve engine: %w", err)
	}
	return nil
}

// service implements the Engine RPCs over commands. steps, when set, names the step ids the engine refuses.
type service struct {
	commands Commands
	steps    StepCommands
}

// ready returns the refusal every RPC gets when there is no session store.
func (s *service) ready() error {
	if s.commands == nil {
		return errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(unavailableMessage)))
	}
	return nil
}

// refuseStep returns the refusal for a session command on id when id names a step. A step runs its own loop, and a raw
// session command on it would drive the step around that loop. It returns nil when id is not a step or there is no step
// store, and an internal error when the step lookup itself fails.
func (s *service) refuseStep(ctx context.Context, id string) error {
	if s.steps == nil {
		return nil
	}
	_, err := s.steps.Status(ctx, id)
	switch {
	case err == nil:
		return errors.Join(connect.NewError(
			connect.CodeFailedPrecondition,
			fmt.Errorf("%s is a step: drive it through the Steps service", id),
		))
	case errors.Is(err, store.ErrNoStep):
		return nil
	default:
		return errors.Join(connect.NewError(connect.CodeInternal, err))
	}
}

func (s *service) Start(
	ctx context.Context,
	req *connect.Request[v1.StartRequest],
) (*connect.Response[v1.StartResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	id, err := s.commands.Start(ctx, StartRequest{
		Dir:    req.Msg.GetDir(),
		Prompt: req.Msg.GetPrompt(),
		Model:  req.Msg.GetModel(),
		ID:     req.Msg.GetId(),
	})
	if err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.StartResponse{Id: id}), nil
}

func (s *service) Say(
	ctx context.Context,
	req *connect.Request[v1.SayRequest],
) (*connect.Response[v1.SayResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	if err := s.commands.Say(ctx, SayRequest{
		ID:       req.Msg.GetId(),
		Text:     req.Msg.GetText(),
		PromptID: req.Msg.GetPromptId(),
	}); err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.SayResponse{}), nil
}

func (s *service) Answer(
	ctx context.Context,
	req *connect.Request[v1.AnswerRequest],
) (*connect.Response[v1.AnswerResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	err := s.commands.Answer(ctx, req.Msg.GetId(), req.Msg.GetRequestId(), req.Msg.GetAnswer())
	if err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.AnswerResponse{}), nil
}

func (s *service) Interrupt(
	ctx context.Context,
	req *connect.Request[v1.InterruptRequest],
) (*connect.Response[v1.InterruptResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	if err := s.commands.Interrupt(ctx, req.Msg.GetId()); err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.InterruptResponse{}), nil
}

func (s *service) Stop(
	ctx context.Context,
	req *connect.Request[v1.StopRequest],
) (*connect.Response[v1.StopResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	if err := s.commands.Stop(ctx, req.Msg.GetId()); err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.StopResponse{}), nil
}

func (s *service) Seen(
	ctx context.Context,
	req *connect.Request[v1.SeenRequest],
) (*connect.Response[v1.SeenResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.commands.Seen(ctx, req.Msg.GetId()); err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.SeenResponse{}), nil
}

// commandError maps a Commands error to its Connect code. The error's text is the message.
func commandError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, ErrCapReached):
		code = connect.CodeResourceExhausted
	case errors.Is(err, ErrNoSession), errors.Is(err, session.ErrNoPendingRequest):
		code = connect.CodeNotFound
	case errors.Is(err, ErrBadDir), errors.Is(err, ErrEmptyPrompt), errors.Is(err, ErrBadModel),
		errors.Is(err, ErrBadID), errors.Is(err, ErrBadAnswer), errors.Is(err, session.ErrUnverifiedAnswer):
		code = connect.CodeInvalidArgument
	}
	return errors.Join(connect.NewError(code, err))
}
