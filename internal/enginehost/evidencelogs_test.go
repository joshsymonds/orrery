package enginehost

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/evidence"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceLogsSteps is an EvidenceSteps that records the prompt of each judge it starts, and ends every judge done.
type evidenceLogsSteps struct {
	mu      sync.Mutex
	prompts []string
}

// Start records the prompt of the judge and returns its id.
func (f *evidenceLogsSteps) Start(_ context.Context, start StepStart) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, start.Prompt)
	return "judge-1", nil
}

// Subscribe returns a subscription to the judge with the id given, offered that judge as ended done.
func (f *evidenceLogsSteps) Subscribe(_ context.Context, ids []string) (*StepSubscription, error) {
	sub := NewStepSubscription(ids)
	sub.Offer(store.StepRow{ID: ids[0], End: stepLoopEndDone, Status: "done"})
	return sub, nil
}

// recorded returns the prompts of the judges started, in order.
func (f *evidenceLogsSteps) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompts
}

// evidenceLogsBlob stores text as a blob with ext in the blob directory of ev, and returns its hash.
func evidenceLogsBlob(t *testing.T, ev *Evidence, text, ext string) string {
	t.Helper()
	hash, _, err := ev.blobs.put([]byte(text), ext)
	if err != nil {
		t.Fatalf("store blob: %v", err)
	}
	return hash
}

// evidenceLogsPath returns the path of the stdout or stderr log stored under hash, as the judge prompt names it.
func evidenceLogsPath(t *testing.T, ev *Evidence, hash string) string {
	t.Helper()
	path, err := ev.blobs.path(hash + ".log")
	if err != nil {
		t.Fatalf("path of log %s: %v", hash, err)
	}
	return path
}

// evidenceLogsStartRun stores a green run of R1 at revision, capturing, and returns it as stored.
func evidenceLogsStartRun(t *testing.T, st *store.Store, id, revision string) store.EvidenceRun {
	t.Helper()
	run := store.EvidenceRun{
		ID: id, Epic: evidenceRunEpic, Item: "R1", Revision: revision, Phase: evidence.PhaseGreen,
		Source: evidence.SourceCaptured, Spec: evidenceRunItem(), Created: time.Unix(1700000000, 0),
	}
	if err := st.AddEvidenceRun(t.Context(), run); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", id, err)
	}
	stored, err := st.EvidenceRun(t.Context(), id)
	if err != nil {
		t.Fatalf("EvidenceRun %s: %v", id, err)
	}
	return stored
}

// TestEvidenceLogsJudgeNamesTheLogsOfACapture judges a captured run once its capture is recorded, as drive does. A run
// with a stdout and a stderr log has both named by path, and a run captured before this release, with its stdout log
// alone, has only that one named.
func TestEvidenceLogsJudgeNamesTheLogsOfACapture(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		withStderr bool
	}{
		{name: "stdout and stderr logs", withStderr: true},
		{name: "stdout log alone, as before this release", withStderr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, head := evidenceRunRepo(t, "exit 0\n")
			st := epicTestStore(t)
			item := evidenceRunItem()
			evidenceRunSeed(t, st, repo, item, item)
			steps := &evidenceLogsSteps{}
			ev := evidenceRunNew(t, st, steps)
			run := evidenceLogsStartRun(t, st, "run-judged", head)
			capture := store.EvidenceCapture{
				LogHash:  evidenceLogsBlob(t, ev, "build ok\n", ".log"),
				LogBytes: int64(len("build ok\n")),
				At:       time.Unix(1700000100, 0),
			}
			if tc.withStderr {
				capture.StderrHash = evidenceLogsBlob(t, ev, "warning: slow\n", ".log")
				capture.StderrBytes = int64(len("warning: slow\n"))
			}
			if err := st.RecordEvidenceCapture(t.Context(), run.ID, capture); err != nil {
				t.Fatalf("RecordEvidenceCapture: %v", err)
			}

			ev.judgeRun(t.Context(), repo, evidenceRunCaptured(run, capture))

			prompts := steps.recorded()
			if len(prompts) != 1 {
				t.Fatalf("%d judges started, want one", len(prompts))
			}
			wantStdout := "Stdout log: " + evidenceLogsPath(t, ev, capture.LogHash)
			if !strings.Contains(prompts[0], wantStdout) {
				t.Errorf("judge prompt lacks %q:\n%s", wantStdout, prompts[0])
			}
			if tc.withStderr {
				wantStderr := "Stderr log: " + evidenceLogsPath(t, ev, capture.StderrHash)
				if !strings.Contains(prompts[0], wantStderr) {
					t.Errorf("judge prompt lacks %q:\n%s", wantStderr, prompts[0])
				}
			} else if strings.Contains(prompts[0], "Stderr log") {
				t.Errorf("judge prompt names a stderr log the run does not have:\n%s", prompts[0])
			}
			judged, err := st.EvidenceRun(t.Context(), run.ID)
			if err != nil {
				t.Fatalf("EvidenceRun: %v", err)
			}
			if judged.State != "ended" || judged.Verdict != evidence.VerdictHolds {
				t.Errorf("run state %q verdict %q, want ended holds", judged.State, judged.Verdict)
			}
		})
	}
}

