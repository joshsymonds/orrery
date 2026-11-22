package paneregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/inotify"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Invented values the watch tests write: the home directory, the working directory of their sessions, the tmux server
// and proxy of their processes, and the pane and session of the ready entry.
const (
	testHome       = "/home/tester"
	testCwd        = "/home/tester/work/app"
	testTmux       = "/tmp/tmux-1000/testsrv,900,0"
	testBaseURL    = "http://127.0.0.1:9/ctx/tester"
	readyPane      = "%1"
	readySession   = "sess-invented-ready"
	readyPID       = 1001
	readyTmux      = "testsrv:@1.%1"
	unreadablePath = "5001.json"
)

// fakeProc is a Proc over invented processes: a pid is alive when alive names it, and its environment is env[pid]. A
// pid with no environment fails Environ. Tests fill it before Watch starts, so Watch reads it without a lock.
type fakeProc struct {
	alive map[int]bool
	env   map[int]map[string]string
}

func (p *fakeProc) Alive(pid int) bool {
	return p.alive[pid]
}

func (p *fakeProc) Environ(pid int) (map[string]string, error) {
	env, ok := p.env[pid]
	if !ok {
		return nil, fmt.Errorf("no environment for pid %d", pid)
	}
	return env, nil
}

// newProc returns a process table with no process in it.
func newProc() *fakeProc {
	return &fakeProc{alive: map[int]bool{}, env: map[int]map[string]string{}}
}

// add makes pid a live process with environment env, and returns p.
func (p *fakeProc) add(pid int, env map[string]string) *fakeProc {
	p.alive[pid] = true
	p.env[pid] = env
	return p
}

// cliEnv returns the environment of an interactive session in the tmux server of testTmux, behind the test proxy.
func cliEnv() map[string]string {
	return map[string]string{"TMUX": testTmux, "ANTHROPIC_BASE_URL": testBaseURL}
}

// cliEntry returns the entry of an interactive session of pid, with the given session id and tmux field.
func cliEntry(pid int, sessionID, tmux string) Entry {
	return Entry{
		PID: pid, SessionID: sessionID, Cwd: testCwd, Entrypoint: EntrypointCLI, Status: StatusIdle, Tmux: tmux,
	}
}

// entryData encodes entry as Claude Code writes an entry.
func entryData(t *testing.T, entry Entry) []byte {
	t.Helper()
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	return data
}

// publish puts data in dir as name, the way an entry is replaced: a temporary file, named so that it is not an entry, is
// written and renamed into place. The watch sees one move, never a partly written entry.
func publish(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", tmp, err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		t.Fatalf("rename %s into place: %v", name, err)
	}
}

// writeInPlace overwrites the entry 4242.json in dir with data of the same length, in one write, so the watch sees one
// modify of the entry.
func writeInPlace(t *testing.T, dir string, data []byte) {
	t.Helper()
	const name = "4242.json"
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer file.Close()
	if _, err = file.WriteAt(data, 0); err != nil {
		t.Fatalf("write %s in place: %v", name, err)
	}
}

// readyRegistry returns a registry directory that holds the entry of the ready session, and a process table in which
// that session's process is alive. The ready session is bound before Watch starts, so its binding is the first event
// of a Watch over the directory. Watch adds its watch before it reads the entries, so that event proves the watch is in
// place for the changes the test makes after it.
func readyRegistry(t *testing.T) (string, *fakeProc) {
	t.Helper()
	dir := t.TempDir()
	publish(t, dir, "1001.json", entryData(t, cliEntry(readyPID, readySession, readyTmux)))
	return dir, newProc().add(readyPID, cliEnv())
}

// unreadableCall is one call of Unreadable.
type unreadableCall struct {
	path string
	err  error
}

// rig is a Watch running over a registry directory. Events arrive on an unbuffered channel, so Watch waits for the test
// to take each one. Unreadable calls are buffered, so a call the test does not expect does not stall Watch.
type rig struct {
	events     chan Event
	unreadable chan unreadableCall
	done       chan struct{}
	err        error
}

