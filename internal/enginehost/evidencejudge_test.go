package enginehost

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// judgeTestBlobDir is the blob directory the judge tests name blob paths under.
const judgeTestBlobDir = "/state/evidence/blobs"

// judgeTestHash returns a sha256-shaped name of lowercase hex: the hex digit c repeated 64 times.
func judgeTestHash(c string) string {
	return strings.Repeat(c, 64)
}

// judgeTestRun returns a judging run of a captured item whose capture exited 3 at its limit. Its log and one artifact
// are kept, one artifact is missing and one is over the cap.
func judgeTestRun() store.EvidenceRun {
	blob := judgeTestHash("b") + ".json"
	return store.EvidenceRun{
		ID:       "run-1",
		Epic:     "epic-1",
		Item:     "item-1",
		Revision: "rev-1",
		Phase:    evidence.PhaseGreen,
		Source:   evidence.SourceCaptured,
		State:    "judging",
		Spec: evidence.Item{
			Capture:    []string{"tool/run"},
			Limit:      60,
			Conclusion: "the build passes",
			Judge:      "reviewer",
		},
		Exit:     3,
		TimedOut: true,
		LogHash:  judgeTestHash("a"),
		Artifacts: []store.EvidenceArtifact{
			{Path: "out/store.json", State: "kept", Hash: judgeTestHash("b"), Blob: blob, Size: 512},
			{Path: "out/trace.txt", State: "missing"},
			{Path: "out/big.bin", State: "over_cap"},
		},
	}
}

func TestEvidenceJudgeGateFields(t *testing.T) {
	t.Parallel()
	item := evidence.Item{
		Capture:    []string{"tool/run"},
		Limit:      60,
		Conclusion: "the build passes",
		Judge:      "reviewer",
	}
	wantGate := gates.Gate{
		Name:       "evidence_judge",
		Agent:      "reviewer",
		Limit:      20 * time.Minute,
		Assertions: []gates.Assertion{{Text: "the build passes", Kind: gates.KindMust}},
	}
	if got := judgeGate(item); !reflect.DeepEqual(got, wantGate) {
		t.Errorf("judgeGate = %+v, want %+v", got, wantGate)
	}
}

func TestEvidenceJudgePromptNamesKeptFilesAndMarksTheRest(t *testing.T) {
	t.Parallel()
	prompt, err := judgePrompt(judgeTestRun(), evidenceBlobs{dir: judgeTestBlobDir})
	if err != nil {
		t.Fatalf("judgePrompt: %v", err)
	}
	wants := []string{
		"read only",
		"Exit: 3",
		"Timed out: true",
		filepath.Join(judgeTestBlobDir, judgeTestHash("a")+".log"),
		"out/store.json: " + filepath.Join(judgeTestBlobDir, judgeTestHash("b")+".json"),
		"out/trace.txt: missing",
		"out/big.bin: over the 10 MiB cap",
	}
	for _, want := range wants {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(want)) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
}

func TestEvidenceJudgePromptOmitsLogWithoutOne(t *testing.T) {
	t.Parallel()
	run := judgeTestRun()
	run.LogHash = ""
	run.Artifacts = nil
	prompt, err := judgePrompt(run, evidenceBlobs{dir: judgeTestBlobDir})
	if err != nil {
		t.Fatalf("judgePrompt: %v", err)
	}
	if strings.Contains(prompt, ".log") {
		t.Errorf("prompt names a log though the run has none:\n%s", prompt)
	}
}

func TestEvidenceJudgePromptNeverNamesPhaseOrSource(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{evidence.PhaseRed, evidence.PhaseGreen} {
		for _, source := range []string{evidence.SourceCaptured, evidence.SourceSupplied} {
			run := judgeTestRun()
			run.Phase = phase
			run.Source = source
			prompt, err := judgePrompt(run, evidenceBlobs{dir: judgeTestBlobDir})
			if err != nil {
				t.Fatalf("judgePrompt for phase %s source %s: %v", phase, source, err)
			}
			lower := strings.ToLower(prompt)
			for _, word := range []string{"red", "green", "phase", evidence.SourceCaptured, evidence.SourceSupplied} {
				if strings.Contains(lower, word) {
					t.Errorf("prompt for phase %s source %s names %q:\n%s", phase, source, word, prompt)
				}
			}
		}
	}
}

