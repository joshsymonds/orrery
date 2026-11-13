package holder

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	// fakeCountName is the file the fake child writes its prompt count to in its working directory.
	fakeCountName = "fakeclaude.n"
	// turnEndMarker is the stream-json field the fake child's result line carries, one per finished turn.
	turnEndMarker = `"type":"result"`
	// blockedWriteBytes is more than a pipe holds, so a write of it blocks while the child reads nothing.
	blockedWriteBytes = 1 << 20
	// writeIDLimit is the longest write id the holder accepts, in bytes.
	writeIDLimit = 256
)

// streamSeen is what a controller has read from its stream: the stdout bytes from offset 0, the written frames in the
// order read, and the exit record once it arrives.
type streamSeen struct {
	out     []byte
	written []Frame
	exit    *Exit
}

// answered reports whether a written frame for id has arrived whose duplicate flag is duplicate.
func (s *streamSeen) answered(id string, duplicate bool) bool {
	return slices.ContainsFunc(s.written, func(f Frame) bool {
		return f.ID == id && f.Duplicate == duplicate
	})
}

// turns returns how many finished turns the stdout seen so far holds.
func (s *streamSeen) turns() int {
	return bytes.Count(s.out, []byte(turnEndMarker))
}

// readUntil reads the stream into seen until done reports true. Out frames must run on from the bytes already seen, and
// the only other frames allowed are written and exit.
func (c *client) readUntil(seen *streamSeen, done func() bool) {
	c.t.Helper()
	for !done() {
		frame := c.read()
		switch {
		case frame.Type == TypeOut && frame.Offset == int64(len(seen.out)):
			seen.out = append(seen.out, frame.Data...)
		case frame.Type == TypeWritten:
			seen.written = append(seen.written, frame)
		case frame.Type == TypeExit && frame.Exit != nil:
			seen.exit = frame.Exit
		default:
			c.t.Fatalf("frame %+v, want out at offset %d, written or exit", frame, len(seen.out))
		}
	}
}

// sendWrite sends a write of data under id from the controller.
func (c *client) sendWrite(id string, data []byte) {
	c.t.Helper()
	c.send(Frame{Type: TypeWrite, ID: id, Data: data})
}

// userLine returns the stream-json user line the fake child answers with the turn its word selects.
func userLine(word string) []byte {
	return []byte(`{"type":"user","text":"` + word + `"}` + "\n")
}

// childPromptCount returns the number of user lines the fake child has counted in childDir.
func childPromptCount(t *testing.T, childDir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(childDir, fakeCountName))
	if err != nil {
		t.Fatalf("read prompt count: %v", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse prompt count %q: %v", raw, err)
	}
	return n
}

// awaitReply reads from conn until the fake child has answered the prompt written under id with its reply and the
// turn has ended.
func awaitReply(conn *client, seen *streamSeen, id string) {
	conn.t.Helper()
	conn.readUntil(seen, func() bool { return seen.answered(id, false) && seen.turns() >= 1 })
}

func TestHolderWriteOfAPromptIsAnsweredWrittenAndItsReplyStreams(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)

	conn.sendWrite("p1", userLine("hello"))
	var seen streamSeen
	awaitReply(conn, &seen, "p1")
	if !bytes.Contains(seen.out, []byte(childReplyOne)) {
		t.Errorf("stream out %q does not carry the reply %q", seen.out, childReplyOne)
	}
	if n := childPromptCount(t, cfg.Child.Dir); n != 1 {
		t.Errorf("fake child counted %d prompts, want 1", n)
	}
}