// startWatch runs Watch over dir, with proc as its process table, in a goroutine. Its context ends at cleanup, and the
// test fails unless Watch then returns the context's error.
func startWatch(t *testing.T, dir string, proc Proc) *rig {
	t.Helper()
	ctx := t.Context()
	r := &rig{
		events:     make(chan Event),
		unreadable: make(chan unreadableCall, 16),
		done:       make(chan struct{}),
	}
	cfg := Config{
		Dir:  dir,
		Home: testHome,
		Proc: proc,
		Events: func(event Event) {
			select {
			case r.events <- event:
			case <-ctx.Done():
			}
		},
		Unreadable: func(path string, err error) {
			select {
			case r.unreadable <- unreadableCall{path: path, err: err}:
			case <-ctx.Done():
			}
		},
	}
	go func() {
		r.err = Watch(ctx, cfg)
		close(r.done)
	}()
	t.Cleanup(func() {
		<-r.done
		if !errors.Is(r.err, context.Canceled) {
			t.Errorf("Watch returned %v, want the context's error after it ended", r.err)
		}
	})
	return r
}

// next returns the next event. It fails the test when Watch returns first.
func (r *rig) next(t *testing.T) Event {
	t.Helper()
	select {
	case event := <-r.events:
		return event
	case <-r.done:
		t.Fatalf("Watch returned before its next event: %v", r.err)
	}
	return Event{}
}

// nextUnreadable returns the next call of Unreadable. It fails the test when Watch returns first.
func (r *rig) nextUnreadable(t *testing.T) unreadableCall {
	t.Helper()
	select {
	case call := <-r.unreadable:
		return call
	case <-r.done:
		t.Fatalf("Watch returned before its next Unreadable call: %v", r.err)
	}
	return unreadableCall{}
}

// expectQuiet fails the test when Watch has an event waiting to be taken. Call it right after an Unreadable call that
// comes after everything Watch emitted for the change, because Watch emits nothing once it has called Unreadable.
func (r *rig) expectQuiet(t *testing.T) {
	t.Helper()
	select {
	case event := <-r.events:
		t.Fatalf("event %+v, want none", event)
	default:
	}
}

// expectEvent fails the test unless got is want.
func expectEvent(t *testing.T, got, want Event) {
	t.Helper()
	if got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
}

// expectBinding fails the test unless event binds pane to sessionID, and returns the binding.
func expectBinding(t *testing.T, event Event, pane, sessionID string) Binding {
	t.Helper()
	if event.Unbound || event.Binding.Pane != pane || event.Binding.SessionID != sessionID {
		t.Fatalf("event = %+v, want pane %s bound to session %s", event, pane, sessionID)
	}
	return event.Binding
}

// queuedEventLimit returns the kernel's limit on the events an inotify instance queues.
func queuedEventLimit(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile("/proc/sys/fs/inotify/max_queued_events")
	if err != nil {
		t.Fatalf("read the queue limit: %v", err)
	}
	limit, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse the queue limit %q: %v", data, err)
	}
	return limit
}

// TestPaneRegistryBindCLIEntryBindsItsPane checks that a live interactive session's entry binds its tmux pane, and that
// the binding carries the session's transcript under the home directory, and the tmux server and proxy of the process
// environment rather than anything the entry names.
func TestPaneRegistryBindCLIEntryBindsItsPane(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	entry := Entry{
		PID: 4242, SessionID: "sess-invented-1", Cwd: testCwd, Entrypoint: EntrypointCLI,
		Status: StatusWaiting, WaitingFor: WaitingInput, Tmux: "testsrv:@7.%9",
	}
	publish(t, dir, "4242.json", entryData(t, entry))
	r := startWatch(t, dir, newProc().add(4242, cliEnv()))

	want := Event{Binding: Binding{
		File:       filepath.Join(dir, "4242.json"),
		PID:        4242,
		Pane:       "%9",
		Tmux:       testTmux,
		SessionID:  "sess-invented-1",
		Cwd:        testCwd,
		Transcript: "/home/tester/.claude/projects/-home-tester-work-app/sess-invented-1.jsonl",
		BaseURL:    testBaseURL,
		Status:     StatusWaiting,
		WaitingFor: WaitingInput,
	}}
	expectEvent(t, r.next(t), want)
}

// TestPaneRegistryBindTakesPaneFromEnvWithoutTmuxField checks that an entry with no tmux field binds the pane named by
// the process's TMUX_PANE.
func TestPaneRegistryBindTakesPaneFromEnvWithoutTmuxField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	publish(t, dir, "4242.json", entryData(t, cliEntry(4242, "sess-invented-1", "")))
	env := cliEnv()
	env["TMUX_PANE"] = "%3"
	r := startWatch(t, dir, newProc().add(4242, env))

	expectBinding(t, r.next(t), "%3", "sess-invented-1")
}

