package holder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/proc"
)

const (
	// reattachDataName is the file a reattach test's child copies to its stdout first.
	reattachDataName = "data.bin"
	// reattachSize is how many bytes that child copies. It is more than one default segment, so the first controller can
	// let the holder trim the first segment away.
	reattachSize = DefaultSegmentBytes + 1<<20
	// reattachMid is the offset the tests reattach at: inside the second segment, past the first controller's replay-from.
	reattachMid = DefaultSegmentBytes + 1000
	// reattachWriteID is the id the first controller writes under before it detaches.
	reattachWriteID = "first-controller"
	// reattachLiveLine is what the child prints after the two stdin lines the reattached controller writes.
	reattachLiveLine = "live\n"
	// reattachScript is the child a detached-spool test runs: it copies its data file to stdout, reads two stdin lines,
	// prints reattachLiveLine and exits with childDiedCode.
	reattachScript = "cat %s; read first; read second; printf 'live\\n'; exit %d"
	// reattachCancelAfter is how long a canceled-Reattach test lets Reattach run before it cancels the context.
	reattachCancelAfter = 200 * time.Millisecond
	// reattachCancelWait is how long a canceled Reattach may take to return.
	reattachCancelWait = 2 * time.Second
	// reattachSlack is how much longer than AttachTimeout a holder that never answers may keep Reattach.
	reattachSlack = 2 * time.Second
)

// launchHolder launches a real holder for testSession under root that runs child, and returns the launch's Process and
// the holder's dir. The holder is stopped when the test ends, if it still runs.
func launchHolder(t *testing.T, root string, child proc.Config) (*Process, string) {
	t.Helper()
	dir, err := SessionDir(filepath.Join(root, holdersName), testSession)
	if err != nil {
		t.Fatalf("SessionDir: %v", err)
	}
	stopServingHolder(t, dir)
	p, err := Launch(t.Context(), LaunchConfig{StateDir: root, Session: testSession, Child: child})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	return p, dir
}

// stopServingHolder stops, when the test ends, the holder that serves dir if one still runs, so a test that fails part
// way leaves no holder behind.
func stopServingHolder(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitArmedTimeout)
		defer cancel()
		if err := Stop(ctx, dir); err != nil {
			t.Errorf("Stop %s at cleanup: %v", dir, err)
		}
	})
}

// launchDetachedSpool launches a real holder whose child runs reattachScript over reattachSize bytes of spoolBytes. Its
// first controller reads every byte, lets the holder trim below the first default segment, writes reattachWriteID, and
// detaches. It returns the holder's dir.
func launchDetachedSpool(t *testing.T) string {
	t.Helper()
	child := launchChild(t, fmt.Sprintf(reattachScript, reattachDataName, childDiedCode))
	writeChildFile(t, child.Dir, reattachDataName, spoolBytes(0, reattachSize))
	p, dir := launchHolder(t, launchRoot(t), child)
	p.SetReplayFrom(DefaultSegmentBytes)
	if got := awaitWrite(t, startWriteWithID(t, p, reattachWriteID, []byte("one\n"))); got.err != nil {
		t.Fatalf("write the first line: %v", got.err)
	}
	data := make([]byte, reattachSize)
	if err := awaitResult(t, "read the child's output", startAsync(func() error {
		_, err := io.ReadFull(p.Stdout(), data)
		return err
	})); err != nil {
		t.Fatalf("read the child's %d bytes: %v", reattachSize, err)
	}
	if !bytes.Equal(data, spoolBytes(0, reattachSize)) {
		t.Fatalf("the first controller read bytes other than the child's output")
	}
	awaitSegmentFiles(t, dir, []string{segmentName(DefaultSegmentBytes)})
	if err := p.Detach(); err != nil {
		t.Fatalf("Detach the first controller: %v", err)
	}
	return dir
}

// rawHolderDir returns a session dir whose lock a sleep holds, as a live holder's lock is held, and listens on the dir's
// socket. The test answers the socket by hand.
func rawHolderDir(t *testing.T) (string, *net.UnixListener) {
	t.Helper()
	dir := sessionDir(t)
	startLockHolder(t, filepath.Join(dir, LockName))
	sock := socketOf(t, dir)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("listen on %s: %v", sock, err)
	}
	t.Cleanup(func() {
		if cerr := listener.Close(); cerr != nil {
			t.Errorf("close listener: %v", cerr)
		}
	})
	return dir, listener
}

