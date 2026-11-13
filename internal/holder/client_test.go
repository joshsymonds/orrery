package holder

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// awaitDial calls dialFn until the holder's socket accepts it, as dialHolder does, and returns the connection. It fails
// at once if run returns first. The connection closes when the test ends.
func awaitDial(t *testing.T, run *running, dialFn func() (*Conn, error)) *Conn {
	t.Helper()
	timeout := time.After(waitArmedTimeout)
	for {
		conn, err := dialFn()
		if err == nil {
			t.Cleanup(func() {
				if cerr := conn.Close(); cerr != nil {
					t.Errorf("close controller connection: %v", cerr)
				}
			})
			return conn
		}
		select {
		case <-run.done:
			t.Fatalf("holder returned %v before its socket accepted; last dial error: %v", run.err, err)
		case <-timeout:
			t.Fatalf("socket did not accept within %v: %v", waitArmedTimeout, err)
		case <-time.After(holderPoll):
		}
	}
}

// listenRaw listens on a new socket in a short root and returns the listener and the socket's path. The listener closes
// when the test ends. A test uses it as a holder that it answers by hand.
func listenRaw(t *testing.T) (*net.UnixListener, string) {
	t.Helper()
	root, err := shortRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if rmErr := os.RemoveAll(root); rmErr != nil {
			t.Errorf("remove %s: %v", root, rmErr)
		}
	})
	sock := filepath.Join(root, "raw.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("listen on %s: %v", sock, err)
	}
	t.Cleanup(func() {
		if cerr := listener.Close(); cerr != nil {
			t.Errorf("close listener: %v", cerr)
		}
	})
	return listener, sock
}

// acceptRaw accepts the one connection a dial makes on listener and returns the test's end of it.
func acceptRaw(t *testing.T, listener *net.UnixListener) *client {
	t.Helper()
	if err := listener.SetDeadline(time.Now().Add(waitArmedTimeout)); err != nil {
		t.Fatalf("set accept deadline: %v", err)
	}
	conn, err := listener.AcceptUnix()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	t.Cleanup(func() {
		if cerr := conn.Close(); cerr != nil {
			t.Errorf("close accepted connection: %v", cerr)
		}
	})
	return &client{t: t, conn: conn, frames: NewFrameReader(conn)}
}

// dialResult is what dial returned.
type dialResult struct {
	conn *Conn
	err  error
}

// dialAsync runs dial at version in a goroutine, so the test can answer its hello on the listener. The result arrives on
// the returned channel.
func dialAsync(t *testing.T, sock string, version int) <-chan dialResult {
	t.Helper()
	result := make(chan dialResult, 1)
	go func() {
		conn, err := dial(t.Context(), sock, version)
		result <- dialResult{conn: conn, err: err}
	}()
	return result
}

// awaitDialResult returns the result of a dialAsync call, failing the test when none arrives within waitArmedTimeout.
func awaitDialResult(t *testing.T, result <-chan dialResult) dialResult {
	t.Helper()
	select {
	case res := <-result:
		return res
	case <-time.After(waitArmedTimeout):
		t.Fatalf("dial did not return within %v", waitArmedTimeout)
		return dialResult{}
	}
}

// requireVersionError fails unless err is a *VersionError and returns it.
func requireVersionError(t *testing.T, err error) *VersionError {
	t.Helper()
	var versionErr *VersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("error = %v, want a *VersionError", err)
	}
	return versionErr
}

func TestClientDialGetsTheSessionAndVersionFromHello(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	conn := awaitDial(t, run, func() (*Conn, error) { return Dial(t.Context(), sock) })

	if conn.Session() != testSession || conn.Version() != WireVersion {
		t.Errorf("Dial got session %q version %d, want session %q version %d",
			conn.Session(), conn.Version(), testSession, WireVersion)
	}
}

func TestClientDialRefusesAFirstFrameThatIsNotHello(t *testing.T) {
	t.Parallel()

	listener, sock := listenRaw(t)
	result := dialAsync(t, sock, WireVersion)
	peer := acceptRaw(t, listener)
	if hello := peer.read(); hello.Type != TypeHello || hello.Version != WireVersion {
		t.Fatalf("controller's first frame = %+v, want hello at version %d", hello, WireVersion)
	}
	peer.send(Frame{Type: TypeOut, Data: []byte("not hello")})

	if res := awaitDialResult(t, result); res.err == nil || res.conn != nil {
		t.Fatalf("Dial = (%v, %v), want an error and no connection", res.conn, res.err)
	}
}

