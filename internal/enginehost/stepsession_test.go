package enginehost

import (
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"

	// The blank import registers the pure-Go "sqlite" driver the kind check opens the file with.
	_ "modernc.org/sqlite"
)

// stepSchema is a JSON schema with no spaces, so it stays one field of the fake claude's argument line.
const stepSchema = `{"type":"object"}`

// Step ids in the form checkID accepts. Each test uses only the ids it names.
const (
	stepA = "0123456789abcdef0123456789abcdef"
	stepB = "1123456789abcdef0123456789abcdef"
	stepC = "2123456789abcdef0123456789abcdef"
)

// newStepManager returns a manager with a session cap of one and stepCap steps that runs the claude at claude and sends its
// notices to sink. It reads rows from the store at the returned path. Close runs before the writer stops and the store
// closes.
func newStepManager(t *testing.T, stepCap int, claude string, sink NoticeSink) (*Manager, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:    sh,
		Claude:   claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH")},
		Cap:      1,
		StepCap:  stepCap,
		Writer:   writer,
		Now:      fixedClock,
		Notifier: sink,
		Store:    s,
	})
	t.Cleanup(m.Close)
	return m, path
}

// rowKind returns the kind of the sessions row id in the store at path, read on a separate connection.
func rowKind(t *testing.T, path, id string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close store connection: %v", err)
		}
	}()
	var kind string
	if err := db.QueryRow("SELECT kind FROM sessions WHERE id = ?", id).Scan(&kind); err != nil {
		t.Fatalf("read kind of %s: %v", id, err)
	}
	return kind
}

// hasArg reports whether the argument line args holds flag as one of its fields.
func hasArg(args, flag string) bool {
	for _, field := range strings.Fields(args) {
		if field == flag {
			return true
		}
	}
	return false
}

// changedHas reports whether an engine_changed event of a history sets field to value.
func changedHas(changes []map[string]string, field, value string) bool {
	for _, fields := range changes {
		if fields[field] == value {
			return true
		}
	}
	return false
}

func TestStartStepRunsAgentWithSchemaAndRecordsRowOpsAndPrompt(t *testing.T) {
	m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	seen := "1700000000"
	err := m.StartStep(t.Context(), StepSession{
		ID:     stepA,
		Dir:    dir,
		Prompt: "hi",
		Agent:  "reviewer",
		Schema: stepSchema,
		Ops:    []store.EngineOp{store.EngineSet{ID: stepA, Changes: store.EngineChanges{Seen: &seen}}},
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	history := waitHistory(t, path, stepA, "the step's reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	args := fakeArgs(t, dir)
	if len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want one", args)
	}
	if !hasFlagValue(args[0], "--agent", "reviewer") {
		t.Fatalf("step args = %q, want --agent reviewer", args[0])
	}
	if !hasArg(args[0], "--strict-mcp-config") {
		t.Fatalf("step args = %q, want --strict-mcp-config", args[0])
	}
	if !hasFlagValue(args[0], "--json-schema", stepSchema) {
		t.Fatalf("step args = %q, want --json-schema %s", args[0], stepSchema)
	}
	if kind := rowKind(t, path, stepA); kind != store.KindStep {
		t.Fatalf("row kind = %q, want %q", kind, store.KindStep)
	}
	if !changedHas(changedFields(t, history), "seen", seen) {
		t.Fatalf("changed fields = %+v, want the op's seen %s", changedFields(t, history), seen)
	}
	if got := conversationOf(t, history); len(got) == 0 || got[0] != "prompt: hi" {
		t.Fatalf("conversation = %q, want the prompt first", got)
	}
}

func TestStartStepCapCountsStepsApartFromSessions(t *testing.T) {
	m, _ := newStepManager(t, 2, fakeClaudePath(t), &fakeSink{})
	if _, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"}); err != nil {
		t.Fatalf("Start of the session: %v", err)
	}
	for _, id := range []string{stepA, stepB} {
		step := StepSession{ID: id, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer"}
		if err := m.StartStep(t.Context(), step); err != nil {
			t.Fatalf("StartStep %s: %v", id, err)
		}
	}
	third := StepSession{ID: stepC, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer"}
	if err := m.StartStep(t.Context(), third); !errors.Is(err, ErrCapReached) {
		t.Fatalf("third StartStep: err = %v, want ErrCapReached", err)
	}
	if err := m.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop of a step: %v", err)
	}
	if err := m.StartStep(t.Context(), third); err != nil {
		t.Fatalf("StartStep after a stop freed a slot: %v", err)
	}
}

func TestStepFinishedTurnSendsNoNoticeAndSessionTurnDoes(t *testing.T) {
	sink := &fakeSink{}
	m, path := newStepManager(t, 1, fakeClaudePath(t), sink)
	if err := m.StartStep(
		t.Context(),
		StepSession{ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer"},
	); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's turn end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	// Stop returns once the step's pump has finished, so any notice the step sent is already in the sink.
	if err := m.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the step's turn = %+v, want none", got)
	}

	sid, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start of the session: %v", err)
	}
	got := waitNotices(t, sink)
	if len(got) != 1 || got[0].Session != sid || got[0].State != string(protocol.StateDone) {
		t.Fatalf("notices = %+v, want only the session's done notice", got)
	}
}

func TestStepInterruptedByCloseSendsNoNotice(t *testing.T) {
	sink := &fakeSink{}
	m, path := newStepManager(t, 1, fakeClaudePath(t), sink)
	if err := m.StartStep(
		t.Context(),
		StepSession{ID: stepA, Dir: t.TempDir(), Prompt: "HANG", Agent: "reviewer"},
	); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})

	m.Close()
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after Close = %+v, want none: a step whose turn Close interrupts sends nothing", got)
	}
	if state := historyOf(t, path, stepA).State; state != stateInterrupted {
		t.Fatalf("step state after Close = %q, want interrupted", state)
	}
}

