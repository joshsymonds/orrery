package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/conversation"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// panesServer answers orrery.v1.Panes with the tmux client, conversation state, locks, seen and read reporters, and
// clock it holds, the store reader its turn ends come from, and the change feed and keepalive timers its
// WatchConversation streams run on. It also holds the PaneSends client each SendText records its send through, and the
// writer its failed records are written to.
type panesServer struct {
	tmux                  *tmuxctl.Client
	reader                *store.Reader
	newest                *conversation.Newest
	goals                 *conversation.Goals
	queues                *conversation.Queues
	textLocks             *paneLocks
	seenLocks             *paneLocks
	report                cli.SeenReporter
	readReport            ReadReporter
	paneSends             enginev1connect.PaneSendsClient
	paneSendErrs          io.Writer
	now                   func() time.Time
	changes               *changeHub
	newTimer              func(time.Duration) (<-chan time.Time, func() bool)
	conversationKeepalive time.Duration
	sendTimeout           time.Duration
	shutdown              <-chan struct{}
	// stepped, when set, is called with the pane of each WatchConversation step before the step reads the pane. A test
	// sets it to count the reads of its streams. It is nil in production.
	stepped func(pane string)
}

// newPanesHandler returns the path and handler that serve Panes over the tmux client, the store reader of turn ends,
// conversation state, locks, seen and read reporters, and clock it is given. Screen, Conversation and
// WatchConversation report each read to readReport, dated by now. SendText records each send through paneSends, and
// writes a failed record to paneSendErrs. WatchConversation steps when changes wakes it, and sends a keepalive after
// conversationKeepalive without a message. Its keepalive timers are armed through newTimer, or through time.NewTimer
// when newTimer is nil. Its sends are bounded by sendTimeout, and its streams end when shutdown closes. The handler
// runs under withResponseWriter, so the stream reaches its connection.
func newPanesHandler(
	tmux *tmuxctl.Client,
	reader *store.Reader,
	newest *conversation.Newest,
	goals *conversation.Goals,
	queues *conversation.Queues,
	textLocks, seenLocks *paneLocks,
	report cli.SeenReporter,
	readReport ReadReporter,
	paneSends enginev1connect.PaneSendsClient,
	paneSendErrs io.Writer,
	now func() time.Time,
	changes *changeHub,
	newTimer func(time.Duration) (<-chan time.Time, func() bool),
	conversationKeepalive, sendTimeout time.Duration,
	shutdown <-chan struct{},
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if newTimer == nil {
		newTimer = stdTimer
	}
	path, handler := orreryv1connect.NewPanesHandler(panesServer{
		tmux:                  tmux,
		reader:                reader,
		newest:                newest,
		goals:                 goals,
		queues:                queues,
		textLocks:             textLocks,
		seenLocks:             seenLocks,
		report:                report,
		readReport:            readReport,
		paneSends:             paneSends,
		paneSendErrs:          paneSendErrs,
		now:                   now,
		changes:               changes,
		newTimer:              newTimer,
		conversationKeepalive: conversationKeepalive,
		sendTimeout:           sendTimeout,
		shutdown:              shutdown,
	}, opts...)
	return path, withResponseWriter(handler)
}

// Keys of a pane object that are read by constant rather than struct tag, since their snake case is not the
// camel case the tag linter asks for. claudeSessionKey is the key protocol.Pane writes for ClaudeSession.
const claudeSessionKey = "claude_session"