func TestClientDialSendsItsVersionAndWriteAckAndReadMoveTheirFrames(t *testing.T) {
	t.Parallel()

	listener, sock := listenRaw(t)
	result := dialAsync(t, sock, WireVersion+1)
	peer := acceptRaw(t, listener)
	if hello := peer.read(); hello.Type != TypeHello || hello.Version != WireVersion+1 {
		t.Fatalf("controller's hello = %+v, want hello at version %d", hello, WireVersion+1)
	}
	peer.send(Frame{Type: TypeHello, Version: WireVersion, Session: testSession})
	res := awaitDialResult(t, result)
	if res.err != nil {
		t.Fatalf("Dial = %v, want nil", res.err)
	}
	conn := res.conn
	t.Cleanup(func() {
		if cerr := conn.Close(); cerr != nil {
			t.Errorf("close controller connection: %v", cerr)
		}
	})

	if err := conn.Write("w1", []byte("in")); err != nil {
		t.Fatalf("Write = %v, want nil", err)
	}
	if got := peer.read(); got.Type != TypeWrite || got.ID != "w1" || string(got.Data) != "in" {
		t.Errorf("frame for Write = %+v, want write w1 carrying %q", got, "in")
	}
	if err := conn.Ack(5, 3); err != nil {
		t.Fatalf("Ack = %v, want nil", err)
	}
	if got := peer.read(); got.Type != TypeAck || got.Consumed != 5 || got.ReplayFrom != 3 {
		t.Errorf("frame for Ack = %+v, want ack consumed 5 replayFrom 3", got)
	}
	peer.send(Frame{Type: TypeOut, Offset: 0, Data: []byte("hi")})
	got, err := conn.Read()
	if err != nil {
		t.Fatalf("Read = %v, want nil", err)
	}
	if got.Type != TypeOut || got.Offset != 0 || string(got.Data) != "hi" {
		t.Errorf("Read = %+v, want out at offset 0 carrying %q", got, "hi")
	}
}

func TestClientDialSendsWireVersionAndStopFailsWhenTheHolderEndsWithoutStopped(t *testing.T) {
	t.Parallel()

	listener, sock := listenRaw(t)
	result := make(chan dialResult, 1)
	go func() {
		conn, err := Dial(t.Context(), sock)
		result <- dialResult{conn: conn, err: err}
	}()
	peer := acceptRaw(t, listener)
	if hello := peer.read(); hello.Type != TypeHello || hello.Version != WireVersion {
		t.Fatalf("Dial's first frame = %+v, want hello at version %d", hello, WireVersion)
	}
	peer.send(Frame{Type: TypeHello, Version: WireVersion, Session: testSession})
	res := awaitDialResult(t, result)
	if res.err != nil {
		t.Fatalf("Dial = %v, want nil", res.err)
	}
	conn := res.conn
	t.Cleanup(func() {
		if cerr := conn.Close(); cerr != nil {
			t.Errorf("close controller connection: %v", cerr)
		}
	})

	stopped := make(chan error, 1)
	go func() { stopped <- conn.Stop() }()
	if got := peer.read(); got.Type != TypeStop {
		t.Fatalf("frame after Dial = %+v, want stop", got)
	}
	// The peer ends its side with no stopped frame, so Stop reads end of stream.
	if err := peer.conn.CloseWrite(); err != nil {
		t.Fatalf("close write: %v", err)
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, io.EOF) {
			t.Errorf("Stop = %v, want an error wrapping end of stream", err)
		}
	case <-time.After(waitArmedTimeout):
		t.Fatalf("Stop did not return within %v", waitArmedTimeout)
	}
}

func TestClientStopStopsTheHolderAndItsExitSaysStopped(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	conn := awaitDial(t, run, func() (*Conn, error) { return Dial(t.Context(), sock) })
	pid := awaitChildPid(t, cfg.Child.Dir)

	if err := conn.Stop(); err != nil {
		t.Fatalf("Stop = %v, want nil", err)
	}
	// stopped is sent only once the exit file is written, so the exit file is readable before Run returns.
	exit := readExitFile(t, cfg.Dir)
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if exit.Reason != ReasonStopped {
		t.Errorf("exit reason = %q, want %q", exit.Reason, ReasonStopped)
	}
	requireChildGroupGone(t, pid)
	requireNoSocket(t, sock)
}

