package telemetry_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	xtrace "golang.org/x/exp/trace"

	"github.com/joshsymonds/orrery/internal/telemetry"
)

// flightThreshold is the lowered threshold the tests run under. Observe takes the duration as a value, so no test
// waits on a real clock.
const flightThreshold = time.Millisecond

// keptTraces is how many trace files the flight directory keeps.
const keptTraces = 10

// startFlight starts a flight recorder on dir and stops it when the test ends. The recorder is process-wide, so the
// tests here do not run in parallel.
func startFlight(t *testing.T, dir string) *telemetry.Flight {
	t.Helper()
	f, err := telemetry.StartFlight(dir, flightThreshold)
	if err != nil {
		t.Fatalf("StartFlight: %v", err)
	}
	t.Cleanup(f.Stop)
	return f
}

// traceFiles returns the names of the .trace files in dir, sorted by name.
func traceFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".trace") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names
}

func TestFlightObserveOverThresholdWritesReadableTrace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flight")
	f := startFlight(t, dir)

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat flight directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("flight directory mode = %v, want 0700", got)
	}

	// Generate scheduler and GC events so the window holds more than the header.
	runtime.GC()
	if err := f.Observe("start", 2*flightThreshold); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	files := traceFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("trace files = %v, want exactly one", files)
	}
	if !strings.HasSuffix(files[0], "-start.trace") {
		t.Fatalf("trace file %q does not carry the phase word start", files[0])
	}
	path := filepath.Join(dir, files[0])
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat trace file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("trace file mode = %v, want 0600", got)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open trace file: %v", err)
	}
	t.Cleanup(func() { _ = file.Close() })
	reader, err := xtrace.NewReader(file)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	events := 0
	for {
		_, err := reader.ReadEvent()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadEvent after %d events: %v", events, err)
		}
		events++
	}
	if events == 0 {
		t.Fatal("trace file parsed to EOF with no events")
	}
}

func TestFlightObserveUnderThresholdWritesNothing(t *testing.T) {
	dir := t.TempDir()
	f := startFlight(t, dir)

	if err := f.Observe("reconcile", flightThreshold/2); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("flight directory holds %d entries after an under-threshold Observe, want 0", len(entries))
	}
}

func TestFlightKeepsNewestTenTraces(t *testing.T) {
	dir := t.TempDir()
	f := startFlight(t, dir)

	// A trace file that is not ours must survive the pruning.
	unrelated := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := f.Observe("start", 2*flightThreshold); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	oldest := traceFiles(t, dir)
	if len(oldest) != 1 {
		t.Fatalf("trace files after first Observe = %v, want exactly one", oldest)
	}

	for range keptTraces {
		if err := f.Observe("reconcile", 2*flightThreshold); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}

	files := traceFiles(t, dir)
	if len(files) != keptTraces {
		t.Fatalf("trace files = %d, want %d: %v", len(files), keptTraces, files)
	}
	if slices.Contains(files, oldest[0]) {
		t.Fatalf("oldest trace %q survived the eleventh trace", oldest[0])
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file was removed: %v", err)
	}
}

func TestFlightFailedSnapshotLeavesNoTrace(t *testing.T) {
	dir := t.TempDir()
	f := startFlight(t, dir)
	f.Stop()

	if err := f.Observe("start", 2*flightThreshold); err == nil {
		t.Fatal("Observe after Stop returned nil, want an error")
	}
	if files := traceFiles(t, dir); len(files) != 0 {
		t.Fatalf("failed snapshot left trace files %v, want none", files)
	}
}

func TestFlightNilDoesNothing(t *testing.T) {
	t.Parallel()

	var f *telemetry.Flight
	if err := f.Observe("start", time.Hour); err != nil {
		t.Fatalf("Observe on nil Flight: %v", err)
	}
	f.Stop()
}
