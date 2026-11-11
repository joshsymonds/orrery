package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Fixture values of the pane that sets every option GET /v1/panes reads. Each differs from the others, so a
// swapped field fails the parity test.
const (
	panesFixtureSession       = "full"
	panesFixtureChanged       = 1794301000
	panesFixtureSeen          = 1794301100
	panesFixtureSummary       = "synthetic summary"
	panesFixtureLabel         = "synthetic label"
	panesFixtureContext       = "synthetic context"
	panesFixtureClaudeSession = "synthetic-claude-session"
	panesFixtureTool          = "synthetic tool"
)

// restPane is one element of the GET /v1/panes array, read by the keys the route writes.
type restPane struct {
	ID            string `json:"id"`
	Host          string `json:"host"`
	Session       string `json:"session"`
	Window        int32  `json:"window"`
	Pane          int32  `json:"pane"`
	Cwd           string `json:"cwd"`
	Command       string `json:"command"`
	State         string `json:"state"`
	Summary       string `json:"summary"`
	Label         string `json:"label"`
	Changed       int64  `json:"changed"`
	Seen          int64  `json:"seen"`
	Question      string `json:"question"`
	Context       string `json:"context"`
	Activity      int64  `json:"activity"`
	Tool          string `json:"tool"`
	ClaudeSession string `json:"claude_session"`
	QuestionHash  string `json:"question_hash"`
	NewestReply   int64  `json:"newest_reply"`
}

// message returns the Pane that holds the same nineteen fields as p.
func (p restPane) message() *orreryv1.Pane {
	return &orreryv1.Pane{
		Id:            p.ID,
		Host:          p.Host,
		Session:       p.Session,
		Window:        p.Window,
		Pane:          p.Pane,
		Cwd:           p.Cwd,
		Command:       p.Command,
		State:         p.State,
		Summary:       p.Summary,
		Label:         p.Label,
		Changed:       p.Changed,
		Seen:          p.Seen,
		Question:      p.Question,
		Context:       p.Context,
		Activity:      p.Activity,
		Tool:          p.Tool,
		ClaudeSession: p.ClaudeSession,
		QuestionHash:  p.QuestionHash,
		NewestReply:   p.NewestReply,
	}
}

// panesClient serves handler on an httptest server and returns a generated Panes client whose requests carry
// allowedLogin and the client header.
func panesClient(t *testing.T, handler http.Handler) orreryv1connect.PanesClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewPanesClient(httpClient, srv.URL)
}

// panesAgree reports whether rest and got hold the same panes in order.
func panesAgree(rest []restPane, got []*orreryv1.Pane) bool {
	if len(rest) != len(got) {
		return false
	}
	for i := range rest {
		if !proto.Equal(rest[i].message(), got[i]) {
			return false
		}
	}
	return true
}

// listBothUntilAgreed reads GET /v1/panes and Panes.List through handler until both answers hold the same panes.
// A pane's activity and command change while its shell starts, so the two reads can differ until it settles.
func listBothUntilAgreed(t *testing.T, handler http.Handler) ([]restPane, []*orreryv1.Pane) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), screenWait)
	defer cancel()
	ticker := time.NewTicker(screenPoll)
	defer ticker.Stop()
	client := panesClient(t, handler)
	for {
		rec := get(t, handler, "/v1/panes", allowedLogin)
		assertStatus(t, rec, http.StatusOK)
		assertJSON(t, rec)
		var rest []restPane
		if err := json.Unmarshal(rec.Body.Bytes(), &rest); err != nil {
			t.Fatalf("decode GET /v1/panes: %v", err)
		}
		got, err := client.List(ctx, connect.NewRequest(&orreryv1.PanesListRequest{}))
		if err != nil {
			t.Fatalf("Panes.List: %v", err)
		}
		if panesAgree(rest, got.Msg.GetPanes()) {
			return rest, got.Msg.GetPanes()
		}
		select {
		case <-ctx.Done():
			t.Fatalf("GET /v1/panes = %v never agreed with Panes.List = %v", rest, got.Msg.GetPanes())
		case <-ticker.C:
		}
	}
}

func TestPanesListParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, panesFixtureSession, longCommand)
	startSession(t, tmux, "plain", longCommand)
	full := paneID(t, tmux, panesFixtureSession)
	options := map[string]string{
		protocol.OptionState:         string(protocol.StateWaiting),
		protocol.OptionChanged:       "1794301000",
		protocol.OptionSeen:          "1794301100",
		protocol.OptionQuestion:      synthQuestion,
		protocol.OptionSummary:       panesFixtureSummary,
		protocol.OptionLabel:         panesFixtureLabel,
		protocol.OptionContext:       panesFixtureContext,
		protocol.OptionClaudeSession: panesFixtureClaudeSession,
		protocol.OptionTool:          panesFixtureTool,
		protocol.OptionTranscript:    writeNewestTranscript(t, newestReplyLine(newestStamp0900)),
	}
	for name, value := range options {
		if err := tmux.SetPaneOption(t.Context(), full, name, value); err != nil {
			t.Fatalf("SetPaneOption(%s, %s): %v", full, name, err)
		}
	}

	rest, got := listBothUntilAgreed(t, newHandler(t, tmux))
	if len(rest) != 2 || len(got) != 2 {
		t.Fatalf("GET /v1/panes has %d panes and Panes.List %d, want 2 each", len(rest), len(got))
	}
	for _, pane := range rest {
		if pane.ID != full {
			continue
		}
		if pane.State != string(protocol.StateWaiting) || pane.Changed != panesFixtureChanged ||
			pane.Seen != panesFixtureSeen || pane.Question != synthQuestion || pane.Summary != panesFixtureSummary ||
			pane.Label != panesFixtureLabel || pane.Context != panesFixtureContext ||
			pane.ClaudeSession != panesFixtureClaudeSession || pane.Tool != panesFixtureTool ||
			pane.QuestionHash != protocol.QuestionHash(synthQuestion) || pane.NewestReply != int64(newestUnix0900) {
			t.Fatalf("pane %s fixture fields = %+v, want the options set on it", full, pane)
		}
		return
	}
	t.Fatalf("pane %s is not in GET /v1/panes %+v", full, rest)
}

func TestPanesListErrorParity(t *testing.T) {
	t.Parallel()

	// A socket inside a directory no one may enter makes tmux fail with a permission error, which is neither a
	// missing server nor a success, so cli.List fails and both routes answer their internal error.
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore %s: %v", dir, err)
		}
	})
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	tmux, err := tmuxctl.New(tmuxctl.Server{Socket: filepath.Join(dir, "tmux.sock")})
	if err != nil {
		t.Fatalf("New(Socket): %v", err)
	}
	handler := newHandler(t, tmux)

	rec := get(t, handler, "/v1/panes", allowedLogin)
	assertStatus(t, rec, http.StatusInternalServerError)
	wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

	_, err = panesClient(t, handler).List(t.Context(), connect.NewRequest(&orreryv1.PanesListRequest{}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Panes.List error = %v, want a *connect.Error", err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Fatalf("Panes.List code = %v, want %v", connectErr.Code(), connect.CodeInternal)
	}
	if connectErr.Message() != wantMessage {
		t.Fatalf("Panes.List message = %q, want the REST body %q", connectErr.Message(), wantMessage)
	}
}

// screenParityCommand prints historyCount numbered lines, then the synthetic color marker in red, then keeps the
// pane alive. The marker is the last line on the visible screen, so ansi=1 changes what the screen shows, and the
// first numbered line is in scrollback, so history=N changes it too.
const screenParityCommand = `i=1; while [ $i -le 60 ]; do echo "synthetic history line $i"; i=$((i+1)); done; ` +
	`printf '\033[31m` + synthColor + `\033[0m\n'; ` + longCommand

func TestPanesScreenParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, panesFixtureSession, screenParityCommand)
	pane := paneID(t, tmux, panesFixtureSession)
	handler := newHandler(t, tmux)
	path := "/v1/panes/" + url.PathEscape(pane) + "/screen"
	pollScreen(t, handler, path, synthColor)
	client := panesClient(t, handler)
	count := strconv.Itoa(historyCount)

	cases := []struct {
		name     string
		query    string
		req      *orreryv1.ScreenRequest
		contains []string
	}{
		{"plain", "", &orreryv1.ScreenRequest{Pane: pane}, []string{synthColor}},
		{"ansi", "ansi=1", &orreryv1.ScreenRequest{Pane: pane, Ansi: true}, []string{synthRed + synthColor}},
		{"history", "history=" + count, &orreryv1.ScreenRequest{Pane: pane, History: historyCount},
			[]string{historyLine(1), synthColor}},
		{"ansi and history", "ansi=1&history=" + count,
			&orreryv1.ScreenRequest{Pane: pane, Ansi: true, History: historyCount},
			[]string{historyLine(1), synthRed + synthColor}},
		{"history zero", "history=0", &orreryv1.ScreenRequest{Pane: pane, History: 0}, []string{synthColor}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			target := path
			if c.query != "" {
				target += "?" + c.query
			}
			rec := get(t, handler, target, allowedLogin)
			assertStatus(t, rec, http.StatusOK)
			for _, want := range c.contains {
				if !strings.Contains(rec.Body.String(), want) {
					t.Fatalf("REST body = %q, want it to contain %q", rec.Body.String(), want)
				}
			}
			got, err := client.Screen(t.Context(), connect.NewRequest(c.req))
			if err != nil {
				t.Fatalf("Panes.Screen: %v", err)
			}
			if !bytes.Equal(got.Msg.GetScreen(), rec.Body.Bytes()) {
				t.Fatalf("Panes.Screen = %q, want the REST body %q", got.Msg.GetScreen(), rec.Body.String())
			}
		})
	}
}

func TestPanesScreenErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, panesFixtureSession, longCommand)
	pane := paneID(t, tmux, panesFixtureSession)
	handler := newHandler(t, tmux)
	client := panesClient(t, handler)

	cases := []struct {
		name   string
		id     string
		query  string
		req    *orreryv1.ScreenRequest
		status int
		code   connect.Code
	}{
		{"malformed pane id", "abc", "", &orreryv1.ScreenRequest{Pane: "abc"},
			http.StatusBadRequest, connect.CodeInvalidArgument},
		{"absent pane", "%999999", "", &orreryv1.ScreenRequest{Pane: "%999999"},
			http.StatusNotFound, connect.CodeNotFound},
		{"history above 2000", pane, "history=2001", &orreryv1.ScreenRequest{Pane: pane, History: 2001},
			http.StatusBadRequest, connect.CodeInvalidArgument},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			target := "/v1/panes/" + url.PathEscape(c.id) + "/screen"
			if c.query != "" {
				target += "?" + c.query
			}
			rec := get(t, handler, target, allowedLogin)
			assertStatus(t, rec, c.status)
			wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

			_, err := client.Screen(t.Context(), connect.NewRequest(c.req))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("Panes.Screen error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != c.code {
				t.Fatalf("Panes.Screen code = %v, want %v", connectErr.Code(), c.code)
			}
			if connectErr.Message() != wantMessage {
				t.Fatalf("Panes.Screen message = %q, want the REST body %q", connectErr.Message(), wantMessage)
			}
		})
	}
}

func TestPanesScreenInternalErrorParity(t *testing.T) {
	t.Parallel()

	// A socket inside a directory no one may enter makes tmux fail with a permission error, so checkPane fails and
	// both routes answer their internal error, as TestPanesListErrorParity arranges.
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore %s: %v", dir, err)
		}
	})
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod %s: %v", dir, err)
	}
	tmux, err := tmuxctl.New(tmuxctl.Server{Socket: filepath.Join(dir, "tmux.sock")})
	if err != nil {
		t.Fatalf("New(Socket): %v", err)
	}
	handler := newHandler(t, tmux)

	rec := get(t, handler, "/v1/panes/"+url.PathEscape("%1")+"/screen", allowedLogin)
	assertStatus(t, rec, http.StatusInternalServerError)
	wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

	_, err = panesClient(t, handler).Screen(t.Context(), connect.NewRequest(&orreryv1.ScreenRequest{Pane: "%1"}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Panes.Screen error = %v, want a *connect.Error", err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Fatalf("Panes.Screen code = %v, want %v", connectErr.Code(), connect.CodeInternal)
	}
	if connectErr.Message() != wantMessage {
		t.Fatalf("Panes.Screen message = %q, want the REST body %q", connectErr.Message(), wantMessage)
	}
}

// Fixture values of the conversation that the conversation parity tests read. Each differs from the others, so a
// swapped field fails the parity test.
const (
	conversationParityTool   = "Bash"
	conversationParityToolID = "synthetic-tool-use"
	conversationParityResult = "synthetic tool output"
	conversationParityReply  = "synthetic reply after the tool"
	synthCanceled            = "synthetic canceled message"
)

