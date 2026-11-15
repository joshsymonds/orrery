package tmuxtest

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// fakeProc is a synthetic process table: one directory per pid, holding comm and cmdline.
type fakeProc struct {
	t    *testing.T
	root string
}

func newFakeProc(t *testing.T) *fakeProc {
	t.Helper()
	return &fakeProc{t: t, root: t.TempDir()}
}

// add puts a process with comm and argv at pid into the table.
func (p *fakeProc) add(pid int, comm string, argv ...string) {
	p.t.Helper()
	dir := filepath.Join(p.root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		p.t.Fatalf("make %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o600); err != nil {
		p.t.Fatalf("write comm: %v", err)
	}
	cmdline := strings.Join(argv, "\x00") + "\x00"
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o600); err != nil {
		p.t.Fatalf("write cmdline: %v", err)
	}
}

// remove takes pid out of the table, as an exit does.
func (p *fakeProc) remove(pid int) {
	p.t.Helper()
	if err := os.RemoveAll(filepath.Join(p.root, strconv.Itoa(pid))); err != nil {
		p.t.Fatalf("remove pid %d: %v", pid, err)
	}
}

// signal records one signal sent to a pid.
type signal struct {
	pid int
	sig syscall.Signal
}

// reaper returns a Reaper over the table that records every signal. A pid in stubborn ignores SIGTERM; any other
// leaves the table on SIGTERM. SIGKILL always removes the pid.
func (p *fakeProc) reaper(sent *[]signal, stubborn ...int) Reaper {
	r := Reaper{ProcRoot: p.root}
	r.Signal = func(pid int, sig syscall.Signal) error {
		*sent = append(*sent, signal{pid: pid, sig: sig})
		if sig == syscall.SIGKILL || !contains(stubborn, pid) {
			p.remove(pid)
		}
		return nil
	}
	r.Exited = func(pid int) bool { return !r.present(pid) }
	return r
}

func contains(pids []int, pid int) bool {
	for _, p := range pids {
		if p == pid {
			return true
		}
	}
	return false
}

func TestReapStopsOnlyServersOfTestBinariesThatAreGone(t *testing.T) {
	t.Parallel()
	proc := newFakeProc(t)
	// The live test binary 100 owns one server, which stays.
	proc.add(100, "integration.test", "/tmp/go-build/integration.test")
	proc.add(201, serverComm, "tmux", "-L", "orrery-integration-100-0a1b", "-f", "/dev/null", "new-session", "-d")
	// The gone binaries 300 and 400 own servers, which are orphans.
	proc.add(202, serverComm, "tmux", "-L", "orrery-test-300-abc123", "new-session", "-d")
	proc.add(203, serverComm, "/nix/store/x-tmux/bin/tmux", "-Lorrery-integration-400-ff")
	// A client of a gone binary's server is no server.
	proc.add(204, "tmux: client", "tmux", "-L", "orrery-test-300-abc123", "attach")
	// Servers the owner or other tools run: no -L, another name, an uppercase hex, extra fields, a pid of zero.
	proc.add(205, serverComm, "tmux", "new-session")
	proc.add(206, serverComm, "tmux", "-L", "default")
	proc.add(207, serverComm, "tmux", "-L", "orrery-test-send-300-abc")
	proc.add(208, serverComm, "tmux", "-L", "orrery-test-300-ABC")
	proc.add(209, serverComm, "tmux", "-L", "orrery-test-300-abc-def")
	proc.add(210, serverComm, "tmux", "-L", "orrery-test-0-abc")
	// A non-tmux process whose arguments look like a test server's.
	proc.add(211, serverComm, "sh", "-L", "orrery-test-300-abc")

	var sent []signal
	reaped, err := proc.reaper(&sent).Reap()
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	want := []Orphan{
		{PID: 202, Name: "orrery-test-300-abc123", Owner: 300},
		{PID: 203, Name: "orrery-integration-400-ff", Owner: 400},
	}
	if !reflect.DeepEqual(reaped, want) {
		t.Fatalf("reaped %+v, want %+v", reaped, want)
	}
	if wantSent := []signal{{202, syscall.SIGTERM}, {203, syscall.SIGTERM}}; !reflect.DeepEqual(sent, wantSent) {
		t.Fatalf("signals %+v, want SIGTERM to each orphan and nothing else", sent)
	}
}

func TestReapKillsAnOrphanThatOutlivesSIGTERM(t *testing.T) {
	t.Parallel()
	proc := newFakeProc(t)
	proc.add(202, serverComm, "tmux", "-L", "orrery-test-300-abc")
	var sent []signal
	if _, err := proc.reaper(&sent, 202).Reap(); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if want := []signal{{202, syscall.SIGTERM}, {202, syscall.SIGKILL}}; !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals %+v, want SIGTERM then SIGKILL", sent)
	}
}

// TestReapLeavesAPidReusedAfterSIGTERM checks that a pid whose orphan exited, and which another process took before
// the SIGKILL, gets no SIGKILL.
func TestReapLeavesAPidReusedAfterSIGTERM(t *testing.T) {
	t.Parallel()
	proc := newFakeProc(t)
	proc.add(202, serverComm, "tmux", "-L", "orrery-test-300-abc")
	var sent []signal
	r := proc.reaper(&sent, 202)
	r.Exited = func(pid int) bool {
		proc.remove(pid)
		proc.add(pid, "bash", "bash")
		return false
	}
	if _, err := r.Reap(); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if want := []signal{{202, syscall.SIGTERM}}; !reflect.DeepEqual(sent, want) {
		t.Fatalf("signals %+v, want only the SIGTERM", sent)
	}
}

func TestReapSkipsAnOrphanGoneBeforeItsSignal(t *testing.T) {
	t.Parallel()
	proc := newFakeProc(t)
	proc.add(202, serverComm, "tmux", "-L", "orrery-test-300-abc")
	proc.add(203, serverComm, "tmux", "-L", "orrery-test-300-def")
	var sent []signal
	r := proc.reaper(&sent)
	inner := r.Signal
	r.Signal = func(pid int, sig syscall.Signal) error {
		if pid == 202 {
			return syscall.ESRCH
		}
		return inner(pid, sig)
	}
	reaped, err := r.Reap()
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(reaped) != 2 {
		t.Fatalf("reaped %+v, want both orphans", reaped)
	}
}

func TestReapReturnsASignalFailure(t *testing.T) {
	t.Parallel()
	proc := newFakeProc(t)
	proc.add(202, serverComm, "tmux", "-L", "orrery-test-300-abc")
	var sent []signal
	r := proc.reaper(&sent)
	r.Signal = func(int, syscall.Signal) error { return syscall.EPERM }
	if _, err := r.Reap(); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("Reap = %v, want the EPERM", err)
	}
}

func TestReapOfAMissingProcessTableFails(t *testing.T) {
	t.Parallel()
	r := Reaper{ProcRoot: filepath.Join(t.TempDir(), "missing")}
	if _, err := r.Reap(); err == nil {
		t.Fatal("Reap of a missing process table = nil, want an error")
	}
}