// TestPaneRegistryBindCreatedAndReplacedEntriesBind checks that an entry created after Watch starts binds its pane, and
// that an entry replaced by another rebinds the pane to the new session.
func TestPaneRegistryBindCreatedAndReplacedEntriesBind(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	r := startWatch(t, dir, proc.add(4242, cliEnv()))
	expectBinding(t, r.next(t), readyPane, readySession)

	publish(t, dir, "4242.json", entryData(t, cliEntry(4242, "sess-invented-1", "testsrv:@7.%9")))
	expectBinding(t, r.next(t), "%9", "sess-invented-1")

	publish(t, dir, "4242.json", entryData(t, cliEntry(4242, "sess-invented-2", "testsrv:@7.%9")))
	expectBinding(t, r.next(t), "%9", "sess-invented-2")
}

// TestPaneRegistryBindEntryWrittenInPlaceRebinds checks that an entry rewritten in place rebinds its pane to the session
// the new contents name.
func TestPaneRegistryBindEntryWrittenInPlaceRebinds(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	r := startWatch(t, dir, proc.add(4242, cliEnv()))
	expectBinding(t, r.next(t), readyPane, readySession)

	publish(t, dir, "4242.json", entryData(t, cliEntry(4242, "sess-invented-a", "testsrv:@7.%9")))
	expectBinding(t, r.next(t), "%9", "sess-invented-a")

	writeInPlace(t, dir, entryData(t, cliEntry(4242, "sess-invented-b", "testsrv:@7.%9")))
	expectBinding(t, r.next(t), "%9", "sess-invented-b")
}

// TestPaneRegistryBindRemovedEntryUnbindsItsPane checks that removing an entry unbinds the pane it bound, and that the
// unbinding carries the binding it ends.
func TestPaneRegistryBindRemovedEntryUnbindsItsPane(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	r := startWatch(t, dir, proc.add(4242, cliEnv()))
	expectBinding(t, r.next(t), readyPane, readySession)

	publish(t, dir, "4242.json", entryData(t, cliEntry(4242, "sess-invented-1", "testsrv:@7.%9")))
	bound := expectBinding(t, r.next(t), "%9", "sess-invented-1")

	if err := os.Remove(filepath.Join(dir, "4242.json")); err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	expectEvent(t, r.next(t), Event{Binding: bound, Unbound: true})
}

// TestPaneRegistryBindSharedPaneGoesToMostRecentEntry checks that when several entries name one pane, removing one
// leaves the pane bound to the entry read most recently among those that remain, and unbinds it only when none remain.
func TestPaneRegistryBindSharedPaneGoesToMostRecentEntry(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	for _, pid := range []int{1002, 1003, 1004} {
		publish(t, dir, fmt.Sprintf("%d.json", pid),
			entryData(t, cliEntry(pid, fmt.Sprintf("sess-invented-%d", pid), "testsrv:@7.%9")))
		proc.add(pid, cliEnv())
	}
	r := startWatch(t, dir, proc)
	expectBinding(t, r.next(t), readyPane, readySession)
	first := expectBinding(t, r.next(t), "%9", "sess-invented-1002")
	second := expectBinding(t, r.next(t), "%9", "sess-invented-1003")
	expectBinding(t, r.next(t), "%9", "sess-invented-1004")

	if err := os.Remove(filepath.Join(dir, "1004.json")); err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	expectEvent(t, r.next(t), Event{Binding: second})

	if err := os.Remove(filepath.Join(dir, "1003.json")); err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	expectEvent(t, r.next(t), Event{Binding: first})

	if err := os.Remove(filepath.Join(dir, "1002.json")); err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	expectEvent(t, r.next(t), Event{Binding: first, Unbound: true})
}

