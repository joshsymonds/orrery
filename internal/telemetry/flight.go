package telemetry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/trace"
	"slices"
	"sync"
	"time"
)

// flightMaxBytes bounds the window the flight recorder keeps, so each trace file stays bounded in size.
const flightMaxBytes = 10 << 20

// keptTraces is how many trace files stay in the flight directory. The oldest are removed first.
const keptTraces = 10

// flightTimeLayout is fixed width and UTC, so names sort in write-time order.
const flightTimeLayout = "20060102T150405.000000000Z"

// traceName matches the trace files Observe writes. Other files in the directory are left alone.
var traceName = regexp.MustCompile(`^\d{8}T\d{6}\.\d{9}Z-.+\.trace$`)

// Flight is a flight recorder over the execution trace. Every method on a nil Flight does nothing.
type Flight struct {
	dir       string
	threshold time.Duration
	recorder  *trace.FlightRecorder

	// mu serializes Observe and Stop. WriteTo fails when a second snapshot is in progress.
	mu sync.Mutex
}

// StartFlight starts recording the execution trace into a moving window. It creates dir with mode 0700 when missing.
func StartFlight(dir string, threshold time.Duration) (*Flight, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create flight directory: %w", err)
	}
	recorder := trace.NewFlightRecorder(trace.FlightRecorderConfig{MaxBytes: flightMaxBytes})
	if err := recorder.Start(); err != nil {
		return nil, fmt.Errorf("start flight recorder: %w", err)
	}
	return &Flight{dir: dir, threshold: threshold, recorder: recorder}, nil
}

// Observe writes the window as a new trace file named for phase when took exceeds the threshold, then removes all
// but the newest keptTraces trace files in the directory. An Observe at or under the threshold writes nothing.
func (f *Flight) Observe(phase string, took time.Duration) error {
	if f == nil || took <= f.threshold {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.writeTrace(phase); err != nil {
		return err
	}
	return f.prune()
}

// writeTrace snapshots the window into a new 0600 file named for phase and the current time. A snapshot that fails
// is removed, so a partial file never takes one of the kept slots.
func (f *Flight) writeTrace(phase string) error {
	name := time.Now().UTC().Format(flightTimeLayout) + "-" + phase + ".trace"
	path := filepath.Clean(filepath.Join(f.dir, name))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create flight trace: %w", err)
	}
	_, err = f.recorder.WriteTo(file)
	if err = errors.Join(err, file.Close()); err != nil {
		return fmt.Errorf("write flight trace: %w", errors.Join(err, os.Remove(path)))
	}
	return nil
}

// prune removes the trace files in the directory except the newest keptTraces. Names sort in write-time order.
func (f *Flight) prune() error {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return fmt.Errorf("list flight directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && traceName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	excess := len(names) - keptTraces
	for _, name := range names[:max(excess, 0)] {
		if err = os.Remove(filepath.Join(f.dir, name)); err != nil {
			return fmt.Errorf("remove old flight trace: %w", err)
		}
	}
	return nil
}

// Stop ends recording. It waits for an Observe in progress to finish.
func (f *Flight) Stop() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorder.Stop()
}