// answerHello reads the controller's hello on server and answers it at the wire version.
func answerHello(t *testing.T, server *client) {
	t.Helper()
	if got := server.read(); got.Type != TypeHello {
		t.Fatalf("frame %+v, want the controller's hello", got)
	}
	server.send(Frame{Type: TypeHello, Version: WireVersion, Session: testSession})
}

// answerAttach answers the hello and the attach a Reattach sends on server, checking that the attach asks for replayFrom,
// and answers the attach with attached.
func answerAttach(t *testing.T, server *client, replayFrom int64, attached Frame) {
	t.Helper()
	answerHello(t, server)
	if got := server.read(); got.Type != TypeAttach || got.ReplayFrom != replayFrom {
		t.Fatalf("frame %+v, want an attach at %d", got, replayFrom)
	}
	server.send(attached)
}

func TestReattachReplaysFromTheOffsetThenGoesLive(t *testing.T) {
	t.Parallel()

	dir := launchDetachedSpool(t)
	p, written, err := Reattach(t.Context(), dir, reattachMid)
	if err != nil {
		t.Fatalf("Reattach at %d: %v", reattachMid, err)
	}
	if !slices.Equal(written, []string{reattachWriteID}) {
		t.Fatalf("written ids = %q, want the first controller's %q", written, reattachWriteID)
	}

	want := spoolBytes(reattachMid, reattachSize-reattachMid)
	got := make([]byte, len(want))
	if err = awaitResult(t, "read from the offset", startAsync(func() error {
		_, readErr := io.ReadFull(p.Stdout(), got)
		return readErr
	})); err != nil {
		t.Fatalf("read %d bytes from offset %d: %v", len(want), reattachMid, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("stdout from offset %d is not the spool's bytes from there", reattachMid)
	}

	if res := awaitWrite(t, startWrite(p, []byte("two\n"))); res.err != nil {
		t.Fatalf("write to the child after reattaching: %v", res.err)
	}
	rest := readToEnd(t, p.Stdout())
	if string(rest.data) != reattachLiveLine || !errors.Is(rest.err, io.EOF) {
		t.Fatalf("stdout after the write = %q, %v; want %q, then io.EOF", rest.data, rest.err, reattachLiveLine)
	}
	wantWait := fmt.Sprintf("exit status %d", childDiedCode)
	if waitErr := awaitResult(t, "Wait", startAsync(p.Wait)); waitErr == nil || waitErr.Error() != wantWait {
		t.Fatalf("Wait = %v, want %q from the exit record", waitErr, wantWait)
	}
	requireHolderRemoved(t, dir)
}

func TestReattachOutsideTheSpoolIsUnattachableAndTheHolderKeepsRunning(t *testing.T) {
	t.Parallel()

	dir := launchDetachedSpool(t)
	// The holder trimmed its first segment, so 0 is below the retained start, and one past the end is past the last byte.
	for _, off := range []int64{0, reattachSize + 1} {
		if _, _, err := Reattach(t.Context(), dir, off); !errors.Is(err, ErrUnattachable) {
			t.Fatalf("Reattach at %d = %v, want ErrUnattachable", off, err)
		}
	}
	p, _, err := Reattach(t.Context(), dir, reattachMid)
	if err != nil {
		t.Fatalf("Reattach at %d after the refusals: %v", reattachMid, err)
	}
	stopProcess(t, p)
	requireHolderRemoved(t, dir)
}

func TestReattachStopEndsTheHolderAndItsChild(t *testing.T) {
	t.Parallel()

	p1, dir := launchHolder(t, launchRoot(t), launchChild(t, "exec cat"))
	if err := p1.Detach(); err != nil {
		t.Fatalf("Detach the first controller: %v", err)
	}
	p, _, err := Reattach(t.Context(), dir, 0)
	if err != nil {
		t.Fatalf("Reattach: %v", err)
	}
	if got := awaitWrite(t, startWrite(p, []byte("ping\n"))); got.err != nil || got.n != len("ping\n") {
		t.Fatalf("Write(ping) = %d, %v; want %d, nil", got.n, got.err, len("ping\n"))
	}
	echoed := make([]byte, len("ping\n"))
	if err = awaitResult(t, "read the echo", startAsync(func() error {
		_, readErr := io.ReadFull(p.Stdout(), echoed)
		return readErr
	})); err != nil || string(echoed) != "ping\n" {
		t.Fatalf("echo = %q, %v; want \"ping\\n\", nil", echoed, err)
	}

	stopProcess(t, p)
	requireHolderRemoved(t, dir)
	waitErr := awaitResult(t, "Wait", startAsync(p.Wait))
	if waitErr == nil || waitErr.Error() != "signal: terminated" {
		t.Fatalf("Wait = %v, want signal: terminated", waitErr)
	}
}

func TestReattachOfAHolderThatHasEndedIsGone(t *testing.T) {
	t.Parallel()

	p1, dir := launchHolder(t, launchRoot(t), launchChild(t, "exec cat"))
	if err := p1.Detach(); err != nil {
		t.Fatalf("Detach the first controller: %v", err)
	}
	if err := Stop(t.Context(), dir); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, _, err := Reattach(t.Context(), dir, 0); !errors.Is(err, ErrGone) {
		t.Fatalf("Reattach of an ended holder = %v, want ErrGone", err)
	}
}

func TestReattachWithNoDirIsGone(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "missing")
	if _, _, err := Reattach(t.Context(), dir, 0); !errors.Is(err, ErrGone) {
		t.Fatalf("Reattach of a missing dir = %v, want ErrGone", err)
	}
}

