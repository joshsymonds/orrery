package server_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// Fixture values of the pane that sets every option Panes.List reads. Each differs from the others, so a swapped
// field fails the test.
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

// panesClient serves handler on an httptest server and returns a generated Panes client whose requests carry
// allowedLogin and the client header.
func panesClient(t *testing.T, handler http.Handler) orreryv1connect.PanesClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewPanesClient(httpClient, srv.URL)
}

// listPanes returns the panes Panes.List answers through handler.
func listPanes(t *testing.T, handler http.Handler) []*orreryv1.Pane {
	t.Helper()
	got, err := panesClient(t, handler).List(t.Context(), connect.NewRequest(&orreryv1.PanesListRequest{}))
	if err != nil {
		t.Fatalf("Panes.List: %v", err)
	}
	return got.Msg.GetPanes()
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

	panes := listPanes(t, newHandler(t, tmux))
	if len(panes) != 2 {
		t.Fatalf("Panes.List has %d panes, want 2", len(panes))
	}
	for _, pane := range panes {
		if pane.GetId() != full {
			continue
		}
		if pane.GetSession() != panesFixtureSession || pane.GetState() != string(protocol.StateWaiting) ||
			pane.GetChanged() != panesFixtureChanged || pane.GetSeen() != panesFixtureSeen ||
			pane.GetQuestion() != synthQuestion || pane.GetSummary() != panesFixtureSummary ||
			pane.GetLabel() != panesFixtureLabel || pane.GetContext() != panesFixtureContext ||
			pane.GetClaudeSession() != panesFixtureClaudeSession || pane.GetTool() != panesFixtureTool ||
			pane.GetQuestionHash() != protocol.QuestionHash(synthQuestion) ||
			pane.GetNewestReply() != newestUnix0900 {
			t.Fatalf("pane %s fixture fields = %v, want the options set on it", full, pane)
		}
		return
	}
	t.Fatalf("pane %s is not in Panes.List %v", full, panes)
}

func TestPanesListErrorParity(t *testing.T) {
	t.Parallel()

	// A socket inside a directory no one may enter makes tmux fail with a permission error, which is neither a
	// missing server nor a success, so cli.List fails and Panes.List answers its internal error.
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

	_, err = panesClient(t, newHandler(t, tmux)).List(t.Context(), connect.NewRequest(&orreryv1.PanesListRequest{}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Panes.List error = %v, want a *connect.Error", err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Fatalf("Panes.List code = %v, want %v", connectErr.Code(), connect.CodeInternal)
	}
	if connectErr.Message() != "internal server error" {
		t.Fatalf("Panes.List message = %q, want %q", connectErr.Message(), "internal server error")
	}
}

// screenParityCommand prints historyCount numbered lines, then the synthetic color marker in red, then keeps the
// pane alive. The marker is the last line on the visible screen, so the ansi field changes what the screen shows, and
// the first numbered line is in scrollback, so the history field changes it too.
const screenParityCommand = `i=1; while [ $i -le 60 ]; do echo "synthetic history line $i"; i=$((i+1)); done; ` +
	`printf '\033[31m` + synthColor + `\033[0m\n'; ` + longCommand

func TestPanesScreenParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, panesFixtureSession, screenParityCommand)
	pane := paneID(t, tmux, panesFixtureSession)
	waitForScreen(t, tmux, pane, synthColor)
	client := panesClient(t, newHandler(t, tmux))

	cases := []struct {
		name     string
		req      *orreryv1.ScreenRequest
		contains []string
	}{
		{"plain", &orreryv1.ScreenRequest{Pane: pane}, []string{synthColor}},
		{"ansi", &orreryv1.ScreenRequest{Pane: pane, Ansi: true}, []string{synthRed + synthColor}},
		{"history", &orreryv1.ScreenRequest{Pane: pane, History: historyCount}, []string{historyLine(1), synthColor}},
		{"ansi and history", &orreryv1.ScreenRequest{Pane: pane, Ansi: true, History: historyCount},
			[]string{historyLine(1), synthRed + synthColor}},
		{"history zero", &orreryv1.ScreenRequest{Pane: pane, History: 0}, []string{synthColor}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := client.Screen(t.Context(), connect.NewRequest(c.req))
			if err != nil {
				t.Fatalf("Panes.Screen: %v", err)
			}
			for _, want := range c.contains {
				if !strings.Contains(string(got.Msg.GetScreen()), want) {
					t.Fatalf("Panes.Screen = %q, want it to contain %q", got.Msg.GetScreen(), want)
				}
			}
		})
	}
}

func TestPanesScreenErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, panesFixtureSession, longCommand)
	pane := paneID(t, tmux, panesFixtureSession)
	client := panesClient(t, newHandler(t, tmux))

	cases := []struct {
		name    string
		req     *orreryv1.ScreenRequest
		code    connect.Code
		message string
	}{
		{"malformed pane id", &orreryv1.ScreenRequest{Pane: "abc"}, connect.CodeInvalidArgument, "malformed pane id"},
		{"absent pane", &orreryv1.ScreenRequest{Pane: "%999999"}, connect.CodeNotFound, "pane not found"},
		{"history above 2000", &orreryv1.ScreenRequest{Pane: pane, History: 2001}, connect.CodeInvalidArgument,
			"bad request query"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := client.Screen(t.Context(), connect.NewRequest(c.req))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("Panes.Screen error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != c.code {
				t.Fatalf("Panes.Screen code = %v, want %v", connectErr.Code(), c.code)
			}
			if connectErr.Message() != c.message {
				t.Fatalf("Panes.Screen message = %q, want %q", connectErr.Message(), c.message)
			}
		})
	}
}

func TestPanesScreenInternalErrorParity(t *testing.T) {
	t.Parallel()

	// A socket inside a directory no one may enter makes tmux fail with a permission error, so checkPane fails and
	// Panes.Screen answers its internal error, as TestPanesListErrorParity arranges.
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

	_, err = panesClient(t, newHandler(t, tmux)).Screen(t.Context(),
		connect.NewRequest(&orreryv1.ScreenRequest{Pane: "%1"}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Panes.Screen error = %v, want a *connect.Error", err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Fatalf("Panes.Screen code = %v, want %v", connectErr.Code(), connect.CodeInternal)
	}
	if connectErr.Message() != "internal server error" {
		t.Fatalf("Panes.Screen message = %q, want %q", connectErr.Message(), "internal server error")
	}
}

// Fixture values of the conversation that the conversation parity tests read. Each differs from the others, so a
// swapped field fails the test.
const (
	conversationParityTool   = "Bash"
	conversationParityToolID = "synthetic-tool-use"
	conversationParityResult = "synthetic tool output"
	conversationParityReply  = "synthetic reply after the tool"
	conversationParityTrig   = "manual"
	conversationParitySum    = "synthetic compaction summary"
	conversationParityAfter  = "synthetic reply after compaction"
	synthCanceled            = "synthetic canceled message"
)

// parityItems returns the items of the conversation writeParityConversation writes, oldest first. Each time is the
// unix second of its line's stamp: 2026-01-02T03:04:01Z for the first line through 03:04:07Z for the last. A
// compaction takes the time of its summary line and the trigger and pre-compaction tokens of its boundary.
func parityItems() []*orreryv1.ConversationItem {
	return []*orreryv1.ConversationItem{
		{Kind: "prompt", Time: 1767323041, Text: synthOrigin},
		{Kind: "tool", Time: 1767323042, Tool: conversationParityTool, Input: "echo synthetic",
			Id: conversationParityToolID},
		{Kind: "result", Time: 1767323043, Text: conversationParityResult, Id: conversationParityToolID, Error: true},
		{Kind: "reply", Time: 1767323044, Text: conversationParityReply},
		{Kind: "compaction", Time: 1767323046, Text: conversationParitySum, Trigger: conversationParityTrig,
			Tokens: 91234},
		{Kind: "reply", Time: 1767323047, Text: conversationParityAfter},
	}
}

// parityConversation returns the conversation answer of writeParityConversation holding items: its opening prompt,
// its goal that is not met, its two waiting messages and the context its last usage line sets.
func parityConversation(items []*orreryv1.ConversationItem) *orreryv1.ConversationResponse {
	return &orreryv1.ConversationResponse{
		Origin: synthOrigin,
		Items:  items,
		Goal:   synthGoal,
		Waiting: []*orreryv1.WaitingMessage{
			{Text: synthQueued, Time: 1767323048},
			{Text: synthCanceled, Time: 1767323049, Canceled: true},
		},
		Context: &orreryv1.ConversationContext{Tokens: 4000, Window: 1000000},
	}
}

// writeParityConversation writes a synthetic transcript to a new file in t.TempDir and returns its path. It holds a
// prompt, a tool call with its result, a reply, a compaction with its boundary, a reply after the compaction whose
// usage sets the context figure, a goal that is not met, a queued message and a canceled one. Each line has its own
// timestamp, so each item and waiting message has its own time.
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
		fmt.Sprintf(`{"type":"system","subtype":"compact_boundary","timestamp":%q,"compactMetadata":`+
			`{"trigger":%q,"preTokens":91234,"postTokens":4321}}`, at(5), conversationParityTrig),
		fmt.Sprintf(`{"type":"user","timestamp":%q,"isCompactSummary":true,"message":{"role":"user","content":%q}}`,
			at(6), conversationParitySum),
		fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"text","text":%q}],`+
			`"usage":{"input_tokens":1500,"cache_creation_input_tokens":500,"cache_read_input_tokens":2000}}}`,
			at(7), conversationParityAfter),
	}, "\n") + "\n" +
		goalRecord(synthGoal, false) +
		queueLine("enqueue", synthQueued, at(8), "") +
		queueLine("enqueue", synthCanceled, at(9), "") +
		queueLine("remove", synthCanceled, at(10), "cleared_on_cancel")
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
	client := panesClient(t, newHandler(t, tmux))

	cases := []struct {
		name string
		req  *orreryv1.ConversationRequest
		want *orreryv1.ConversationResponse
	}{
		{"limit absent", &orreryv1.ConversationRequest{Pane: pane}, parityConversation(parityItems())},
		{"limit explicit", &orreryv1.ConversationRequest{Pane: pane, Limit: 3},
			parityConversation(parityItems()[3:])},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := client.Conversation(t.Context(), connect.NewRequest(c.req))
			if err != nil {
				t.Fatalf("Panes.Conversation: %v", err)
			}
			if !proto.Equal(got.Msg, c.want) {
				t.Fatalf("Panes.Conversation = %v, want %v", got.Msg, c.want)
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
	client := panesClient(t, newHandler(t, tmux))

	cases := []struct {
		name    string
		req     *orreryv1.ConversationRequest
		code    connect.Code
		message string
	}{
		{"limit above 100", &orreryv1.ConversationRequest{Pane: ids["live"], Limit: 101}, connect.CodeInvalidArgument,
			"bad request query"},
		{"malformed pane id", &orreryv1.ConversationRequest{Pane: "abc"}, connect.CodeInvalidArgument,
			"malformed pane id"},
		{"absent pane", &orreryv1.ConversationRequest{Pane: "%999999"}, connect.CodeNotFound, "pane not found"},
		{"no live session", &orreryv1.ConversationRequest{Pane: ids["unlive"]}, connect.CodeNotFound,
			"no live session"},
		{"no transcript", &orreryv1.ConversationRequest{Pane: ids["untranscribed"]}, connect.CodeNotFound,
			"no transcript"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := client.Conversation(t.Context(), connect.NewRequest(c.req))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("Panes.Conversation error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != c.code {
				t.Fatalf("Panes.Conversation code = %v, want %v", connectErr.Code(), c.code)
			}
			if connectErr.Message() != c.message {
				t.Fatalf("Panes.Conversation message = %q, want %q", connectErr.Message(), c.message)
			}
		})
	}
}

func TestPanesSendTextParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "connect", synthScreen)
	client := panesClient(t, newHandler(t, tmux))

	_, err := client.SendText(t.Context(), connect.NewRequest(&orreryv1.SendTextRequest{Pane: pane, Text: synthText}))
	if err != nil {
		t.Fatalf("Panes.SendText: %v", err)
	}

	want := "\x1b[200~" + synthText + "\x1b[201~\r"
	awaitInput(t, file, want)
}

func TestPanesSendTextErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	startSession(t, tmux, panesFixtureSession, longCommand)
	client := panesClient(t, newHandler(t, tmux))

	cases := []struct {
		name    string
		pane    string
		code    connect.Code
		message string
	}{
		{"malformed pane id", "abc", connect.CodeInvalidArgument, "malformed pane id"},
		{"absent pane", "%999999", connect.CodeNotFound, "pane not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := client.SendText(t.Context(),
				connect.NewRequest(&orreryv1.SendTextRequest{Pane: c.pane, Text: synthText}))
			var connectErr *connect.Error
			if !errors.As(err, &connectErr) {
				t.Fatalf("Panes.SendText error = %v, want a *connect.Error", err)
			}
			if connectErr.Code() != c.code {
				t.Fatalf("Panes.SendText code = %v, want %v", connectErr.Code(), c.code)
			}
			if connectErr.Message() != c.message {
				t.Fatalf("Panes.SendText message = %q, want %q", connectErr.Message(), c.message)
			}
		})
	}
}

func TestPanesSendKeysParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "connect", synthQuestion)
	if err := tmux.SetPaneOption(t.Context(), pane, protocol.OptionQuestion, synthQuestion); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}
	client := panesClient(t, newHandler(t, tmux))

	// With expect_question absent, the key reaches the pane.
	if _, err := client.SendKeys(t.Context(),
		connect.NewRequest(&orreryv1.SendKeysRequest{Pane: pane, Keys: []string{"a"}})); err != nil {
		t.Fatalf("Panes.SendKeys unguarded: %v", err)
	}

	// With a matching expect_question, the key reaches the pane.
	hash := protocol.QuestionHash(synthQuestion)
	guarded := &orreryv1.SendKeysRequest{Pane: pane, Keys: []string{"b"}, ExpectQuestion: &hash}
	if _, err := client.SendKeys(t.Context(), connect.NewRequest(guarded)); err != nil {
		t.Fatalf("Panes.SendKeys guarded: %v", err)
	}

	awaitInput(t, file, "ab")
}

func TestPanesSendKeysErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, file := startInputPane(t, tmux, "s1", synthScreen)
	if err := tmux.SetPaneOption(t.Context(), pane, protocol.OptionQuestion, synthQuestion); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}
	client := panesClient(t, newHandler(t, tmux))
	mismatch := protocol.QuestionHash("a different question")

	cases := []struct {
		name    string
		pane    string
		keys    []string
		expect  *string
		code    connect.Code
		message string
	}{
		{"malformed pane id", "abc", []string{"x"}, nil, connect.CodeInvalidArgument, "malformed pane id"},
		{"absent pane", "%999999", []string{"x"}, nil, connect.CodeNotFound, "pane not found"},
		{"empty key list", pane, []string{}, nil, connect.CodeInvalidArgument, "bad request body"},
		{"mismatched expect_question", pane, []string{"x"}, &mismatch, connect.CodeAborted, "question refused"},
	}
	for _, c := range cases {
		_, err := client.SendKeys(t.Context(),
			connect.NewRequest(&orreryv1.SendKeysRequest{Pane: c.pane, Keys: c.keys, ExpectQuestion: c.expect}))
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("%s: Panes.SendKeys error = %v, want a *connect.Error", c.name, err)
		}
		if connectErr.Code() != c.code {
			t.Fatalf("%s: Panes.SendKeys code = %v, want %v", c.name, connectErr.Code(), c.code)
		}
		if connectErr.Message() != c.message {
			t.Fatalf("%s: Panes.SendKeys message = %q, want %q", c.name, connectErr.Message(), c.message)
		}
	}

	// Nothing from the refused sends reached the pane, so the next delivery is exactly its own key.
	if _, err := client.SendKeys(t.Context(),
		connect.NewRequest(&orreryv1.SendKeysRequest{Pane: pane, Keys: []string{"ok"}})); err != nil {
		t.Fatalf("Panes.SendKeys after refusals: %v", err)
	}
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
	// The absent read_up_to case needs a pane whose time is unset, and the reported-time cases need their own pane,
	// since setting now would hold the stored time above every reported time.
	nowPane, _ := startInputPane(t, tmux, "now", synthScreen)
	pane, _ := startInputPane(t, tmux, "seen", synthScreen)
	client := panesClient(t, newHandler(t, tmux))

	// Absent read_up_to stores now. The stored time lands within the bounds TestServePaneSeen uses.
	before := time.Now().Unix()
	if _, err := client.MarkSeen(
		t.Context(),
		connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: nowPane}),
	); err != nil {
		t.Fatalf("Panes.MarkSeen absent read_up_to: %v", err)
	}
	after := time.Now().Unix()
	if seen := seenOption(t, tmux, nowPane); seen < before || seen > after {
		t.Fatalf("absent read_up_to on %s: %s = %d, want a time between %d and %d",
			nowPane, protocol.OptionSeen, seen, before, after)
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
		if _, err := client.MarkSeen(t.Context(),
			connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: pane, ReadUpTo: &readUpTo})); err != nil {
			t.Fatalf("%s: Panes.MarkSeen: %v", step.name, err)
		}
		if got := seenOption(t, tmux, pane); got != step.want {
			t.Fatalf("%s: Panes.MarkSeen %s = %d, want %d", step.name, protocol.OptionSeen, got, step.want)
		}
	}
}

func TestPanesMarkSeenErrorParity(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	pane, _ := startInputPane(t, tmux, "s1", synthScreen)
	client := panesClient(t, newHandler(t, tmux))
	negative := int64(-1)

	cases := []struct {
		name     string
		pane     string
		readUpTo *int64
		code     connect.Code
		message  string
	}{
		{"malformed pane id", "abc", nil, connect.CodeInvalidArgument, "malformed pane id"},
		{"absent pane", "%999999", nil, connect.CodeNotFound, "pane not found"},
		{"negative read_up_to", pane, &negative, connect.CodeInvalidArgument, "bad request body"},
	}
	for _, c := range cases {
		_, err := client.MarkSeen(t.Context(),
			connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: c.pane, ReadUpTo: c.readUpTo}))
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("%s: Panes.MarkSeen error = %v, want a *connect.Error", c.name, err)
		}
		if connectErr.Code() != c.code {
			t.Fatalf("%s: Panes.MarkSeen code = %v, want %v", c.name, connectErr.Code(), c.code)
		}
		if connectErr.Message() != c.message {
			t.Fatalf("%s: Panes.MarkSeen message = %q, want %q", c.name, connectErr.Message(), c.message)
		}
	}
}

func TestPanesMarkSeenReportsStoredTime(t *testing.T) {
	t.Parallel()

	const (
		reported = int64(1700000100)
		earlier  = int64(1700000050)
		later    = int64(1700000200)
	)
	tmux := newTmuxServer(t)
	// The absent read_up_to case needs a pane whose time is unset, and the reported-time cases need their own pane,
	// since setting now would hold the stored time above every reported time.
	nowPane, _ := startInputPane(t, tmux, "now", synthScreen)
	pane, _ := startInputPane(t, tmux, "s1", synthScreen)
	var calls []seenCall
	handler := newSeenHandler(t, tmux, seenRecorder(&calls, nil))
	client := panesClient(t, handler)

	if _, err := client.MarkSeen(t.Context(),
		connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: nowPane})); err != nil {
		t.Fatalf("Panes.MarkSeen absent read_up_to: %v", err)
	}
	if want := seenOption(t, tmux, nowPane); len(calls) != 1 || calls[0] != (seenCall{pane: nowPane, seen: want}) {
		t.Fatalf("absent read_up_to: reports = %+v, want one for %s at stored time %d", calls, nowPane, want)
	}

	steps := []struct {
		name     string
		readUpTo int64
		want     int64
	}{
		{"unset takes the reported time", reported, reported},
		{"earlier time reports the stored time", earlier, reported},
		{"later time reports itself", later, later},
	}
	for _, step := range steps {
		calls = nil
		readUpTo := step.readUpTo
		if _, err := client.MarkSeen(t.Context(),
			connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: pane, ReadUpTo: &readUpTo})); err != nil {
			t.Fatalf("%s: Panes.MarkSeen: %v", step.name, err)
		}
		if len(calls) != 1 || calls[0] != (seenCall{pane: pane, seen: step.want}) {
			t.Fatalf("%s: reports = %+v, want one for %s at %d", step.name, calls, pane, step.want)
		}
	}
}

func TestPanesMarkSeenReporterErrorAnswersInternal(t *testing.T) {
	t.Parallel()

	reported := int64(1700000100)
	tmux := newTmuxServer(t)
	pane, _ := startInputPane(t, tmux, "s1", synthScreen)
	var calls []seenCall
	handler := newSeenHandler(t, tmux, seenRecorder(&calls, errors.New("daemon unreachable")))
	client := panesClient(t, handler)

	_, err := client.MarkSeen(t.Context(),
		connect.NewRequest(&orreryv1.MarkSeenRequest{Pane: pane, ReadUpTo: &reported}))
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("Panes.MarkSeen error = %v, want a *connect.Error", err)
	}
	if connectErr.Code() != connect.CodeInternal {
		t.Fatalf("Panes.MarkSeen code = %v, want %v", connectErr.Code(), connect.CodeInternal)
	}
	if connectErr.Message() != "internal server error" {
		t.Fatalf("Panes.MarkSeen message = %q, want %q", connectErr.Message(), "internal server error")
	}
	if len(calls) != 1 {
		t.Fatalf("reports = %d, want 1", len(calls))
	}
	if got := seenOption(t, tmux, pane); got != reported {
		t.Fatalf("%s = %d after reporter error, want the mark kept at %d", protocol.OptionSeen, got, reported)
	}
}
