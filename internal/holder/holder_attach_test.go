package holder

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	// attachSegmentBytes is the spool segment size the attach tests use, so their output crosses segment boundaries.
	attachSegmentBytes = 16
	// attachSettle is how long a test watches a stream for a frame that must not come.
	attachSettle = 200 * time.Millisecond
	// attachTestLen is the length of the output the tail-fed attach tests start from.
	attachTestLen = 100
	// attachFlag is the file the sh child waits for before it writes its payload.
	attachFlag = "go.flag"
	// attachPayload is the file the sh child copies to its stdout.
	attachPayload = "payload.bin"
	// attachExitCode is the status the sh child exits with after its payload.
	attachExitCode = 3
)

// tailChild returns the child that copies file to its stdout from the first byte and then follows the file, so the
// test decides what the child writes and when.
func tailChild(t *testing.T, file string) ChildConfig {
	t.Helper()
	tail, err := exec.LookPath("tail")
	if err != nil {
		t.Fatalf("find tail: %v", err)
	}
	return ChildConfig{Path: tail, Args: []string{"-n", "+1", "-f", file}, Dir: t.TempDir()}
}

// newStdoutFile writes data to a new file in a new temporary directory and returns the file's path.
func newStdoutFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdout.txt")
	if err := os.WriteFile(path, data, filesTestMode); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
	return path
}

// appendStdout appends data to the file a tail child follows, so the child writes data to its stdout.
func appendStdout(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	_, err = f.Write(data)
	if err = errors.Join(err, f.Close()); err != nil {
		t.Fatalf("append to %s: %v", path, err)
	}
}

// spoolOnDisk returns the bytes of the spool's segment files in dir, in offset order.
func spoolOnDisk(t *testing.T, dir string) []byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	var all []byte
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), segmentPrefix) {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			t.Fatalf("ReadFile %s: %v", entry.Name(), readErr)
		}
		all = append(all, raw...)
	}
	return all
}

// attachAt sends attach at offset and returns the holder's answer, which must be an attached frame.
func (c *client) attachAt(offset int64) Frame {
	c.t.Helper()
	c.send(Frame{Type: TypeAttach, ReplayFrom: offset})
	answer := c.read()
	if answer.Type != TypeAttached {
		c.t.Fatalf("answer to attach at %d = %+v, want an attached frame", offset, answer)
	}
	return answer
}

// attachRefused sends attach at offset and returns the message of the error frame that refuses it. The holder closes
// the connection after that frame, which this checks too.
func (c *client) attachRefused(offset int64) string {
	c.t.Helper()
	c.send(Frame{Type: TypeAttach, ReplayFrom: offset})
	answer := c.read()
	if answer.Type != TypeError || answer.Message == "" {
		c.t.Fatalf("answer to attach at %d = %+v, want an error frame with a message", offset, answer)
	}
	c.expectClosed()
	return answer.Message
}

// sendAck sends an ack of consumed and replayFrom. A valid ack gets no answer.
func (c *client) sendAck(consumed, replayFrom int64) {
	c.t.Helper()
	c.send(Frame{Type: TypeAck, Consumed: consumed, ReplayFrom: replayFrom})
}

// sendStop sends stop from the controller.
func (c *client) sendStop() {
	c.t.Helper()
	c.send(Frame{Type: TypeStop})
}

// detach ends the controller's side of the connection. The holder reads end of stream and drops the connection, which
// detaches it; detach returns once the holder has closed the connection, so a later attach sees the detach done. Until
// the holder sees the end of stream it goes on streaming, so out and written frames already in flight are read and
// dropped; any other frame, or no close within the read deadline, fails the test.
func (c *client) detach() {
	c.t.Helper()
	if err := c.conn.CloseWrite(); err != nil {
		c.t.Fatalf("close write: %v", err)
	}
	for {
		c.setDeadline()
		frame, err := c.frames.Read()
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil || (frame.Type != TypeOut && frame.Type != TypeWritten) {
			c.t.Fatalf("read after detach: frame %+v, err %v; want in-flight out or written frames, then a close",
				frame, err)
		}
	}
}

