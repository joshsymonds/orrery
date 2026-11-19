package enginehost

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// judgeCarry returns the lines judgeCarryLines gives for run when run is a Requirement's green run, whose judge is shown the
// earlier judged runs of its item. A task's run and a red run read nothing, so they give nil without reading the store.
// Otherwise it reads the runs of run's epic, and returns an error when they cannot be read.
func (e *Evidence) judgeCarry(ctx context.Context, run store.EvidenceRun) ([]string, error) {
	if run.Phase != evidence.PhaseGreen || strings.HasPrefix(run.Item, "T") {
		return nil, nil
	}
	runs, err := e.store.EvidenceRuns(ctx, run.Epic)
	if err != nil {
		return nil, fmt.Errorf("evidence runs of epic %s for evidence run %s: %w", run.Epic, run.ID, err)
	}
	return judgeCarryLines(run, runs, e.blobs)
}

// judgeCarryLines returns the lines a judge prompt of run puts after judgePrompt's, which carry the earlier judged runs of
// run's evidence item to its judge. The earlier runs are the members of runs that come before run in the store's order, with
// an ID other than run's, the epic and item of run, phase green, a spec equal to run's and a verdict. With none it returns
// nil. Otherwise the lines are a heading, one line per earlier run oldest first with its revision, verdict and reason, the
// stdout and stderr log paths of the latest earlier run whose verdict is holds, the revision of the latest earlier run and the
// revision now judged, and a question: whether the earlier verdict still applies given what changed between those two
// revisions. It returns an error when a log's name is not a blob name.
func judgeCarryLines(run store.EvidenceRun, runs []store.EvidenceRun, blobs evidenceBlobs) ([]string, error) {
	var earlier []store.EvidenceRun
	for _, other := range runs {
		if other.ID == run.ID {
			break
		}
		if other.Epic == run.Epic && other.Item == run.Item && other.Phase == evidence.PhaseGreen &&
			reflect.DeepEqual(other.Spec, run.Spec) && other.Verdict != "" {
			earlier = append(earlier, other)
		}
	}
	if len(earlier) == 0 {
		return nil, nil
	}
	lines := []string{"Earlier judged runs of this evidence item, oldest first:"}
	holds := -1
	for i, prior := range earlier {
		if prior.Verdict == evidence.VerdictHolds {
			holds = i
		}
		reason := prior.JudgeReason
		if reason == "" {
			reason = "no reason recorded"
		}
		lines = append(
			lines,
			fmt.Sprintf("- revision %s: verdict %s, reason: %s", prior.Revision, prior.Verdict, reason),
		)
	}
	if holds >= 0 {
		logs, err := judgeCarryLogLines(earlier[holds], blobs)
		if err != nil {
			return nil, err
		}
		lines = append(lines, logs...)
	}
	latest := earlier[len(earlier)-1]
	lines = append(
		lines,
		fmt.Sprintf(
			"The latest earlier run is at revision %s; the revision now judged is %s.",
			latest.Revision,
			run.Revision,
		),
		"Decide whether the earlier verdict still applies given what changed between those two revisions. "+
			"You may run git diff between them in your worktree, which is at the judged revision. "+
			"Give your reason in the evidence of your result.",
	)
	return lines, nil
}

// judgeCarryLogLines returns a line naming each of holds's stdout and stderr logs that it has, by its blob path, as
// judgePrompt names a run's logs. It returns an error when a log's name is not a blob name.
func judgeCarryLogLines(holds store.EvidenceRun, blobs evidenceBlobs) ([]string, error) {
	logs := []struct{ label, hash string }{{"Stdout log", holds.LogHash}, {"Stderr log", holds.StderrHash}}
	var lines []string
	for _, entry := range logs {
		if entry.hash == "" {
			continue
		}
		logPath, err := blobs.path(entry.hash + ".log")
		if err != nil {
			return nil, fmt.Errorf("%s of evidence run %s: %w", entry.label, holds.ID, err)
		}
		lines = append(
			lines,
			fmt.Sprintf("%s of the latest holds run at revision %s: %s", entry.label, holds.Revision, logPath),
		)
	}
	return lines, nil
}
