package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/conversation"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// panesServer answers orrery.v1.Panes with the instances the REST pane routes share. The embedded Unimplemented
// handler answers every method until its method lands.
type panesServer struct {
	orreryv1connect.UnimplementedPanesHandler
	tmux      *tmuxctl.Client
	newest    *conversation.Newest
	goals     *conversation.Goals
	queues    *conversation.Queues
	textLocks *paneLocks
	seenLocks *paneLocks
}

// newPanesHandler returns the path and handler that serve Panes over the instances New shares with the REST pane routes.
func newPanesHandler(
	tmux *tmuxctl.Client,
	newest *conversation.Newest,
	goals *conversation.Goals,
	queues *conversation.Queues,
	textLocks, seenLocks *paneLocks,
) (string, http.Handler) {
	return orreryv1connect.NewPanesHandler(panesServer{
		tmux:      tmux,
		newest:    newest,
		goals:     goals,
		queues:    queues,
		textLocks: textLocks,
		seenLocks: seenLocks,
	})
}

// Keys of GET /v1/panes that are read by constant rather than struct tag, since their snake case is not the
// camel case the tag linter asks for. claudeSessionKey is the key protocol.Pane writes for ClaudeSession.
const claudeSessionKey = "claude_session"

// paneJSON is one element of the GET /v1/panes array, decoded by the keys its tags name. The keys
// claudeSessionKey, questionHashKey and newestReplyKey are read from the same object by paneField.
type paneJSON struct {
	ID       string `json:"id"`
	Host     string `json:"host"`
	Session  string `json:"session"`
	Window   int32  `json:"window"`
	Pane     int32  `json:"pane"`
	Cwd      string `json:"cwd"`
	Command  string `json:"command"`
	State    string `json:"state"`
	Summary  string `json:"summary"`
	Label    string `json:"label"`
	Changed  int64  `json:"changed"`
	Seen     int64  `json:"seen"`
	Question string `json:"question"`
	Context  string `json:"context"`
	Activity int64  `json:"activity"`
	Tool     string `json:"tool"`
}

// List answers Panes.List with the panes GET /v1/panes answers.
func (s panesServer) List(
	ctx context.Context,
	_ *connect.Request[v1.PanesListRequest],
) (*connect.Response[v1.PanesListResponse], error) {
	panes, err := listPanes(ctx, s.tmux, s.newest)
	if err != nil {
		return nil, connectError(err)
	}
	out, err := paneMessages(panes)
	if err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&v1.PanesListResponse{Panes: out}), nil
}

// Screen answers Panes.Screen with the screen GET /v1/panes/{id}/screen answers. The request fields are encoded as
// the route's query, so screenFlags validates them and builds the capture arguments exactly as the route does.
func (s panesServer) Screen(
	ctx context.Context,
	req *connect.Request[v1.ScreenRequest],
) (*connect.Response[v1.ScreenResponse], error) {
	query := url.Values{queryHistory: {strconv.FormatUint(uint64(req.Msg.GetHistory()), 10)}}
	if req.Msg.GetAnsi() {
		query.Set(queryANSI, "1")
	}
	flags, ok := screenFlags(query)
	if !ok {
		return nil, connectError(routeError{status: http.StatusBadRequest, message: "bad request query"})
	}
	pane, err := checkPane(ctx, s.tmux, req.Msg.GetPane())
	if err != nil {
		return nil, connectError(paneRouteError(err))
	}
	screen, err := s.tmux.Run(ctx, append(flags, "-t", pane.ID)...)
	if err != nil {
		return nil, connectError(internalError())
	}
	return connect.NewResponse(&v1.ScreenResponse{Screen: []byte(screen)}), nil
}

// paneMessages returns the Pane messages of the panes listPanes gives, in order. It reads the same JSON objects
// GET /v1/panes writes, so the two answers hold the same fields.
func paneMessages(panes []map[string]json.RawMessage) ([]*v1.Pane, error) {
	out := make([]*v1.Pane, 0, len(panes))
	for _, fields := range panes {
		data, err := json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("server: encode pane: %w", err)
		}
		var pane paneJSON
		if err = json.Unmarshal(data, &pane); err != nil {
			return nil, fmt.Errorf("server: decode pane: %w", err)
		}
		claudeSession, questionHash, newestReply, err := paneExtras(fields)
		if err != nil {
			return nil, err
		}
		out = append(out, &v1.Pane{
			Id:            pane.ID,
			Host:          pane.Host,
			Session:       pane.Session,
			Window:        pane.Window,
			Pane:          pane.Pane,
			Cwd:           pane.Cwd,
			Command:       pane.Command,
			State:         pane.State,
			Summary:       pane.Summary,
			Label:         pane.Label,
			Changed:       pane.Changed,
			Seen:          pane.Seen,
			Question:      pane.Question,
			Context:       pane.Context,
			Activity:      pane.Activity,
			Tool:          pane.Tool,
			ClaudeSession: claudeSession,
			QuestionHash:  questionHash,
			NewestReply:   newestReply,
		})
	}
	return out, nil
}

// paneExtras returns the claude_session, question_hash and newest_reply of one pane of GET /v1/panes, which
// paneJSON does not tag.
func paneExtras(fields map[string]json.RawMessage) (string, string, int64, error) {
	var claudeSession, questionHash string
	var newestReply int64
	if err := paneField(fields, claudeSessionKey, &claudeSession); err != nil {
		return "", "", 0, err
	}
	if err := paneField(fields, questionHashKey, &questionHash); err != nil {
		return "", "", 0, err
	}
	if err := paneField(fields, newestReplyKey, &newestReply); err != nil {
		return "", "", 0, err
	}
	return claudeSession, questionHash, newestReply, nil
}

// paneField decodes the value of key in fields into dst. A value that does not decode is an error.
func paneField(fields map[string]json.RawMessage, key string, dst any) error {
	if err := json.Unmarshal(fields[key], dst); err != nil {
		return fmt.Errorf("server: decode pane %s: %w", key, err)
	}
	return nil
}
