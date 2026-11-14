package enginehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceCaptureScript is the capture the scratch repository commits as capture.sh. It prints 64 KiB and then END,
// keeps small.txt and cap.bin (exactly 10 MiB), writes big.bin one byte over the cap, and exits 3. It never writes
// third.txt.
const evidenceCaptureScript = `head -c 65536 /dev/zero | tr '\0' a
printf END
printf 'kept bytes\n' > small.txt
head -c 10485760 /dev/zero > cap.bin
head -c 10485761 /dev/zero > big.bin
exit 3
`

// evidenceCaptureDetachScript is a capture that writes $STARTED once it runs, waits until $RELEASE exists, then prints done
// and exits 3.
const evidenceCaptureDetachScript = `echo started > "$STARTED"; while [ ! -e "$RELEASE" ]; do sleep 0.05; done; echo done; exit 3`

// evidenceCaptureRepo returns a scratch repository whose HEAD commits evidenceCaptureScript as capture.sh, and the HEAD.
func evidenceCaptureRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"capture.sh": evidenceCaptureScript})
	return repo, head
}

// evidenceCaptureFor returns a capture whose pools are pools, whose machine pool file declares the gate pool, and whose
// environment is gateEnv plus extra. Its holder directories live under a short state directory.
func evidenceCaptureFor(t *testing.T, pools PoolCommands, extra ...string) evidenceCapture {
	t.Helper()
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "pools.toml"), stepGatePoolsText("gate"))
	return evidenceCapture{
		pools:        pools,
		environ:      gateEnv(append([]string{"XDG_CONFIG_HOME=" + home}, extra...)...),
		worktrees:    t.TempDir(),
		gateStateDir: gateShortStateDir(t),
		blobs:        evidenceBlobs{dir: filepath.Join(t.TempDir(), "blobs")},
	}
}

// evidenceCaptureItem returns an item that runs capture.sh for 60 seconds and keeps the named artifacts.
func evidenceCaptureItem(artifacts ...string) evidence.Item {
	return evidence.Item{Capture: []string{"/bin/sh", "capture.sh"}, Limit: 60, Artifacts: artifacts}
}

// evidenceCaptureHash returns the sha256 hex digest of data, which is the name the blob store gives it.
func evidenceCaptureHash(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// evidenceCaptureRepoState returns the HEAD, status, index, branch list and worktree list of repo, which a capture leaves as
// it found them.
func evidenceCaptureRepoState(t *testing.T, repo string) string {
	t.Helper()
	return strings.Join([]string{
		stepGitTestRun(t, repo, "rev-parse", "HEAD"),
		stepGitTestRun(t, repo, "status", "--porcelain", "--untracked-files=all"),
		stepGitTestRun(t, repo, "ls-files", "--stage"),
		stepGitTestRun(t, repo, "branch", "--list"),
		stepGitTestRun(t, repo, "worktree", "list", "--porcelain"),
	}, "\n")
}

// evidenceCaptureWait fails the test unless done closes within a few seconds.
func evidenceCaptureWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("capture did not return")
	}
}

