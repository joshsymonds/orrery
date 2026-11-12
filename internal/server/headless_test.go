package server

import (
	"database/sql"
	"fmt"
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
	"github.com/joshsymonds/orrery/internal/store"
)

const (
	headlessAlphaID = "0123456789abcdef0123456789abcdef"
	headlessBetaID  = "fedcba9876543210fedcba9876543210"
	headlessStepID  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headlessOldID   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// openHeadlessStore opens the writer for the store at path. The test closes it before reading.
func openHeadlessStore(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return s
}

// optionalString returns a pointer to value, for an EngineChanges field.
func optionalString(value string) *string {
	return &value
}

// applyHeadless applies ops to s in one write stamped at at.
func applyHeadless(t *testing.T, s *store.Store, at time.Time, ops ...store.EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(t.Context(), ops, at); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// listHeadlessClient returns the Sessions client over the store at path.
func listHeadlessClient(t *testing.T, path string) orreryv1connect.SessionsClient {
	t.Helper()
	return sessionsClient(t, store.NewReader(path))
}

// expectHeadlessCode calls ListHeadless and fails the test unless the call fails with want.
func expectHeadlessCode(t *testing.T, client orreryv1connect.SessionsClient, want connect.Code) {
	t.Helper()
	_, err := client.ListHeadless(t.Context(), connect.NewRequest(&orreryv1.ListHeadlessRequest{}))
	if err == nil {
		t.Fatalf("ListHeadless succeeded, want code %v", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("ListHeadless code: got %v (%v), want %v", got, err, want)
	}
}

// stamp is the unix seconds of at as the store and the proto carry it.
func stamp(at time.Time) string {
	return strconv.FormatInt(at.Unix(), 10)
}

func TestListHeadlessAnswersRecentEngineRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openHeadlessStore(t, path)
	now := time.Now()

	// alpha is waiting on a question, and its newest reply is older than the question.
	applyHeadless(t, s, now.Add(-3*time.Hour), store.EngineAdd{ID: headlessAlphaID, Dir: "/work/alpha", State: "idle"})
	applyHeadless(t, s, now.Add(-2*time.Hour), store.EngineBlock{
		ID: headlessAlphaID, Kind: "text", Role: "assistant", MessageID: "m-1", Text: "Working on it.",
	})
	applyHeadless(t, s, now.Add(-time.Hour), store.EngineSet{ID: headlessAlphaID, Changes: store.EngineChanges{
		State:    optionalString("waiting"),
		Seen:     optionalString(stamp(now.Add(-30 * time.Minute))),
		Question: optionalString("Run tests?"),
	}})

	// beta is working, and its summary is its newest reply.
	applyHeadless(t, s, now.Add(-4*time.Hour), store.EngineAdd{ID: headlessBetaID, Dir: "/work/beta", State: "working"})
	applyHeadless(t, s, now.Add(-3*time.Hour), store.EngineBlock{
		ID: headlessBetaID, Kind: "text", Role: "assistant", MessageID: "m-2", Text: "Earlier.",
	})
	applyHeadless(t, s, now.Add(-10*time.Minute), store.EngineBlock{
		ID: headlessBetaID, Kind: "text", Role: "assistant", MessageID: "m-3", Text: "All done.",
	})

	// The step row has an event in the window, and the old row's only event is past the retention window.
	applyHeadless(t, s, now, store.StepAdd{
		ID: headlessStepID, Agent: "implementer", Prompt: "p", Schema: "{}", Workspace: "/w", Revision: "r",
		Worktree: "/w", DoneValue: "done",
	})
	applyHeadless(t, s, now.Add(-15*24*time.Hour), store.EngineAdd{ID: headlessOldID, Dir: "/work/old", State: "idle"})
	applyHeadless(t, s, now.Add(-15*24*time.Hour), store.EngineBlock{
		ID: headlessOldID, Kind: "text", Role: "assistant", MessageID: "m-4", Text: "Stale.",
	})
	insertStepEvent(t, path, headlessStepID, now)
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	got, err := listHeadlessClient(t, path).ListHeadless(
		t.Context(),
		connect.NewRequest(&orreryv1.ListHeadlessRequest{}),
	)
	if err != nil {
		t.Fatalf("ListHeadless: %v", err)
	}
	want := &orreryv1.ListHeadlessResponse{
		Sessions: []*orreryv1.HeadlessSession{
			{
				Id:          headlessAlphaID,
				Dir:         "/work/alpha",
				Label:       "alpha · 01234567",
				State:       "waiting",
				Changed:     now.Add(-time.Hour).Unix(),
				Seen:        now.Add(-30 * time.Minute).Unix(),
				Summary:     "Run tests?",
				Question:    "Run tests?",
				NewestReply: now.Add(-2 * time.Hour).Unix(),
			},
			{
				Id:          headlessBetaID,
				Dir:         "/work/beta",
				Label:       "beta · fedcba98",
				State:       "working",
				Changed:     now.Add(-4 * time.Hour).Unix(),
				Seen:        0,
				Summary:     "All done.",
				Question:    "",
				NewestReply: now.Add(-10 * time.Minute).Unix(),
			},
		},
	}
	if !proto.Equal(got.Msg, want) {
		t.Fatalf("ListHeadless: got %v, want %v", got.Msg, want)
	}
}

// insertStepEvent appends one engine event to the session id through a second connection, so a step row has an
// event in the retention window.
func insertStepEvent(t *testing.T, path, id string, at time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store for step event: %v", err)
	}
	_, execErr := db.ExecContext(
		t.Context(),
		"INSERT INTO events (session_id, kind, at, payload) VALUES (?, 'engine_block', ?, '{}')",
		id, at.Unix(),
	)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close store after step event: %v", closeErr)
	}
	if execErr != nil {
		t.Fatalf("insert step event: %v", execErr)
	}
}