// evidenceLogsCredentialLog is a stdout log of synthetic credentials of two listed shapes between plain lines, and
// evidenceLogsCredentialLogRedacted is that log as a read returns it.
const (
	evidenceLogsCredentialLog = "build step\nAuthorization: Bearer EXAMPLEEXAMPLEEXAMPLE0\n" +
		"key AKIAEXAMPLEEXAMPLE12\nplain line\n"
	evidenceLogsCredentialLogRedacted = "build step\nAuthorization: [redacted]\nkey [redacted]\nplain line\n"
)

// evidenceLogsCredentialStderr is a stderr log holding a synthetic GitHub token between plain text, and
// evidenceLogsCredentialStderrRedacted is that log as a read returns it.
const (
	evidenceLogsCredentialStderr         = "warn: ghp_EXAMPLEEXAMPLEEXAMPLE0000 went stale\nplain stderr\n"
	evidenceLogsCredentialStderrRedacted = "warn: [redacted] went stale\nplain stderr\n"
)

// TestEvidenceLogsReadRedactsEveryLogAndKeepsArtifacts reads the logs of a run captured with credentials in both
// streams, of a run captured with plain streams, and of a run captured before this release, whose stdout log holds a
// credential raw and which has no stderr log. A log comes back redacted under the hash of the bytes returned, and a
// plain log unchanged under its stored hash. A kept artifact comes back as stored, credential and all.
func TestEvidenceLogsReadRedactsEveryLogAndKeepsArtifacts(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	item := evidenceRunItem("out/config.txt")
	evidenceRunSeed(t, st, repo, item, item)
	ev := evidenceRunNew(t, st, &evidenceLogsSteps{})

	const artifact = "token sk-EXAMPLEEXAMPLEEXAMPLE00\n"
	artifactHash := evidenceLogsBlob(t, ev, artifact, ".txt")
	const plainLog, plainStderr = "build ok\n", "warning: slow\n"
	leaky := evidenceLogsStartRun(t, st, "run-leaky", head)
	plain := evidenceLogsStartRun(t, st, "run-plain", head)
	before := evidenceLogsStartRun(t, st, "run-before", head)
	records := []struct {
		run     store.EvidenceRun
		capture store.EvidenceCapture
	}{
		{leaky, store.EvidenceCapture{
			LogHash:     evidenceLogsBlob(t, ev, evidenceLogsCredentialLog, ".log"),
			LogBytes:    int64(len(evidenceLogsCredentialLog)),
			StderrHash:  evidenceLogsBlob(t, ev, evidenceLogsCredentialStderr, ".log"),
			StderrBytes: int64(len(evidenceLogsCredentialStderr)),
			Artifacts: []store.EvidenceArtifact{{
				Path: "out/config.txt", State: "kept", Hash: artifactHash, Blob: artifactHash + ".txt",
				Size: int64(len(artifact)),
			}},
			At: time.Unix(1700000100, 0),
		}},
		{plain, store.EvidenceCapture{
			LogHash:     evidenceLogsBlob(t, ev, plainLog, ".log"),
			LogBytes:    int64(len(plainLog)),
			StderrHash:  evidenceLogsBlob(t, ev, plainStderr, ".log"),
			StderrBytes: int64(len(plainStderr)),
			At:          time.Unix(1700000100, 0),
		}},
		{before, store.EvidenceCapture{
			LogHash:  evidenceLogsBlob(t, ev, evidenceLogsCredentialLog, ".log"),
			LogBytes: int64(len(evidenceLogsCredentialLog)),
			At:       time.Unix(1700000100, 0),
		}},
	}
	for _, record := range records {
		if err := st.RecordEvidenceCapture(t.Context(), record.run.ID, record.capture); err != nil {
			t.Fatalf("RecordEvidenceCapture %s: %v", record.run.ID, err)
		}
	}
	stored := func(id string) store.EvidenceRun {
		t.Helper()
		run, err := st.EvidenceRun(t.Context(), id)
		if err != nil {
			t.Fatalf("EvidenceRun %s: %v", id, err)
		}
		return run
	}

	evidenceRunWantArtifact(t, ev, leaky.ID, "", evidenceLogsCredentialLogRedacted)
	_, leakyHash, err := ev.Artifact(t.Context(), leaky.ID, "")
	if err != nil {
		t.Fatalf("Artifact of the leaky log: %v", err)
	}
	if leakyHash == stored(leaky.ID).LogHash {
		t.Errorf("redacted log hash = stored hash %s, want the hash of the redacted bytes", leakyHash)
	}
	evidenceRunWantArtifact(t, ev, before.ID, "", evidenceLogsCredentialLogRedacted)
	evidenceRunWantLog(t, ev, stored(plain.ID), plainLog)
	evidenceRunWantArtifact(t, ev, leaky.ID, "out/config.txt", artifact)

	evidenceLogsWantStderr(t, ev, leaky.ID, evidenceLogsCredentialStderrRedacted)
	evidenceLogsWantStderr(t, ev, plain.ID, plainStderr)
	if _, _, err := ev.StderrLog(t.Context(), before.ID); !errors.Is(err, ErrNoEvidenceArtifact) {
		t.Errorf("StderrLog of a run with no stderr log = %v, want an error matching ErrNoEvidenceArtifact", err)
	}
	if _, _, err := ev.StderrLog(t.Context(), "no-such-run"); !errors.Is(err, store.ErrNoEvidenceRun) {
		t.Errorf("StderrLog of an unknown run = %v, want an error matching ErrNoEvidenceRun", err)
	}
}

