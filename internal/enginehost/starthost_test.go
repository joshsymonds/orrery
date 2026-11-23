package enginehost

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/store"
)

// startHostID is the session id the refused start names, so the test can check that no holder of it was launched.
const startHostID = "0123456789abcdef0123456789abcdef"

// newHostsManager returns a manager named host "a" over the state directory stateA, with the other host "b" over the
// state directory stateB. Both run the claude at claude, and each host has an environment of its own. It returns the
// manager and the path of its store.
func newHostsManager(t *testing.T, claude string) (m *Manager, path, stateA, stateB string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	stateA, stateB = t.TempDir(), t.TempDir()
	other := holderhost.NewLocal(holderhost.LocalConfig{
		Shell:    sh,
		Claude:   claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		StateDir: stateB,
		CronHook: true,
	})
	s, storePath := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	m = NewManager(Config{
		Shell:    sh,
		Claude:   claude,
		Environ:  []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		StateDir: stateA,
		HostName: "a",
		Hosts:    map[string]holderhost.Host{"b": other},
		Writer:   writer,
		Now:      fixedClock,
		Store:    s,
	})
	t.Cleanup(m.Close)
	return m, storePath, stateA, stateB
}

// holderDirOf is the holder directory of session id under the state directory state, where a host's Local keeps it.
func holderDirOf(state, id string) string {
	return filepath.Join(state, "holders", id)
}

func TestStartHostNamedHost(t *testing.T) {
	t.Parallel()
	m, path, stateA, stateB := newHostsManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi", Host: "b"})
	if err != nil {
		t.Fatalf("Start on b: %v", err)
	}
	if _, err := os.Stat(holderDirOf(stateB, id)); err != nil {
		t.Fatalf("holder of %s under b's state directory: %v", id, err)
	}
	if _, err := os.Stat(holderDirOf(stateA, id)); !os.IsNotExist(err) {
		t.Fatalf("holder of %s under a's state directory: stat = %v, want none", id, err)
	}
	if host := historyOf(t, path, id).Host; host != "b" {
		t.Fatalf("row host = %q, want b", host)
	}
}

// The name is short on purpose: t.TempDir carries it into the holder's socket path, which holds at most 107 bytes.
func TestStartHostWithoutHost(t *testing.T) {
	t.Parallel()
	m, path, stateA, stateB := newHostsManager(t, fakeClaudePath(t))
	for _, host := range []string{"", "a"} {
		id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi", Host: host})
		if err != nil {
			t.Fatalf("Start with host %q: %v", host, err)
		}
		if _, err := os.Stat(holderDirOf(stateA, id)); err != nil {
			t.Fatalf("holder of %s with host %q under a's state directory: %v", id, host, err)
		}
		if _, err := os.Stat(holderDirOf(stateB, id)); !os.IsNotExist(err) {
			t.Fatalf("holder of %s with host %q under b's state directory: stat = %v, want none", id, host, err)
		}
		if got := historyOf(t, path, id).Host; got != "a" {
			t.Fatalf("row host with host %q = %q, want a", host, got)
		}
	}
}

func TestStartHostRefusesUnknownHost(t *testing.T) {
	t.Parallel()
	m, path, stateA, stateB := newHostsManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{
		ID: startHostID, Dir: t.TempDir(), Prompt: "hi", Host: "c",
	})
	if !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("Start on c: error = %v, want %v", err, ErrUnknownHost)
	}
	if id != "" {
		t.Fatalf("refused Start returned id %q, want none", id)
	}
	if n := countRows(t, path, "sessions"); n != 0 {
		t.Fatalf("sessions rows = %d, want 0", n)
	}
	if n := countRows(t, path, "events"); n != 0 {
		t.Fatalf("events rows = %d, want 0", n)
	}
	for _, state := range []string{stateA, stateB} {
		if _, err := os.Stat(holderDirOf(state, startHostID)); !os.IsNotExist(err) {
			t.Fatalf("refused Start launched a holder under %s: stat = %v", state, err)
		}
	}
}

func TestStartHostResume(t *testing.T) {
	t.Parallel()
	m, path, stateA, stateB := newHostsManager(t, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi", Host: "b"})
	if err != nil {
		t.Fatalf("Start on b: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say of a stopped session on b: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the resumed reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	if _, err := os.Stat(holderDirOf(stateB, id)); err != nil {
		t.Fatalf("holder of %s under b's state directory after the resume: %v", id, err)
	}
	if _, err := os.Stat(holderDirOf(stateA, id)); !os.IsNotExist(err) {
		t.Fatalf("resume of %s on b launched a holder under a's state directory: stat = %v", id, err)
	}
}