// restConversation is the GET /v1/panes/{id}/conversation body, read by the keys the route writes.
type restConversation struct {
	Origin  string           `json:"origin"`
	Items   []restItem       `json:"items"`
	Goal    string           `json:"goal"`
	Waiting []restWaitingMsg `json:"waiting"`
}

// restItem is one element of the items array of GET /v1/panes/{id}/conversation.
type restItem struct {
	Kind   string `json:"kind"`
	Time   int64  `json:"time"`
	Text   string `json:"text"`
	Tool   string `json:"tool"`
	Input  string `json:"input"`
	ID     string `json:"id"`
	Error  bool   `json:"error"`
	Notice string `json:"notice"`
}

// restWaitingMsg is one element of the waiting array of GET /v1/panes/{id}/conversation.
type restWaitingMsg struct {
	Text     string `json:"text"`
	Time     int64  `json:"time"`
	Canceled bool   `json:"canceled"`
}

// message returns the ConversationResponse that holds the same fields as c.
func (c restConversation) message() *orreryv1.ConversationResponse {
	items := make([]*orreryv1.ConversationItem, 0, len(c.Items))
	for _, item := range c.Items {
		items = append(items, &orreryv1.ConversationItem{
			Kind:   item.Kind,
			Time:   item.Time,
			Text:   item.Text,
			Tool:   item.Tool,
			Input:  item.Input,
			Id:     item.ID,
			Error:  item.Error,
			Notice: item.Notice,
		})
	}
	waiting := make([]*orreryv1.WaitingMessage, 0, len(c.Waiting))
	for _, msg := range c.Waiting {
		waiting = append(waiting, &orreryv1.WaitingMessage{Text: msg.Text, Time: msg.Time, Canceled: msg.Canceled})
	}
	return &orreryv1.ConversationResponse{Origin: c.Origin, Items: items, Goal: c.Goal, Waiting: waiting}
}

