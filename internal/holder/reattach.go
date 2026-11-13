package holder

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// AttachTimeout is how long Reattach waits for a holder to answer its hello and its attach.
const AttachTimeout = 5 * time.Second

// ErrUnattachable means a live holder could not be attached: its lock is held, yet its socket refused the connection,
// did not answer in time, spoke a wire version this controller does not attach, refused the attach, or was cut off.
var ErrUnattachable = errors.New("holder: live holder cannot be attached")

// Reattach takes over the holder that serves dir, a session's holder directory, although this controller did not start
// it. It attaches to the holder at replayFrom, with the hello and the attach bounded by AttachTimeout and ctx. It returns
// the Process, whose stdout starts at replayFrom, and the ids of the writes the holder has already applied to the child's
// stdin.
//
// ErrGone means no holder holds the session's lock: the dir is missing, or the lock is free, as it is once a holder has
// ended, whether or not its socket still exists. Any other failure to attach a live holder wraps ErrUnattachable.
//
// The holder is not the caller's child, so the Process learns the holder's end from the session lock: once the lock is
// free, the exit record is read for Wait and Stderr. Its fallback Stop is Stop on dir.
func Reattach(ctx context.Context, dir string, replayFrom int64) (*Process, []string, error) {
	lockPath := filepath.Join(dir, LockName)
	lock, err := os.OpenFile(filepath.Clean(lockPath), os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, ErrGone
	}
	if err != nil {
		return nil, nil, unattachable(fmt.Errorf("holder: open lock %s: %w", lockPath, err))
	}
	free, err := tryLock(lock)
	if err != nil {
		return nil, nil, unattachable(errors.Join(err, lock.Close()))
	}
	if free {
		return nil, nil, errors.Join(ErrGone, lock.Close())
	}
	sock, err := SocketPath(dir)
	if err != nil {
		return nil, nil, failedAttach(lock, err)
	}
	conn, attached, err := attachBounded(ctx, sock, replayFrom)
	if err != nil {
		return nil, nil, failedAttach(lock, err)
	}
	held := watchHolder(dir, lock)
	return newAttachedProcess(held, conn, sock, dir, replayFrom, attached), attached.Written, nil
}

// unattachable wraps cause in ErrUnattachable.
func unattachable(cause error) error {
	return fmt.Errorf("%w: %w", ErrUnattachable, cause)
}

// failedAttach returns the error for an attach of the holder whose session lock lock holds the open file of, which failed
// with cause, and closes lock. The lock decides it: a lock free by now means the holder has ended, so the error is
// ErrGone; a lock still held means a live holder that cannot be attached.
func failedAttach(lock *os.File, cause error) error {
	free, probeErr := tryLock(lock)
	closeErr := lock.Close()
	switch {
	case probeErr != nil:
		return unattachable(errors.Join(cause, probeErr, closeErr))
	case free:
		return errors.Join(ErrGone, closeErr)
	default:
		return unattachable(errors.Join(cause, closeErr))
	}
}

// tryLock takes the session lock that lock holds the open file of, when no other open file holds it, and reports whether
// it did. A lock taken is held until lock is closed.
func tryLock(lock *os.File) (bool, error) {
	err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return false, nil
	default:
		return false, fmt.Errorf("holder: lock %s: %w", lock.Name(), err)
	}
}

// awaitFree waits until no other open file holds the session lock that lock holds the open file of, then takes it. It
// waits on lock, not on the path, so it waits on the lock the holder held even when the path has since been removed.
func awaitFree(lock *os.File) error {
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, syscall.EINTR):
			continue
		default:
			return fmt.Errorf("holder: wait for lock %s: %w", lock.Name(), err)
		}
	}
}

// attachResult is what the exchange in attachBounded returned.
type attachResult struct {
	attached Frame
	err      error
}

