package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/conversation"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// panesServer answers orrery.v1.Panes with the tmux client, conversation state, locks, seen and read reporters, and
// clock it holds.
type panesServer struct {
	tmux                  *tmuxctl.Client
	newest                *conversation.Newest
	goals                 *conversation.Goals
	queues                *conversation.Queues
	textLocks             *paneLocks
	seenLocks             *paneLocks
	report                cli.SeenReporter
	readReport            ReadReporter
	now                   func() time.Time
	conversationPoll      time.Duration
	conversationKeepalive time.Duration
	sendTimeout           time.Duration
	shutdown              <-chan struct{}
}

// newPanesHandler returns the path and handler that serve Panes over the tmux client, conversation state, locks,
// seen and read reporters, and clock it is given. Screen, Conversation and WatchConversation report each read to
// readReport, dated by now. WatchConversation polls every conversationPoll and sends a keepalive after
// conversationKeepalive without a delta. Its sends are bounded by sendTimeout, and its streams end when shutdown
// closes. The handler runs under withResponseWriter, so the stream reaches its connection.
func newPanesHandler(
	tmux *tmuxctl.Client,
	newest *conversation.Newest,
	goals *conversation.Goals,
	queues *conversation.Queues,
	textLocks, seenLocks *paneLocks,
	report cli.SeenReporter,
	readReport ReadReporter,
	now func() time.Time,
	conversationPoll, conversationKeepalive, sendTimeout time.Duration,
	shutdown <-chan struct{},
) (string, http.Handler) {
	path, handler := orreryv1connect.NewPanesHandler(panesServer{
		tmux:                  tmux,
		newest:                newest,
		goals:                 goals,
		queues:                queues,
		textLocks:             textLocks,
		seenLocks:             seenLocks,
		report:                report,
		readReport:            readReport,
		now:                   now,
		conversationPoll:      conversationPoll,
		conversationKeepalive: conversationKeepalive,
		sendTimeout:           sendTimeout,
		shutdown:              shutdown,
	})
	return path, withResponseWriter(handler)
}

// Keys of a pane object that are read by constant rather than struct tag, since their snake case is not the
// camel case the tag linter asks for. claudeSessionKey is the key protocol.Pane writes for ClaudeSession.
const claudeSessionKey = "claude_session"

// paneJSON is one element of the array listPanes returns, decoded by the keys its tags name. The keys
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

// List answers Panes.List with the panes listPanes gives.
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

// Screen answers Panes.Screen with the screen of the pane. The request fields are encoded as the query screenFlags
// reads, so screenFlags validates them and builds the capture arguments.
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
		return nil, connectError(routeError{status: http.StatusBadRequest, message: badQueryMessage})
	}
	pane, err := checkPane(ctx, s.tmux, req.Msg.GetPane())
	if err != nil {
		return nil, connectError(paneRouteError(err))
	}
	screen, err := s.tmux.Run(ctx, append(flags, "-t", pane.ID)...)
	if err != nil {
		return nil, connectError(internalError())
	}
	s.reportRead(pane.ID)
	return connect.NewResponse(&v1.ScreenResponse{Screen: []byte(screen)}), nil
}

// Conversation answers Panes.Conversation with the conversation conversationAnswer builds. The limit field is
// encoded as the query conversationLimit reads, so conversationLimit validates it. Limit 0 is absent from that query,
// which is the default.
func (s panesServer) Conversation(
	ctx context.Context,
	req *connect.Request[v1.ConversationRequest],
) (*connect.Response[v1.ConversationResponse], error) {
	query := url.Values{}
	if req.Msg.GetLimit() != 0 {
		query.Set(queryLimit, strconv.FormatUint(uint64(req.Msg.GetLimit()), 10))
	}
	limit, ok := conversationLimit(query)
	if !ok {
		return nil, connectError(routeError{status: http.StatusBadRequest, message: badQueryMessage})
	}
	conv, err := conversationAnswer(ctx, s.tmux, s.goals, s.queues, req.Msg.GetPane(), limit)
	if err != nil {
		return nil, connectError(err)
	}
	s.reportRead(req.Msg.GetPane())
	return connect.NewResponse(conversationMessage(conv)), nil
}