// TestEvidenceLogsServiceServesStderrAndCarriesItsHash asks the Evidence service for a run's stderr log, its stdout log
// and an artifact by name with the stderr flag. The stderr log comes back redacted under its hash, the stderr flag with
// a name is refused, and a run with no stderr log is not found. ListEvidence carries the run's stderr hash and size.
func TestEvidenceLogsServiceServesStderrAndCarriesItsHash(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	item := evidenceRunItem()
	evidenceRunSeed(t, st, repo, item, item)
	ev := evidenceRunNew(t, st, &evidenceLogsSteps{})
	served := evidenceLogsStartRun(t, st, "run-served", head)
	quiet := evidenceLogsStartRun(t, st, "run-quiet", head)
	stderrHash := evidenceLogsBlob(t, ev, evidenceLogsCredentialStderr, ".log")
	records := []struct {
		id      string
		capture store.EvidenceCapture
	}{
		{served.ID, store.EvidenceCapture{
			LogHash:     evidenceLogsBlob(t, ev, evidenceLogsCredentialLog, ".log"),
			LogBytes:    int64(len(evidenceLogsCredentialLog)),
			StderrHash:  stderrHash,
			StderrBytes: int64(len(evidenceLogsCredentialStderr)),
			At:          time.Unix(1700000100, 0),
		}},
		{quiet.ID, store.EvidenceCapture{
			LogHash:  evidenceLogsBlob(t, ev, "build ok\n", ".log"),
			LogBytes: int64(len("build ok\n")),
			At:       time.Unix(1700000100, 0),
		}},
	}
	for _, record := range records {
		if err := st.RecordEvidenceCapture(t.Context(), record.id, record.capture); err != nil {
			t.Fatalf("RecordEvidenceCapture %s: %v", record.id, err)
		}
	}
	client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, ev))

	stderr, err := client.GetEvidenceArtifact(t.Context(), connect.NewRequest(&v1.GetEvidenceArtifactRequest{
		Run: served.ID, Stderr: true,
	}))
	if err != nil {
		t.Fatalf("GetEvidenceArtifact stderr: %v", err)
	}
	if got := string(stderr.Msg.GetData()); got != evidenceLogsCredentialStderrRedacted ||
		stderr.Msg.GetHash() != evidenceCaptureHash(evidenceLogsCredentialStderrRedacted) {
		t.Errorf("stderr = %q hash %s, want the redacted stderr log under its hash", got, stderr.Msg.GetHash())
	}

	stdout, err := client.GetEvidenceArtifact(t.Context(), connect.NewRequest(&v1.GetEvidenceArtifactRequest{
		Run: served.ID,
	}))
	if err != nil {
		t.Fatalf("GetEvidenceArtifact stdout: %v", err)
	}
	if got := string(stdout.Msg.GetData()); got != evidenceLogsCredentialLogRedacted {
		t.Errorf("stdout = %q, want the redacted stdout log", got)
	}

	_, err = client.GetEvidenceArtifact(t.Context(), connect.NewRequest(&v1.GetEvidenceArtifactRequest{
		Run: served.ID, Name: "out/config.txt", Stderr: true,
	}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("stderr with a name = %v, want InvalidArgument", err)
	}
	_, err = client.GetEvidenceArtifact(t.Context(), connect.NewRequest(&v1.GetEvidenceArtifactRequest{
		Run: quiet.ID, Stderr: true,
	}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("stderr of a run with no stderr log = %v, want NotFound", err)
	}

	listed, err := client.ListEvidence(t.Context(), connect.NewRequest(&v1.ListEvidenceRequest{Epic: evidenceRunEpic}))
	if err != nil {
		t.Fatalf("ListEvidence: %v", err)
	}
	var found bool
	for _, run := range listed.Msg.GetRuns() {
		if run.GetId() != served.ID {
			continue
		}
		found = true
		if run.GetStderrHash() != stderrHash || run.GetStderrBytes() != int64(len(evidenceLogsCredentialStderr)) {
			t.Errorf("listed stderr = %s %d, want %s %d", run.GetStderrHash(), run.GetStderrBytes(),
				stderrHash, len(evidenceLogsCredentialStderr))
		}
	}
	if !found {
		t.Errorf("ListEvidence lacks run %s", served.ID)
	}
}

// evidenceLogsWantStderr fails the test unless the stderr log of the run with id reads as want, hashed by the bytes
// read.
func evidenceLogsWantStderr(t *testing.T, ev *Evidence, id, want string) {
	t.Helper()
	data, hash, err := ev.StderrLog(t.Context(), id)
	if err != nil {
		t.Fatalf("StderrLog of %s: %v", id, err)
	}
	if string(data) != want || hash != evidenceCaptureHash(want) {
		t.Errorf("stderr log of %s = %q hash %s, want %q hash %s", id, data, hash, want, evidenceCaptureHash(want))
	}
}