// readOut reads out frames until n bytes starting at offset from have arrived, and returns them. Each frame must start
// where the one before it ended and carry at most ChunkBytes. A frame that runs past n fails the test.
func (c *client) readOut(from int64, n int) []byte {
	c.t.Helper()
	got := make([]byte, 0, n)
	for len(got) < n {
		frame := c.read()
		want := from + int64(len(got))
		if frame.Type != TypeOut || frame.Offset != want {
			c.t.Fatalf("frame %+v, want out at offset %d", frame, want)
		}
		if len(frame.Data) == 0 || len(frame.Data) > ChunkBytes || len(got)+len(frame.Data) > n {
			c.t.Fatalf("out frame at offset %d carries %d bytes with %d of %d still due", want, len(frame.Data),
				n-len(got), n)
		}
		got = append(got, frame.Data...)
	}
	return got
}

// readExit returns the record in the next frame, which must be an exit frame.
func (c *client) readExit() Exit {
	c.t.Helper()
	frame := c.read()
	if frame.Type != TypeExit || frame.Exit == nil {
		c.t.Fatalf("frame %+v, want an exit frame", frame)
	}
	return *frame.Exit
}

// expectSilent fails the test when the holder sends a frame within attachSettle, which a held stream must not do.
func (c *client) expectSilent() {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(attachSettle)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	frame, err := c.frames.Read()
	var netErr net.Error
	if err == nil || !errors.As(err, &netErr) || !netErr.Timeout() {
		c.t.Fatalf("read on a held stream: frame %+v, err %v; want no frame before the deadline", frame, err)
	}
}

// attachTailHolder starts a holder whose child copies content from a file to its stdout and then follows the file. It
// returns the holder's run, its config and the file the child follows.
func attachTailHolder(t *testing.T, spool SpoolConfig, content []byte) (*running, Config, string) {
	t.Helper()
	cfg := newHolderConfig(t)
	cfg.Spool = spool
	file := newStdoutFile(t, content)
	cfg.Child = tailChild(t, file)
	return startHolder(t.Context(), t, cfg), cfg, file
}

func TestHolderAttachAtZeroStreamsSpoolInOrder(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, attachTestLen)
	run, cfg, _ := attachTailHolder(t, SpoolConfig{SegmentBytes: attachSegmentBytes}, content)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)

	if attached := conn.attachAt(0); attached.Start != 0 {
		t.Errorf("attached start = %d, want 0", attached.Start)
	}
	got := conn.readOut(0, len(content))
	if !bytes.Equal(got, content) {
		t.Errorf("out bytes differ from the child's output: got %d bytes, want %d", len(got), len(content))
	}
	if disk := spoolOnDisk(t, cfg.Dir); !bytes.Equal(disk, content) {
		t.Errorf("spool on disk holds %d bytes, want the %d the controller read", len(disk), len(content))
	}
}

func TestHolderAttachAtMidOffsetStartsThere(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, attachTestLen)
	run, cfg, _ := attachTailHolder(t, SpoolConfig{SegmentBytes: attachSegmentBytes}, content)
	sock := socketOf(t, cfg.Dir)

	first := dialHolder(t, run, sock)
	first.hello(WireVersion)
	first.attachAt(0)
	// Reading every byte makes the whole output retained, so any offset up to the end may be replayed.
	first.readOut(0, len(content))
	first.detach()

	const from = 37
	second := dialHolder(t, run, sock)
	second.hello(WireVersion)
	attached := second.attachAt(from)
	if attached.End != attachTestLen {
		t.Errorf("attached end = %d, want %d", attached.End, attachTestLen)
	}
	got := second.readOut(from, len(content)-from)
	if !bytes.Equal(got, content[from:]) {
		t.Errorf("out bytes from offset %d differ from the child's output", from)
	}
}

func TestHolderAttachBelowStartAfterAckIsRefusedAndCloses(t *testing.T) {
	t.Parallel()

	// With segments of attachSegmentBytes, an ack at replayFrom 64 deletes every segment that ends at or below 64.
	const trimmed = 64
	content := spoolBytes(0, attachTestLen)
	run, cfg, _ := attachTailHolder(t, SpoolConfig{SegmentBytes: attachSegmentBytes}, content)
	sock := socketOf(t, cfg.Dir)

	conn := dialHolder(t, run, sock)
	conn.hello(WireVersion)
	conn.attachAt(0)
	conn.readOut(0, len(content))
	conn.sendAck(attachTestLen, trimmed)
	// The holder handles one connection's frames in order. The error for this second hello comes only after the ack
	// has been applied, so the refused attach below sees the trimmed range.
	conn.send(Frame{Type: TypeHello, Version: WireVersion})
	if got := conn.read(); got.Type != TypeError {
		t.Fatalf("answer to a second hello = %+v, want an error frame", got)
	}

	late := dialHolder(t, run, sock)
	late.hello(WireVersion)
	msg := late.attachRefused(0)
	if !strings.Contains(msg, "64") || !strings.Contains(msg, "100") {
		t.Errorf("refusal %q does not name the retained range [64, 100)", msg)
	}
}

