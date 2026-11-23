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
	OwnerInterrupt(ctx context.Context, id string) ([]string, error)
	Decline(ctx context.Context, id, requestID string) ([]string, error)
	DeclinePane(ctx context.Context, pane, questionHash string) error
	Stop(ctx context.Context, id string) error
	Seen(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
	Withdraw(ctx context.Context, id, promptID string) ([]string, error)
	NotNow(ctx context.Context, req PutOffRequest) error
	BringBack(ctx context.Context, req PutOffRequest) error
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

// ServeConfig is what ServeWith serves. Each field is one of the services Serve takes, and the nil rules are
// Serve's. Options apply to each handler.
type ServeConfig struct {
	Commands     Commands
	Steps        StepCommands
	Epics        EpicCommands
	Steering     SteeringSessions
	Pools        PoolCommands
	Evidence     EvidenceCommands
	Notices      NoticeSource
	Crons        CronCommands
	Changes      *ChangeFeed
	Contracts    *Contracts
	ReleaseSlots ReleaseSlotCommands
	PaneSends    PaneSendRecorder
	Options      []connect.HandlerOption
}

// Serve answers the engine service on ln with commands, the steps service with steps, the epics service with epics and
// steering, the pools service with pools, the evidence service with evidence, the notices service with notices, the
// crons service with crons, the changes service with changes and the contracts service with contracts until ctx ends.
// It then shuts down within shutdownWait and returns nil. A nil commands refuses every engine RPC as unavailable, a nil
// steps refuses every step RPC as unavailable, a nil epics refuses every epic RPC as unavailable, a nil steering
// refuses as unavailable each epic create that needs a steering session started, a nil pools refuses every Acquire as
// unavailable, a nil evidence refuses every evidence RPC as unavailable, a nil notices refuses every notices RPC as
// unavailable, a nil crons refuses every cron call as unavailable, a nil changes refuses every Watch as unavailable,
// and a nil contracts refuses every Contracts RPC it serves as unavailable. The crons service answers each call for the
// session in the Orrery-Session header, and refuses a call with no such header as InvalidArgument. With steps served,
// the engine refuses Say, Answer, Interrupt, Stop and Resume on a step id, Decline on a headless step id, and NotNow
// and BringBack on a headless step id, since a step is driven only through the Steps service. opts apply to each of the
// ten handlers. Serve serves no release slots: it is ServeWith with ReleaseSlots nil, so the ReleaseSlots service is
// served and refuses every RPC as unavailable. Likewise it serves no pane sends, so the PaneSends service refuses every
// RPC as unavailable.
func Serve(
	ctx context.Context,
	ln net.Listener,
	commands Commands,
	steps StepCommands,
	epics EpicCommands,
	steering SteeringSessions,
	pools PoolCommands,
	evidence EvidenceCommands,
	notices NoticeSource,
	crons CronCommands,
	changes *ChangeFeed,
	contracts *Contracts,
	opts ...connect.HandlerOption,
) error {
	return ServeWith(ctx, ln, ServeConfig{
		Commands:  commands,
		Steps:     steps,
		Epics:     epics,
		Steering:  steering,
		Pools:     pools,
		Evidence:  evidence,
		Notices:   notices,
		Crons:     crons,
		Changes:   changes,
		Contracts: contracts,
		Options:   opts,
	})
}

// ServeWith answers the services that cfg describes on ln until ctx ends, as Serve does, and also answers the release
// slots service with cfg.ReleaseSlots and the PaneSends service with cfg.PaneSends. It then shuts down within
// shutdownWait and returns nil. A nil cfg.ReleaseSlots refuses every ReleaseSlots RPC as unavailable, and a nil
// cfg.PaneSends refuses every PaneSends RPC as unavailable. The other fields keep Serve's nil rules, and cfg.Options
// apply to each handler.
func ServeWith(ctx context.Context, ln net.Listener, cfg ServeConfig) error {
	opts := cfg.Options
	path, handler := enginev1connect.NewEngineHandler(&service{commands: cfg.Commands, steps: cfg.Steps}, opts...)
	stepsPath, stepsHandler := enginev1connect.NewStepsHandler(
		&stepService{steps: cfg.Steps},
		opts...)
	epicsPath, epicsHandler := enginev1connect.NewEpicsHandler(
		&epicService{epics: cfg.Epics, steps: cfg.Steps, steering: cfg.Steering}, opts...)
	poolsPath, poolsHandler := enginev1connect.NewPoolsHandler(&poolService{pools: cfg.Pools}, opts...)
	evidencePath, evidenceHandler := enginev1connect.NewEvidenceHandler(
		&evidenceService{evidence: cfg.Evidence}, opts...)
	noticesPath, noticesHandler := enginev1connect.NewNoticesHandler(&noticesService{notices: cfg.Notices}, opts...)
	cronsPath, cronsHandler := enginev1connect.NewCronsHandler(cronService{crons: cfg.Crons}, opts...)
	changesPath, changesHandler := NewChangesHandler(cfg.Changes, opts...)
	contractsPath, contractsHandler := enginev1connect.NewContractsHandler(
		&contractService{contracts: cfg.Contracts}, opts...)
	releaseSlotsPath, releaseSlotsHandler := enginev1connect.NewReleaseSlotsHandler(
		releaseSlotService{slots: cfg.ReleaseSlots}, opts...)
	paneSendsPath, paneSendsHandler := enginev1connect.NewPaneSendsHandler(
		paneSendService{sends: cfg.PaneSends, now: time.Now}, opts...)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	mux.Handle(stepsPath, stepsHandler)
	mux.Handle(epicsPath, epicsHandler)
	mux.Handle(poolsPath, poolsHandler)
	mux.Handle(evidencePath, evidenceHandler)
	mux.Handle(noticesPath, noticesHandler)
	mux.Handle(cronsPath, cronsHandler)
	mux.Handle(changesPath, changesHandler)
	mux.Handle(contractsPath, contractsHandler)
	mux.Handle(releaseSlotsPath, releaseSlotsHandler)
	mux.Handle(paneSendsPath, paneSendsHandler)
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
		Client: req.Msg.GetClient(),
		Host:   req.Msg.GetHost(),
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
		Client:   req.Msg.GetClient(),
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
	texts, err := s.commands.OwnerInterrupt(ctx, req.Msg.GetId())
	if err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.InterruptResponse{Texts: texts}), nil
}

