package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
)

// contextDir is the working directory of the headless session the context tests write. Its transcripts are in the
// project directory -work-my-app, because the dot is not an ASCII letter or digit.
const contextDir = "/work/my.app"

// contextClaudeSession is the Claude session id of that session, so its transcript is <contextClaudeSession>.jsonl.
const contextClaudeSession = "claude-session-1"

// contextAssistantLine is a main-chain assistant line whose usage counts 1234 input tokens.
const contextAssistantLine = `{"type":"assistant","message":{"usage":{"input_tokens":1234}}}` + "\n"

// contextBoundaryLine is a compact_boundary line that leaves 4321 post-compaction tokens.
const contextBoundaryLine = `{"type":"system","subtype":"compact_boundary","compactMetadata":` +
	`{"trigger":"auto","preTokens":91234,"postTokens":4321}}` + "\n"

// contextHandler returns the guarded handler that reads sessions from reader, with home as its Config.Home.
func contextHandler(t *testing.T, reader *store.Reader, home string) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:                []string{allowedLogin},
		Tmux:                  newTmuxClient(t),
		Push:                  newPushStore(t),
		Sessions:              reader,
		Home:                  home,
		ConversationPoll:      convPoll,
		ConversationKeepalive: convKeepalive,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// addContextSession writes the engine session eventEngine in contextDir, with the Claude session contextClaudeSession.
func addContextSession(t *testing.T, s *store.Store) {
	t.Helper()
	applyEngineOps(t, s,
		store.EngineAdd{ID: eventEngine, Dir: contextDir, State: "idle"},
		store.EngineSet{ID: eventEngine, Changes: store.EngineChanges{ClaudeSession: optional(contextClaudeSession)}},
	)
}

// contextTranscriptPath is the path of the transcript of the context session under home.
func contextTranscriptPath(home string) string {
	return filepath.Join(home, ".claude", "projects", "-work-my-app", contextClaudeSession+".jsonl")
}

// writeContextTranscript writes text as the transcript of the context session under home.
func writeContextTranscript(t *testing.T, home, text string) {
	t.Helper()
	path := contextTranscriptPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make transcript directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

// wantFigure fails the test unless figure has tokens tokens out of window.
func wantFigure(t *testing.T, figure *orreryv1.ConversationContext, tokens, window int64) {
	t.Helper()
	if figure.GetTokens() != tokens || figure.GetWindow() != window {
		t.Fatalf("context figure tokens=%d window=%d, want tokens %d and window %d",
			figure.GetTokens(), figure.GetWindow(), tokens, window)
	}
}

// TestSessionWatchHeadlessContextFigure serves the figure of a headless session whose transcript holds a main-chain
// assistant line with usage: the figure carries that line's tokens and the window.
func TestSessionWatchHeadlessContextFigure(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addContextSession(t, s)
	writeContextTranscript(t, home, contextAssistantLine)
	client := sessionsWatchClient(t, contextHandler(t, store.NewReader(path), home))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))

	wantFigure(t, stream.next(t).GetState().GetContext(), 1234, convContextWindow)
}

// TestSessionWatchHeadlessContextHonoursCompaction appends a compaction boundary after the first delta. The next delta
// carries the boundary's post-compaction tokens as the figure, as a pane's conversation does.
func TestSessionWatchHeadlessContextHonoursCompaction(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addContextSession(t, s)
	writeContextTranscript(t, home, contextAssistantLine)
	client := sessionsWatchClient(t, contextHandler(t, store.NewReader(path), home))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantFigure(t, stream.next(t).GetState().GetContext(), 1234, convContextWindow)

	appendTranscript(t, contextTranscriptPath(home), contextBoundaryLine)
	wantFigure(t, stream.nextDelta(t).GetState().GetContext(), 4321, convContextWindow)
}

// TestSessionWatchHeadlessContextMissingTranscriptServesZeroFigure serves a session whose transcript is not on disk.
// Its figure is zero, and its conversation is served as it is with no transcript.
func TestSessionWatchHeadlessContextMissingTranscriptServesZeroFigure(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addContextSession(t, s)
	applyEngineOps(t, s, replyBlock(1))
	client := sessionsWatchClient(t, contextHandler(t, store.NewReader(path), home))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))

	msg := stream.next(t)
	wantFigure(t, msg.GetState().GetContext(), 0, 0)
	assertTexts(t, msg.GetItems(), "reply 1")
	if got := msg.GetState().GetSession().GetId(); got != eventEngine {
		t.Fatalf("state session id = %q, want %q", got, eventEngine)
	}
}