func TestReattachWithAStaleSocketAndAFreeLockIsGone(t *testing.T) {
	t.Parallel()

	dir := sessionDir(t)
	lock, err := Lock(filepath.Join(dir, LockName))
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err = lock.Close(); err != nil {
		t.Fatalf("close lock: %v", err)
	}
	if err = os.WriteFile(socketOf(t, dir), nil, socketTestMode); err != nil {
		t.Fatalf("WriteFile stale socket: %v", err)
	}
	if _, _, reErr := Reattach(t.Context(), dir, 0); !errors.Is(reErr, ErrGone) {
		t.Fatalf("Reattach with a stale socket and a free lock = %v, want ErrGone", reErr)
	}
}

func TestReattachHolderThatNeverAnswersIsUnattachableWithinTheTimeout(t *testing.T) {
	t.Parallel()

	dir, _ := startWedged(t)
	start := time.Now()
	_, _, err := Reattach(t.Context(), dir, 0)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrUnattachable) || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("Reattach of a holder that never answers = %v, want ErrUnattachable from the timeout", err)
	}
	if elapsed < AttachTimeout || elapsed > AttachTimeout+reattachSlack {
		t.Fatalf("Reattach returned after %v, want about %v", elapsed, AttachTimeout)
	}
}

func TestReattachCanceledWhileAttachingIsUnattachable(t *testing.T) {
	t.Parallel()

	dir, _ := startWedged(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	timer := time.AfterFunc(reattachCancelAfter, cancel)
	defer timer.Stop()
	result := startAsync(func() error {
		_, _, err := Reattach(ctx, dir, 0)
		return err
	})
	var err error
	select {
	case err = <-result:
	case <-time.After(reattachCancelWait):
		t.Fatalf("Reattach did not return within %v of its context ending", reattachCancelWait)
	}
	if !errors.Is(err, ErrUnattachable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Reattach canceled while attaching = %v, want ErrUnattachable wrapping context.Canceled", err)
	}
}

func TestReattachAHolderOfAnotherVersionIsUnattachable(t *testing.T) {
	t.Parallel()

	dir, listener := rawHolderDir(t)
	const version = WireVersion + 2
	result := startAsync(func() error {
		_, _, err := Reattach(t.Context(), dir, 0)
		return err
	})
	server := acceptRaw(t, listener)
	if got := server.read(); got.Type != TypeHello {
		t.Fatalf("frame %+v, want the controller's hello", got)
	}
	server.send(Frame{Type: TypeHello, Version: version, Session: testSession})
	err := awaitResult(t, "Reattach", result)
	if !errors.Is(err, ErrUnattachable) {
		t.Fatalf("Reattach of a holder at version %d = %v, want ErrUnattachable", version, err)
	}
	if versionErr := requireVersionError(t, err); versionErr.Holder != version {
		t.Fatalf("VersionError holder = %d, want %d", versionErr.Holder, version)
	}
}

func TestReattachAHolderThatRefusesTheAttachIsUnattachable(t *testing.T) {
	t.Parallel()

	dir, listener := rawHolderDir(t)
	const refusal = "holder: attach at offset 100: retained output is [200, 2000)"
	result := startAsync(func() error {
		_, _, err := Reattach(t.Context(), dir, 100)
		return err
	})
	server := acceptRaw(t, listener)
	answerHello(t, server)
	if got := server.read(); got.Type != TypeAttach || got.ReplayFrom != 100 {
		t.Fatalf("frame %+v, want an attach at 100", got)
	}
	server.send(Frame{Type: TypeError, Message: refusal})
	err := awaitResult(t, "Reattach", result)
	if !errors.Is(err, ErrUnattachable) || !strings.Contains(err.Error(), refusal) {
		t.Fatalf("Reattach refused by the holder = %v, want ErrUnattachable carrying %q", err, refusal)
	}
}

// reattachResult is what a Reattach returned.
type reattachResult struct {
	p       *Process
	written []string
	err     error
}

// startReattach runs Reattach for dir at replayFrom in a goroutine, since the test answers the holder while it runs.
func startReattach(ctx context.Context, dir string, replayFrom int64) <-chan reattachResult {
	return startAsync(func() reattachResult {
		p, written, err := Reattach(ctx, dir, replayFrom)
		return reattachResult{p: p, written: written, err: err}
	})
}

// readExact makes one Read of n bytes from p's stdout and returns them. It fails the test unless the Read returns all n.
func readExact(t *testing.T, p *Process, n int) []byte {
	t.Helper()
	got := awaitResult(t, "Read", startRead(p.Stdout(), n))
	if got.err != nil || len(got.data) != n {
		t.Fatalf("Read of %d bytes = %d bytes, %v", n, len(got.data), got.err)
	}
	return got.data
}

func TestReattachStreamsFromItsOffsetAndAcksAtOrAboveTheAttachedFrame(t *testing.T) {
	t.Parallel()

	dir, listener := rawHolderDir(t)
	// The holder's spool holds [0, 1000), has consumed 600 and lets its controllers trim below 200. The controller
	// reattaches at 300, below the consumed offset, so its acks must carry the consumed offset and the replay-from floor.
	result := startReattach(t.Context(), dir, 300)
	server := acceptRaw(t, listener)
	answerAttach(t, server, 300, Frame{
		Type: TypeAttached, Start: 0, End: 1000, Consumed: 600, ReplayFrom: 200, Written: []string{"w1"},
	})
	res := awaitResult(t, "Reattach", result)
	if res.err != nil {
		t.Fatalf("Reattach at 300 = %v, want nil", res.err)
	}
	if !slices.Equal(res.written, []string{"w1"}) {
		t.Fatalf("written ids = %q, want the attached frame's [w1]", res.written)
	}
	p := res.p

	// Each out frame is sent alone and read in one Read of its size, so each ack is checked against the frame it follows.
	server.send(Frame{Type: TypeOut, Offset: 300, Data: spoolBytes(300, 16)})
	if got := readExact(t, p, 16); !bytes.Equal(got, spoolBytes(300, 16)) {
		t.Fatalf("stdout from offset 300 is not the spool's bytes there")
	}
	// Consumed is 316, raised to the attached 600; replay-from is 0, raised to the attached floor 200.
	requireAck(t, server, 600, 200)

	server.send(Frame{Type: TypeOut, Offset: 316, Data: spoolBytes(316, 284)})
	if got := readExact(t, p, 284); !bytes.Equal(got, spoolBytes(316, 284)) {
		t.Fatalf("stdout from offset 316 is not the spool's bytes there")
	}
	requireAck(t, server, 600, 200)

	// Consumed passes the attached 600 and is carried as read.
	server.send(Frame{Type: TypeOut, Offset: 600, Data: spoolBytes(600, 400)})
	if got := readExact(t, p, 400); !bytes.Equal(got, spoolBytes(600, 400)) {
		t.Fatalf("stdout from offset 600 is not the spool's bytes there")
	}
	requireAck(t, server, 1000, 200)

	// Replay-from follows SetReplayFrom while it stays below consumed.
	p.SetReplayFrom(700)
	server.send(Frame{Type: TypeOut, Offset: 1000, Data: spoolBytes(1000, 50)})
	if got := readExact(t, p, 50); !bytes.Equal(got, spoolBytes(1000, 50)) {
		t.Fatalf("stdout from offset 1000 is not the spool's bytes there")
	}
	requireAck(t, server, 1050, 700)

	// A lower SetReplayFrom changes nothing, so the replay-from stays at 700.
	p.SetReplayFrom(100)
	server.send(Frame{Type: TypeOut, Offset: 1050, Data: spoolBytes(1050, 10)})
	if got := readExact(t, p, 10); !bytes.Equal(got, spoolBytes(1050, 10)) {
		t.Fatalf("stdout from offset 1050 is not the spool's bytes there")
	}
	requireAck(t, server, 1060, 700)
}

func TestReattachHeldEndReadsTheExitRecordWhenDetached(t *testing.T) {
	t.Parallel()

	p1, dir := launchHolder(t, launchRoot(t), launchChild(t, "read line; "+exitTailChild))
	if err := p1.Detach(); err != nil {
		t.Fatalf("Detach the first controller: %v", err)
	}
	p2, _, err := Reattach(t.Context(), dir, 0)
	if err != nil {
		t.Fatalf("Reattach: %v", err)
	}
	// The write releases the child, and the controller detaches before the child ends, so the end is read by the lock
	// watch, not from an exit frame.
	if got := awaitWrite(t, startWrite(p2, []byte("go\n"))); got.err != nil {
		t.Fatalf("write to release the child: %v", got.err)
	}
	if err = p2.Detach(); err != nil {
		t.Fatalf("Detach the reattached controller: %v", err)
	}
	held, ok := p2.held.(*attachedHolder)
	if !ok {
		t.Fatalf("held process is %T, want *attachedHolder", p2.held)
	}
	select {
	case <-held.Done():
	case <-time.After(waitArmedTimeout):
		t.Fatalf("holder did not end within %v", waitArmedTimeout)
	}
	wantWait := fmt.Sprintf("exit status %d", childDiedCode)
	if waitErr := held.Wait(); waitErr == nil || waitErr.Error() != wantWait {
		t.Fatalf("held Wait = %v, want %q from exit.json", waitErr, wantWait)
	}
	if got := held.Stderr(); got != "err\n" {
		t.Fatalf("held Stderr = %q, want the exit record's %q", got, "err\n")
	}
}

func TestReattachStopOfAWedgedHolderFallsBackWithinTheBound(t *testing.T) {
	t.Parallel()

	p, server, held, listener := rawProcessHeld(t)
	// Ending the held process also ends the stream, with the exit frame the holder sends when its child has ended.
	held.cancel = func() {
		close(held.done)
		exit := Frame{Type: TypeExit, Exit: &Exit{Reason: ReasonStopped, Code: -1, Signal: "terminated"}}
		if writeErr := WriteFrame(server.conn, exit); writeErr != nil {
			t.Errorf("send the exit frame: %v", writeErr)
		}
	}
	start := time.Now()
	stopped := startAsync(p.Stop)
	// The stop dials the holder's socket again. The test accepts that connection and never answers its hello.
	wedged := acceptRaw(t, listener)
	if got := wedged.read(); got.Type != TypeHello {
		t.Fatalf("frame %+v, want the stop's hello", got)
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop = %v, want nil from the held process's stop", err)
		}
	case <-time.After(holderStopTimeout + reattachSlack):
		t.Fatalf("Stop of a holder that never answers did not return within %v", holderStopTimeout+reattachSlack)
	}
	if elapsed := time.Since(start); elapsed < holderStopTimeout {
		t.Fatalf(
			"Stop returned after %v, want it to wait out the %v bound before falling back",
			elapsed,
			holderStopTimeout,
		)
	}
}