func TestHolderCapStopsChildUntilAckRaisesConsumed(t *testing.T) {
	t.Parallel()

	const capBytes = 64
	content := spoolBytes(0, 200)
	run, cfg, _ := attachTailHolder(t, SpoolConfig{SegmentBytes: attachSegmentBytes, CapBytes: capBytes}, content)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)

	got := conn.readOut(0, capBytes)
	// The child has filled the cap and no ack has come, so nothing more may arrive.
	conn.expectSilent()
	// Each ack raises consumed by one cap, which lets the child write exactly one more cap of output.
	for from := int64(capBytes); from < int64(len(content)); from += capBytes {
		conn.sendAck(from, from)
		n := min(capBytes, len(content)-int(from))
		got = append(got, conn.readOut(from, n)...)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("out bytes differ from the child's output after the acks")
	}
}

func TestHolderSecondAttachReplacesTheFirstController(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, attachTestLen)
	run, cfg, file := attachTailHolder(t, SpoolConfig{}, content)
	sock := socketOf(t, cfg.Dir)

	first := dialHolder(t, run, sock)
	first.hello(WireVersion)
	first.attachAt(0)
	first.readOut(0, len(content))

	second := dialHolder(t, run, sock)
	second.hello(WireVersion)
	second.attachAt(0)
	first.expectClosed()

	appendStdout(t, file, spoolBytes(attachTestLen, 50))
	got := second.readOut(0, attachTestLen+50)
	if !bytes.Equal(got, spoolBytes(0, attachTestLen+50)) {
		t.Errorf("replacing controller's out bytes differ from the child's output")
	}
}

func TestHolderReattachAtConsumedOffsetGetsBytesWrittenWhileDetached(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, attachTestLen)
	run, cfg, file := attachTailHolder(t, SpoolConfig{SegmentBytes: attachSegmentBytes}, content)
	sock := socketOf(t, cfg.Dir)

	first := dialHolder(t, run, sock)
	first.hello(WireVersion)
	first.attachAt(0)
	first.readOut(0, len(content))
	first.sendAck(attachTestLen, attachTestLen)
	first.detach()

	// The child writes these with no controller attached. They must be spooled, not lost.
	appendStdout(t, file, spoolBytes(attachTestLen, 300))
	second := dialHolder(t, run, sock)
	second.hello(WireVersion)
	second.attachAt(attachTestLen)
	got := second.readOut(attachTestLen, 300)
	if !bytes.Equal(got, spoolBytes(attachTestLen, 300)) {
		t.Errorf("bytes written while detached differ from the child's output")
	}
}

func TestHolderReattachCarriesAckedConsumedAndReplayFrom(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, attachTestLen)
	run, cfg, _ := attachTailHolder(t, SpoolConfig{SegmentBytes: attachSegmentBytes}, content)
	sock := socketOf(t, cfg.Dir)

	first := dialHolder(t, run, sock)
	first.hello(WireVersion)
	if fresh := first.attachAt(0); fresh.Consumed != 0 || fresh.ReplayFrom != 0 {
		t.Errorf("fresh attached frame carries consumed %d and replayFrom %d, want 0 and 0",
			fresh.Consumed, fresh.ReplayFrom)
	}
	first.readOut(0, len(content))
	const consumed, replayFrom = 80, 64
	first.sendAck(consumed, replayFrom)
	first.detach()

	second := dialHolder(t, run, sock)
	second.hello(WireVersion)
	attached := second.attachAt(replayFrom)
	if attached.Consumed != consumed || attached.ReplayFrom != replayFrom {
		t.Errorf("re-attached frame carries consumed %d and replayFrom %d, want %d and %d",
			attached.Consumed, attached.ReplayFrom, consumed, replayFrom)
	}
	got := second.readOut(replayFrom, len(content)-replayFrom)
	if !bytes.Equal(got, content[replayFrom:]) {
		t.Errorf("replay from offset %d differs from the child's output", replayFrom)
	}
}

