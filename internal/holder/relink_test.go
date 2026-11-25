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
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/proc"
)

const (
	// relinkSize is the stdout a relink test's child copies. It is more than four out frames, so a cut can fall between
	// them.
	relinkSize = 300 << 10
	// relinkHead is how much of that stdout a test reads before it cuts the link.
	relinkHead = 100 << 10
	// relinkChunk is the buffer a relay copies bytes through.
	relinkChunk = 32 << 10
	// relinkDials bounds the dial outcomes a cutLink holds for its test to read.
	relinkDials = 16
	// relinkWriteID is the id the test of a cut answer writes under.
	relinkWriteID = "relink-write"
)

// errLinkDown is what a cutLink's Dial fails with while a test holds the link down.
var errLinkDown = errors.New("test link is down")

// cutLink is a Link onto a holder's socket through relays the test can cut, hold and take down. Each Dial is one relayed
// connection: the Process holds one end of it, and a relay carries its bytes to the holder's socket. cut ends every
// relayed connection, as a link that drops does. hold keeps the holder's bytes in the relays until cut releases them, so
// a test can end a write's answer in the cut. setDown makes Dial fail, as a link that is down does. Each Dial's outcome
// is sent on dials, which the test reads.
type cutLink struct {
	sock  string
	watch *attachedHolder
	dials chan error

	mu     sync.Mutex
	down   bool
	gate   *heldBytes
	relays []*relay
}

// relay is one connection a cutLink carries: side is the relay's end of the pipe the Process holds the other end of, and
// holder is the connection to the holder's socket.
type relay struct {
	side   net.Conn
	holder net.Conn
}

// heldBytes is a hold: the holder's bytes wait in the relays until release closes. seen closes once bytes first wait.
type heldBytes struct {
	release  chan struct{}
	seen     chan struct{}
	seenOnce sync.Once
}

// newCutLink returns a cutLink onto the holder whose directory is dir. Its Done closes once that holder has ended.
func newCutLink(t *testing.T, dir string) *cutLink {
	t.Helper()
	lockPath := filepath.Join(dir, LockName)
	lock, err := os.OpenFile(filepath.Clean(lockPath), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open lock %s: %v", lockPath, err)
	}
	return &cutLink{
		sock:  socketOf(t, dir),
		watch: watchHolder(dir, lock),
		dials: make(chan error, relinkDials),
	}
}

// Dial relays one connection to the holder's socket. Once the holder has ended it fails with ErrGone, and while the link
// is down with errLinkDown. Its outcome is sent on dials.
func (l *cutLink) Dial(ctx context.Context) (net.Conn, error) {
	conn, err := l.dial(ctx)
	l.dials <- err
	return conn, err
}

// Done returns the channel that closes once the holder has ended.
func (l *cutLink) Done() <-chan struct{} {
	return l.watch.Done()
}

// Exit returns the holder's exit record once Done has closed, and whether the holder wrote one.
func (l *cutLink) Exit() (Exit, bool) {
	select {
	case <-l.watch.Done():
	default:
		return Exit{}, false
	}
	if l.watch.gone == nil {
		return Exit{}, false
	}
	return l.watch.gone.Exit()
}

// dial does the work of Dial, without sending its outcome.
func (l *cutLink) dial(ctx context.Context) (net.Conn, error) {
	select {
	case <-l.watch.Done():
		return nil, fmt.Errorf("%w: the holder has ended", ErrGone)
	default:
	}
	l.mu.Lock()
	down := l.down
	l.mu.Unlock()
	if down {
		return nil, errLinkDown
	}
	var dialer net.Dialer
	holder, err := dialer.DialContext(ctx, "unix", l.sock)
	if err != nil {
		return nil, err
	}
	procSide, side := net.Pipe()
	r := &relay{side: side, holder: holder}
	l.mu.Lock()
	l.relays = append(l.relays, r)
	l.mu.Unlock()
	go r.forward()
	go l.backward(r)
	return procSide, nil
}