// TestEvidenceCaptureKeepsItsExitLogAndArtifacts runs the capture and checks what it kept: the exit, the log's 64 KiB and
// marker, a small artifact by its bytes, the artifact at the cap, the artifact over it, and the artifact never written.
func TestEvidenceCaptureKeepsItsExitLogAndArtifacts(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	c := evidenceCaptureFor(t, &fakeGatePools{})
	item := evidenceCaptureItem("small.txt", "big.bin", "cap.bin", "third.txt")
	got, err := c.run(t.Context(), repo, head, "keep", item, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if got.NotCaptured != "" || got.TimedOut || got.Exit != 3 {
		t.Fatalf("capture = %+v, want exit 3 and captured", got)
	}
	wantLog := strings.Repeat("a", 64<<10) + "END"
	if got.LogBytes != int64(len(wantLog)) || got.LogHash != evidenceCaptureHash(wantLog) {
		t.Fatalf("log = %d bytes hash %s, want %d bytes hash %s",
			got.LogBytes, got.LogHash, len(wantLog), evidenceCaptureHash(wantLog))
	}
	log, err := c.blobs.read(got.LogHash + ".log")
	if err != nil {
		t.Fatalf("read log blob: %v", err)
	}
	if string(log) != wantLog {
		t.Fatalf("log blob holds %d bytes, want the 64 KiB and END (%d bytes)", len(log), len(wantLog))
	}

	if len(got.Artifacts) != 4 {
		t.Fatalf("Artifacts = %+v, want four in item order", got.Artifacts)
	}
	const kept = "kept bytes\n"
	wantSmall := store.EvidenceArtifact{
		Path: "small.txt", State: "kept", Hash: evidenceCaptureHash(kept), Blob: evidenceCaptureHash(kept) + ".txt",
		Size: int64(len(kept)),
	}
	if got.Artifacts[0] != wantSmall {
		t.Fatalf("small.txt = %+v, want %+v", got.Artifacts[0], wantSmall)
	}
	smallBytes, err := c.blobs.read(got.Artifacts[0].Blob)
	if err != nil {
		t.Fatalf("read small.txt blob: %v", err)
	}
	if string(smallBytes) != kept {
		t.Fatalf("small.txt blob = %q, want the file's bytes %q", smallBytes, kept)
	}
	if want := (store.EvidenceArtifact{Path: "big.bin", State: "over_cap", Size: 10<<20 + 1}); got.Artifacts[1] != want {
		t.Fatalf("big.bin = %+v, want %+v", got.Artifacts[1], want)
	}
	if capped := got.Artifacts[2]; capped.Path != "cap.bin" || capped.State != "kept" || capped.Size != 10<<20 {
		t.Fatalf("cap.bin = %+v, want kept at exactly 10 MiB", capped)
	}
	capBytes, err := c.blobs.read(got.Artifacts[2].Blob)
	if err != nil {
		t.Fatalf("read cap.bin blob: %v", err)
	}
	if len(capBytes) != 10<<20 {
		t.Fatalf("cap.bin blob holds %d bytes, want 10 MiB", len(capBytes))
	}
	if want := (store.EvidenceArtifact{Path: "third.txt", State: "missing"}); got.Artifacts[3] != want {
		t.Fatalf("third.txt = %+v, want %+v", got.Artifacts[3], want)
	}
}

// TestEvidenceCaptureRemovesItsWorktreeAndLeavesTheRepositoryAlone runs the capture and checks that its worktree is gone and
// the repository's HEAD, status, index, branches and worktree list are as they were.
func TestEvidenceCaptureRemovesItsWorktreeAndLeavesTheRepositoryAlone(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	before := evidenceCaptureRepoState(t, repo)
	c := evidenceCaptureFor(t, &fakeGatePools{})
	got, err := c.run(t.Context(), repo, head, "clean", evidenceCaptureItem("small.txt"), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.NotCaptured != "" || got.Exit != 3 {
		t.Fatalf("capture = %+v, want exit 3 and captured", got)
	}

	if _, statErr := os.Lstat(filepath.Join(c.worktrees, "clean")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("worktree after the capture: err = %v, want it removed", statErr)
	}
	if after := evidenceCaptureRepoState(t, repo); after != before {
		t.Fatalf("repository state changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestEvidenceCaptureEndsKilledAtItsLimit runs a capture that sleeps past its one-second limit.
func TestEvidenceCaptureEndsKilledAtItsLimit(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	c := evidenceCaptureFor(t, &fakeGatePools{})
	item := evidence.Item{Capture: []string{"/bin/sh", "-c", "sleep 30"}, Limit: 1}
	got, err := c.run(t.Context(), repo, head, "limit", item, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !got.TimedOut || got.NotCaptured != "killed at its limit" {
		t.Fatalf("capture = %+v, want TimedOut and NotCaptured %q", got, "killed at its limit")
	}
}

// TestEvidenceCaptureEndsNotCapturedWhenItsProgramDoesNotStart runs a capture whose program does not exist.
func TestEvidenceCaptureEndsNotCapturedWhenItsProgramDoesNotStart(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	c := evidenceCaptureFor(t, &fakeGatePools{})
	item := evidence.Item{Capture: []string{"./no-such-program"}, Limit: 60}
	got, err := c.run(t.Context(), repo, head, "missing", item, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(got.NotCaptured, "could not start: ") {
		t.Fatalf("NotCaptured = %q, want it to begin %q", got.NotCaptured, "could not start: ")
	}
}

// TestEvidenceCaptureNotFinishedWhenItsLogCannotBeKept runs a capture whose blob directory cannot be created, so its log is
// not kept. The capture reports a could not finish reason, and its worktree is still removed.
func TestEvidenceCaptureNotFinishedWhenItsLogCannotBeKept(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	c := evidenceCaptureFor(t, &fakeGatePools{})
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	c.blobs = evidenceBlobs{dir: filepath.Join(blocker, "blobs")}
	got, err := c.run(t.Context(), repo, head, "unkept", evidenceCaptureItem(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(got.NotCaptured, "could not finish: ") {
		t.Fatalf("NotCaptured = %q, want it to begin %q", got.NotCaptured, "could not finish: ")
	}
	if _, statErr := os.Lstat(filepath.Join(c.worktrees, "unkept")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("worktree after the capture: err = %v, want it removed", statErr)
	}
}

// TestEvidenceCaptureReadoptOfAGateWithNoRecordIsLost readopts a capture whose gate left no holder and no exit record. The
// capture is lost at a restart, and its worktree is removed.
func TestEvidenceCaptureReadoptOfAGateWithNoRecordIsLost(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	c := evidenceCaptureFor(t, &fakeGatePools{})
	got, err := c.readopt(t.Context(), repo, head, "lost", evidenceCaptureItem(), "grant-1")
	if err != nil {
		t.Fatalf("readopt: %v", err)
	}
	if got.NotCaptured != "lost at a restart" {
		t.Fatalf("NotCaptured = %q, want %q", got.NotCaptured, "lost at a restart")
	}
	if _, statErr := os.Lstat(filepath.Join(c.worktrees, "lost")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("worktree after the readopt: err = %v, want it removed", statErr)
	}
}

// TestEvidenceCaptureReadoptReadsTheExitRecordOfAGateThatEndedDetached detaches a capture once its gate runs, which leaves
// the gate under its holder and returns errGateDetach with the zero capture. The gate then ends on its own, and readopt
// reads its exit record and its output.
func TestEvidenceCaptureReadoptReadsTheExitRecordOfAGateThatEndedDetached(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	release := filepath.Join(t.TempDir(), "release")
	startedFile := filepath.Join(t.TempDir(), "started")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	pools := &fakeGatePools{}
	c := evidenceCaptureFor(t, pools, "STARTED="+startedFile, "RELEASE="+release)
	item := evidence.Item{Capture: []string{"/bin/sh", "-c", evidenceCaptureDetachScript}, Limit: 60}

	granted := make(chan string, 1)
	started := func(ctx context.Context, grant, _ string) error {
		granted <- grant
		return ctx.Err()
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	var got store.EvidenceCapture
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		got, runErr = c.run(ctx, repo, head, "detached", item, started)
	}()
	gateWaitFile(t, startedFile)
	cancel(errGateDetach)
	evidenceCaptureWait(t, done)
	if !errors.Is(runErr, errGateDetach) || !reflect.DeepEqual(got, store.EvidenceCapture{}) {
		t.Fatalf("run = %+v, %v, want the zero capture and errGateDetach", got, runErr)
	}
	// The detached gate still runs in its worktree, so the run leaves the worktree for readopt.
	worktree := filepath.Join(c.worktrees, "detached")
	if _, statErr := os.Lstat(worktree); statErr != nil {
		t.Fatalf("worktree after the detached run: %v, want it left for readopt", statErr)
	}
	// A restart that removed the worktree directory leaves git's record of it, which readopt must clear to re-add it.
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatalf("remove the worktree directory: %v", err)
	}

	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	holderDir := filepath.Join(c.gateStateDir, "holders", "evidence-detached")
	gateWaitExit(t, holderDir)
	gateWaitLockFree(t, holderDir)

	got, err := c.readopt(t.Context(), repo, head, "detached", item, <-granted)
	if err != nil {
		t.Fatalf("readopt: %v", err)
	}
	if got.NotCaptured != "" || got.TimedOut || got.Exit != 3 {
		t.Fatalf("readopt = %+v, want exit 3 and captured", got)
	}
	log, err := c.blobs.read(got.LogHash + ".log")
	if err != nil {
		t.Fatalf("read log blob: %v", err)
	}
	if !strings.HasSuffix(string(log), "done\n") {
		t.Fatalf("log blob = %q, want the gate's output ending in done", log)
	}
}

// TestEvidenceCaptureReadoptDetachesWhenItsBrokerCloses runs a scoped capture whose broker closes under its running gate,
// which detaches it. The readopt that follows finds the broker closed too, so it detaches again and keeps the worktree. Once
// the gate ends, a readopt under a serving broker reads its exit record.
func TestEvidenceCaptureReadoptDetachesWhenItsBrokerCloses(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	release := filepath.Join(t.TempDir(), "release")
	startedFile := filepath.Join(t.TempDir(), "started")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	pools := &fakeGatePools{drop: make(chan error, 1)}
	c := evidenceCaptureFor(t, pools, "STARTED="+startedFile, "RELEASE="+release)
	c.gateScope = fakeSystemdRunPath(t)
	item := evidence.Item{Capture: []string{"/bin/sh", "-c", evidenceCaptureDetachScript}, Limit: 60}

	granted := make(chan string, 1)
	started := func(ctx context.Context, grant, _ string) error {
		granted <- grant
		return ctx.Err()
	}
	var got store.EvidenceCapture
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		got, runErr = c.run(t.Context(), repo, head, "broker", item, started)
	}()
	gateWaitFile(t, startedFile)
	pools.drop <- grants.ErrClosed
	evidenceCaptureWait(t, done)
	if !errors.Is(runErr, errGateDetach) || !reflect.DeepEqual(got, store.EvidenceCapture{}) {
		t.Fatalf("run = %+v, %v, want the zero capture and errGateDetach", got, runErr)
	}
	grant := <-granted
	worktree := filepath.Join(c.worktrees, "broker")

	closed := c
	closed.pools = &fakeGatePools{err: grants.ErrClosed}
	got, err := closed.readopt(t.Context(), repo, head, "broker", item, grant)
	if !errors.Is(err, errGateDetach) || !reflect.DeepEqual(got, store.EvidenceCapture{}) {
		t.Fatalf("readopt under a closed broker = %+v, %v, want the zero capture and errGateDetach", got, err)
	}
	if _, statErr := os.Lstat(worktree); statErr != nil {
		t.Fatalf("worktree after the detached readopt: %v, want it left for readopt", statErr)
	}

	if err = os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release the gate: %v", err)
	}
	holderDir := filepath.Join(c.gateStateDir, "holders", "evidence-broker")
	gateWaitExit(t, holderDir)
	gateWaitLockFree(t, holderDir)

	got, err = c.readopt(t.Context(), repo, head, "broker", item, grant)
	if err != nil {
		t.Fatalf("readopt: %v", err)
	}
	if got.NotCaptured != "" || got.TimedOut || got.Exit != 3 {
		t.Fatalf("readopt = %+v, want exit 3 and captured", got)
	}
}

// TestEvidenceCaptureEndsNotFinishedWhenItsContextEndsMidRun cancels a capture's context while its gate runs, without a
// detach. The gate is ended and its holder cleared, the worktree is removed, and the capture reports that it did not finish.
func TestEvidenceCaptureEndsNotFinishedWhenItsContextEndsMidRun(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	release := filepath.Join(t.TempDir(), "release")
	startedFile := filepath.Join(t.TempDir(), "started")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	c := evidenceCaptureFor(t, &fakeGatePools{}, "STARTED="+startedFile, "RELEASE="+release)
	item := evidence.Item{Capture: []string{"/bin/sh", "-c", evidenceCaptureDetachScript}, Limit: 60}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var got store.EvidenceCapture
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		got, runErr = c.run(ctx, repo, head, "cut", item, nil)
	}()
	gateWaitFile(t, startedFile)
	cancel()
	evidenceCaptureWait(t, done)
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if !strings.HasPrefix(got.NotCaptured, "could not finish: ") {
		t.Fatalf("NotCaptured = %q, want it to begin %q", got.NotCaptured, "could not finish: ")
	}
	if _, statErr := os.Lstat(filepath.Join(c.worktrees, "cut")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("worktree after the capture: err = %v, want it removed", statErr)
	}
	holderDir := filepath.Join(c.gateStateDir, "holders", "evidence-cut")
	if _, statErr := os.Lstat(holderDir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("holder directory after the capture: err = %v, want it cleared", statErr)
	}
}

// TestEvidenceCaptureReadoptLosesAGateWhoseWorktreeCannotBeMade detaches a capture, which leaves its gate running under its
// holder, then removes the worktree and readopts from a revision that no longer exists. The worktree cannot be made, so the
// gate is lost at a restart, and readopt ends its holder.
func TestEvidenceCaptureReadoptLosesAGateWhoseWorktreeCannotBeMade(t *testing.T) {
	t.Parallel()
	repo, head := evidenceCaptureRepo(t)
	release := filepath.Join(t.TempDir(), "release")
	startedFile := filepath.Join(t.TempDir(), "started")
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	c := evidenceCaptureFor(t, &fakeGatePools{}, "STARTED="+startedFile, "RELEASE="+release)
	item := evidence.Item{Capture: []string{"/bin/sh", "-c", evidenceCaptureDetachScript}, Limit: 60}

	granted := make(chan string, 1)
	started := func(ctx context.Context, grant, _ string) error {
		granted <- grant
		return ctx.Err()
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	var got store.EvidenceCapture
	var runErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		got, runErr = c.run(ctx, repo, head, "unmade", item, started)
	}()
	gateWaitFile(t, startedFile)
	cancel(errGateDetach)
	evidenceCaptureWait(t, done)
	if !errors.Is(runErr, errGateDetach) || !reflect.DeepEqual(got, store.EvidenceCapture{}) {
		t.Fatalf("run = %+v, %v, want the zero capture and errGateDetach", got, runErr)
	}
	grant := <-granted
	if err := os.RemoveAll(filepath.Join(c.worktrees, "unmade")); err != nil {
		t.Fatalf("remove the worktree directory: %v", err)
	}

	got, err := c.readopt(t.Context(), repo, "no-such-revision", "unmade", item, grant)
	if err != nil {
		t.Fatalf("readopt: %v", err)
	}
	if got.NotCaptured != "lost at a restart" {
		t.Fatalf("NotCaptured = %q, want %q", got.NotCaptured, "lost at a restart")
	}
	holderDir := filepath.Join(c.gateStateDir, "holders", "evidence-unmade")
	if _, statErr := os.Lstat(holderDir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("holder directory after the readopt: err = %v, want it ended and cleared", statErr)
	}
}