// paneJSON is one pane object of a listing, decoded by the keys its tags name. The keys
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
	panes, err := listPanes(ctx, s.tmux, s.newest, s.reader)
	if err != nil {
		return nil, connectError(err)
	}
	out, err := paneMessages(paneObjects(panes))
	if err != nil {
		return nil, connectError(err)
	}
	if markErr := markPanePutOff(ctx, s.reader, out); markErr != nil {
		return nil, markErr
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
	pane := req.Msg.GetPane()
	sends, earlier := s.echoSends(ctx, pane), s.echoEarlier(ctx, pane)
	if markErr := markPaneEchoes(conv.Items, conv.Oldest, sends, earlier); markErr != nil {
		return nil, connectError(markErr)
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

// SendText answers Panes.SendText. The pane is checked first, then the send is recorded, and the text is sent by
// sendText under the pane's text lock.
func (s panesServer) SendText(
	ctx context.Context,
	req *connect.Request[v1.SendTextRequest],
) (*connect.Response[v1.SendTextResponse], error) {
	if _, err := checkPane(ctx, s.tmux, req.Msg.GetPane()); err != nil {
		return nil, connectError(paneRouteError(err))
	}
	s.recordSend(ctx, req.Msg.GetPane(), req.Msg.GetText(), req.Header().Get(HeaderClient))
	if err := sendText(ctx, s.tmux, s.textLocks, req.Msg.GetPane(), req.Msg.GetText()); err != nil {
		return nil, connectError(err)
	}
	return connect.NewResponse(&v1.SendTextResponse{}), nil
}

// recordSend records the text client sent pane through the PaneSends client, before the text is typed, so the echo's
// transcript time is never before the send's. With no client it records nothing. A failed record is written to
// paneSendErrs as one line, and never stops the send.
func (s panesServer) recordSend(ctx context.Context, pane, text, client string) {
	if s.paneSends == nil {
		return
	}
	_, err := s.paneSends.Record(ctx, connect.NewRequest(&enginev1.PaneSendsRecordRequest{
		Pane: pane, Text: text, Client: client,
	}))
	if err != nil {
		fmt.Fprintf(s.paneSendErrs, "orrery serve: record pane send: %v\n", err)
	}
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
			Kind:     item.Kind,
			Time:     item.Time,
			Text:     item.Text,
			Tool:     item.Tool,
			Input:    item.Input,
			Id:       item.ID,
			Error:    item.Error,
			Notice:   item.Notice,
			Trigger:  item.Trigger,
			Tokens:   item.Tokens,
			Epic:     item.Epic,
			Contract: item.Contract,
			Added:    item.Added,
			Removed:  item.Removed,
			Client:   item.Client,
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
		claudeSession, questionHash, newestReply, lastTurnEnd, orreryID, err := paneExtras(fields)
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
			LastTurnEnd:   lastTurnEnd,
			OrreryId:      orreryID,
		})
	}
	return out, nil
}

// paneExtras returns the claude_session, question_hash, newest_reply, last_turn_end and orrery_id of one pane object of
// a listing, which paneJSON does not tag.
func paneExtras(fields map[string]json.RawMessage) (string, string, int64, int64, string, error) {
	var claudeSession, questionHash, orreryID string
	var newestReply, lastTurnEnd int64
	if err := paneField(fields, claudeSessionKey, &claudeSession); err != nil {
		return "", "", 0, 0, "", err
	}
	if err := paneField(fields, questionHashKey, &questionHash); err != nil {
		return "", "", 0, 0, "", err
	}
	if err := paneField(fields, newestReplyKey, &newestReply); err != nil {
		return "", "", 0, 0, "", err
	}
	if err := paneField(fields, lastTurnEndKey, &lastTurnEnd); err != nil {
		return "", "", 0, 0, "", err
	}
	if err := paneField(fields, orreryIDKey, &orreryID); err != nil {
		return "", "", 0, 0, "", err
	}
	return claudeSession, questionHash, newestReply, lastTurnEnd, orreryID, nil
}

// paneField decodes the value of key in fields into dst. A value that does not decode is an error.
func paneField(fields map[string]json.RawMessage, key string, dst any) error {
	if err := json.Unmarshal(fields[key], dst); err != nil {
		return fmt.Errorf("server: decode pane %s: %w", key, err)
	}
	return nil
}