// attachBounded dials the holder on sock, greets it and attaches it at replayFrom, all within AttachTimeout and ctx. It
// returns the connection and the attached frame. The connection's deadline is cleared, since its stream runs on. When ctx
// ends first, the connection is closed, which ends the exchange.
func attachBounded(ctx context.Context, sock string, replayFrom int64) (*Conn, Frame, error) {
	due := time.Now().Add(AttachTimeout)
	dialer := net.Dialer{Deadline: due}
	raw, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, Frame{}, fmt.Errorf("holder: dial %s: %w", sock, err)
	}
	conn := &Conn{conn: raw, frames: NewFrameReader(raw), controllerVersion: WireVersion}
	if err = raw.SetDeadline(due); err != nil {
		return nil, Frame{}, conn.closeAfter(fmt.Errorf("holder: set deadline on %s: %w", sock, err))
	}
	exchanged := make(chan attachResult, 1)
	go func() {
		attached, exchangeErr := exchangeAttach(conn, replayFrom)
		exchanged <- attachResult{attached: attached, err: exchangeErr}
	}()
	select {
	case <-ctx.Done():
		return nil, Frame{}, conn.closeAfter(fmt.Errorf("holder: attach to %s: %w", sock, ctx.Err()))
	case res := <-exchanged:
		if res.err != nil {
			return nil, Frame{}, res.err
		}
		if err = raw.SetDeadline(time.Time{}); err != nil {
			return nil, Frame{}, conn.closeAfter(fmt.Errorf("holder: clear deadline on %s: %w", sock, err))
		}
		return conn, res.attached, nil
	}
}

// exchangeAttach greets the holder on conn and attaches it at replayFrom. A failure closes conn.
func exchangeAttach(conn *Conn, replayFrom int64) (Frame, error) {
	if err := conn.greet(); err != nil {
		return Frame{}, conn.closeAfter(err)
	}
	return conn.Attach(replayFrom)
}

// attachedHolder is the heldProcess of a holder that Reattach attached. The holder is not the caller's child, so its end
// is the session lock's: done closes once the lock is free. The exit record is read while the lock is still held, so no
// holder's directory is cleared before it is read.
type attachedHolder struct {
	dir string
	// done closes once the holder has ended. gone and err are set before it closes: gone is the holder's end as Gone
	// reports it, and err is why the end could not be read.
	done chan struct{}
	gone *Gone
	err  error
}

// watchHolder returns the heldProcess of the holder in dir whose session lock lock holds the open file of. A goroutine
// waits for the holder's end and reads it, which closes lock and so releases the lock.
func watchHolder(dir string, lock *os.File) *attachedHolder {
	h := &attachedHolder{dir: dir, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		h.gone, h.err = awaitEnd(dir, lock)
	}()
	return h
}

// awaitEnd waits until the session lock that lock holds the open file of is free, then takes it, reads the holder's end
// in dir, and closes lock.
func awaitEnd(dir string, lock *os.File) (*Gone, error) {
	if err := awaitFree(lock); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	gone, err := readEnd(dir)
	return gone, errors.Join(err, lock.Close())
}

// readEnd returns the holder's end in dir as a Gone: the exit record the holder wrote there, if it wrote one. A holder
// killed before it wrote one has a Gone with no exit record, which Gone.Wait reports.
func readEnd(dir string) (*Gone, error) {
	exit, found, err := readGoneExit(dir)
	if err != nil {
		return nil, err
	}
	gone := &Gone{dir: dir}
	if found {
		gone.exit = &exit
	}
	return gone, nil
}

// Done returns the channel that closes once the holder has ended.
func (h *attachedHolder) Done() <-chan struct{} {
	return h.done
}

// Wait returns the holder's end as Gone.Wait reports it, or the error that kept the end from being read.
func (h *attachedHolder) Wait() error {
	<-h.done
	if h.err != nil {
		return h.err
	}
	return h.gone.Wait()
}

// Stderr returns the exit record's stderr tail once the holder has ended, and "" before then.
func (h *attachedHolder) Stderr() string {
	select {
	case <-h.done:
	default:
		return ""
	}
	if h.gone == nil {
		return ""
	}
	return h.gone.Stderr()
}

// Stop stops the holder serving dir, as Stop does, and gives it holderStopTimeout to end. Process uses it only when the
// holder's socket refuses a stop.
func (h *attachedHolder) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), holderStopTimeout)
	defer cancel()
	return Stop(ctx, h.dir)
}
