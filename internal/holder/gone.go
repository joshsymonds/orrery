package holder

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ErrGone means a write was made to the stdin of a session whose holder has ended, so no child reads it.
var ErrGone = errors.New("holder: session has ended")

// Gone is a session's child as a controller sees it once its holder has ended: the stdout the holder spooled in its
// directory and the exit record the holder wrote there. Nothing runs any more, so its stdin takes no writes, and Stop
// and Detach do nothing. Gone has the method set of session.Process.
type Gone struct {
	dir  string
	segs []segment
	// exit is the exit record, or nil when the holder wrote none.
	exit *Exit

	// mu guards next, the stdout offset the next read starts at.
	mu   sync.Mutex
	next int64
}

// OpenGone returns the session process a gone holder left in dir. Its stdout starts at offset from and runs to the last
// spooled byte. An offset below the first retained byte fails with ErrTrimmed, and an offset past the last byte fails
// too.
func OpenGone(dir string, from int64) (*Gone, error) {
	segs, err := goneSegments(dir)
	if err != nil {
		return nil, err
	}
	start, end := segs[0].start, segs[len(segs)-1].end()
	if from < start {
		return nil, fmt.Errorf("%w: offset %d, retained from %d", ErrTrimmed, from, start)
	}
	if from > end {
		return nil, fmt.Errorf("holder: offset %d is past the end %d of %s", from, end, dir)
	}
	exit, found, err := readGoneExit(dir)
	if err != nil {
		return nil, err
	}
	gone := &Gone{dir: dir, segs: segs, next: from}
	if found {
		gone.exit = &exit
	}
	return gone, nil
}

// Stdin returns a stdin that takes no writes, since the child has ended.
func (g *Gone) Stdin() io.WriteCloser {
	return goneStdin{}
}

// Stdout returns the read end of the child's stdout. Every Stdout of one Gone shares its read position, as the
// stdout of a Process does.
func (g *Gone) Stdout() io.ReadCloser {
	return goneStdout{g: g}
}

// Stderr returns the exit record's stderr tail, or "" when the holder wrote no exit record.
func (g *Gone) Stderr() string {
	if g.exit == nil {
		return ""
	}
	return g.exit.Stderr
}

// Stop does nothing, since the child has already ended.
func (g *Gone) Stop() error {
	return nil
}

// Detach does nothing, since there is no holder left running.
func (g *Gone) Detach() error {
	return nil
}

// Wait returns nil when the child exited with status 0, "exit status N" when it exited with status N, and "signal: NAME"
// when a signal ended it, as Process.Wait reports them. Without an exit record it returns an error that names the record.
func (g *Gone) Wait() error {
	switch {
	case g.exit == nil:
		return fmt.Errorf("holder: no exit record %s in %s", ExitName, g.dir)
	case g.exit.Signal != "":
		return errors.New("signal: " + g.exit.Signal)
	case g.exit.Code != 0:
		return fmt.Errorf("exit status %d", g.exit.Code)
	}
	return nil
}

// Exit returns the exit record, and whether the holder wrote one.
func (g *Gone) Exit() (Exit, bool) {
	if g.exit == nil {
		return Exit{}, false
	}
	return *g.exit, true
}

// read copies spooled bytes at the read position into buf, from the one segment that holds the position, and advances
// the position past them. It returns io.EOF once the position is at the end of the spool.
func (g *Gone) read(buf []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	seg, ok := g.segmentAt(g.next)
	if !ok {
		return 0, io.EOF
	}
	chunk := min(int64(len(buf)), seg.end()-g.next)
	name := segmentName(seg.start)
	f, err := os.Open(filepath.Clean(filepath.Join(g.dir, name)))
	if err != nil {
		return 0, fmt.Errorf("holder: open %s: %w", name, err)
	}
	defer f.Close()
	read, err := f.ReadAt(buf[:chunk], g.next-seg.start)
	g.next += int64(read)
	if int64(read) == chunk {
		return read, nil
	}
	if errors.Is(err, io.EOF) {
		// The file is shorter than the spool says it is. Its io.EOF must not reach the caller as the end of stdout.
		err = io.ErrUnexpectedEOF
	}
	return read, fmt.Errorf("holder: read %s: %w", name, err)
}

// segmentAt returns the segment that holds offset off. It returns false when off is at or past the end of the spool.
func (g *Gone) segmentAt(off int64) (segment, bool) {
	i := sort.Search(len(g.segs), func(i int) bool { return g.segs[i].end() > off })
	if i == len(g.segs) {
		return segment{}, false
	}
	return g.segs[i], true
}

// goneStdin is the write end of a Gone's stdin. Nothing reads it, so each write fails with ErrGone.
type goneStdin struct{}

// Write fails with ErrGone, writing nothing.
func (goneStdin) Write([]byte) (int, error) {
	return 0, ErrGone
}

// Close does nothing, since nothing is open.
func (goneStdin) Close() error {
	return nil
}

// goneStdout is the read end of a Gone's stdout.
type goneStdout struct {
	g *Gone
}

// Read copies the spooled bytes from the read position into b. It returns io.EOF after the last spooled byte.
func (s goneStdout) Read(b []byte) (int, error) {
	return s.g.read(b)
}

// Close does nothing, since nothing is held open between reads.
func (s goneStdout) Close() error {
	return nil
}

// goneSegments returns the stdout segments in dir in offset order. It fails when dir holds no segment, when a segment
// name is not one the spool writes, and when the segments do not run one into the next.
func goneSegments(dir string) ([]segment, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("holder: read %s: %w", dir, err)
	}
	var segs []segment
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, segmentPrefix) {
			continue
		}
		start, parseErr := strconv.ParseInt(strings.TrimPrefix(name, segmentPrefix), 10, 64)
		if parseErr != nil || segmentName(start) != name {
			return nil, fmt.Errorf("holder: %s in %s is not a segment name", name, dir)
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, fmt.Errorf("holder: inspect %s: %w", name, infoErr)
		}
		segs = append(segs, segment{start: start, size: info.Size()})
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("holder: %s holds no stdout segment", dir)
	}
	slices.SortFunc(segs, func(a, b segment) int { return cmp.Compare(a.start, b.start) })
	for i := 1; i < len(segs); i++ {
		if segs[i].start != segs[i-1].end() {
			return nil, fmt.Errorf("holder: stdout segments in %s are not contiguous at %d", dir, segs[i].start)
		}
	}
	return segs, nil
}

// readGoneExit returns the exit record the holder wrote in dir, and whether it wrote one.
func readGoneExit(dir string) (Exit, bool, error) {
	data, err := os.ReadFile(filepath.Clean(filepath.Join(dir, ExitName)))
	if errors.Is(err, fs.ErrNotExist) {
		return Exit{}, false, nil
	}
	if err != nil {
		return Exit{}, false, fmt.Errorf("holder: read %s in %s: %w", ExitName, dir, err)
	}
	var exit Exit
	if err = json.Unmarshal(data, &exit); err != nil {
		return Exit{}, false, fmt.Errorf("holder: decode %s in %s: %w", ExitName, dir, err)
	}
	return exit, true, nil
}