// reportRead tells the read reporter that the phone read pane at the clock's time, or does nothing when there is no
// reporter.
func (s panesServer) reportRead(pane string) {
	if s.readReport != nil {
		s.readReport(pane, s.now().Unix())
	}
}

// SendText answers Panes.SendText. The pane is checked first, and the text is sent by sendText under the pane's text
// lock.
func (s panesServer) SendText(
	ctx context.Context,
	req *connect.Request[v1.SendTextRequest],
) (*connect.Response[v1.SendTextResponse], error) {
	if _, err := checkPane(ctx, s.tmux, req.Msg.GetPane()); err != nil {
		return nil, connectError(paneRouteError(err))
	}
	if err := sendText(ctx, s.tmux, s.textLocks, req.Msg.GetPane(), req.Msg.GetText()); err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&v1.SendTextResponse{}), nil
}

// SendKeys answers Panes.SendKeys. The pane is checked first, and the keys are sent by sendKeys, which carries the
// expect_question guard.
func (s panesServer) SendKeys(
	ctx context.Context,
	req *connect.Request[v1.SendKeysRequest],
) (*connect.Response[v1.SendKeysResponse], error) {
	if _, err := checkPane(ctx, s.tmux, req.Msg.GetPane()); err != nil {
		return nil, connectError(paneRouteError(err))
	}
	if err := sendKeys(ctx, s.tmux, req.Msg.GetPane(), req.Msg.GetKeys(), req.Msg.ExpectQuestion); err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&v1.SendKeysResponse{}), nil
}

// MarkSeen answers Panes.MarkSeen. The pane is checked first, and the seen time is set by markPaneSeen under the
// pane's seen lock.
func (s panesServer) MarkSeen(
	ctx context.Context,
	req *connect.Request[v1.MarkSeenRequest],
) (*connect.Response[v1.MarkSeenResponse], error) {
	if _, err := checkPane(ctx, s.tmux, req.Msg.GetPane()); err != nil {
		return nil, connectError(paneRouteError(err))
	}
	if err := markPaneSeen(ctx, s.tmux, s.seenLocks, s.report, req.Msg.GetPane(), req.Msg.ReadUpTo); err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&v1.MarkSeenResponse{}), nil
}

// conversationMessage returns the ConversationResponse of conv.
func conversationMessage(conv conversation.Conversation) *v1.ConversationResponse {
	figure := &v1.ConversationContext{Tokens: conv.Context.Tokens, Window: conv.Context.Window}
	return &v1.ConversationResponse{
		Origin:  conv.Origin,
		Items:   conversationItems(conv.Items),
		Goal:    conv.Goal,
		Waiting: waitingMessages(conv.Waiting),
		Context: figure,
	}
}

// conversationItems returns the ConversationItem message of each of items, in order.
func conversationItems(items []conversation.Item) []*v1.ConversationItem {
	out := make([]*v1.ConversationItem, 0, len(items))
	for _, item := range items {
		out = append(out, &v1.ConversationItem{
			Kind:    item.Kind,
			Time:    item.Time,
			Text:    item.Text,
			Tool:    item.Tool,
			Input:   item.Input,
			Id:      item.ID,
			Error:   item.Error,
			Notice:  item.Notice,
			Trigger: item.Trigger,
			Tokens:  item.Tokens,
		})
	}
	return out
}

// waitingMessages returns the WaitingMessage message of each of waiting, in order.
func waitingMessages(waiting []conversation.Waiting) []*v1.WaitingMessage {
	out := make([]*v1.WaitingMessage, 0, len(waiting))
	for _, msg := range waiting {
		out = append(out, &v1.WaitingMessage{Text: msg.Text, Time: msg.Time, Canceled: msg.Canceled})
	}
	return out
}

// paneMessages returns the Pane messages of the panes listPanes gives, in order. It reads the same JSON objects
// listPanes gives, so the two answers hold the same fields.
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

// paneExtras returns the claude_session, question_hash and newest_reply of one pane object of listPanes, which
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
