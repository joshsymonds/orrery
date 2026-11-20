package enginehost

import (
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// errRender is the error the render test's Render returns.
var errRender = errors.New("fake render failed")

// errSessionRead is the error failingSessions returns.
var errSessionRead = errors.New("fake session read failed")

// failingSessions is a steeringSessions whose every read of a session row fails with errSessionRead.
type failingSessions struct{}

// EngineSession returns errSessionRead.
func (failingSessions) EngineSession(context.Context, string) (store.EngineRow, error) {
	return store.EngineRow{}, errSessionRead
}

// fakeRender is the Render the steering tests give every epic: a contract line and a log line.
func fakeRender(store.EpicRow) ([]string, error) {
	return []string{"Contract: fake contract", "Log: fake log"}, nil
}

// steeringOf returns the EpicSteering of m, which reads its session rows from m's store and renders with render.
func steeringOf(m *Manager, render func(store.EpicRow) ([]string, error)) *EpicSteering {
	return NewEpicSteering(EpicSteeringConfig{Manager: m, Sessions: m.store, Render: render})
}

func TestEpicSteeringStartRunsOneSessionInRepository(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	repo := t.TempDir()
	id, err := steeringOf(m, fakeRender).Start(t.Context(), store.EpicRow{ID: "orrery-epic", Repository: repo})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	requireSessionID(t, id)
	if n := countRows(t, path, "sessions"); n != 1 {
		t.Fatalf("sessions rows = %d, want 1", n)
	}

	history := waitHistory(t, engineOf(m), path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if history.Dir != repo {
		t.Fatalf("session dir = %q, want %q", history.Dir, repo)
	}
	if err := syscall.Kill(fakePID(t, repo), 0); err != nil {
		t.Fatalf("fake claude is not running in the repository: %v", err)
	}
	want := strings.Join([]string{
		"orreryd builds this epic; this session steers it by answering its questions and events, " +
			"and changes nothing in the repository.",
		"Epic: orrery-epic",
		"Contract: fake contract",
		"Log: fake log",
	}, "\n")
	prompts := enginePromptsOf(t, history)
	if len(prompts) != 1 || prompts[0].Text != want {
		t.Fatalf("prompts = %+v, want one prompt with text %q", prompts, want)
	}
}

func TestEpicSteeringStartAtFullCapNamesCapAndStartsNoSession(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	if _, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"}); err != nil {
		t.Fatalf("Start of the session that fills the cap: %v", err)
	}

	id, err := steeringOf(m, fakeRender).Start(t.Context(), store.EpicRow{ID: "orrery-epic", Repository: t.TempDir()})
	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("Start error = %v, want ErrCapReached", err)
	}
	if !strings.Contains(err.Error(), "cap of 1") {
		t.Fatalf("Start error %q does not name the cap of 1", err)
	}
	if id != "" {
		t.Fatalf("refused Start returned id %q, want none", id)
	}
	if n := countRows(t, path, "sessions"); n != 1 {
		t.Fatalf("sessions rows = %d, want 1", n)
	}
}

func TestEpicSteeringStartNamesSpawnFailure(t *testing.T) {
	t.Parallel()
	const reason = "fake systemd-run refused the scope"
	root := holderRoot(t)
	m, _ := newHolderManager(t, fakeSystemdRunPath(t), root, "FAKE_SYSTEMD_RUN_FAIL="+reason)

	id, startErr := steeringOf(m, fakeRender).Start(t.Context(),
		store.EpicRow{ID: "orrery-epic", Repository: t.TempDir()})
	if startErr == nil || !strings.Contains(startErr.Error(), reason) {
		t.Fatalf("Start error = %v, want one carrying systemd-run's reason %q", startErr, reason)
	}
	if id != "" {
		t.Fatalf("failed Start returned id %q, want none", id)
	}
	rows, err := m.store.EngineSessions(t.Context())
	if err != nil {
		t.Fatalf("EngineSessions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("engine sessions = %d, want the one failed session", len(rows))
	}
	if !strings.Contains(startErr.Error(), rows[0].ID) {
		t.Fatalf("Start error %q does not name the failed session %s", startErr, rows[0].ID)
	}
}

func TestEpicSteeringRenderErrorStartsNothing(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	failing := func(store.EpicRow) ([]string, error) { return nil, errRender }

	id, err := steeringOf(m, failing).Start(t.Context(), store.EpicRow{ID: "orrery-epic", Repository: t.TempDir()})
	if !errors.Is(err, errRender) {
		t.Fatalf("Start error = %v, want the render error", err)
	}
	if id != "" {
		t.Fatalf("Start after a render error returned id %q, want none", id)
	}
	if n := countRows(t, path, "sessions"); n != 0 {
		t.Fatalf("sessions rows = %d, want 0", n)
	}
}

func TestEpicSteeringStopStopsStartedSession(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	repo := t.TempDir()
	steering := steeringOf(m, fakeRender)
	id, err := steering.Start(t.Context(), store.EpicRow{ID: "orrery-epic", Repository: repo})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	pid := fakePID(t, repo)

	if err := steering.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := historyOf(t, path, id).State; got != "stopped" {
		t.Fatalf("state after Stop = %q, want stopped", got)
	}
	requireGone(t, pid)
}

func TestEpicSteeringStartStopsSessionWhoseRowReadFails(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	repo := t.TempDir()
	steering := NewEpicSteering(EpicSteeringConfig{Manager: m, Sessions: failingSessions{}, Render: fakeRender})

	id, err := steering.Start(t.Context(), store.EpicRow{ID: "orrery-epic", Repository: repo})
	if !errors.Is(err, errSessionRead) {
		t.Fatalf("Start error = %v, want the session read error", err)
	}
	if id != "" {
		t.Fatalf("Start after a failed read returned id %q, want none", id)
	}
	rows, err := m.store.EngineSessions(t.Context())
	if err != nil {
		t.Fatalf("EngineSessions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("engine sessions = %d, want the one started session", len(rows))
	}
	if got := historyOf(t, path, rows[0].ID).State; got != "stopped" {
		t.Fatalf("state of the started session = %q, want stopped", got)
	}
	if live := m.Live(); len(live) != 0 {
		t.Fatalf("live sessions = %v, want none", live)
	}
	// The stop freed the cap's one slot, so another session starts.
	if _, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"}); err != nil {
		t.Fatalf("Start after the stopped steering session: %v", err)
	}
}
