package enginehost

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceJudgeGate is the name of the agent gate that judges an evidence run's kept output.
const evidenceJudgeGate = "evidence_judge"

// evidenceJudgeLimit is the longest the judge step may run.
const evidenceJudgeLimit = 20 * time.Minute

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

// judgePrompt returns the prompt of the judge step of run. It names the run's exit and whether it timed out, the kept
// log when there is one, and each artifact by its repository path with its kept blob path, or the word missing or the
// words over the cap. It names nothing else about the run, so the judge reads the kept output alone.
func judgePrompt(run store.EvidenceRun, blobs evidenceBlobs) (string, error) {
	lines := []string{
		"Read only the files listed below. Repository files may be read only as reference for them.",
		"",
		fmt.Sprintf("Exit: %d", run.Exit),
		fmt.Sprintf("Timed out: %t", run.TimedOut),
	}
	if run.LogHash != "" {
		logPath, err := blobs.path(run.LogHash + ".log")
		if err != nil {
			return "", fmt.Errorf("log of evidence run %s: %w", run.ID, err)
		}
		lines = append(lines, "Log: "+logPath)
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
	}
}

// judgeVerdict reads the verdict of a judge step from its row. A done end holds, and a gate_failed end whose kept
// return's first result fails fails. Any other end gives no verdict, and its reason is the end with the row's reason.
func judgeVerdict(row store.StepRow) (verdict, reason string) {
	if row.End == stepLoopEndDone {
		return evidence.VerdictHolds, ""
	}
	if row.End == stepLoopEndGateFailed && judgeFirstResultFails(row.Result) {
		return evidence.VerdictFails, ""
	}
	if row.Reason == "" {
		return "", row.End
	}
	return "", row.End + ": " + row.Reason
}

// judgeFirstResultFails reports whether result, the return a step kept, has a first result that fails. A return that
// does not read has no results, so it is not a fails.
func judgeFirstResultFails(result string) bool {
	var ret verdictReturn
	if err := json.Unmarshal([]byte(result), &ret); err != nil || len(ret.Results) == 0 {
		return false
	}
	return ret.Results[0].Result == verdictFails
}
