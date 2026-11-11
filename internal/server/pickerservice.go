package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/open"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// pickerServer answers orrery.v1.Picker with the configuration, token store, tmux client and clock the REST picker
// routes share.
type pickerServer struct {
	picker func() (open.Config, error)
	tokens *pickerTokens
	tmux   *tmuxctl.Client
	now    func() time.Time
}

// newPickerHandler returns the path and handler that serve Picker over the instances New shares with the REST picker
// routes.
func newPickerHandler(
	picker func() (open.Config, error),
	tokens *pickerTokens,
	tmux *tmuxctl.Client,
	now func() time.Time,
) (string, http.Handler) {
	return orreryv1connect.NewPickerHandler(pickerServer{picker: picker, tokens: tokens, tmux: tmux, now: now})
}

// Ask answers Picker.Ask with the rows POST /v1/picker/ask answers.
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

// Run answers Picker.Run with the pane POST /v1/picker/run answers.
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

// pickerRunError returns the Connect error of a Picker.Run failure. Its 409, no tmux server, is failed_precondition
// as the table of docs/contract.md gives it; connectError answers every 409 as aborted, which is SendKeys' code.
func pickerRunError(err error) *connect.Error {
	var route routeError
	if errors.As(err, &route) && route.status == http.StatusConflict {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New(route.message))
	}
	return connectError(err)
}