func TestEvidenceJudgePromptRefusesBadNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(run *store.EvidenceRun)
	}{
		{
			name:   "log hash that is not a hash",
			mutate: func(run *store.EvidenceRun) { run.LogHash = "../escape" },
		},
		{
			name:   "kept blob that is not a blob name",
			mutate: func(run *store.EvidenceRun) { run.Artifacts[0].Blob = "../escape" },
		},
		{
			name:   "artifact in no known state",
			mutate: func(run *store.EvidenceRun) { run.Artifacts[1].State = "unknown" },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := judgeTestRun()
			tc.mutate(&run)
			if _, err := judgePrompt(run, evidenceBlobs{dir: judgeTestBlobDir}); err == nil {
				t.Error("judgePrompt error = nil, want an error")
			}
		})
	}
}

func TestEvidenceJudgeStartFields(t *testing.T) {
	t.Parallel()
	run := judgeTestRun()
	start := judgeStart(run, "/repo", "judge the run")
	if start.AgentGate == nil {
		t.Fatal("AgentGate is nil, want the judge gate")
	}
	if wantGate := judgeGate(run.Spec); !reflect.DeepEqual(*start.AgentGate, wantGate) {
		t.Errorf("AgentGate = %+v, want %+v", *start.AgentGate, wantGate)
	}
	start.AgentGate = nil
	want := StepStart{
		ReadOnly:  true,
		Workspace: "/repo",
		Revision:  "rev-1",
		Prompt:    "judge the run",
		Epic:      "epic-1",
		Task:      "evidence-run-1",
	}
	if !reflect.DeepEqual(start, want) {
		t.Errorf("StepStart = %+v, want %+v", start, want)
	}
}

func TestEvidenceJudgeVerdict(t *testing.T) {
	t.Parallel()
	const assertion = "the build passes"
	fails := `{"status":"done","results":[{"assertion":"` + assertion + `","kind":"must","result":"fails"}],` +
		`"findings":[{"citation":"` + assertion + `"}]}`
	undetermined := `{"status":"done","results":[{"assertion":"` + assertion + `","kind":"must",` +
		`"result":"undetermined"}],"findings":[]}`
	holdsThenFails := `{"status":"done","results":[{"assertion":"a","kind":"must","result":"holds"},` +
		`{"assertion":"b","kind":"must","result":"fails"}],"findings":[]}`
	cases := []struct {
		name        string
		row         store.StepRow
		wantVerdict string
		wantReason  string
	}{
		{
			name:        "done is holds",
			row:         store.StepRow{End: "done", Status: "done"},
			wantVerdict: evidence.VerdictHolds,
		},
		{
			name:        "gate_failed whose first result fails is fails",
			row:         store.StepRow{End: "gate_failed", Result: fails, Reason: "fails: " + assertion},
			wantVerdict: evidence.VerdictFails,
		},
		{
			name:       "gate_failed whose first result is undetermined is no verdict",
			row:        store.StepRow{End: "gate_failed", Result: undetermined, Reason: "undetermined: " + assertion},
			wantReason: "gate_failed: undetermined: " + assertion,
		},
		{
			name:       "gate_failed whose only fails result is not first is no verdict",
			row:        store.StepRow{End: "gate_failed", Result: holdsThenFails, Reason: "fails: b"},
			wantReason: "gate_failed: fails: b",
		},
		{
			name:       "gate_failed whose return does not read is no verdict",
			row:        store.StepRow{End: "gate_failed", Result: "{", Reason: "fails: " + assertion},
			wantReason: "gate_failed: fails: " + assertion,
		},
		{
			name:       "overtime with a reason is no verdict",
			row:        store.StepRow{End: "overtime", Reason: "gate limit reached"},
			wantReason: "overtime: gate limit reached",
		},
		{
			name:       "overtime without a reason is no verdict",
			row:        store.StepRow{End: "overtime"},
			wantReason: "overtime",
		},
		{
			name:       "failed is no verdict",
			row:        store.StepRow{End: "failed", Reason: "boom"},
			wantReason: "failed: boom",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			verdict, reason := judgeVerdict(tc.row)
			if verdict != tc.wantVerdict || reason != tc.wantReason {
				t.Errorf("judgeVerdict = (%q, %q), want (%q, %q)", verdict, reason, tc.wantVerdict, tc.wantReason)
			}
		})
	}
}