// Decline routes by kind. A pane is declined by its question_hash with no step check, since a step is never a pane, and
// answers no texts. A headless call, or one with an empty kind, is refused on a step id and otherwise declined by its
// request_id. Any other kind is an InvalidArgument.
func (s *service) Decline(
	ctx context.Context,
	req *connect.Request[v1.DeclineRequest],
) (*connect.Response[v1.DeclineResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	switch kind := req.Msg.GetKind(); kind {
	case store.PutOffPane:
		if err := s.commands.DeclinePane(ctx, req.Msg.GetId(), req.Msg.GetQuestionHash()); err != nil {
			return nil, commandError(err)
		}
		return connect.NewResponse(&v1.DeclineResponse{}), nil
	case "", store.PutOffHeadless:
		if err := s.refuseStep(ctx, req.Msg.GetId()); err != nil {
			return nil, err
		}
		texts, err := s.commands.Decline(ctx, req.Msg.GetId(), req.Msg.GetRequestId())
		if err != nil {
			return nil, commandError(err)
		}
		return connect.NewResponse(&v1.DeclineResponse{Texts: texts}), nil
	default:
		return nil, errors.Join(connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("decline kind %q is neither %s nor %s", kind, store.PutOffHeadless, store.PutOffPane),
		))
	}
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

func (s *service) Resume(
	ctx context.Context,
	req *connect.Request[v1.ResumeRequest],
) (*connect.Response[v1.ResumeResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	if err := s.commands.Resume(ctx, req.Msg.GetId()); err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.ResumeResponse{}), nil
}

func (s *service) Withdraw(
	ctx context.Context,
	req *connect.Request[v1.WithdrawRequest],
) (*connect.Response[v1.WithdrawResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.refuseStep(ctx, req.Msg.GetId()); err != nil {
		return nil, err
	}
	texts, err := s.commands.Withdraw(ctx, req.Msg.GetId(), req.Msg.GetPromptId())
	if err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.WithdrawResponse{Texts: texts}), nil
}

func (s *service) NotNow(
	ctx context.Context,
	req *connect.Request[v1.NotNowRequest],
) (*connect.Response[v1.NotNowResponse], error) {
	put := PutOffRequest{
		Kind:         req.Msg.GetKind(),
		ID:           req.Msg.GetId(),
		RequestID:    req.Msg.GetRequestId(),
		QuestionHash: req.Msg.GetQuestionHash(),
		Changed:      req.Msg.GetChanged(),
	}
	if err := s.checkPutOff(ctx, put); err != nil {
		return nil, err
	}
	if err := s.commands.NotNow(ctx, put); err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.NotNowResponse{}), nil
}

func (s *service) BringBack(
	ctx context.Context,
	req *connect.Request[v1.BringBackRequest],
) (*connect.Response[v1.BringBackResponse], error) {
	put := PutOffRequest{
		Kind:         req.Msg.GetKind(),
		ID:           req.Msg.GetId(),
		RequestID:    req.Msg.GetRequestId(),
		QuestionHash: req.Msg.GetQuestionHash(),
		Changed:      req.Msg.GetChanged(),
	}
	if err := s.checkPutOff(ctx, put); err != nil {
		return nil, err
	}
	if err := s.commands.BringBack(ctx, put); err != nil {
		return nil, commandError(err)
	}
	return connect.NewResponse(&v1.BringBackResponse{}), nil
}

// checkPutOff returns the refusal a NotNow or BringBack gets before the commands see it: the refusal ready gives, and for
// a headless call, the refusal refuseStep gives for its id. A pane call gets no step check.
func (s *service) checkPutOff(ctx context.Context, req PutOffRequest) error {
	if err := s.ready(); err != nil {
		return err
	}
	if req.Kind == store.PutOffHeadless {
		return s.refuseStep(ctx, req.ID)
	}
	return nil
}

// commandError maps a Commands error to its Connect code. The error's text is the message.
func commandError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, ErrNoSession), errors.Is(err, session.ErrNoPendingRequest):
		code = connect.CodeNotFound
	case errors.Is(err, ErrBadDir), errors.Is(err, ErrEmptyPrompt), errors.Is(err, ErrBadModel),
		errors.Is(err, ErrBadID), errors.Is(err, ErrBadAnswer), errors.Is(err, session.ErrUnverifiedAnswer),
		errors.Is(err, ErrBadPutOff), errors.Is(err, ErrUnknownHost):
		code = connect.CodeInvalidArgument
	case errors.Is(err, ErrNotResumable), errors.Is(err, ErrUnknownPrompt), errors.Is(err, ErrQuestionNotOpen),
		errors.Is(err, ErrPaneInMode):
		code = connect.CodeFailedPrecondition
	case errors.Is(err, ErrNoPane):
		code = connect.CodeNotFound
	case errors.Is(err, ErrPutOffUnavailable), errors.Is(err, ErrHostUnreachable):
		code = connect.CodeUnavailable
	}
	return errors.Join(connect.NewError(code, err))
}
