package enginehost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// ctlSteering returns the EpicSteering of m whose controller is named ctl, which is not m's own name a. Its sessions
// are m's store and its renders are fakeRender.
func ctlSteering(m *Manager) *EpicSteering {
	return NewEpicSteering(EpicSteeringConfig{Manager: m, Sessions: m.store, Render: fakeRender, Host: "ctl"})
}

// The names are short on purpose: t.TempDir carries the test name into the holder's socket path, which holds at most
// 107 bytes.
func TestEpicSteeringHostRemote(t *testing.T) {
	t.Parallel()
	m, path, stateA, stateB := newHostsManager(t, fakeClaudePath(t))
	id, err := ctlSteering(m).Start(t.Context(), store.EpicRow{ID: "orrery-epic", Host: "b", Repository: t.TempDir()})
	if err != nil {
		t.Fatalf("Start of an epic on b: %v", err)
	}
	requireSessionID(t, id)
	if host := historyOf(t, path, id).Host; host != "b" {
		t.Fatalf("row host = %q, want b", host)
	}
	if _, err := os.Stat(holderDirOf(stateB, id)); err != nil {
		t.Fatalf("holder of %s under b's state directory: %v", id, err)
	}
	if _, err := os.Stat(holderDirOf(stateA, id)); !os.IsNotExist(err) {
		t.Fatalf("holder of %s under a's state directory: stat = %v, want none", id, err)
	}
}

func TestEpicSteeringHostController(t *testing.T) {
	t.Parallel()
	m, path, stateA, stateB := newHostsManager(t, fakeClaudePath(t))
	steering := ctlSteering(m)
	for _, host := range []string{"ctl", ""} {
		id, err := steering.Start(t.Context(), store.EpicRow{ID: "orrery-epic", Host: host, Repository: t.TempDir()})
		if err != nil {
			t.Fatalf("Start of an epic with host %q: %v", host, err)
		}
		if got := historyOf(t, path, id).Host; got != "a" {
			t.Fatalf("row host of an epic with host %q = %q, want a", host, got)
		}
		if _, err := os.Stat(holderDirOf(stateA, id)); err != nil {
			t.Fatalf("holder of %s with host %q under a's state directory: %v", id, host, err)
		}
		if _, err := os.Stat(holderDirOf(stateB, id)); !os.IsNotExist(err) {
			t.Fatalf("holder of %s with host %q under b's state directory: stat = %v, want none", id, host, err)
		}
	}
}

func TestEpicSteeringHostDir(t *testing.T) {
	t.Parallel()
	m, path, _, _ := newHostsManager(t, fakeClaudePath(t))
	missing := filepath.Join(t.TempDir(), "missing")

	// The controller does not stat a remote directory, so the start reaches b's launch, which records the session failed
	// with the missing directory as its reason. A spawn failure returns the id and no error (see Manager.start).
	id, err := m.Start(t.Context(), StartRequest{Dir: missing, Prompt: "hi", Host: "b"})
	if errors.Is(err, ErrBadDir) {
		t.Fatalf("Start on b with a missing directory: error = %v, want one that is not %v", err, ErrBadDir)
	}
	if id == "" {
		t.Fatalf("Start on b with a missing directory returned no id")
	}
	row := historyOf(t, path, id)
	if row.Host != "b" {
		t.Fatalf("row host = %q, want b", row.Host)
	}
	if row.State != "failed" || !strings.Contains(row.Reason, missing) {
		t.Fatalf("row state = %q reason = %q, want failed with a reason that names %s", row.State, row.Reason, missing)
	}
	if n := countRows(t, path, "sessions"); n != 1 {
		t.Fatalf("sessions rows = %d, want 1", n)
	}

	relative := filepath.Join("relative", "dir")
	id, err = m.Start(t.Context(), StartRequest{Dir: relative, Prompt: "hi", Host: "b"})
	if !errors.Is(err, ErrBadDir) {
		t.Fatalf("Start on b with a relative directory: error = %v, want %v", err, ErrBadDir)
	}
	if id != "" {
		t.Fatalf("refused Start returned id %q, want none", id)
	}
	if n := countRows(t, path, "sessions"); n != 1 {
		t.Fatalf("sessions rows after the refused start = %d, want still 1", n)
	}

	_, err = m.Start(t.Context(), StartRequest{Dir: missing, Prompt: "hi"})
	if !errors.Is(err, ErrBadDir) {
		t.Fatalf("Start on the controller with a missing directory: error = %v, want %v", err, ErrBadDir)
	}
}