// forward carries the Process's bytes to the holder until either end ends.
func (r *relay) forward() {
	buf := make([]byte, relinkChunk)
	for {
		n, err := r.side.Read(buf)
		if n > 0 {
			if _, writeErr := r.holder.Write(buf[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// backward carries the holder's bytes to the Process, waiting while the link holds them.
func (l *cutLink) backward(r *relay) {
	buf := make([]byte, relinkChunk)
	for {
		n, err := r.holder.Read(buf)
		if n > 0 {
			l.waitHeld()
			if _, writeErr := r.side.Write(buf[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// waitHeld waits while the link holds the holder's bytes, and marks that bytes wait.
func (l *cutLink) waitHeld() {
	l.mu.Lock()
	held := l.gate
	l.mu.Unlock()
	if held == nil {
		return
	}
	held.seenOnce.Do(func() { close(held.seen) })
	<-held.release
}

// cut ends every relayed connection, and then releases the bytes a hold kept, which reach a closed connection and are
// lost. It returns how many connections it ended, and fails the test when one does not close.
func (l *cutLink) cut(t *testing.T) int {
	t.Helper()
	l.mu.Lock()
	relays := l.relays
	l.relays = nil
	held := l.gate
	l.gate = nil
	l.mu.Unlock()
	for _, r := range relays {
		if err := errors.Join(r.side.Close(), r.holder.Close()); err != nil {
			t.Fatalf("cut a relayed connection: %v", err)
		}
	}
	if held != nil {
		close(held.release)
	}
	return len(relays)
}

// hold keeps the holder's bytes in the relays until cut. It returns the channel that closes once bytes wait.
func (l *cutLink) hold() <-chan struct{} {
	held := &heldBytes{release: make(chan struct{}), seen: make(chan struct{})}
	l.mu.Lock()
	l.gate = held
	l.mu.Unlock()
	return held.seen
}

// awaitRefusal waits for the next dial, which must be refused while the link is down. It is how a test knows the Process
// has begun to re-attach, so a write it makes then is a write made while re-dialing.
func (l *cutLink) awaitRefusal(t *testing.T) {
	t.Helper()
	if err := <-l.dials; !errors.Is(err, errLinkDown) {
		t.Fatalf("dial the link = %v, want a refusal while it is down", err)
	}
}

// setDown makes Dial fail while down is true.
func (l *cutLink) setDown(down bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.down = down
}

// awaitRedial waits for a dial that succeeds. The dials the link refused before it are skipped, since a test that brings
// the link back up need not know how many attempts failed first. Any other failure fails the test.
func (l *cutLink) awaitRedial(t *testing.T) {
	t.Helper()
	for {
		err := <-l.dials
		if err == nil {
			return
		}
		if !errors.Is(err, errLinkDown) {
			t.Fatalf("dial the link = %v, want a connection or a refusal while it is down", err)
		}
	}
}

// detachedHolder launches a holder whose child is child, detaches its first controller and returns the holder's dir. The
// holder keeps running, with no controller, until the test stops it.
func detachedHolder(t *testing.T, child proc.Config) string {
	t.Helper()
	p, dir := launchHolder(t, launchRoot(t), child)
	if err := p.Detach(); err != nil {
		t.Fatalf("Detach the first controller: %v", err)
	}
	return dir
}

// attachRelink attaches the holder in dir through a new cutLink, and waits for the attach's dial to succeed. It returns
// the Process and the link.
func attachRelink(t *testing.T, dir string) (*Process, *cutLink) {
	t.Helper()
	link := newCutLink(t, dir)
	p, _, err := ReattachLink(t.Context(), link, 0, WireVersion)
	if err != nil {
		t.Fatalf("ReattachLink: %v", err)
	}
	link.awaitRedial(t)
	return p, link
}

// requireEcho writes line to p's child, which echoes it, and fails the test unless the echo is line.
func requireEcho(t *testing.T, p *Process, line string) {
	t.Helper()
	if got := awaitWrite(t, startWrite(p, []byte(line))); got.err != nil || got.n != len(line) {
		t.Fatalf("Write(%q) = %d, %v; want %d, nil", line, got.n, got.err, len(line))
	}
	echo := make([]byte, len(line))
	if err := awaitResult(t, "read the echo", startAsync(func() error {
		_, err := io.ReadFull(p.Stdout(), echo)
		return err
	})); err != nil || string(echo) != line {
		t.Fatalf("echo of %q = %q, %v; want %q, nil", line, echo, err, line)
	}
}

func TestRelinkReadsEachStdoutByteOnceAcrossACut(t *testing.T) {
	t.Parallel()

	data := spoolBytes(0, relinkSize)
	child := launchChild(t, fmt.Sprintf("cat %s; exec cat", reattachDataName))
	writeChildFile(t, child.Dir, reattachDataName, data)
	p, link := attachRelink(t, detachedHolder(t, child))
	got := make([]byte, relinkSize)
	if err := awaitResult(t, "read the head", startAsync(func() error {
		_, err := io.ReadFull(p.Stdout(), got[:relinkHead])
		return err
	})); err != nil {
		t.Fatalf("read the first %d bytes: %v", relinkHead, err)
	}
	if link.cut(t) == 0 {
		t.Fatalf("the link had no connection to cut")
	}
	link.awaitRedial(t)
	if err := awaitResult(t, "read the rest", startAsync(func() error {
		_, err := io.ReadFull(p.Stdout(), got[relinkHead:])
		return err
	})); err != nil {
		t.Fatalf("read the rest of the output across the cut: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("stdout across the cut is not the child's output, each byte once")
	}
	stopProcess(t, p)
}

func TestRelinkAppliesAWriteWhoseAnswerWasCutOnce(t *testing.T) {
	t.Parallel()

	// The child echoes each line it reads, so the echoes show how many times the holder applied each write.
	child := launchChild(t, `while read -r line; do echo "got $line"; done`)
	p, link := attachRelink(t, detachedHolder(t, child))
	seen := link.hold()
	write := startWriteWithID(t, p, relinkWriteID, []byte("one\n"))
	// The holder sends its bytes only after it has written the write to the child, so the first bytes the link holds
	// mean the holder applied it, and its answer is held with them until the cut ends them.
	<-seen
	if link.cut(t) == 0 {
		t.Fatalf("the link had no connection to cut")
	}
	if got := awaitWrite(t, write); got.err != nil || got.n != len("one\n") {
		t.Fatalf("the write whose answer was cut = %d, %v; want %d, nil", got.n, got.err, len("one\n"))
	}
	link.awaitRedial(t)
	if got := awaitWrite(t, startWrite(p, []byte("two\n"))); got.err != nil {
		t.Fatalf("write two after the re-attach: %v", got.err)
	}
	want := "got one\ngot two\n"
	echoes := make([]byte, len(want))
	if err := awaitResult(t, "read the echoes", startAsync(func() error {
		_, err := io.ReadFull(p.Stdout(), echoes)
		return err
	})); err != nil {
		t.Fatalf("read the echoes: %v", err)
	}
	if string(echoes) != want {
		t.Fatalf("the child's output = %q, want %q: a write applied twice would show as a repeated echo", echoes, want)
	}
	stopProcess(t, p)
}

func TestRelinkWaitReportsTheExitRecordAfterTheHolderEnds(t *testing.T) {
	t.Parallel()

	// The child waits for its first line, so the test attaches before the child can end.
	p, _ := attachRelink(t, detachedHolder(t, launchChild(t, "read line; "+exitTailChild)))
	if got := awaitWrite(t, startWrite(p, []byte("go\n"))); got.err != nil {
		t.Fatalf("release the child: %v", got.err)
	}
	rest := readToEnd(t, p.Stdout())
	if string(rest.data) != "out\n" || !errors.Is(rest.err, io.EOF) {
		t.Fatalf("stdout = %q, %v; want %q, then io.EOF", rest.data, rest.err, "out\n")
	}
	wantWait := fmt.Sprintf("exit status %d", childDiedCode)
	if waitErr := awaitResult(t, "Wait", startAsync(p.Wait)); waitErr == nil || waitErr.Error() != wantWait {
		t.Fatalf("Wait = %v, want %q from the exit record", waitErr, wantWait)
	}
	if got := p.Stderr(); got != "err\n" {
		t.Fatalf("Stderr = %q, want the exit record's %q", got, "err\n")
	}
}

func TestRelinkStdoutReadsBlockWhileRedialingAndWritesFailWithTheDialError(t *testing.T) {
	t.Parallel()

	child := launchChild(t, `read first; echo ready; read second; echo "got $second"; exec cat`)
	p, link := attachRelink(t, detachedHolder(t, child))
	if got := awaitWrite(t, startWrite(p, []byte("first\n"))); got.err != nil {
		t.Fatalf("write first: %v", got.err)
	}
	ready := make([]byte, len("ready\n"))
	if err := awaitResult(t, "read ready", startAsync(func() error {
		_, err := io.ReadFull(p.Stdout(), ready)
		return err
	})); err != nil || string(ready) != "ready\n" {
		t.Fatalf("the child's ready line = %q, %v; want %q, nil", ready, err, "ready\n")
	}
	link.setDown(true)
	if link.cut(t) == 0 {
		t.Fatalf("the link had no connection to cut")
	}
	link.awaitRefusal(t)
	if got := awaitWrite(t, startWrite(p, []byte("second\n"))); !errors.Is(got.err, errLinkDown) {
		t.Fatalf("Write while the link is down = %v, want an error wrapping the dial error %v", got.err, errLinkDown)
	}
	echo := make([]byte, len("got release\n"))
	read := startAsync(func() error {
		_, err := io.ReadFull(p.Stdout(), echo)
		return err
	})
	select {
	case err := <-read:
		t.Fatalf("a Read returned while the link was down: %v", err)
	default:
	}
	link.setDown(false)
	link.awaitRedial(t)
	if got := awaitWrite(t, startWrite(p, []byte("release\n"))); got.err != nil {
		t.Fatalf("Write after the link is back: %v", got.err)
	}
	if err := awaitResult(t, "read the echo", read); err != nil || string(echo) != "got release\n" {
		t.Fatalf("echo = %q, %v; want %q, nil", echo, err, "got release\n")
	}
	stopProcess(t, p)
}

func TestRelinkDetachClosesTheConnectionAndLeavesTheHolderRunning(t *testing.T) {
	t.Parallel()

	dir := detachedHolder(t, launchChild(t, "exec cat"))
	p, _ := attachRelink(t, dir)
	if err := p.Detach(); err != nil {
		t.Fatalf("Detach = %v, want nil", err)
	}
	p2, _, err := Reattach(t.Context(), dir, 0, WireVersion)
	if err != nil {
		t.Fatalf("Reattach after the link's Detach: %v", err)
	}
	requireEcho(t, p2, "ping\n")
	stopProcess(t, p2)
}

func TestRelinkReturnsTheWritesTheHolderHasApplied(t *testing.T) {
	t.Parallel()

	p, link := attachRelink(t, detachedHolder(t, launchChild(t, "exec cat")))
	if got := awaitWrite(t, startWriteWithID(t, p, relinkWriteID, []byte("ping\n"))); got.err != nil {
		t.Fatalf("write under %s: %v", relinkWriteID, got.err)
	}
	if err := p.Detach(); err != nil {
		t.Fatalf("Detach = %v, want nil", err)
	}
	p2, written, err := ReattachLink(t.Context(), link, 0, WireVersion)
	if err != nil {
		t.Fatalf("ReattachLink after the Detach: %v", err)
	}
	if !slices.Equal(written, []string{relinkWriteID}) {
		t.Fatalf("written ids = %q, want the applied write %q", written, relinkWriteID)
	}
	stopProcess(t, p2)
}

func TestRelinkStopEndsTheHolderAndItsChild(t *testing.T) {
	t.Parallel()

	p, _ := attachRelink(t, detachedHolder(t, launchChild(t, "exec cat")))
	requireEcho(t, p, "ping\n")
	stopProcess(t, p)
	waitErr := awaitResult(t, "Wait", startAsync(p.Wait))
	if waitErr == nil || waitErr.Error() != "signal: terminated" {
		t.Fatalf("Wait = %v, want signal: terminated", waitErr)
	}
}

// TestRelinkStopAfterACutStillStopsTheHolder stops the holder straight after the link is cut. Whether the Process has
// noticed the cut yet decides how the stop goes: over a connection that fails, the stop falls back to the link's own,
// and over a connection the Process has re-attached, it goes over that. Either way Stop returns nil and the child ends.
func TestRelinkStopAfterACutStillStopsTheHolder(t *testing.T) {
	t.Parallel()

	p, link := attachRelink(t, detachedHolder(t, launchChild(t, "exec cat")))
	if link.cut(t) == 0 {
		t.Fatalf("the link had no connection to cut")
	}
	stopProcess(t, p)
	waitErr := awaitResult(t, "Wait", startAsync(p.Wait))
	if waitErr == nil || waitErr.Error() != "signal: terminated" {
		t.Fatalf("Wait = %v, want signal: terminated", waitErr)
	}
}

func TestRelinkOfAHolderThatHasEndedIsGone(t *testing.T) {
	t.Parallel()

	dir := detachedHolder(t, launchChild(t, "exec cat"))
	link := newCutLink(t, dir)
	if err := Stop(t.Context(), dir); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	<-link.Done()
	_, _, err := ReattachLink(t.Context(), link, 0, WireVersion)
	if !errors.Is(err, ErrGone) {
		t.Fatalf("ReattachLink of an ended holder = %v, want ErrGone", err)
	}
}