func TestStepWithUnfindableClaudeFailsWithoutNotice(t *testing.T) {
	sink := &fakeSink{}
	missing := filepath.Join(t.TempDir(), "no-claude")
	m, path := newStepManager(t, 1, missing, sink)
	if err := m.StartStep(
		t.Context(),
		StepSession{ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer"},
	); err == nil {
		t.Fatalf("StartStep with a missing claude: err = nil, want the spawn error")
	}
	if state := historyOf(t, path, stepA).State; state != "failed" {
		t.Fatalf("step state = %q, want failed", state)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the failed step = %+v, want none", got)
	}

	sid, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start with a missing claude: %v", err)
	}
	got := waitNotices(t, sink)
	if len(got) != 1 || got[0].Session != sid || got[0].State != "failed" {
		t.Fatalf("notices = %+v, want only the session's failed notice", got)
	}
}

func TestSayToStepIsRefusedAndNeverResumes(t *testing.T) {
	sink := &fakeSink{}
	claude := fakeClaudePath(t)
	m, path := newStepManager(t, 1, claude, sink)
	dir := t.TempDir()
	if err := m.StartStep(t.Context(), StepSession{ID: stepA, Dir: dir, Prompt: "hi", Agent: "reviewer"}); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	err := m.Say(t.Context(), SayRequest{ID: stepA, Text: "again"})
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("Say to a stopped step: err = %v, want ErrNoSession", err)
	}
	if n := len(fakeArgs(t, dir)); n != 1 {
		t.Fatalf("fake claude starts = %d, want one: the step must not resume", n)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices = %+v, want none", got)
	}
}

func TestStartStepRefusesBadInputAndStoresNothing(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		step StepSession
		want error
	}{
		{"short id", StepSession{ID: "abc", Dir: dir, Prompt: "hi", Agent: "reviewer"}, ErrBadID},
		{"relative dir", StepSession{ID: stepA, Dir: "relative", Prompt: "hi", Agent: "reviewer"}, ErrBadDir},
		{"empty prompt", StepSession{ID: stepA, Dir: dir, Agent: "reviewer"}, ErrEmptyPrompt},
		{"empty agent", StepSession{ID: stepA, Dir: dir, Prompt: "hi"}, ErrBadAgent},
		{"agent with a space", StepSession{ID: stepA, Dir: dir, Prompt: "hi", Agent: "a b"}, ErrBadAgent},
		{"agent starting with a dash", StepSession{ID: stepA, Dir: dir, Prompt: "hi", Agent: "-x"}, ErrBadAgent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
			err := m.StartStep(t.Context(), tc.step)
			if !errors.Is(err, tc.want) {
				t.Fatalf("StartStep: err = %v, want %v", err, tc.want)
			}
			if n := countRows(t, path, "sessions"); n != 0 {
				t.Fatalf("rows = %d, want none", n)
			}
		})
	}
}
