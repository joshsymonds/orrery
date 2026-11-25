package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/hostload"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// proposeNotFoundMessage and proposeBadRequestMessage are the messages Picker.Propose answers a nil picker and blank
// text with, the messages Picker.Ask answers them with too.
const (
	proposeNotFoundMessage   = "404 page not found"
	proposeBadRequestMessage = "bad request"
)

// pickerServer answers orrery.v1.Picker with the configuration, token store, tmux client and clock. Propose ranks its
// proposals over the controller, named name and read through localLoad, and each of hosts that answers.
type pickerServer struct {
	picker    func() (open.Config, error)
	tokens    *pickerTokens
	tmux      *tmuxctl.Client
	now       func() time.Time
	name      string
	hosts     []*node.Client
	localLoad func() (node.Load, error)
}

// newPickerHandler returns the path and handler that serve Picker over the configuration, token store, tmux client
// and clock it is given. The controller is named name and its load is read through localLoad (hostload.Local when
// nil), and hosts are the agents of the other hosts a proposal may place a session on.
func newPickerHandler(
	picker func() (open.Config, error),
	tokens *pickerTokens,
	tmux *tmuxctl.Client,
	now func() time.Time,
	name string,
	hosts []*node.Client,
	localLoad func() (node.Load, error),
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if localLoad == nil {
		localLoad = hostload.Local
	}
	answers := pickerServer{
		picker: picker, tokens: tokens, tmux: tmux, now: now, name: name, hosts: hosts, localLoad: localLoad,
	}
	return orreryv1connect.NewPickerHandler(answers, opts...)
}

// Ask answers Picker.Ask with the rows askPicker returns.
func (s pickerServer) Ask(
	ctx context.Context,
	req *connect.Request[v1.AskRequest],
) (*connect.Response[v1.AskResponse], error) {
	answer, err := askPicker(ctx, s.picker, s.tokens, s.tmux, s.now, req.Msg.GetText())
	if err != nil {
		return nil, connectError(err)
	}
	rows := make([]*v1.PickerRow, 0, len(answer.Rows))
	for _, row := range answer.Rows {
		rows = append(rows, &v1.PickerRow{
			Token:  row.Token,
			Kind:   row.Kind,
			Dir:    row.Dir,
			Pane:   row.Pane,
			Label:  row.Label,
			Note:   row.Note,
			Prompt: row.Prompt,
		})
	}
	return connect.NewResponse(&v1.AskResponse{Rows: rows}), nil
}

// Run answers Picker.Run with the pane runPicker returns.
func (s pickerServer) Run(
	ctx context.Context,
	req *connect.Request[v1.RunRequest],
) (*connect.Response[v1.RunResponse], error) {
	pane, err := runPicker(ctx, s.tokens, s.tmux, s.now, req.Msg.GetToken())
	if err != nil {
		return nil, pickerRunError(err)
	}
	return connect.NewResponse(&v1.RunResponse{Pane: pane}), nil
}

// Propose answers Picker.Propose with the proposals propose returns, best first, each with its host and directory.
func (s pickerServer) Propose(
	ctx context.Context,
	req *connect.Request[v1.ProposeRequest],
) (*connect.Response[v1.ProposeResponse], error) {
	proposals, err := s.propose(ctx, req.Msg.GetText())
	if err != nil {
		return nil, connectError(err)
	}
	answer := make([]*v1.StartProposal, 0, len(proposals))
	for _, proposal := range proposals {
		answer = append(answer, &v1.StartProposal{Host: proposal.Host, Dir: proposal.Dir})
	}
	return connect.NewResponse(&v1.ProposeResponse{Proposals: answer}), nil
}

// propose returns the proposals open.Propose ranks for text over the hosts that choices reads. A nil picker is a 404,
// blank text a 400, and a failure of the configuration, the choices or the model a 502 with its reason.
func (s pickerServer) propose(ctx context.Context, text string) ([]open.Proposal, error) {
	if s.picker == nil {
		return nil, routeError{status: http.StatusNotFound, message: proposeNotFoundMessage}
	}
	if strings.TrimSpace(text) == "" {
		return nil, routeError{status: http.StatusBadRequest, message: proposeBadRequestMessage}
	}
	cfg, err := s.picker()
	if err != nil {
		return nil, routeError{status: http.StatusBadGateway, message: err.Error()}
	}
	choices, err := s.choices(ctx, cfg)
	if err != nil {
		return nil, routeError{status: http.StatusBadGateway, message: err.Error()}
	}
	proposals, err := open.Propose(ctx, cfg.Claude, text, choices)
	if err != nil {
		return nil, routeError{status: http.StatusBadGateway, message: err.Error()}
	}
	return proposals, nil
}

// choices returns the hosts a proposal may place a session on: the controller first, with its folders from cfg and its
// load, then each of s.hosts that answers both its load and its folders, in order. The controller always counts, so a
// folder or load of the controller that cannot be read fails the call, while a host where either fails is left out.
func (s pickerServer) choices(ctx context.Context, cfg open.Config) ([]open.HostChoice, error) {
	folders, err := open.KnownFolders(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("server: known folders: %w", err)
	}
	load, err := s.localLoad()
	if err != nil {
		return nil, err
	}
	reached := make([]*open.HostChoice, len(s.hosts))
	var wg sync.WaitGroup
	for index, client := range s.hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reached[index] = reachedChoice(ctx, client)
		}()
	}
	wg.Wait()
	choices := []open.HostChoice{{
		Name: s.name, LoadPercent: load.LoadPercent, MemoryPercent: load.MemoryPercent, Folders: folders,
	}}
	for _, choice := range reached {
		if choice != nil {
			choices = append(choices, *choice)
		}
	}
	return choices, nil
}

// reachedChoice returns the choice of client's host, from the Load and the Folders its agent answers, which are asked at
// once within the client's bound. It returns nil when either fails, so the host is unreachable.
func reachedChoice(ctx context.Context, client *node.Client) *open.HostChoice {
	var (
		load    node.Load
		loadErr error
		folders []string
		dirErr  error
	)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		load, loadErr = client.Load(ctx)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		folders, dirErr = client.Folders(ctx)
	}()
	wg.Wait()
	if loadErr != nil || dirErr != nil {
		return nil
	}
	return &open.HostChoice{
		Name: client.Name(), LoadPercent: load.LoadPercent, MemoryPercent: load.MemoryPercent, Folders: folders,
	}
}

// pickerRunError returns the Connect error of a Picker.Run failure. Its 409, no tmux server, is failed_precondition
// as the table of docs/contract.md gives it; connectError answers every 409 as aborted, which is SendKeys' code.
func pickerRunError(err error) *connect.Error {
	var route routeError
	if errors.As(err, &route) && route.status == http.StatusConflict {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New(route.message))
	}
	return connectError(err)
}