// sendsFunc reads the sends to a pane between the unix seconds from and to, oldest first (see markPaneEchoes).
type sendsFunc func(from, to int64) ([]conversation.Send, error)

// earlierFunc reads the prompt items of a transcript before a line start, back to the unix second since (see
// markPaneEchoes).
type earlierFunc func(before, since int64) ([]conversation.Item, error)

// echoSends returns the sends reader of the echo marking of pane. It reads the store reader's sends to pane through
// ctx, or is nil when the server has no store reader.
func (s panesServer) echoSends(ctx context.Context, pane string) sendsFunc {
	if s.reader == nil {
		return nil
	}
	return func(from, to int64) ([]conversation.Send, error) {
		rows, err := s.reader.PaneSends(ctx, pane, from, to)
		if err != nil {
			return nil, fmt.Errorf("read pane sends: %w", err)
		}
		sends := make([]conversation.Send, 0, len(rows))
		for _, row := range rows {
			sends = append(sends, conversation.Send{Text: row.Text, Client: row.Client, At: row.At})
		}
		return sends, nil
	}
}

// echoEarlier returns the earlier reader of the echo marking of pane. It reads the prompts of the pane's live
// transcript before a line start, resolving the transcript through ctx only when it reads.
func (s panesServer) echoEarlier(ctx context.Context, pane string) earlierFunc {
	return func(before, since int64) ([]conversation.Item, error) {
		path, err := s.paneTranscript(ctx, pane)
		if err != nil {
			return nil, err
		}
		return earlierIn(path)(before, since)
	}
}

// noTranscriptMessage is the message of the 404 a pane with no transcript is answered with.
const noTranscriptMessage = "no transcript"

// paneTranscript returns the transcript path in the @orrery_transcript option of pane, as conversationAnswer reads it.
// It fails as conversationAnswer does when the option cannot be read or names no transcript.
func (s panesServer) paneTranscript(ctx context.Context, pane string) (string, error) {
	path, ok, err := s.tmux.PaneOption(ctx, pane, protocol.OptionTranscript)
	if err != nil {
		return "", internalError()
	}
	if !ok || path == "" {
		return "", routeError{status: http.StatusNotFound, message: noTranscriptMessage}
	}
	return path, nil
}

// earlierIn returns the earlier reader of the transcript at path: its prompt items before a line start, back to the
// unix second since (see conversation.EarlierPrompts).
func earlierIn(path string) earlierFunc {
	return func(before, since int64) ([]conversation.Item, error) {
		prompts, err := conversation.EarlierPrompts(path, before, since)
		if err != nil {
			return nil, transcriptError(err)
		}
		return prompts, nil
	}
}

// markPaneEchoes sets the client of each prompt of items that a pane send echoes, as conversation.MarkEchoes sets it.
// items are a read's items, oldest first, and start is the line start in their transcript that they begin at. The sends
// are read for the times from the oldest item less conversation.EchoBound to the newest item. When the earliest of them
// is before the oldest item, the prompts before start are read back to its time through earlier, so an echo before the
// read takes its send. A read with no prompt reads nothing, and so does a nil sends or a store read that is
// unavailable: those mark nothing and are no error.
func markPaneEchoes(items []conversation.Item, start int64, sends sendsFunc, earlier earlierFunc) error {
	hasPrompt := slices.ContainsFunc(items, func(item conversation.Item) bool { return item.Kind == promptKind })
	if sends == nil || !hasPrompt {
		return nil
	}
	oldest, newest := items[0].Time, items[len(items)-1].Time
	found, err := sends(oldest-conversation.EchoBound, newest)
	switch {
	case errors.Is(err, store.ErrUnavailable):
		return nil
	case err != nil:
		return internalError()
	case len(found) == 0:
		return nil
	}
	var prompts []conversation.Item
	if found[0].At < oldest {
		if prompts, err = earlier(start, found[0].At); err != nil {
			return err
		}
	}
	conversation.MarkEchoes(items, prompts, found)
	return nil
}
