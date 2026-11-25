package enginehost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/taskspec"
)

// evidenceJudgeGate is the name of the agent gate that judges an evidence run's kept output.
const evidenceJudgeGate = "evidence_judge"

// evidenceJudgeLimit is the longest the judge step may run.
const evidenceJudgeLimit = 20 * time.Minute

// evidenceJudgeNamespace starts the parts a judge's derived id hashes, so the ids are this package's.
const evidenceJudgeNamespace = "orrery-evidence-judge"

// evidenceJudgeIDLen is the number of hex digits of a judge step's id, the form StepStart.ID requires.
const evidenceJudgeIDLen = 32

// evidenceJudgeID returns the id of judge number judge of the evidence run runID. A judge starts under its id, and a restart
// finds a judge that started but was not recorded by the id of the next number. The same inputs give the same id, so a judge
// started again under its id is that judge.
func evidenceJudgeID(runID string, judge int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{evidenceJudgeNamespace, runID, strconv.Itoa(judge)}, "\x00")))
	return hex.EncodeToString(sum[:])[:evidenceJudgeIDLen]
}

// The states an artifact of an evidence run is stored with.
const (
	judgeStateKept    = "kept"
	judgeStateMissing = "missing"
	judgeStateOverCap = "over_cap"
)

// judgeGate returns the agent gate that judges item's conclusion: the item's judge checks one must assertion, the
// conclusion, within evidenceJudgeLimit.
func judgeGate(item evidence.Item) gates.Gate {
	return gates.Gate{
		Name:       evidenceJudgeGate,
		Agent:      item.Judge,
		Limit:      evidenceJudgeLimit,
		Assertions: []gates.Assertion{{Text: item.Conclusion, Kind: gates.KindMust}},
	}
}

// judgePrompt returns the prompt of the judge step of run. It names the run's capture command as a JSON array of its
// argv, when the run has a capture, its exit and whether it timed out, its stdout and stderr logs by their blob paths
// for each one it has, and each artifact by its repository path with its kept blob path, or the word missing or the
// words over the cap. It names nothing else about the run. The prompt lets the judge read the source of a named test
// that the output shows passing, at the run's revision, and asks it to decide whether the logs, artifacts and those
// passing tests show what the conclusion is meant to prove.
func judgePrompt(run store.EvidenceRun, blobs evidenceBlobs) (string, error) {
	lines := []string{
		"Read only the files listed below. Repository files may be read only as reference for them.",
		"When a log shows a named test passing, you may read that test's source in this worktree, which is at the " +
			"judged revision, to see what it asserts.",
		"Decide whether the logs, artifacts and those passing tests show what the conclusion is meant to prove.",
		"",
	}
	if len(run.Spec.Capture) > 0 {
		argv, err := json.Marshal(run.Spec.Capture)
		if err != nil {
			return "", fmt.Errorf("command of evidence run %s: %w", run.ID, err)
		}
		lines = append(lines, "Command: "+string(argv))
	}
	lines = append(lines,
		fmt.Sprintf("Exit: %d", run.Exit),
		fmt.Sprintf("Timed out: %t", run.TimedOut),
	)
	logs := []struct{ label, hash string }{{"Stdout log", run.LogHash}, {"Stderr log", run.StderrHash}}
	for _, entry := range logs {
		if entry.hash == "" {
			continue
		}
		logPath, err := blobs.path(entry.hash + ".log")
		if err != nil {
			return "", fmt.Errorf("%s of evidence run %s: %w", entry.label, run.ID, err)
		}
		lines = append(lines, entry.label+": "+logPath)
	}
	if len(run.Artifacts) > 0 {
		lines = append(lines, "Artifacts:")
	}
	for _, artifact := range run.Artifacts {
		where, err := judgeArtifactWhere(artifact, blobs)
		if err != nil {
			return "", fmt.Errorf("artifact of evidence run %s: %w", run.ID, err)
		}
		lines = append(lines, artifact.Path+": "+where)
	}
	return strings.Join(lines, "\n"), nil
}

// judgeArtifactWhere returns where the judge prompt says artifact is: its kept blob path, or the words for a state that
// keeps no blob. A state the store does not keep is an error.
func judgeArtifactWhere(artifact store.EvidenceArtifact, blobs evidenceBlobs) (string, error) {
	switch artifact.State {
	case judgeStateKept:
		return blobs.path(artifact.Blob)
	case judgeStateMissing:
		return "missing", nil
	case judgeStateOverCap:
		return "over the 10 MiB cap", nil
	}
	return "", fmt.Errorf("state %q is not one the store keeps", artifact.State)
}

// judgeSpecLines returns the lines a judge prompt of run puts before its own, which name the spec version of task whose
// conclusion the judge judges. In phase red while task has a checking version that changes its evidence, they name that
// version's evidence item as the conclusion the red run checks at the task's base, and say that the version governs only
// once its red does not hold there. Otherwise, for a task at version above 1, they name the task's current version's
// evidence item, which governs where it differs from earlier versions. Both begin with the lines taskspec.Lines gives for
// task, so a task at version 1 with a checking version has only the checking line. It returns nil for a task at version 1
// or lower with no checking version in red, whose prompts stay as they were before any version changed it.
func judgeSpecLines(run store.EvidenceRun, task store.EpicTask) []string {
	if version, ok := task.Checking(); ok && run.Phase == evidence.PhaseRed && version.Evidence != nil {
		return append(taskspec.Lines(task), fmt.Sprintf(
			"Conclusion: the conclusion you judge is version %d's evidence item, "+
				"which this red run checks at the task's base. "+
				"Version %d governs only once its red does not hold there.", version.Version, version.Version,
		))
	}
	if task.Version > 1 {
		return append(taskspec.Lines(task), fmt.Sprintf(
			"Conclusion: the conclusion you judge is version %d's evidence item, the task's current spec, "+
				"which governs where it differs from earlier versions.", task.Version,
		))
	}
	return nil
}

