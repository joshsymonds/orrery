package enginehost

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// unjudgedVerdict returns the verdict and reason run is recorded with without a judge, and whether it has one. A green run
// whose capture exited non-zero has the verdict greenExitReason gives, since evidence.Passed passes no such run whatever a
// judge finds. A run an earlier judged run of its item matches exactly has that run's verdict (see reusedVerdict). Any other
// run has none and goes to its judge, and so does a run whose match could not be read, which is logged.
func (e *Evidence) unjudgedVerdict(ctx context.Context, repo string, run store.EvidenceRun) (string, string, bool) {
	if run.Phase == evidence.PhaseGreen && run.Exit != 0 {
		return evidence.VerdictFails, greenExitReason(run.Exit), true
	}
	verdict, reason, ok, err := e.reusedVerdict(ctx, repo, run)
	if err != nil {
		e.logger.WarnContext(ctx, "evidence verdict not reused, judged instead", "run", run.ID, "err", err)
		return "", "", false
	}
	return verdict, reason, ok
}

// greenExitReason returns the reason a green run whose capture exited exit is recorded fails with, without a judge: green
// requires exit 0, and a conclusion that expects another exit wraps its command so that it exits 0 when it holds.
func greenExitReason(exit int) string {
	return fmt.Sprintf(
		"orrery recorded fails without a judge: the capture exited %d, and a green run passes only when its capture "+
			"exits 0, so no verdict could pass it. A conclusion that expects another exit must wrap its command so that it "+
			"exits 0 when the conclusion holds, such as `! git grep …` for text that must be absent.",
		exit,
	)
}

// reusedVerdict returns the verdict and reason of the latest earlier judged run that run matches exactly, with a note
// naming it, and whether there is one. The earlier run is one of run's epic other than run, of its item and phase, with a
// verdict, captured, with a spec equal to run's, the same exit, timeout, stdout and stderr hashes and artifacts, a capture
// with the same argv and runner when either has a capture, and a revision whose tree is run's revision's tree. The same
// command over the same files gave the same bytes, so a judge would read what that run's judge read. It returns an error
// when the runs, a capture or a tree cannot be read.
func (e *Evidence) reusedVerdict(
	ctx context.Context, repo string, run store.EvidenceRun,
) (string, string, bool, error) {
	runs, err := e.store.EvidenceRuns(ctx, run.Epic)
	if err != nil {
		return "", "", false, fmt.Errorf("evidence runs of epic %s: %w", run.Epic, err)
	}
	trees := map[string]string{}
	for _, earlier := range slices.Backward(runs) {
		if !sameCapturedOutput(run, earlier) {
			continue
		}
		same, matchErr := e.sameCaptureAndTree(ctx, repo, run, earlier, trees)
		if matchErr != nil {
			return "", "", false, matchErr
		}
		if same {
			return earlier.Verdict, reusedReason(earlier), true, nil
		}
	}
	return "", "", false, nil
}

// sameCapturedOutput reports whether earlier is a judged run other than run that ran run's item in run's phase with run's
// spec and left the same exit, timeout, logs and artifacts.
func sameCapturedOutput(run, earlier store.EvidenceRun) bool {
	return earlier.ID != run.ID && earlier.Epic == run.Epic && earlier.Item == run.Item && earlier.Phase == run.Phase &&
		earlier.Verdict != "" && earlier.NotCaptured == "" && reflect.DeepEqual(earlier.Spec, run.Spec) &&
		earlier.Exit == run.Exit && earlier.TimedOut == run.TimedOut && earlier.LogHash == run.LogHash &&
		earlier.StderrHash == run.StderrHash && reflect.DeepEqual(earlier.Artifacts, run.Artifacts)
}

// sameCaptureAndTree reports whether earlier's capture has run's argv and runner, when either has a capture, and earlier's
// revision has run's revision's tree in repo. trees holds the tree of each revision read so far, by revision. It returns an
// error when a capture or a tree cannot be read.
func (e *Evidence) sameCaptureAndTree(
	ctx context.Context, repo string, run, earlier store.EvidenceRun, trees map[string]string,
) (bool, error) {
	if run.Capture != earlier.Capture && (run.Capture == "" || earlier.Capture == "") {
		return false, nil
	}
	if run.Capture != earlier.Capture {
		same, err := e.sameCaptureCommand(ctx, run.Capture, earlier.Capture)
		if err != nil || !same {
			return false, err
		}
	}
	if run.Revision == earlier.Revision {
		return true, nil
	}
	tree, err := e.revisionTree(ctx, repo, run, run.Revision, trees)
	if err != nil {
		return false, err
	}
	earlierTree, err := e.revisionTree(ctx, repo, run, earlier.Revision, trees)
	if err != nil {
		return false, err
	}
	return tree == earlierTree, nil
}

// sameCaptureCommand reports whether the captures with ids first and second ran the same argv under the same runner. It
// returns an error when there is no briefs store to read them from, or either cannot be read.
func (e *Evidence) sameCaptureCommand(ctx context.Context, first, second string) (bool, error) {
	if e.briefs == nil {
		return false, fmt.Errorf("captures %s and %s: no briefs store", first, second)
	}
	one, err := e.briefs.Capture(ctx, first)
	if err != nil {
		return false, fmt.Errorf("capture %s: %w", first, err)
	}
	other, err := e.briefs.Capture(ctx, second)
	if err != nil {
		return false, fmt.Errorf("capture %s: %w", second, err)
	}
	return slices.Equal(one.Argv, other.Argv) && one.Runner == other.Runner, nil
}

// revisionTree returns the tree of revision in repo, read with git rev-parse REV^{tree} on the host of run's epic, and keeps
// it in trees by revision, where a revision already read is taken from. It returns an error when the host or the tree
// cannot be read.
func (e *Evidence) revisionTree(
	ctx context.Context, repo string, run store.EvidenceRun, revision string, trees map[string]string,
) (string, error) {
	if tree, ok := trees[revision]; ok {
		return tree, nil
	}
	host, err := hostOfEpic(ctx, e.store, e.hosts, run.Epic)
	if err != nil {
		return "", err
	}
	tree, err := stepGit(ctx, host, repo, "rev-parse", "--verify", "--end-of-options", revision+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("tree of revision %s: %w", revision, err)
	}
	trees[revision] = tree
	return tree, nil
}

// reusedReason returns the reason a run that reuses earlier's verdict keeps: a note naming earlier and its revision, then
// earlier's reason, or a note that it recorded none.
func reusedReason(earlier store.EvidenceRun) string {
	reason := earlier.JudgeReason
	if reason == "" {
		reason = "no reason recorded"
	}
	return fmt.Sprintf(
		"orrery reused the verdict of evidence run %s at revision %s without a judge: that run captured the same item and "+
			"command in the same phase over the same tree, and left the same exit, logs and artifacts. Its reason: %s",
		earlier.ID,
		earlier.Revision,
		reason,
	)
}