func TestListHeadlessSummaryFollowsStateAndCutsToRunes(t *testing.T) {
	type row struct {
		state, question, reason, reply, want string
	}
	cases := []row{
		{state: "waiting", question: "Run the migration?", reply: "Looking now.", want: "Run the migration?"},
		{state: "waiting", reply: "Still looking.", want: "Still looking."},
		{state: "failed", reason: "rate limited", reply: "Trying.", want: "rate limited"},
		{state: "interrupted", reason: "daemon restarted", reply: "Halfway.", want: "daemon restarted"},
		{state: "failed", reply: "Gave up.", want: "Gave up."},
		{state: "working", reason: "stale reason", reply: "Going.", want: "Going."},
		{state: "working", reply: strings.Repeat("r", 400), want: strings.Repeat("r", 300)},
		{state: "working", reply: strings.Repeat("é", 400), want: strings.Repeat("é", 300)},
	}

	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openHeadlessStore(t, path)
	now := time.Now()
	ids := make([]string, len(cases))
	for i, c := range cases {
		ids[i] = fmt.Sprintf("%032x", i+1)
		applyHeadless(t, s, now.Add(-2*time.Hour), store.EngineAdd{ID: ids[i], Dir: "/work/case", State: c.state})
		if c.reply != "" {
			applyHeadless(t, s, now.Add(-time.Hour), store.EngineBlock{
				ID: ids[i], Kind: "text", Role: "assistant", MessageID: "m", Text: c.reply,
			})
		}
		applyHeadless(t, s, now, store.EngineSet{ID: ids[i], Changes: store.EngineChanges{
			Question: optionalString(c.question), Reason: optionalString(c.reason),
		}})
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	got, err := listHeadlessClient(t, path).ListHeadless(
		t.Context(),
		connect.NewRequest(&orreryv1.ListHeadlessRequest{}),
	)
	if err != nil {
		t.Fatalf("ListHeadless: %v", err)
	}
	if len(got.Msg.GetSessions()) != len(cases) {
		t.Fatalf("ListHeadless: got %d sessions, want %d", len(got.Msg.GetSessions()), len(cases))
	}
	for i, session := range got.Msg.GetSessions() {
		if session.GetId() != ids[i] {
			t.Fatalf("session %d: id %q, want %q", i, session.GetId(), ids[i])
		}
		if session.GetSummary() != cases[i].want {
			t.Fatalf("session %d (%s): summary %q, want %q", i, cases[i].state, session.GetSummary(), cases[i].want)
		}
	}
}

func TestListHeadlessReturnsUnavailableWhenReaderIsNil(t *testing.T) {
	expectHeadlessCode(t, sessionsClient(t, nil), connect.CodeUnavailable)
}

func TestListHeadlessReturnsUnavailableWhenStoreIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "orrery.db")
	expectHeadlessCode(t, listHeadlessClient(t, path), connect.CodeUnavailable)
}

func TestListHeadlessReturnsInternalWhenStampDoesNotParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openHeadlessStore(t, path)
	applyHeadless(t, s, time.Now(), store.EngineAdd{ID: headlessAlphaID, Dir: "/work/alpha", State: "idle"})
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store to corrupt changed: %v", err)
	}
	_, execErr := db.ExecContext(
		t.Context(),
		"UPDATE sessions SET changed = 'not-a-number' WHERE id = ?",
		headlessAlphaID,
	)
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close store after corrupt changed: %v", closeErr)
	}
	if execErr != nil {
		t.Fatalf("corrupt changed: %v", execErr)
	}

	_, err = listHeadlessClient(t, path).ListHeadless(
		t.Context(),
		connect.NewRequest(&orreryv1.ListHeadlessRequest{}),
	)
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Fatalf("ListHeadless code: got %v (%v), want %v", got, err, connect.CodeInternal)
	}
	if strings.Contains(err.Error(), "invalid syntax") {
		t.Fatalf("ListHeadless echoed parse detail: %v", err)
	}
}

func TestListHeadlessReturnsInternalWithoutStoreDetail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	if err := os.WriteFile(path, []byte("this file is not a sqlite database and has no header"), 0o600); err != nil {
		t.Fatalf("write garbage store: %v", err)
	}

	_, err := listHeadlessClient(t, path).ListHeadless(
		t.Context(),
		connect.NewRequest(&orreryv1.ListHeadlessRequest{}),
	)
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Fatalf("ListHeadless code: got %v (%v), want %v", got, err, connect.CodeInternal)
	}
	if strings.Contains(err.Error(), "database") {
		t.Fatalf("ListHeadless echoed store detail: %v", err)
	}
}