// writeParityConversation writes a synthetic transcript to a new file in t.TempDir and returns its path. It holds a
// prompt, a tool call with its result, a reply, a goal that is not met, a queued message and a canceled one. Each
// line has its own timestamp, so each item and waiting message has its own time.
func writeParityConversation(t *testing.T) string {
	t.Helper()
	at := func(second int) string { return fmt.Sprintf("2026-01-02T03:04:%02dZ", second) }
	text := strings.Join([]string{
		fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":%q}}`, at(1), synthOrigin),
		fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"tool_use","id":%q,`+
			`"name":%q,"input":{"command":"echo synthetic"}}]}}`, at(2), conversationParityToolID, conversationParityTool),
		fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"content":[{"type":"tool_result","tool_use_id":%q,`+
			`"content":%q,"is_error":true}]}}`, at(3), conversationParityToolID, conversationParityResult),
		fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"text","text":%q}]}}`,
			at(4), conversationParityReply),
	}, "\n") + "\n" +
		goalRecord(synthGoal, false) +
		queueLine("enqueue", synthQueued, at(5), "") +
		queueLine("enqueue", synthCanceled, at(6), "") +
		queueLine("remove", synthCanceled, at(7), "cleared_on_cancel")
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func TestPanesConversationParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane := conversationPane(t, tmux, liveOptions(writeParityConversation(t)))
	handler := newHandler(t, tmux)
	client := panesClient(t, handler)

	cases := []struct {
		name      string
		query     string
		req       *orreryv1.ConversationRequest
		wantItems int
	}{
		{"limit absent", "", &orreryv1.ConversationRequest{Pane: pane}, 4},
		{"limit explicit", "limit=3", &orreryv1.ConversationRequest{Pane: pane, Limit: 3}, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := get(t, handler, conversationPath(pane, c.query), allowedLogin)
			assertStatus(t, rec, http.StatusOK)
			assertJSON(t, rec)
			var rest restConversation
			if err := json.Unmarshal(rec.Body.Bytes(), &rest); err != nil {
				t.Fatalf("decode %q: %v", rec.Body.String(), err)
			}
			if len(rest.Items) != c.wantItems || rest.Goal != synthGoal || len(rest.Waiting) != 2 {
				t.Fatalf("REST answer = %+v, want %d items, goal %q and 2 waiting messages",
					rest, c.wantItems, synthGoal)
			}

			got, err := client.Conversation(t.Context(), connect.NewRequest(c.req))
			if err != nil {
				t.Fatalf("Panes.Conversation: %v", err)
			}
			if !proto.Equal(rest.message(), got.Msg) {
				t.Fatalf("Panes.Conversation = %v, want the REST answer %v", got.Msg, rest.message())
			}
		})
	}
}

func TestPanesConversationErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	path := writeParityConversation(t)
	sessions := map[string]map[string]string{
		"live":          liveOptions(path),
		"unlive":        {protocol.OptionTranscript: path},
		"untranscribed": {protocol.OptionState: string(protocol.StateWorking)},
	}
	ids := make(map[string]string, len(sessions))
	for name, options := range sessions {
		startSession(t, tmux, name, longCommand)
		ids[name] = paneID(t, tmux, name)
		for option, value := range options {
			if err := tmux.SetPaneOption(t.Context(), ids[name], option, value); err != nil {
				t.Fatalf("SetPaneOption(%s, %s): %v", ids[name], option, err)
			}
		}
	}
	handler := newHandler(t, tmux)
	client := panesClient(t, handler)

	cases := []struct {
		name   string
		id     string
		query  string
		req    *orreryv1.ConversationRequest
		status int
		code   connect.Code
	}{
		{"limit above 100", ids["live"], "limit=101", &orreryv1.ConversationRequest{Pane: ids["live"], Limit: 101},
			http.StatusBadRequest, connect.CodeInvalidArgument},
		{"malformed pane id", "abc", "", &orreryv1.ConversationRequest{Pane: "abc"},
			http.StatusBadRequest, connect.CodeInvalidArgument},
		{"absent pane", "%999999", "", &orreryv1.ConversationRequest{Pane: "%999999"},
			http.StatusNotFound, connect.CodeNotFound},
		{"no live session", ids["unlive"], "", &orreryv1.ConversationRequest{Pane: ids["unlive"]},
			http.StatusNotFound, connect.CodeNotFound},
		{"no transcript", ids["untranscribed"], "", &orreryv1.ConversationRequest{Pane: ids["untranscribed"]},
			http.StatusNotFound, connect.CodeNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := get(t, handler, conversationPath(c.id, c.query), allowedLogin)
			assertStatus(t, rec, c.status)
			wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

			_, err := client.Conversation(t.Context(), connect.NewRequest(c.req))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("Panes.Conversation error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != c.code {
				t.Fatalf("Panes.Conversation code = %v, want %v", connectErr.Code(), c.code)
			}
			if connectErr.Message() != wantMessage {
				t.Fatalf("Panes.Conversation message = %q, want the REST body %q", connectErr.Message(), wantMessage)
			}
		})
	}
}

func TestPanesSendTextParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	restPane, restFile := startInputPane(t, tmux, "rest", synthScreen)
	connectPane, connectFile := startInputPane(t, tmux, "connect", synthScreen)
	handler := newHandler(t, tmux)

	rec := postJSON(t, handler, paneRoute(restPane, "text"), jsonBody(t, map[string]any{"text": synthText}))
	assertStatus(t, rec, http.StatusNoContent)

	_, err := panesClient(t, handler).SendText(t.Context(),
		connect.NewRequest(&orreryv1.SendTextRequest{Pane: connectPane, Text: synthText}))
	if err != nil {
		t.Fatalf("Panes.SendText: %v", err)
	}

	want := "\x1b[200~" + synthText + "\x1b[201~\r"
	awaitInput(t, restFile, want)
	awaitInput(t, connectFile, want)
}

func TestPanesSendTextErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, panesFixtureSession, longCommand)
	handler := newHandler(t, tmux)
	client := panesClient(t, handler)

	cases := []struct {
		name   string
		id     string
		status int
		code   connect.Code
	}{
		{"malformed pane id", "abc", http.StatusBadRequest, connect.CodeInvalidArgument},
		{"absent pane", "%999999", http.StatusNotFound, connect.CodeNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rec := postJSON(t, handler, paneRoute(c.id, "text"), jsonBody(t, map[string]any{"text": synthText}))
			assertStatus(t, rec, c.status)
			wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

			_, err := client.SendText(t.Context(),
				connect.NewRequest(&orreryv1.SendTextRequest{Pane: c.id, Text: synthText}))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("Panes.SendText error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != c.code {
				t.Fatalf("Panes.SendText code = %v, want %v", connectErr.Code(), c.code)
			}
			if connectErr.Message() != wantMessage {
				t.Fatalf("Panes.SendText message = %q, want the REST body %q", connectErr.Message(), wantMessage)
			}
		})
	}
}

func TestPanesSendKeysParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	restPane, restFile := startInputPane(t, tmux, "rest", synthQuestion)
	connectPane, connectFile := startInputPane(t, tmux, "connect", synthQuestion)
	for _, pane := range []string{restPane, connectPane} {
		if err := tmux.SetPaneOption(t.Context(), pane, protocol.OptionQuestion, synthQuestion); err != nil {
			t.Fatalf("SetPaneOption: %v", err)
		}
	}
	handler := newHandler(t, tmux)
	client := panesClient(t, handler)

	// With expect_question absent, the key reaches each pane the same way.
	assertStatus(t, postJSON(t, handler, paneRoute(restPane, "keys"), `{"keys":["a"]}`), http.StatusNoContent)
	if _, err := client.SendKeys(t.Context(),
		connect.NewRequest(&orreryv1.SendKeysRequest{Pane: connectPane, Keys: []string{"a"}})); err != nil {
		t.Fatalf("Panes.SendKeys unguarded: %v", err)
	}

	// With a matching expect_question, the key reaches each pane the same way.
	hash := protocol.QuestionHash(synthQuestion)
	guarded := jsonBody(t, map[string]any{"keys": []string{"b"}, "expect_question": hash})
	assertStatus(t, postJSON(t, handler, paneRoute(restPane, "keys"), guarded), http.StatusNoContent)
	guardedRequest := &orreryv1.SendKeysRequest{Pane: connectPane, Keys: []string{"b"}, ExpectQuestion: &hash}
	if _, err := client.SendKeys(t.Context(), connect.NewRequest(guardedRequest)); err != nil {
		t.Fatalf("Panes.SendKeys guarded: %v", err)
	}

	awaitInput(t, restFile, "ab")
	awaitInput(t, connectFile, "ab")
}

func TestPanesSendKeysErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "s1", synthScreen)
	if err := tmux.SetPaneOption(t.Context(), pane, protocol.OptionQuestion, synthQuestion); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}
	handler := newHandler(t, tmux)
	client := panesClient(t, handler)
	mismatch := protocol.QuestionHash("a different question")

	cases := []struct {
		name   string
		id     string
		keys   []string
		expect *string
		status int
		code   connect.Code
	}{
		{"malformed pane id", "abc", []string{"x"}, nil, http.StatusBadRequest, connect.CodeInvalidArgument},
		{"absent pane", "%999999", []string{"x"}, nil, http.StatusNotFound, connect.CodeNotFound},
		{"empty key list", pane, []string{}, nil, http.StatusBadRequest, connect.CodeInvalidArgument},
		{"mismatched expect_question", pane, []string{"x"}, &mismatch, http.StatusConflict, connect.CodeAborted},
	}
	for _, c := range cases {
		fields := map[string]any{"keys": c.keys}
		if c.expect != nil {
			fields["expect_question"] = *c.expect
		}
		rec := postJSON(t, handler, paneRoute(c.id, "keys"), jsonBody(t, fields))
		assertStatus(t, rec, c.status)
		wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

		_, err := client.SendKeys(t.Context(),
			connect.NewRequest(&orreryv1.SendKeysRequest{Pane: c.id, Keys: c.keys, ExpectQuestion: c.expect}))
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("%s: Panes.SendKeys error = %v, want a *connect.Error", c.name, err)
		}
		if connectErr.Code() != c.code {
			t.Fatalf("%s: Panes.SendKeys code = %v, want %v", c.name, connectErr.Code(), c.code)
		}
		if connectErr.Message() != wantMessage {
			t.Fatalf("%s: Panes.SendKeys message = %q, want the REST body %q",
				c.name, connectErr.Message(), wantMessage)
		}
	}

	// Nothing from the refused sends reached the pane, so the next delivery is exactly its own key.
	assertStatus(t, postJSON(t, handler, paneRoute(pane, "keys"), `{"keys":["ok"]}`), http.StatusNoContent)
	awaitInput(t, file, "ok")
}

func TestPanesMarkSeenParity(t *testing.T) {
	t.Parallel()

	const (
		reported = int64(1700000100)
		earlier  = int64(1700000050)
		later    = int64(1700000200)
	)
	tmux := newTmuxServer(t)
	// Two pane pairs: the absent read_up_to case needs panes whose time is unset, and the reported-time cases need
	// their own, since setting now would hold the stored time above every reported time.
	restNow, _ := startInputPane(t, tmux, "rest-now", synthScreen)
	connectNow, _ := startInputPane(t, tmux, "connect-now", synthScreen)
	restPane, _ := startInputPane(t, tmux, "rest", synthScreen)
	connectPane, _ := startInputPane(t, tmux, "connect", synthScreen)
	handler := newHandler(t, tmux)
	client := panesClient(t, handler)

	// Absent read_up_to is now on both answers. Each stored time lands within the bounds TestServePaneSeen uses.
	before := time.Now().Unix()
	assertStatus(t, postJSON(t, handler, paneRoute(restNow, "seen"), "{}"), http.StatusNoContent)
	if _, err := client.MarkSeen(t.Context(),
		connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: connectNow})); err != nil {
		t.Fatalf("Panes.MarkSeen absent read_up_to: %v", err)
	}
	after := time.Now().Unix()
	for _, pane := range []string{restNow, connectNow} {
		if seen := seenOption(t, tmux, pane); seen < before || seen > after {
			t.Fatalf("absent read_up_to on %s: %s = %d, want a time between %d and %d",
				pane, protocol.OptionSeen, seen, before, after)
		}
	}

	// A reported time is stored when unset, an earlier one leaves the stored time, and a later one is stored.
	steps := []struct {
		name     string
		readUpTo int64
		want     int64
	}{
		{"unset takes the reported time", reported, reported},
		{"earlier time leaves the stored time", earlier, reported},
		{"later time is stored", later, later},
	}
	for _, step := range steps {
		readUpTo := step.readUpTo
		postReadUpTo(t, handler, paneRoute(restPane, "seen"), readUpTo)
		if _, err := client.MarkSeen(t.Context(),
			connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: connectPane, ReadUpTo: &readUpTo})); err != nil {
			t.Fatalf("%s: Panes.MarkSeen: %v", step.name, err)
		}
		if got := seenOption(t, tmux, restPane); got != step.want {
			t.Fatalf("%s: REST %s = %d, want %d", step.name, protocol.OptionSeen, got, step.want)
		}
		if got := seenOption(t, tmux, connectPane); got != step.want {
			t.Fatalf("%s: Panes.MarkSeen %s = %d, want %d", step.name, protocol.OptionSeen, got, step.want)
		}
	}
}

func TestPanesMarkSeenErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, _ := startInputPane(t, tmux, "s1", synthScreen)
	handler := newHandler(t, tmux)
	client := panesClient(t, handler)
	negative := int64(-1)

	cases := []struct {
		name     string
		id       string
		body     string
		readUpTo *int64
		status   int
		code     connect.Code
	}{
		{"malformed pane id", "abc", "{}", nil, http.StatusBadRequest, connect.CodeInvalidArgument},
		{"absent pane", "%999999", "{}", nil, http.StatusNotFound, connect.CodeNotFound},
		{"negative read_up_to", pane, jsonBody(t, map[string]any{"read_up_to": negative}), &negative,
			http.StatusBadRequest, connect.CodeInvalidArgument},
	}
	for _, c := range cases {
		rec := postJSON(t, handler, paneRoute(c.id, "seen"), c.body)
		assertStatus(t, rec, c.status)
		wantMessage := strings.TrimSuffix(rec.Body.String(), "\n")

		_, err := client.MarkSeen(t.Context(),
			connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: c.id, ReadUpTo: c.readUpTo}))
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("%s: Panes.MarkSeen error = %v, want a *connect.Error", c.name, err)
		}
		if connectErr.Code() != c.code {
			t.Fatalf("%s: Panes.MarkSeen code = %v, want %v", c.name, connectErr.Code(), c.code)
		}
		if connectErr.Message() != wantMessage {
			t.Fatalf("%s: Panes.MarkSeen message = %q, want the REST body %q",
				c.name, connectErr.Message(), wantMessage)
		}
	}
}
