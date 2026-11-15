package enginehost

import (
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// The first lines of the logs the cut-tail tests store. Each holds a synthetic GitHub token, so a read that keeps it
// shows the token, and a log as long as the capture's tail drops it whole.
const (
	evidenceLogsCutStdoutFirst = "ghp_EXAMPLEEXAMPLEEXAMPLE0000 went stale\n"
	evidenceLogsCutStderrFirst = "warn: ghp_EXAMPLEEXAMPLEEXAMPLE1111 went stale\n"
	evidenceLogsCutPlain       = "plain line\n"
	// evidenceLogsTestTail is the capture tail the cut test reads its logs with.
	evidenceLogsTestTail = 4096
)

// evidenceLogsCutBlob returns a log of size bytes that starts with first and continues with plain lines. The last
// line is cut at size, as a capture's tail is cut at its start.
func evidenceLogsCutBlob(first string, size int) string {
	var b strings.Builder
	b.WriteString(first)
	for b.Len() < size {
		b.WriteString(evidenceLogsCutPlain)
	}
	return b.String()[:size]
}

// evidenceLogsDiffAt returns the index of the first byte at which got and want differ, or the shorter length when one
// is a prefix of the other.
func evidenceLogsDiffAt(got, want string) int {
	n := min(len(got), len(want))
	for i := range n {
		if got[i] != want[i] {
			return i
		}
	}
	return n
}

// evidenceLogsWantRead fails the test unless data reads as want and hash is the sha256 of data. A mismatch reports the
// lengths and the first differing byte, not the logs, which are kilobytes long.
func evidenceLogsWantRead(t *testing.T, label string, data []byte, hash, want string) {
	t.Helper()
	if string(data) != want {
		t.Errorf("%s reads %d bytes, want %d; first difference at byte %d",
			label, len(data), len(want), evidenceLogsDiffAt(string(data), want))
	}
	if hash != evidenceCaptureHash(string(data)) {
		t.Errorf("%s hash = %s, want the sha256 of the bytes returned %s",
			label, hash, evidenceCaptureHash(string(data)))
	}
}

// TestEvidenceLogsReadCutTailLosesItsFirstLine reads the logs of two runs. The cut run's stdout and stderr logs are
// stored with evidenceLogsTestTail bytes, the tail the test gives the capture, as a capture that filled its tail leaves
// them. Each reads back with its first line replaced by [redacted] and every later line kept. The short run's logs are
// one byte shorter, so they read back as redaction in place gives them, first line and all.
func TestEvidenceLogsReadCutTailLosesItsFirstLine(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	item := evidenceRunItem()
	evidenceRunSeed(t, st, repo, item, item)
	ev := evidenceRunNew(t, st, &evidenceLogsSteps{})
	// The tail is a test-sized one, so the logs are a few kilobytes and not a mebibyte each: the rule under test, that a
	// log as long as the tail lost its first line and a shorter one did not, is the same at any size.
	ev.captures.logTail = evidenceLogsTestTail
	cut := evidenceLogsStartRun(t, st, "run-cut", head)
	short := evidenceLogsStartRun(t, st, "run-short", head)

	cutStdout := evidenceLogsCutBlob(evidenceLogsCutStdoutFirst, evidenceLogsTestTail)
	cutStderr := evidenceLogsCutBlob(evidenceLogsCutStderrFirst, evidenceLogsTestTail)
	shortStdout := evidenceLogsCutBlob(evidenceLogsCutStdoutFirst, evidenceLogsTestTail-1)
	shortStderr := evidenceLogsCutBlob(evidenceLogsCutStderrFirst, evidenceLogsTestTail-1)
	records := []struct {
		id      string
		capture store.EvidenceCapture
	}{
		{cut.ID, store.EvidenceCapture{
			LogHash:     evidenceLogsBlob(t, ev, cutStdout, ".log"),
			LogBytes:    int64(len(cutStdout)),
			StderrHash:  evidenceLogsBlob(t, ev, cutStderr, ".log"),
			StderrBytes: int64(len(cutStderr)),
			At:          time.Unix(1700000100, 0),
		}},
		{short.ID, store.EvidenceCapture{
			LogHash:     evidenceLogsBlob(t, ev, shortStdout, ".log"),
			LogBytes:    int64(len(shortStdout)),
			StderrHash:  evidenceLogsBlob(t, ev, shortStderr, ".log"),
			StderrBytes: int64(len(shortStderr)),
			At:          time.Unix(1700000100, 0),
		}},
	}
	for _, record := range records {
		if err := st.RecordEvidenceCapture(t.Context(), record.id, record.capture); err != nil {
			t.Fatalf("RecordEvidenceCapture %s: %v", record.id, err)
		}
	}

	stdout, stdoutHash, err := ev.Artifact(t.Context(), cut.ID, "")
	if err != nil {
		t.Fatalf("Artifact of the cut stdout log: %v", err)
	}
	evidenceLogsWantRead(t, "cut stdout log", stdout, stdoutHash,
		"[redacted]\n"+cutStdout[len(evidenceLogsCutStdoutFirst):])

	stderr, stderrHash, err := ev.StderrLog(t.Context(), cut.ID)
	if err != nil {
		t.Fatalf("StderrLog of the cut run: %v", err)
	}
	evidenceLogsWantRead(t, "cut stderr log", stderr, stderrHash,
		"[redacted]\n"+cutStderr[len(evidenceLogsCutStderrFirst):])

	shortOut, shortOutHash, err := ev.Artifact(t.Context(), short.ID, "")
	if err != nil {
		t.Fatalf("Artifact of the short stdout log: %v", err)
	}
	evidenceLogsWantRead(t, "short stdout log", shortOut, shortOutHash,
		"[redacted] went stale\n"+shortStdout[len(evidenceLogsCutStdoutFirst):])

	shortErr, shortErrHash, err := ev.StderrLog(t.Context(), short.ID)
	if err != nil {
		t.Fatalf("StderrLog of the short run: %v", err)
	}
	evidenceLogsWantRead(t, "short stderr log", shortErr, shortErrHash,
		"warn: [redacted] went stale\n"+shortStderr[len(evidenceLogsCutStderrFirst):])
}