// TestPaneRegistryBindSamePaneIDOnAnotherServerIsADifferentPane checks that pane ids on two tmux servers are two panes:
// removing the entry on one server leaves the entry on the other alone, and unbinds only its own pane.
func TestPaneRegistryBindSamePaneIDOnAnotherServerIsADifferentPane(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	other := cliEnv()
	other["TMUX"] = "/tmp/tmux-1000/othersrv,901,0"
	proc.add(1002, cliEnv()).add(1003, other)
	publish(t, dir, "1002.json", entryData(t, cliEntry(1002, "sess-invented-a", "testsrv:@7.%9")))
	publish(t, dir, "1003.json", entryData(t, cliEntry(1003, "sess-invented-b", "othersrv:@7.%9")))
	r := startWatch(t, dir, proc)
	expectBinding(t, r.next(t), readyPane, readySession)
	expectBinding(t, r.next(t), "%9", "sess-invented-a")
	bound := expectBinding(t, r.next(t), "%9", "sess-invented-b")

	if err := os.Remove(filepath.Join(dir, "1003.json")); err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	expectEvent(t, r.next(t), Event{Binding: bound, Unbound: true})
}

// TestPaneRegistryBindSdkAndDeadEntriesBindNothing checks that an entry of another entrypoint, and an entry whose pid is
// not alive, bind nothing, even when their panes are named.
func TestPaneRegistryBindSdkAndDeadEntriesBindNothing(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	r := startWatch(t, dir, proc.add(2001, cliEnv()))
	expectBinding(t, r.next(t), readyPane, readySession)

	sdk := Entry{PID: 2001, SessionID: "sess-invented-sdk", Cwd: testCwd, Entrypoint: "sdk-cli",
		Status: StatusBusy, Tmux: "testsrv:@7.%5"}
	publish(t, dir, "2001.json", entryData(t, sdk))
	publish(t, dir, "2002.json", entryData(t, cliEntry(2002, "sess-invented-dead", "testsrv:@7.%6")))
	proc.add(4242, cliEnv())
	publish(t, dir, "4242.json", entryData(t, cliEntry(4242, "sess-invented-1", "testsrv:@7.%9")))
	expectBinding(t, r.next(t), "%9", "sess-invented-1")
}

// TestPaneRegistryBindHeadlessAndSummarizerEntriesBindNothing checks that a process whose environment carries the
// marker of an orrery-started headless session, or the summarizer marker, binds nothing.
func TestPaneRegistryBindHeadlessAndSummarizerEntriesBindNothing(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	headless := cliEnv()
	headless[protocol.EnvSession] = "session-7"
	summarizer := cliEnv()
	summarizer[protocol.EnvSummarizer] = protocol.SummarizerValue
	r := startWatch(t, dir, proc.add(3001, headless).add(3002, summarizer).add(4242, cliEnv()))
	expectBinding(t, r.next(t), readyPane, readySession)

	publish(t, dir, "3001.json", entryData(t, cliEntry(3001, "sess-invented-headless", "testsrv:@7.%7")))
	publish(t, dir, "3002.json", entryData(t, cliEntry(3002, "sess-invented-summarizer", "testsrv:@7.%8")))
	publish(t, dir, "4242.json", entryData(t, cliEntry(4242, "sess-invented-1", "testsrv:@7.%9")))
	expectBinding(t, r.next(t), "%9", "sess-invented-1")
}

// TestPaneRegistryBindIgnoresNamesThatAreNotEntries checks that a file beside an entry, such as a <pid>.<hash>.key file,
// is not read as an entry.
func TestPaneRegistryBindIgnoresNamesThatAreNotEntries(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	r := startWatch(t, dir, proc.add(4242, cliEnv()).add(4243, cliEnv()))
	expectBinding(t, r.next(t), readyPane, readySession)

	publish(t, dir, "4242.0123abcd.key", entryData(t, cliEntry(4242, "sess-invented-key", "testsrv:@7.%9")))
	publish(t, dir, "4243.json", entryData(t, cliEntry(4243, "sess-invented-1", "testsrv:@7.%9")))
	expectBinding(t, r.next(t), "%9", "sess-invented-1")
}

