package paneregistry

import (
	"os"
	"path/filepath"
	"testing"
)

// unparsableEntry is a registry content that Parse rejects, named for the defect it has.
type unparsableEntry struct {
	name string
	data []byte
}

// unparsableEntries returns the contents Parse rejects: text that is not JSON, an entry lacking its status, an entry
// lacking its session id, and an entry whose status is none of busy, waiting, idle and shell. Each one is otherwise the
// entry of pid 4242 in session sess-invented-1, which names pane %9.
func unparsableEntries(t *testing.T) []unparsableEntry {
	t.Helper()
	whole := cliEntry(4242, "sess-invented-1", "testsrv:@7.%9")
	noStatus := whole
	noStatus.Status = ""
	noSession := whole
	noSession.SessionID = ""
	unknown := whole
	unknown.Status = "running"
	return []unparsableEntry{
		{name: "not JSON", data: []byte(`{"pid":4242,"sessionId":"sess-invented-1","status":`)},
		{name: "no status", data: entryData(t, noStatus)},
		{name: "no sessionId", data: entryData(t, noSession)},
		{name: "status outside busy waiting idle shell", data: entryData(t, unknown)},
	}
}

// settle publishes a whole entry of pid 4243, which binds pane %10, and takes the event it makes. Watch handles the
// changes of a registry in order, so once that event is taken, every read before it has been handled.
func settle(t *testing.T, dir string, r *rig) {
	t.Helper()
	publish(t, dir, "4243.json", entryData(t, cliEntry(4243, "sess-invented-2", "testsrv:@7.%10")))
	expectBinding(t, r.next(t), "%10", "sess-invented-2")
}

// expectNoNotice fails the test when Unreadable has been called and not yet taken.
func expectNoNotice(t *testing.T, r *rig) {
	t.Helper()
	if n := len(r.unreadable); n != 0 {
		t.Fatalf("Unreadable called %d more time(s), want none", n)
	}
}

// expectNotice fails the test unless the next Unreadable call names path and carries an error.
func expectNotice(t *testing.T, r *rig, path string) {
	t.Helper()
	call := r.nextUnreadable(t)
	if call.path != path || call.err == nil {
		t.Fatalf("Unreadable(%q, %v), want %q with an error", call.path, call.err, path)
	}
}

// TestPaneRegistryUnreadableReportsOnceWhenStillUnreadable checks that an entry which cannot be parsed raises no notice
// on its first failed read, raises one notice naming it on the next read that fails too, and raises no notice on a
// later failed read.
func TestPaneRegistryUnreadableReportsOnceWhenStillUnreadable(t *testing.T) {
	t.Parallel()
	for _, bad := range unparsableEntries(t) {
		t.Run(bad.name, func(t *testing.T) {
			t.Parallel()
			dir, proc := readyRegistry(t)
			proc.add(4242, cliEnv()).add(4243, cliEnv())
			r := startWatch(t, dir, proc)
			expectBinding(t, r.next(t), readyPane, readySession)

			publish(t, dir, "4242.json", bad.data)
			settle(t, dir, r)
			expectNoNotice(t, r)

			publish(t, dir, "4242.json", bad.data)
			expectNotice(t, r, filepath.Join(dir, "4242.json"))

			writeInPlace(t, dir, bad.data)
			settle(t, dir, r)
			expectNoNotice(t, r)
		})
	}
}

// TestPaneRegistryUnreadableKeepsPaneStateWhileUnreadable checks that an entry which turns unparsable keeps the binding
// of its pane: the failed reads emit no event, so the next event is the unbinding that removing the entry makes.
func TestPaneRegistryUnreadableKeepsPaneStateWhileUnreadable(t *testing.T) {
	t.Parallel()
	for _, bad := range unparsableEntries(t) {
		t.Run(bad.name, func(t *testing.T) {
			t.Parallel()
			dir, proc := readyRegistry(t)
			r := startWatch(t, dir, proc.add(4242, cliEnv()))
			expectBinding(t, r.next(t), readyPane, readySession)

			publish(t, dir, "4242.json", entryData(t, cliEntry(4242, "sess-invented-1", "testsrv:@7.%9")))
			bound := expectBinding(t, r.next(t), "%9", "sess-invented-1")

			publish(t, dir, "4242.json", bad.data)
			publish(t, dir, "4242.json", bad.data)
			expectNotice(t, r, filepath.Join(dir, "4242.json"))
			writeInPlace(t, dir, bad.data)

			if err := os.Remove(filepath.Join(dir, "4242.json")); err != nil {
				t.Fatalf("remove entry: %v", err)
			}
			expectEvent(t, r.next(t), Event{Binding: bound, Unbound: true})
		})
	}
}

// TestPaneRegistryUnreadableLeavesPaneWithNoStateUnset checks that an entry which is unparsable from the start binds no
// pane and emits no event, so the pane it names stays unset.
func TestPaneRegistryUnreadableLeavesPaneWithNoStateUnset(t *testing.T) {
	t.Parallel()
	for _, bad := range unparsableEntries(t) {
		t.Run(bad.name, func(t *testing.T) {
			t.Parallel()
			dir, proc := readyRegistry(t)
			proc.add(4242, cliEnv()).add(4243, cliEnv())
			r := startWatch(t, dir, proc)
			expectBinding(t, r.next(t), readyPane, readySession)

			publish(t, dir, "4242.json", bad.data)
			publish(t, dir, "4242.json", bad.data)
			expectNotice(t, r, filepath.Join(dir, "4242.json"))

			settle(t, dir, r)
		})
	}
}

// TestPaneRegistryUnreadableTornWriteThenCompleteNoticesNothing checks that an entry read torn, and then read whole
// after a complete write, raises no notice and binds the pane the whole entry names.
func TestPaneRegistryUnreadableTornWriteThenCompleteNoticesNothing(t *testing.T) {
	t.Parallel()
	dir, proc := readyRegistry(t)
	proc.add(4242, cliEnv()).add(4243, cliEnv())
	r := startWatch(t, dir, proc)
	expectBinding(t, r.next(t), readyPane, readySession)

	whole := entryData(t, cliEntry(4242, "sess-invented-1", "testsrv:@7.%9"))
	publish(t, dir, "4242.json", whole[:len(whole)/2])
	settle(t, dir, r)
	writeInPlace(t, dir, whole)

	expectBinding(t, r.next(t), "%9", "sess-invented-1")
	expectNoNotice(t, r)
}

// TestPaneRegistryUnreadableNoticesAgainAfterWholeRead checks that once an entry has been read whole, a later failure
// starts over: it is held on its first failed read, and raises one notice on the next.
func TestPaneRegistryUnreadableNoticesAgainAfterWholeRead(t *testing.T) {
	t.Parallel()
	bad := unparsableEntries(t)[0].data
	dir, proc := readyRegistry(t)
	proc.add(4242, cliEnv()).add(4243, cliEnv())
	r := startWatch(t, dir, proc)
	expectBinding(t, r.next(t), readyPane, readySession)

	publish(t, dir, "4242.json", bad)
	publish(t, dir, "4242.json", bad)
	expectNotice(t, r, filepath.Join(dir, "4242.json"))

	publish(t, dir, "4242.json", entryData(t, cliEntry(4242, "sess-invented-1", "testsrv:@7.%9")))
	expectBinding(t, r.next(t), "%9", "sess-invented-1")

	publish(t, dir, "4242.json", bad)
	settle(t, dir, r)
	expectNoNotice(t, r)

	publish(t, dir, "4242.json", bad)
	expectNotice(t, r, filepath.Join(dir, "4242.json"))
}