func TestClientRollbackRefusesANewerHolderByNameAndLeavesItRunning(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	cfg.version = WireVersion + 1
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	conn := awaitDial(t, run, func() (*Conn, error) { return Dial(t.Context(), sock) })
	if conn.Version() != WireVersion+1 {
		t.Fatalf("Dial got version %d from a holder at %d, want Dial to accept it", conn.Version(), WireVersion+1)
	}
	pid := awaitChildPid(t, cfg.Child.Dir)

	_, err := conn.Attach(0)
	versionErr := requireVersionError(t, err)
	want := VersionError{Session: testSession, Holder: WireVersion + 1, Controller: WireVersion}
	if *versionErr != want {
		t.Errorf("VersionError = %+v, want %+v", *versionErr, want)
	}
	wantMsg := fmt.Sprintf("holder for session %s speaks wire version %d; this controller attaches versions %d and %d",
		testSession, WireVersion+1, WireVersion, WireVersion-1)
	if err.Error() != wantMsg {
		t.Errorf("error = %q, want %q", err.Error(), wantMsg)
	}
	requireChildGroupAlive(t, pid)

	// The holder still answers, and a controller of the same release still stops it.
	fresh := awaitDial(t, run, func() (*Conn, error) { return Dial(t.Context(), sock) })
	if fresh.Session() != testSession {
		t.Errorf("fresh Dial answered session %q, want %q", fresh.Session(), testSession)
	}
	if stopErr := fresh.Stop(); stopErr != nil {
		t.Fatalf("Stop from a fresh connection = %v, want nil", stopErr)
	}
	if exit := readExitFile(t, cfg.Dir); exit.Reason != ReasonStopped {
		t.Errorf("exit reason = %q, want %q", exit.Reason, ReasonStopped)
	}
	if runErr := run.wait(t); runErr != nil {
		t.Fatalf("Run = %v, want nil", runErr)
	}
}

func TestClientControllerOneNewerAttachesAHolderAtItsOwnVersion(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	conn := awaitDial(t, run, func() (*Conn, error) { return dial(t.Context(), sock, WireVersion+1) })

	attached, err := conn.Attach(0)
	if err != nil {
		t.Fatalf("Attach = %v, want nil from a controller one version newer", err)
	}
	if attached.Type != TypeAttached {
		t.Errorf("Attach returned %+v, want an attached frame", attached)
	}
	if stopErr := conn.Stop(); stopErr != nil {
		t.Fatalf("Stop after Attach = %v, want nil", stopErr)
	}
	if runErr := run.wait(t); runErr != nil {
		t.Fatalf("Run = %v, want nil", runErr)
	}
}

func TestClientControllerTwoNewerRefusesAHolderTwoVersionsBehind(t *testing.T) {
	t.Parallel()

	cfg := newHolderConfig(t)
	run := startHolder(t.Context(), t, cfg)
	sock := socketOf(t, cfg.Dir)
	conn := awaitDial(t, run, func() (*Conn, error) { return dial(t.Context(), sock, WireVersion+2) })
	pid := awaitChildPid(t, cfg.Child.Dir)

	_, err := conn.Attach(0)
	versionErr := requireVersionError(t, err)
	if versionErr.Holder != WireVersion || versionErr.Controller != WireVersion+2 {
		t.Errorf("VersionError = %+v, want holder %d and controller %d", *versionErr, WireVersion, WireVersion+2)
	}
	requireChildGroupAlive(t, pid)
	fresh := awaitDial(t, run, func() (*Conn, error) { return Dial(t.Context(), sock) })
	if fresh.Session() != testSession {
		t.Errorf("fresh Dial answered session %q, want %q", fresh.Session(), testSession)
	}
}

func TestClientAttachBelowTheRetainedStartReturnsTheHoldersError(t *testing.T) {
	t.Parallel()

	// With segments of attachSegmentBytes, an ack at replayFrom 64 deletes every segment that ends at or below 64.
	const trimmed = 64
	content := spoolBytes(0, attachTestLen)
	run, cfg, _ := attachTailHolder(t, SpoolConfig{SegmentBytes: attachSegmentBytes}, content)
	sock := socketOf(t, cfg.Dir)

	// The holder closes a connection only after it has handled the frames before the close, so the ack is applied
	// once detach returns and the Attach below sees the trimmed range.
	reader := dialHolder(t, run, sock)
	reader.hello(WireVersion)
	reader.attachAt(0)
	reader.readOut(0, len(content))
	reader.sendAck(attachTestLen, trimmed)
	reader.detach()

	late := awaitDial(t, run, func() (*Conn, error) { return Dial(t.Context(), sock) })
	_, err := late.Attach(0)
	if err == nil {
		t.Fatal("Attach below the retained start = nil, want the holder's refusal")
	}
	want := fmt.Sprintf("retained output is [%d, %d)", trimmed, attachTestLen)
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want the holder's message naming %q", err.Error(), want)
	}
	var versionErr *VersionError
	if errors.As(err, &versionErr) {
		t.Errorf("error = %v, want the holder's refusal, not a version error", err)
	}
}
