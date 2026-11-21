// Package inotify wraps one Linux inotify instance: its watches, and the events a reader takes from it. The instance's
// descriptor is non-blocking, so the runtime poller serves its reads, and Close ends a read that waits.
package inotify

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// bufSize is the size of the buffer a read takes events into. It holds several events, each with a name of at most
// NAME_MAX bytes, and always one of the largest.
const bufSize = 4096

// Inotify is one inotify instance. Add and Remove may be called while a read waits, but only one goroutine reads.
type Inotify struct {
	file   *os.File
	buf    []byte
	closed atomic.Bool
}

// Event is one inotify event: the watch it belongs to, its mask, and, for an event of a watched directory, the name
// of the entry it concerns. A queue overflow has WD -1 and the mask IN_Q_OVERFLOW.
type Event struct {
	WD   int
	Mask uint32
	Name string
}

// New creates an inotify instance. It fails when the kernel refuses one, as it does past fs.inotify.max_user_instances.
func New() (*Inotify, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("create inotify: %w", err)
	}
	return &Inotify{file: os.NewFile(uintptr(fd), "inotify"), buf: make([]byte, bufSize)}, nil
}

// Add adds a watch for the events in mask on path, or replaces the mask of the watch the inode of path already has,
// and returns its watch descriptor.
func (n *Inotify) Add(path string, mask uint32) (int, error) {
	var wd int
	var addErr error
	if err := n.control(func(fd int) { wd, addErr = unix.InotifyAddWatch(fd, path, mask) }); err != nil {
		return 0, fmt.Errorf("watch %s: %w", path, err)
	}
	if addErr != nil {
		return 0, fmt.Errorf("watch %s: %w", path, addErr)
	}
	return wd, nil
}

// AddFile is Add for the inode of the open file. The watch names it by /proc/self/fd/<its fd>, which resolves to the
// file that was opened, whatever its path names now.
func (n *Inotify) AddFile(file *os.File, mask uint32) (int, error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("access %s: %w", file.Name(), err)
	}
	var wd int
	var addErr error
	if err = raw.Control(func(fd uintptr) {
		wd, addErr = n.Add(fmt.Sprintf("/proc/self/fd/%d", fd), mask)
	}); err != nil {
		return 0, fmt.Errorf("access %s: %w", file.Name(), err)
	}
	return wd, addErr
}

// Remove removes the watch wd. It fails when the kernel has already dropped that watch.
func (n *Inotify) Remove(wd int) error {
	if wd < 0 || wd > math.MaxUint32 {
		return fmt.Errorf("remove watch %d: not a watch descriptor", wd)
	}
	watch := uint32(wd)
	var rmErr error
	if err := n.control(func(fd int) { _, rmErr = unix.InotifyRmWatch(fd, watch) }); err != nil {
		return fmt.Errorf("remove watch %d: %w", wd, err)
	}
	if rmErr != nil {
		return fmt.Errorf("remove watch %d: %w", wd, rmErr)
	}
	return nil
}

// Read waits for events and returns those one read of the descriptor gives. It fails once Close has run.
func (n *Inotify) Read() ([]Event, error) {
	size, err := n.file.Read(n.buf)
	if err != nil {
		return nil, fmt.Errorf("read inotify: %w", err)
	}
	return parse(n.buf[:size]), nil
}

// Close closes the instance, which drops its watches and ends a read that waits. Add and Remove fail with os.ErrClosed
// once it has run.
func (n *Inotify) Close() error {
	n.closed.Store(true)
	if err := n.file.Close(); err != nil {
		return fmt.Errorf("close inotify: %w", err)
	}
	return nil
}

// control runs fn on the instance's descriptor while it stays open. fn must not keep the descriptor. It fails with
// os.ErrClosed once Close has run.
func (n *Inotify) control(fn func(fd int)) error {
	if n.closed.Load() {
		return os.ErrClosed
	}
	raw, err := n.file.SyscallConn()
	if err != nil {
		return fmt.Errorf("access inotify: %w", err)
	}
	if err = raw.Control(func(fd uintptr) { fn(int(fd)) }); err != nil {
		return fmt.Errorf("access inotify: %w", err)
	}
	return nil
}

// parse returns the events in buf, which holds whole inotify_event records, each followed by its name padded with
// NULs.
func parse(buf []byte) []Event {
	var events []Event
	for len(buf) >= unix.SizeofInotifyEvent {
		var head unix.InotifyEvent
		if _, err := binary.Decode(buf[:unix.SizeofInotifyEvent], binary.NativeEndian, &head); err != nil {
			break
		}
		size := unix.SizeofInotifyEvent + int(head.Len)
		if size > len(buf) {
			break
		}
		events = append(events, Event{
			WD:   int(head.Wd),
			Mask: head.Mask,
			Name: string(bytes.TrimRight(buf[unix.SizeofInotifyEvent:size], "\x00")),
		})
		buf = buf[size:]
	}
	return events
}