func TestHolderRepeatedWriteIDIsAnsweredDuplicateAndNeverWritten(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)

	conn.sendWrite("p1", userLine("hello"))
	var seen streamSeen
	awaitReply(conn, &seen, "p1")

	conn.sendWrite("p1", userLine("again"))
	conn.readUntil(&seen, func() bool { return seen.answered("p1", true) })
	if n := childPromptCount(t, cfg.Child.Dir); n != 1 {
		t.Errorf("after the duplicate, fake child counted %d prompts, want 1", n)
	}

	// The child reads its stdin in order and ends on DIE, so once its exit is seen every line written before DIE has
	// been counted. A duplicate that reached the child would make the count 3.
	conn.sendWrite("d1", userLine("DIE"))
	conn.readUntil(&seen, func() bool { return seen.exit != nil })
	if n := childPromptCount(t, cfg.Child.Dir); n != 2 {
		t.Errorf("fake child counted %d prompts, want 2: the prompt and DIE, not the duplicate", n)
	}
}

func TestHolderAttachReportsWrittenIDsInOrderAcrossControllers(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)

	first := dialHolder(t, run, sock)
	first.hello(WireVersion)
	first.attachAt(0)
	first.sendWrite("p1", userLine("hello"))
	var seenFirst streamSeen
	awaitReply(first, &seenFirst, "p1")
	first.detach()

	second := dialHolder(t, run, sock)
	second.hello(WireVersion)
	if got := second.attachAt(0).Written; !slices.Equal(got, []string{"p1"}) {
		t.Fatalf("attached written = %q, want [p1]", got)
	}
	second.sendWrite("p2", userLine("next"))
	var seenSecond streamSeen
	second.readUntil(&seenSecond, func() bool { return seenSecond.answered("p2", false) && seenSecond.turns() >= 2 })
	second.detach()

	third := dialHolder(t, run, sock)
	third.hello(WireVersion)
	if got := third.attachAt(0).Written; !slices.Equal(got, []string{"p1", "p2"}) {
		t.Errorf("attached written = %q, want [p1 p2]", got)
	}
}

func TestHolderWriteBeforeAttachIsRefusedAndCloses(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)

	attached := dialHolder(t, run, sock)
	attached.hello(WireVersion)
	attached.attachAt(0)
	attached.sendWrite("p1", userLine("hello"))
	var seen streamSeen
	attached.readUntil(&seen, func() bool { return seen.answered("p1", false) })
	attached.detach()

	stranger := dialHolder(t, run, sock)
	stranger.hello(WireVersion)
	stranger.sendWrite("p2", userLine("next"))
	if got := stranger.read(); got.Type != TypeError || got.Message == "" {
		t.Fatalf("answer to a write before attach = %+v, want an error frame with a message", got)
	}
	stranger.expectClosed()

	// The refused write was not recorded, so the next controller to attach is told of p1 alone.
	next := dialHolder(t, run, sock)
	next.hello(WireVersion)
	if got := next.attachAt(0).Written; !slices.Equal(got, []string{"p1"}) {
		t.Errorf("attached written = %q, want [p1]", got)
	}
}

func TestHolderWriteIDOutsideOneToLimitIsRefusedAndCloses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
	}{
		{name: "empty", id: ""},
		{name: "one byte over the limit", id: strings.Repeat("x", writeIDLimit+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := newHolderConfig(t)
			run := startHolder(t.Context(), t, cfg)
			conn := dialHolder(t, run, socketOf(t, cfg.Dir))
			conn.hello(WireVersion)
			conn.attachAt(0)
			// A valid write first, so this test fails before the write path exists, not only at the refusal.
			conn.sendWrite("ok", userLine("hello"))
			var seen streamSeen
			awaitReply(conn, &seen, "ok")

			conn.sendWrite(tc.id, userLine("hello"))
			if got := conn.read(); got.Type != TypeError || got.Message == "" {
				t.Fatalf("answer to a write with a %d-byte id = %+v, want an error frame with a message",
					len(tc.id), got)
			}
			conn.expectClosed()
		})
	}
}

func TestHolderWriteAcceptsAnIDOfTheLimitBytes(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)

	id := strings.Repeat("x", writeIDLimit)
	conn.sendWrite(id, []byte("\n"))
	if got := conn.read(); got.Type != TypeWritten || got.ID != id || got.Duplicate {
		t.Fatalf("answer to an id of %d bytes = %+v, want written with that id", writeIDLimit, got)
	}
}