// TestPaneRegistryBindUnparsableEntryKeepsItsBinding checks that an entry rewritten with contents that do not parse
// emits nothing, calls Unreadable with the entry's path once it is still unparsable at its next read, and keeps the
// binding it held.
func TestPaneRegistryBindUnparsableEntryKeepsItsBinding(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	r := startWatch(t, dir, proc.add(4242, cliEnv()))
	expectBinding(t, r.next(t), readyPane, readySession)

	publish(t, dir, "4242.json", entryData(t, cliEntry(4242, "sess-invented-1", "testsrv:@7.%9")))
	bound := expectBinding(t, r.next(t), "%9", "sess-invented-1")

	torn := []byte(`{"pid":4242,"sessionId":"sess-invented-1","status":`)
	publish(t, dir, "4242.json", torn)
	publish(t, dir, "4242.json", torn)
	call := r.nextUnreadable(t)
	if want := filepath.Join(dir, "4242.json"); call.path != want || call.err == nil {
		t.Fatalf("Unreadable(%q, %v), want %q with a parse error", call.path, call.err, want)
	}
	r.expectQuiet(t)

	if err := os.Remove(filepath.Join(dir, "4242.json")); err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	expectEvent(t, r.next(t), Event{Binding: bound, Unbound: true})
}

// TestPaneRegistryBindUnreadableEnvironmentEmitsNothing checks that a live session whose environment cannot be read
// emits nothing, and calls Unreadable with the entry's path.
func TestPaneRegistryBindUnreadableEnvironmentEmitsNothing(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	publish(t, dir, unreadablePath, entryData(t, cliEntry(5001, "sess-invented-5", "testsrv:@7.%5")))
	proc.alive[5001] = true
	r := startWatch(t, dir, proc)
	expectBinding(t, r.next(t), readyPane, readySession)

	call := r.nextUnreadable(t)
	if want := filepath.Join(dir, unreadablePath); call.path != want || call.err == nil {
		t.Fatalf("Unreadable(%q, %v), want %q with an environment error", call.path, call.err, want)
	}
	r.expectQuiet(t)
}

// TestPaneRegistryBindOverflowRereadsTheDirectory checks that when the kernel drops events, Watch rereads the directory
// and binds an entry whose event was dropped. Watch is held at its first event while the directory is flooded past the
// kernel's queue limit, so the queue overflows before Watch reads it.
func TestPaneRegistryBindOverflowRereadsTheDirectory(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	proc.add(1002, cliEnv())
	ctx := t.Context()
	parked := make(chan struct{})
	gate := make(chan struct{})
	unpark := sync.OnceFunc(func() { close(gate) })
	events := make(chan Event)
	var logs bytes.Buffer
	first := true // Events runs only on Watch's goroutine.
	cfg := Config{
		Dir:    dir,
		Home:   testHome,
		Proc:   proc,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
		Events: func(event Event) {
			if first {
				first = false
				close(parked)
				<-gate
			}
			select {
			case events <- event:
			case <-ctx.Done():
			}
		},
		Unreadable: func(path string, err error) {
			t.Errorf("Unreadable(%s): %v", path, err)
		},
	}
	var watchErr error
	done := make(chan struct{})
	go func() {
		watchErr = Watch(ctx, cfg)
		close(done)
	}()
	t.Cleanup(func() {
		unpark()
		<-done
		if !errors.Is(watchErr, context.Canceled) {
			t.Errorf("Watch returned %v, want the context's error after it ended", watchErr)
		}
	})

	<-parked
	for i := range queuedEventLimit(t) + 1 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("flood-%06d", i)), nil, 0o600); err != nil {
			t.Fatalf("write flood file: %v", err)
		}
	}
	publish(t, dir, "1002.json", entryData(t, cliEntry(1002, "sess-invented-2", "testsrv:@1.%2")))
	unpark()

	var event Event
	for {
		event = <-events
		if event.Binding.SessionID == "sess-invented-2" {
			break
		}
		expectBinding(t, event, readyPane, readySession)
	}
	expectBinding(t, event, "%2", "sess-invented-2")
	if !strings.Contains(logs.String(), "dropped") {
		t.Fatalf("Watch logged no overflow: %q", logs.String())
	}
}

// TestPaneRegistryBindOpenFailureIsReturned checks that Watch returns the error of its Open, and binds nothing.
func TestPaneRegistryBindOpenFailureIsReturned(t *testing.T) {
	t.Parallel()
	failure := errors.New("too many inotify instances")
	err := Watch(t.Context(), Config{
		Dir:  t.TempDir(),
		Proc: newProc(),
		Open: func() (*inotify.Inotify, error) { return nil, failure },
		Events: func(Event) {
			t.Error("Events called although Watch never opened")
		},
		Unreadable: func(string, error) {
			t.Error("Unreadable called although Watch never opened")
		},
	})
	if !errors.Is(err, failure) {
		t.Fatalf("Watch = %v, want %v", err, failure)
	}
}
