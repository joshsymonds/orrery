package holder

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// goneSegmentBytes is the spool segment size the gone tests run their holders with, so their output spans several
	// segment files.
	goneSegmentBytes = 16
	// goneOutLen is how many bytes a gone test's child writes to its stdout: six full segments and a partial one.
	goneOutLen = 100
	// goneMidOffset is an offset inside the third segment, so a read starts mid-segment.
	goneMidOffset = 37
	// goneTrimmed is the replay-from of a trimmed spool. An ack at it deletes every segment that ends at or below it.
	goneTrimmed = 64
	// goneOutFile is the file the child copies to its stdout.
	goneOutFile = "out.bin"
)

// The gone process is a session process, so it has the method set session.Process requires.
var _ processLike = (*Gone)(nil)

// goneHolder runs a holder whose child copies content to its stdout and then runs script under sh. It returns the
// holder's directory once the holder has ended. The spool it leaves is in segments of goneSegmentBytes.
func goneHolder(t *testing.T, content []byte, script string) string {
	t.Helper()
	cfg := newHolderConfig(t)
	cfg.Spool = SpoolConfig{SegmentBytes: goneSegmentBytes}
	childDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(childDir, goneOutFile), content, filesTestMode); err != nil {
		t.Fatalf("write child output: %v", err)
	}
	command := "cat " + goneOutFile + "; " + script
	cfg.Child = ChildConfig{Path: shPath(t), Args: []string{"-c", command}, Dir: childDir}
	run := startHolder(t.Context(), t, cfg)
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	return cfg.Dir
}

// openGone opens the gone holder in dir at offset from, and fails the test when OpenGone refuses it.
func openGone(t *testing.T, dir string, from int64) *Gone {
	t.Helper()
	gone, err := OpenGone(dir, from)
	if err != nil {
		t.Fatalf("OpenGone(%s, %d): %v", dir, from, err)
	}
	return gone
}

// countSegments returns how many stdout segment files dir holds.
func countSegments(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	n := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), segmentPrefix) {
			n++
		}
	}
	return n
}

func TestGoneStdoutReadsSpooledBytesFromOffsetToEnd(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, goneOutLen)
	dir := goneHolder(t, content, "")
	if n := countSegments(t, dir); n < 2 {
		t.Fatalf("spool holds %d segment file(s), want several", n)
	}
	for _, from := range []int64{0, goneMidOffset, goneOutLen} {
		gone := openGone(t, dir, from)
		got, err := io.ReadAll(gone.Stdout())
		if err != nil {
			t.Fatalf("read stdout from %d: %v", from, err)
		}
		if !bytes.Equal(got, content[from:]) {
			t.Errorf("stdout from %d = %d bytes, want the %d spooled bytes from there", from, len(got), goneOutLen-from)
		}
		if n, err := gone.Stdout().Read(make([]byte, 1)); n != 0 || err != io.EOF {
			t.Errorf("read after the end from %d = %d, %v; want 0, io.EOF", from, n, err)
		}
	}
}

func TestOpenGoneRefusesOffsetOutsideRetainedBytes(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, goneOutLen)
	dir := t.TempDir()
	spool, err := NewSpool(dir, SpoolConfig{SegmentBytes: goneSegmentBytes})
	if err != nil {
		t.Fatalf("NewSpool: %v", err)
	}
	mustAppend(t, spool, content)
	mustAck(t, spool, goneOutLen, goneTrimmed)
	if err = spool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, from := range []int64{0, goneTrimmed - 1} {
		if _, openErr := OpenGone(dir, from); !errors.Is(openErr, ErrTrimmed) {
			t.Errorf("OpenGone at %d = %v, want ErrTrimmed", from, openErr)
		}
	}
	if _, openErr := OpenGone(dir, goneOutLen+1); openErr == nil {
		t.Errorf("OpenGone past the end %d succeeded, want an error", goneOutLen)
	}
	got, err := io.ReadAll(openGone(t, dir, goneTrimmed).Stdout())
	if err != nil {
		t.Fatalf("read stdout from %d: %v", goneTrimmed, err)
	}
	if !bytes.Equal(got, content[goneTrimmed:]) {
		t.Errorf("stdout from %d = %d bytes, want the %d retained bytes", goneTrimmed, len(got), goneOutLen-goneTrimmed)
	}
}

