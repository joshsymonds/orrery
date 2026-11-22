package interpret

import (
	"bytes"
	"context"
	"database/sql"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// worldImage is a copy of an integrate or sync world at one point of its walk: its store file, its scratch repository
// and origin, the epic's base and the batch's candidate, and the calls its fakes recorded. A test restores it into
// directories of its own and goes on from there, so the walk to that point runs once for the package. The runner keeps
// nothing between passes, so a fresh runner over the restored store goes on as the world's own would.
type worldImage struct {
	db         []byte
	repo       fstest.MapFS
	repoPath   string
	origin     fstest.MapFS
	originPath string
	base       string
	candidate  string
	checks     []store.NodeCheck
	evidence   []evidenceCall
	steps      []enginehost.StepStart
}

// walkPoint is a point of a world's walk that a world image holds: at gate is the end of toGate, at captured the end of
// toCaptured.
type walkPoint int

const (
	atGate walkPoint = iota
	atCaptured
)

// TestFromWorldImages runs the tests that start from a world image as its subtests, each named for what it shows, over
// one set of images (see worldImages), so the walk to each image runs once for the package however many tests start
// there.
func TestFromWorldImages(t *testing.T) {
	t.Parallel()
	images := &worldImages{}
	t.Run("AmendmentRecomputesGreenAtTheStoredBase", amendmentRecomputesGreenAtTheStoredBase(images))
	t.Run("CatastropheParkInterpretOpensParkQuestion", catastropheParkInterpretOpensParkQuestion(images))
	t.Run("IntegrateBatchGreenAdvancesBranchAndStoredBase", integrateBatchGreenAdvancesBranchAndStoredBase(images))
	t.Run(
		"IntegrateFailedGateKeepsBranchAndNextBatchComposesOnCandidate",
		integrateFailedGateKeepsBranchAndNextBatchComposesOnCandidate(images),
	)
	t.Run("IntegrateMovedOrCheckedOutBranchParksBatch", integrateMovedOrCheckedOutBranchParksBatch(images))
	t.Run("IntegrateCutAdvanceEndsPassedOnce", integrateCutAdvanceEndsPassedOnce(images))
	t.Run(
		"IntegrateBatchOnAnOlderBaseNeverMovesTheBranchBack",
		integrateBatchOnAnOlderBaseNeverMovesTheBranchBack(images),
	)
	t.Run(
		"RetryFailedIntegrateDoneGateRerunsOnCandidateAndAdvancesBranch",
		retryFailedIntegrateDoneGateRerunsOnCandidateAndAdvancesBranch(images),
	)
	t.Run(
		"SyncPastAFailedBatchThatComposedNothingLeavesNoCandidate",
		syncPastAFailedBatchThatComposedNothingLeavesNoCandidate(images),
	)
	t.Run("SyncMergesOriginMainAtAQuietPoint", syncMergesOriginMainAtAQuietPoint(images))
	t.Run("SyncConflictTellsOnceAndIsNotTriedAgain", syncConflictTellsOnceAndIsNotTriedAgain(images))
	t.Run("SyncGateFailureTellsAndALaterMainIsTried", syncGateFailureTellsAndALaterMainIsTried(images))
	t.Run("SyncCaptureRegressionTellsAndDoesNotAdvance", syncCaptureRegressionTellsAndDoesNotAdvance(images))
	t.Run("SyncStoresNoRunWhenTheStoredBaseHoldsOriginMain", syncStoresNoRunWhenTheStoredBaseHoldsOriginMain(images))
	t.Run("SyncWithNoOriginStoresNoRun", syncWithNoOriginStoresNoRun(images))
	t.Run("SyncReadThatFailsStoresNoRunAndLogs", syncReadThatFailsStoresNoRunAndLogs(images))
	t.Run("SyncSkipsOriginMainForcedPastTheSyncedCommit", syncSkipsOriginMainForcedPastTheSyncedCommit(images))
	t.Run("SyncRestartFinishesTheWalk", syncRestartFinishesTheWalk(images))
	t.Run("SyncAtTheMergeEndsPassedWhileTheEpicIsBusy", syncAtTheMergeEndsPassedWhileTheEpicIsBusy(images))
	t.Run("SyncDoesNotAdvanceABusyEpic", syncDoesNotAdvanceABusyEpic(images))
	t.Run("SyncDoesNotAdvanceAMovedBranch", syncDoesNotAdvanceAMovedBranch(images))
	t.Run(
		"FailedSyncRoutesNoParkAndNoSyncRunsWhileAnActionIsPending",
		failedSyncRoutesNoParkAndNoSyncRunsWhileAnActionIsPending(images),
	)
	t.Run("SyncWaitsForThePlannersRerunOfAFailedBatch", syncWaitsForThePlannersRerunOfAFailedBatch(images))
	t.Run(
		"SyncCloseWhileItPreparesEndsItAndStartsNothingMore",
		syncCloseWhileItPreparesEndsItAndStartsNothingMore(images),
	)
	t.Run(
		"SyncReadThatStallsIsStoppedAtTheReadLimitAndBacksOff",
		syncReadThatStallsIsStoppedAtTheReadLimitAndBacksOff(images),
	)
	t.Run(
		"SyncFetchThatStallsIsStoppedAtTheFetchLimitAndBacksOff",
		syncFetchThatStallsIsStoppedAtTheFetchLimitAndBacksOff(images),
	)
	t.Run("SyncNotAdvancedWhileBusyIsTriedAgain", syncNotAdvancedWhileBusyIsTriedAgain(images))
	t.Run(
		"SyncPausedBeforeItsAdvanceIsNotMovedAndIsTriedAgain",
		syncPausedBeforeItsAdvanceIsNotMovedAndIsTriedAgain(images),
	)
	t.Run("SyncParkedJustBeforeItsAdvanceIsNotMoved", syncParkedJustBeforeItsAdvanceIsNotMoved(images))
	t.Run("SyncCheckLostTwiceEndsSkippedToldAndIsTriedAgain", syncCheckLostTwiceEndsSkippedToldAndIsTriedAgain(images))
	t.Run("SyncWorkThatFailsEndsSkippedAndIsTriedAgain", syncWorkThatFailsEndsSkippedAndIsTriedAgain(images))
}

// worldImages are the world images of the package's tests: those of newIntegrateWorld and those of newSyncWorld.
type worldImages struct {
	integrate, sync worldWalk
}

// worldWalk is one world's walk to its gate and on to its capture, and its image at each, made by the first test that
// asks for either. A test that fails while it walks the world leaves both unmade, so the next test to ask walks it again
// and fails the same way. Tests that ask for another world's images do not wait for this walk.
type worldWalk struct {
	mu       sync.Mutex
	gate     *worldImage
	captured *worldImage
}

// image returns the world's image at point, walking the world that world makes first when no test has. world returns
// the world and its bare origin, or "" for a world with none.
func (ww *worldWalk) image(
	t *testing.T,
	point walkPoint,
	world func(t *testing.T) (*integrateWorld, string),
) worldImage {
	t.Helper()
	ww.mu.Lock()
	defer ww.mu.Unlock()
	if ww.captured == nil {
		w, origin := world(t)
		gate, captured := walkedImages(t, w, origin)
		ww.gate, ww.captured = &gate, &captured
	}
	if point == atGate {
		return *ww.gate
	}
	return *ww.captured
}

// walkedImages walks w to its gate and then to its capture, and returns its image at each. origin is the world's bare
// origin, or "" for a world with none.
func walkedImages(t *testing.T, w *integrateWorld, origin string) (worldImage, worldImage) {
	t.Helper()
	candidate := w.toGate()
	gate := imageOf(t, w, origin, candidate)
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	return gate, imageOf(t, w, origin, candidate)
}

// imageOf returns the image of w as it stands, the batch's candidate given. origin is the world's bare origin, or "".
func imageOf(t *testing.T, w *integrateWorld, origin, candidate string) worldImage {
	t.Helper()
	image := worldImage{
		db: checkpointed(t, w.path), repo: dirImage(t, w.repo), repoPath: w.repo, originPath: origin, base: w.base,
		candidate: candidate, checks: slices.Clone(w.fakes.checks.calls),
		evidence: slices.Clone(w.fakes.evidence.calls), steps: slices.Clone(w.fakes.steps.calls),
	}
	if origin != "" {
		image.origin = dirImage(t, origin)
	}
	return image
}

// checkpointed checkpoints the WAL of the open store at path into its file and returns the file's bytes, which then
// hold every committed write.
func checkpointed(t *testing.T, path string) []byte {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	var busy, logged, moved int
	if err = db.QueryRowContext(context.Background(), `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logged,
		&moved); err != nil {
		t.Fatalf("checkpoint %s: %v", path, err)
	}
	if busy != 0 {
		t.Fatalf("checkpoint %s: busy", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// dirImage returns the files and directories under dir, each with its mode.
func dirImage(t *testing.T, dir string) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[filepath.ToSlash(rel)] = &fstest.MapFile{Mode: fs.ModeDir | info.Mode().Perm()}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = &fstest.MapFile{Data: data, Mode: info.Mode()}
		return nil
	})
	if err != nil {
		t.Fatalf("copy %s: %v", dir, err)
	}
	return files
}

// restored returns a world restored from image in directories of t's own, with its origin, or "" when the image has
// none. The epic's repository is the restored repository, and the repository's origin remote the restored origin.
func (image worldImage) restored(t *testing.T) (*integrateWorld, string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.CopyFS(repo, image.repo); err != nil {
		t.Fatalf("restore repository: %v", err)
	}
	var origin string
	if image.origin != nil {
		origin = t.TempDir()
		if err := os.CopyFS(origin, image.origin); err != nil {
			t.Fatalf("restore origin: %v", err)
		}
		config := filepath.Join(repo, ".git", "config")
		text, err := os.ReadFile(config)
		if err != nil {
			t.Fatalf("read repository config: %v", err)
		}
		if !bytes.Contains(text, []byte(image.originPath)) {
			t.Fatalf("repository config names no origin %s:\n%s", image.originPath, text)
		}
		text = bytes.ReplaceAll(text, []byte(image.originPath), []byte(origin))
		if err = os.WriteFile(config, text, 0o600); err != nil {
			t.Fatalf("write repository config: %v", err)
		}
	}

	path := filepath.Join(t.TempDir(), "orrery.db")
	if err := os.WriteFile(path, image.db, 0o600); err != nil {
		t.Fatalf("restore store: %v", err)
	}
	moveRepository(t, path, image.repoPath, repo)
	s, err := store.Open(context.Background(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	fakes := newNodeFakes(s)
	fakes.checks.calls = slices.Clone(image.checks)
	fakes.evidence.calls = slices.Clone(image.evidence)
	fakes.steps.calls = slices.Clone(image.steps)
	logs := &bytes.Buffer{}
	runner := runNodes(s, fakes)
	runner.logger = slog.New(slog.NewTextHandler(logs, nil))
	return &integrateWorld{
		t: t, s: s, path: path, repo: repo, base: image.base, fakes: fakes, runner: runner, logs: logs,
	}, origin
}

// moveRepository points the test epic of the store file at path from the repository from to the repository to.
func moveRepository(t *testing.T, path, from, to string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	result, err := db.ExecContext(
		context.Background(),
		`UPDATE epics SET repository = ? WHERE id = ? AND repository = ?`,
		to,
		runEpic,
		from,
	)
	if err != nil {
		t.Fatalf("move the test epic's repository: %v", err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("move the test epic's repository: %d rows moved (%v), want 1", n, err)
	}
}

// integrateWorld returns an integrate world restored from its image at point, and the candidate of its first batch.
func (wi *worldImages) integrateWorld(t *testing.T, point walkPoint) (*integrateWorld, string) {
	t.Helper()
	image := wi.integrate.image(t, point, func(t *testing.T) (*integrateWorld, string) {
		t.Helper()
		return newIntegrateWorld(t), ""
	})
	w, _ := image.restored(t)
	return w, image.candidate
}

// syncWorld returns a sync world restored from its image at point, and the candidate of its first batch.
func (wi *worldImages) syncWorld(t *testing.T, point walkPoint) (*syncWorld, string) {
	t.Helper()
	image := wi.sync.image(t, point, func(t *testing.T) (*integrateWorld, string) {
		t.Helper()
		w := newSyncWorld(t)
		return w.integrateWorld, w.origin
	})
	w, origin := image.restored(t)
	return &syncWorld{integrateWorld: w, origin: origin}, image.candidate
}