// judgeCaptureLines returns the lines a judge prompt of run puts after judgePrompt's when run has the capture capture. They
// name the capture by its id, its argv as a JSON array, its commit and its runner, which is "local" when it names none, and
// the step that asked for it by its id and role. They ask the judge to decide whether that command at that commit, run by
// that runner, could have produced the bytes named above, and to fail the run when the output does not fit that command. It
// returns an error when the argv cannot be encoded.
func judgeCaptureLines(run store.EvidenceRun, capture store.Capture) ([]string, error) {
	argv, err := json.Marshal(capture.Argv)
	if err != nil {
		return nil, fmt.Errorf("argv of capture %s of evidence run %s: %w", capture.ID, run.ID, err)
	}
	runner := capture.Runner
	if runner == "" {
		runner = localRunnerName
	}
	return []string{
		"Capture: " + capture.ID,
		"Capture command: " + string(argv),
		"Capture commit: " + capture.Commit,
		"Capture runner: " + runner,
		"Asking step: " + capture.Step,
		"Asking role: " + capture.Role,
		"Decide whether that command at that commit, run by that runner, could have produced those bytes. " +
			"Fail the run when the output does not fit that command.",
	}, nil
}

// judgeStart returns the read-only start of the judge step of run at repo. The step runs at run's revision under the
// judge gate of run's item, with prompt, and is labeled with run's epic and the run's evidence task. The gate supplies
// the agent, schema and done value.
func judgeStart(run store.EvidenceRun, repo, prompt string) StepStart {
	gate := judgeGate(run.Spec)
	return StepStart{
		ReadOnly:  true,
		Workspace: repo,
		Revision:  run.Revision,
		Prompt:    prompt,
		AgentGate: &gate,
		Epic:      run.Epic,
		Task:      "evidence-" + run.ID,
		Purpose:   judgePurpose(run),
	}
}

// The places a judge phase is judged at: a red run at the task's base, any other run at head.
const (
	judgePlaceBase = "base"
	judgePlaceHead = "head"
)

// judgePurpose returns the purpose of the judge step of run: its phase, the place the phase is judged at, which is base for
// a red run and head otherwise, and the item it judges.
func judgePurpose(run store.EvidenceRun) string {
	place := judgePlaceHead
	if run.Phase == evidence.PhaseRed {
		place = judgePlaceBase
	}
	return fmt.Sprintf("Judge %s's %s evidence at %s", run.Item, run.Phase, place)
}

// judgeVerdict reads the verdict of a judge step from its row, and the reason it keeps: the judge's reasoning, read from
// the row's result, which is the return the step kept. A done end holds, with the first result's evidence, trimmed, as its
// reason; the reason is empty when the return does not read or has no results. A gate_failed end whose kept return's first
// result fails fails, with the reason judgeFailsReason gives. Any other end gives no verdict, and its reason is the end with
// the row's reason. The reasoning is kept as the judge wrote it, and is never checked.
func judgeVerdict(row store.StepRow) (verdict, reason string) {
	if row.End == stepLoopEndDone {
		return evidence.VerdictHolds, judgeHoldsReason(row.Result)
	}
	if row.End == stepLoopEndGateFailed {
		if ret, ok := judgeReturn(row.Result); ok && ret.Results[0].Result == verdictFails {
			return evidence.VerdictFails, judgeFailsReason(ret)
		}
	}
	if row.Reason == "" {
		return "", row.End
	}
	return "", row.End + ": " + row.Reason
}

// judgeReturn reads result, the return a judge step kept, as the verdict reads it. It reports false when result does not
// read or has no results, so that there is no first result to read.
func judgeReturn(result string) (verdictReturn, bool) {
	var ret verdictReturn
	if err := json.Unmarshal([]byte(result), &ret); err != nil || len(ret.Results) == 0 {
		return verdictReturn{}, false
	}
	return ret, true
}

// judgeHoldsReason returns the reason a holds verdict keeps for result, the return a judge step kept: the first result's
// evidence with its spaces trimmed. It is empty when result does not read or has no results.
func judgeHoldsReason(result string) string {
	ret, ok := judgeReturn(result)
	if !ok {
		return ""
	}
	return strings.TrimSpace(ret.Results[0].Evidence)
}

// judgeFailsReason returns the reason a fails verdict keeps for ret, a return whose first result fails. It is the claim and
// evidence of the first finding that cites that result's assertion, joined by ": " with an empty one left out. It is the
// first result's evidence when no finding cites that assertion, or when the finding has neither a claim nor evidence.
func judgeFailsReason(ret verdictReturn) string {
	first := ret.Results[0]
	i := slices.IndexFunc(ret.Findings, func(f verdictFinding) bool { return f.Citation == first.Assertion })
	if i < 0 {
		return first.Evidence
	}
	finding := ret.Findings[i]
	reason := strings.Join(slices.DeleteFunc([]string{finding.Claim, finding.Evidence}, func(s string) bool {
		return s == ""
	}), ": ")
	if reason == "" {
		return first.Evidence
	}
	return reason
}