func TestHolderWriteBlockedOnFullPipeLeavesHolderServing(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	// The child never reads its stdin, so a write of blockedWriteBytes blocks until the child ends.
	cfg.Child = ChildConfig{Path: shPath(t), Args: []string{"-c", "exec sleep 60"}, Dir: t.TempDir()}
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)

	first := dialHolder(t, run, sock)
	first.hello(WireVersion)
	first.attachAt(0)
	first.sendWrite("b1", bytes.Repeat([]byte("x"), blockedWriteBytes))
	// b1 is still blocked on the pipe, so its repeat must be answered as a duplicate all the same.
	first.sendWrite("b1", []byte("\n"))
	var seen streamSeen
	first.readUntil(&seen, func() bool { return seen.answered("b1", true) })

	// The attach and the stop are served while b1 stays blocked.
	second := dialHolder(t, run, sock)
	second.hello(WireVersion)
	if got := second.attachAt(0).Written; !slices.Equal(got, []string{"b1"}) {
		t.Fatalf("attached written = %q, want [b1]", got)
	}
	second.sendStop()
	if exit := second.readExit(); exit.Reason != ReasonStopped {
		t.Errorf("exit reason = %q, want %q", exit.Reason, ReasonStopped)
	}
	if got := second.read(); got.Type != TypeStopped {
		t.Errorf("frame after exit = %+v, want a stopped frame", got)
	}
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
}

func TestHolderWritesReachTheChildInTheOrderReceived(t *testing.T) {
	t.Parallel()

	childDir := t.TempDir()
	cfg := newHolderConfig(t)
	// The child echoes its stdin only once the flag exists, so the first write blocks and the rest queue behind it.
	script := "while [ ! -e " + attachFlag + " ]; do sleep 0.01; done; exec cat"
	cfg.Child = ChildConfig{Path: shPath(t), Args: []string{"-c", script}, Dir: childDir}
	run := startHolder(t.Context(), t, cfg)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)

	first := bytes.Repeat([]byte("a"), blockedWriteBytes)
	conn.sendWrite("o1", first)
	conn.sendWrite("o2", []byte("b"))
	conn.sendWrite("o3", []byte("c"))
	// A repeat is answered at once, so once it arrives o2 and o3 are queued behind the blocked o1.
	conn.sendWrite("o3", []byte("c"))
	var seen streamSeen
	conn.readUntil(&seen, func() bool { return seen.answered("o3", true) })
	if err := os.WriteFile(filepath.Join(childDir, attachFlag), nil, filesTestMode); err != nil {
		t.Fatalf("WriteFile flag: %v", err)
	}

	want := append(append(first, 'b'), 'c')
	conn.readUntil(&seen, func() bool { return len(seen.out) >= len(want) })
	if !bytes.Equal(seen.out, want) {
		t.Fatalf("child echoed %d bytes ending %q, want the writes in the order received, ending %q", len(seen.out),
			seen.out[max(0, len(seen.out)-4):], want[len(want)-4:])
	}
	conn.sendStop()
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
}

func TestHolderDyingChildEndsWithExitAfterEveryOutByte(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)

	conn.sendWrite("d1", userLine("DIE"))
	var seen streamSeen
	conn.readUntil(&seen, func() bool { return seen.exit != nil })
	exit := *seen.exit
	if exit.Reason != ReasonExited || exit.Code != childDiedCode || exit.Stderr != childDiedStderr {
		t.Errorf("exit = %+v, want reason %q, code %d, stderr %q", exit, ReasonExited, childDiedCode, childDiedStderr)
	}
	// The exit frame follows the last out frame, so the stdout it reports as ended is all the controller has read.
	if exit.StdoutEnd != int64(len(seen.out)) || !bytes.Contains(seen.out, []byte(childDyingOne)) {
		t.Errorf("exit stdoutEnd %d after %d out bytes, want the whole DIE turn before exit",
			exit.StdoutEnd, len(seen.out))
	}
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if onDisk := readExitFile(t, cfg.Dir); onDisk != exit {
		t.Errorf("exit frame %+v differs from exit.json %+v", exit, onDisk)
	}
}
