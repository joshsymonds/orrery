package server

import (
	"errors"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/inotify"
)

// transcriptFileEvents are the inotify events a pane stream takes from its transcript file: a write, and the file's
// removal or rename, after which the path no longer names the file the stream follows.
const transcriptFileEvents = unix.IN_MODIFY | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// transcriptDirEvents are the inotify events a pane stream takes from its transcript's directory: an entry created or
// moved in, which replaces the transcript when it has the transcript's name.
const transcriptDirEvents = unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_ONLYDIR

// transcriptWatch is the inotify watch of one pane stream on its transcript: one inotify instance, with a watch on the
// transcript file and one on its directory. Its goroutine reads the instance's events and puts a token on wake for each
// batch that concerns the transcript, so a busy stream takes one token for any number of events. A batch that replaces
// the file at the path, removes it, or overflows the kernel's queue also marks the transcript replaced, and the stream's
// next step then reads it from the start. A watch whose instance could not be made, or whose watches could not be added,
// has no instance and never wakes: it is down, and its stream falls back to the change feed and its keepalive (see
// paneWatch).
type transcriptWatch struct {
	wake chan struct{}
	done chan struct{} // closed once the goroutine has returned, or at once for a watch that is down

	mu       sync.Mutex
	ino      *inotify.Inotify // nil while the watch is down
	path     string           // the transcript the watches were last armed on, or empty before the first arm
	name     string           // the base name of path
	fileWD   int              // the watch descriptor of the file, or -1 for none
	dirWD    int              // the watch descriptor of the directory, or -1 for none
	replaced bool
	closeErr error // the error closing the instance gave, if any
}

// newTranscriptWatch returns the watch of a stream, with an instance from open. The watch is down when open fails, and
// it holds no file watch until arm names one.
func newTranscriptWatch(open func() (*inotify.Inotify, error)) *transcriptWatch {
	watch := &transcriptWatch{wake: make(chan struct{}, 1), done: make(chan struct{}), fileWD: -1, dirWD: -1}
	ino, err := open()
	if err != nil {
		close(watch.done)
		return watch
	}
	watch.ino = ino
	go watch.read(ino)
	return watch
}

// up reports whether the watch has its instance, and so wakes the stream when the transcript changes.
func (t *transcriptWatch) up() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ino != nil
}

// armed returns the transcript the watch was last armed on, or empty before the first arm.
func (t *transcriptWatch) armed() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.path
}

// arm points the watch at the transcript at path: it watches the directory, then the file, and removes the watches of
// the transcript it followed before. The directory is watched first, so a file replaced while the file watch is added is
// still seen replaced. A watch that is down only records path. When a watch cannot be added or removed, the watch goes
// down and closes its instance.
func (t *transcriptWatch) arm(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.path, t.name = path, filepath.Base(path)
	if t.ino == nil {
		return
	}
	dirWD, err := t.ino.Add(filepath.Dir(path), transcriptDirEvents)
	if err != nil {
		t.downLocked()
		return
	}
	fileWD, err := t.ino.Add(path, transcriptFileEvents)
	if err != nil {
		t.downLocked()
		return
	}
	for _, old := range []int{t.fileWD, t.dirWD} {
		if old < 0 || old == fileWD || old == dirWD {
			continue
		}
		// The kernel answers EINVAL for a watch it has already dropped, as it drops the watch of a removed file, and
		// such a watch needs no removal.
		if err = t.ino.Remove(old); err != nil && !errors.Is(err, unix.EINVAL) {
			t.downLocked()
			return
		}
	}
	t.fileWD, t.dirWD = fileWD, dirWD
}

// takeReplaced reports whether events have marked the transcript replaced since the last call, and clears the mark.
func (t *transcriptWatch) takeReplaced() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	replaced := t.replaced
	t.replaced = false
	return replaced
}

// close closes the watch's instance, if it has one, and waits until its goroutine has returned. It returns the error
// closing the instance gave, whether now or when the watch went down before. It may be called more than once.
func (t *transcriptWatch) close() error {
	t.mu.Lock()
	t.downLocked()
	err := t.closeErr
	t.mu.Unlock()
	<-t.done
	return err
}

// downLocked closes the instance and leaves the watch down, keeping any close error for close to return. Closing the
// instance ends the goroutine's read. The caller holds mu.
func (t *transcriptWatch) downLocked() {
	if t.ino == nil {
		return
	}
	t.closeErr = t.ino.Close()
	t.ino = nil
	t.fileWD, t.dirWD = -1, -1
}

// read takes the events of ino until a read fails, which closing ino causes, and wakes the stream for each batch that
// concerns its transcript.
func (t *transcriptWatch) read(ino *inotify.Inotify) {
	defer close(t.done)
	for {
		events, err := ino.Read()
		if err != nil {
			return
		}
		if t.note(events) {
			select {
			case t.wake <- struct{}{}:
			default:
			}
		}
	}
}

// note records what events say of the transcript, and reports whether they concern it. A write to the file concerns
// it. The file's removal or rename, an entry with its name created or moved into its directory, and a queue overflow,
// after which any of those may have been lost, also mark it replaced.
func (t *transcriptWatch) note(events []inotify.Event) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	concerns := false
	for _, event := range events {
		switch {
		case event.Mask&unix.IN_Q_OVERFLOW != 0:
			t.replaced, concerns = true, true
		case event.WD == t.fileWD && event.Mask&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF) != 0:
			t.replaced, concerns = true, true
		case event.WD == t.fileWD && event.Mask&unix.IN_MODIFY != 0:
			concerns = true
		case event.WD == t.dirWD && event.Mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 && event.Name == t.name:
			t.replaced, concerns = true, true
		}
	}
	return concerns
}