func TestGoneExitRecordAndWaitReportTheChildsEnd(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, goneOutLen)
	tests := []struct {
		name     string
		script   string
		want     Exit
		wantWait string
	}{
		{
			name:   "status zero",
			script: "",
			want:   Exit{Reason: ReasonExited, Code: 0, StdoutEnd: goneOutLen},
		},
		{
			name:     "status three",
			script:   "echo err >&2; exit 3",
			want:     Exit{Reason: ReasonExited, Code: 3, StdoutEnd: goneOutLen, Stderr: "err\n"},
			wantWait: "exit status 3",
		},
		{
			name:   "signal",
			script: "echo err >&2; kill -KILL $$",
			want: Exit{
				Reason:    ReasonExited,
				Code:      -1,
				Signal:    childSignalKill,
				StdoutEnd: goneOutLen,
				Stderr:    "err\n",
			},
			wantWait: "signal: " + childSignalKill,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gone := openGone(t, goneHolder(t, content, tc.script), 0)
			exit, ok := gone.Exit()
			if !ok || exit != tc.want {
				t.Errorf("Exit() = %+v, %t; want %+v, true", exit, ok, tc.want)
			}
			err := gone.Wait()
			switch {
			case tc.wantWait == "" && err != nil:
				t.Errorf("Wait() = %v, want nil", err)
			case tc.wantWait != "" && (err == nil || err.Error() != tc.wantWait):
				t.Errorf("Wait() = %v, want %q", err, tc.wantWait)
			}
			if got := gone.Stderr(); got != tc.want.Stderr {
				t.Errorf("Stderr() = %q, want %q", got, tc.want.Stderr)
			}
		})
	}
}

func TestGoneWithoutExitRecordNamesTheMissingRecord(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, goneOutLen)
	dir := goneHolder(t, content, "")
	if err := os.Remove(filepath.Join(dir, ExitName)); err != nil {
		t.Fatalf("remove exit record: %v", err)
	}
	gone := openGone(t, dir, 0)
	if exit, ok := gone.Exit(); ok || exit != (Exit{}) {
		t.Errorf("Exit() = %+v, %t; want no record", exit, ok)
	}
	if err := gone.Wait(); err == nil || !strings.Contains(err.Error(), ExitName) {
		t.Errorf("Wait() = %v, want an error naming %s", err, ExitName)
	}
	if got := gone.Stderr(); got != "" {
		t.Errorf("Stderr() = %q, want none", got)
	}
	got, err := io.ReadAll(gone.Stdout())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("stdout = %d bytes, want the %d spooled bytes", len(got), goneOutLen)
	}
}

func TestGoneStdinRefusesWritesAndStopAndDetachDoNothing(t *testing.T) {
	t.Parallel()

	content := spoolBytes(0, goneOutLen)
	dir := goneHolder(t, content, "")
	gone := openGone(t, dir, 0)
	if n, err := gone.Stdin().Write([]byte("prompt\n")); n != 0 || !errors.Is(err, ErrGone) {
		t.Errorf("Stdin().Write = %d, %v; want 0, ErrGone", n, err)
	}
	if err := gone.Stdin().Close(); err != nil {
		t.Errorf("Stdin().Close() = %v, want nil", err)
	}
	if err := gone.Stop(); err != nil {
		t.Errorf("Stop() = %v, want nil", err)
	}
	if err := gone.Detach(); err != nil {
		t.Errorf("Detach() = %v, want nil", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("holder dir after Stop and Detach: %v, want it kept", err)
	}
	got, err := io.ReadAll(gone.Stdout())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("stdout after Stop and Detach = %d bytes, want the %d spooled bytes", len(got), goneOutLen)
	}
}
