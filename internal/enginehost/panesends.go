package enginehost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// PaneSendRecorder is the store the PaneSends service records each send in. *store.Store satisfies it.
type PaneSendRecorder interface {
	RecordPaneSend(ctx context.Context, pane, text, client string, at time.Time) error
}

// paneSendService implements the PaneSends RPCs over sends, stamping each send with now. A nil sends refuses every RPC
// as unavailable.
type paneSendService struct {
	sends PaneSendRecorder
	now   func() time.Time
}

// Record stores the text the request's client sent pane, stamped with now. A pane that is not a % then digits, or an
// empty text or client, is refused as InvalidArgument and nothing is stored.
func (s paneSendService) Record(
	ctx context.Context,
	req *connect.Request[v1.PaneSendsRecordRequest],
) (*connect.Response[v1.PaneSendsRecordResponse], error) {
	if s.sends == nil {
		return nil, paneSendsUnavailable()
	}
	pane, text, client := req.Msg.GetPane(), req.Msg.GetText(), req.Msg.GetClient()
	if err := checkPaneSend(pane, text, client); err != nil {
		return nil, err
	}
	if err := s.sends.RecordPaneSend(ctx, pane, text, client, s.now()); err != nil {
		return nil, errors.Join(connect.NewError(connect.CodeInternal, err))
	}
	return connect.NewResponse(&v1.PaneSendsRecordResponse{}), nil
}

// checkPaneSend returns the refusal for a pane that is not a % followed by digits, or a text or client that is empty.
func checkPaneSend(pane, text, client string) error {
	switch {
	case !validPaneID(pane):
		return invalidPaneSend(fmt.Errorf("pane %q is not a %% followed by digits", pane))
	case text == "":
		return invalidPaneSend(errors.New("the text is empty"))
	case client == "":
		return invalidPaneSend(errors.New("the client is empty"))
	}
	return nil
}

// invalidPaneSend returns the InvalidArgument refusal of a pane send with the reason err.
func invalidPaneSend(err error) error {
	return errors.Join(connect.NewError(connect.CodeInvalidArgument, err))
}

// validPaneID reports whether id is % followed by one or more decimal digits.
func validPaneID(id string) bool {
	digits, found := strings.CutPrefix(id, "%")
	if !found || digits == "" {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// paneSendsUnavailable returns the refusal every PaneSends RPC gets when there is no store to record sends in.
func paneSendsUnavailable() error {
	return errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(unavailableMessage)))
}