func TestHolderChildEndingWhileAttachedDeliversEveryByteThenExit(t *testing.T) {
	t.Parallel()

	childDir := t.TempDir()
	// The payload is larger than ChunkBytes, so it cannot come in one out frame.
	payload := spoolBytes(0, 200<<10)
	if err := os.WriteFile(filepath.Join(childDir, attachPayload), payload, filesTestMode); err != nil {
		t.Fatalf("WriteFile payload: %v", err)
	}
	cfg := newHolderConfig(t)
	script := "while [ ! -e " + attachFlag + " ]; do sleep 0.01; done; cat " + attachPayload + "; exit 3"
	cfg.Child = ChildConfig{Path: shPath(t), Args: []string{"-c", script}, Dir: childDir}
	run := startHolder(t.Context(), t, cfg)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)

	// The child waits for this flag, written only once the controller is attached, so every byte goes to it live.
	if err := os.WriteFile(filepath.Join(childDir, attachFlag), nil, filesTestMode); err != nil {
		t.Fatalf("WriteFile flag: %v", err)
	}
	got := conn.readOut(0, len(payload))
	if !bytes.Equal(got, payload) {
		t.Errorf("out bytes differ from the child's payload")
	}
	exit := conn.readExit()
	if exit.Reason != ReasonExited || exit.Code != attachExitCode || exit.StdoutEnd != int64(len(payload)) {
		t.Errorf("exit = %+v, want reason %q, code %d, stdoutEnd %d", exit, ReasonExited, attachExitCode, len(payload))
	}
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if onDisk := readExitFile(t, cfg.Dir); onDisk != exit {
		t.Errorf("exit frame %+v differs from exit.json %+v", exit, onDisk)
	}
}

func TestHolderStopWhileAttachedSendsExitThenStopped(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, attachTestLen)
	run, cfg, _ := attachTailHolder(t, SpoolConfig{}, content)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)
	conn.readOut(0, len(content))

	conn.sendStop()
	exit := conn.readExit()
	if exit.Reason != ReasonStopped {
		t.Errorf("exit reason = %q, want %q", exit.Reason, ReasonStopped)
	}
	if got := conn.read(); got.Type != TypeStopped {
		t.Errorf("frame after exit = %+v, want a stopped frame", got)
	}
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if onDisk := readExitFile(t, cfg.Dir); onDisk != exit {
		t.Errorf("exit frame %+v differs from exit.json %+v", exit, onDisk)
	}
}

func TestHolderAttachBeyondEndIsRefusedAndCloses(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, attachTestLen)
	run, cfg, _ := attachTailHolder(t, SpoolConfig{}, content)
	sock := socketOf(t, cfg.Dir)
	first := dialHolder(t, run, sock)
	first.hello(WireVersion)
	first.attachAt(0)
	first.readOut(0, len(content))

	late := dialHolder(t, run, sock)
	late.hello(WireVersion)
	if msg := late.attachRefused(attachTestLen + 1); !strings.Contains(msg, "101") {
		t.Errorf("refusal %q does not name offset 101", msg)
	}
}

func TestHolderAckBeyondEndIsRefusedAndCloses(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, attachTestLen)
	run, cfg, _ := attachTailHolder(t, SpoolConfig{}, content)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)
	conn.readOut(0, len(content))

	conn.sendAck(attachTestLen+1, 0)
	if got := conn.read(); got.Type != TypeError || got.Message == "" {
		t.Fatalf("answer to an ack past the end = %+v, want an error frame with a message", got)
	}
	conn.expectClosed()
}

func TestHolderAckFromUnattachedConnectionIsRefusedAndCloses(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)

	conn.sendAck(0, 0)
	if got := conn.read(); got.Type != TypeError || got.Message == "" {
		t.Fatalf("answer to an ack from an unattached connection = %+v, want an error frame", got)
	}
	conn.expectClosed()
}

func TestHolderAttachFromAttachedConnectionIsRefusedAndCloses(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, attachTestLen)
	run, cfg, _ := attachTailHolder(t, SpoolConfig{}, content)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)
	conn.readOut(0, len(content))

	if msg := conn.attachRefused(0); msg == "" {
		t.Errorf("refusal of a second attach on the same connection has no message")
	}
}
