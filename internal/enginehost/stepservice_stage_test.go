package enginehost

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestStepMessageCarriesStageGateAndVerdicts checks that stepMessage fills the stage and its start, the gate's name
// and limit in whole seconds, and every verdict in stored order, each with its round, kind, verdict and detail.
func TestStepMessageCarriesStageGateAndVerdicts(t *testing.T) {
	t.Parallel()
	row := fullStepRow()
	row.LoopStage = store.StepStageGate
	row.LoopStageFrom = time.Unix(1700000100, 0)
	row.GateName = "unit"
	row.GateLimit = 90*time.Second + 500*time.Millisecond
	row.Verdicts = []store.StepVerdict{
		{Kind: watchLoopKind, Round: 1, Verdict: "continue", Detail: "synthetic monitor detail"},
		{Kind: judgeLoopKind, Round: 2, Verdict: "pass", Detail: "synthetic judge detail"},
	}

	step, err := stepMessage(row, false)
	if err != nil {
		t.Fatalf("stepMessage: %v", err)
	}
	if got := step.GetStage(); got != store.StepStageGate {
		t.Errorf("stage = %q, want %q", got, store.StepStageGate)
	}
	if got := step.GetStageStarted(); got != 1700000100 {
		t.Errorf("stage started = %d, want 1700000100", got)
	}
	if got := step.GetGateName(); got != "unit" {
		t.Errorf("gate name = %q, want %q", got, "unit")
	}
	if got := step.GetGateLimitSeconds(); got != 90 {
		t.Errorf("gate limit seconds = %d, want 90 (the limit truncated to whole seconds)", got)
	}
	want := []*v1.StepVerdict{
		{Round: 1, Kind: watchLoopKind, Verdict: "continue", Detail: "synthetic monitor detail"},
		{Round: 2, Kind: judgeLoopKind, Verdict: "pass", Detail: "synthetic judge detail"},
	}
	got := step.GetVerdicts()
	if len(got) != len(want) {
		t.Fatalf("verdicts = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("verdict %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestStepMessageZeroStageStartIsZero checks that a row stored with no stage start yields a zero stage_started, not
// the unix seconds of the zero time.
func TestStepMessageZeroStageStartIsZero(t *testing.T) {
	t.Parallel()
	row := fullStepRow()
	row.LoopStage = store.StepStageTurn

	step, err := stepMessage(row, false)
	if err != nil {
		t.Fatalf("stepMessage: %v", err)
	}
	if got := step.GetStageStarted(); got != 0 {
		t.Errorf("stage started = %d, want 0 for a row with no stage start", got)
	}
}

// TestStepMessageRefusesVerdictRoundOutsideUint32 checks that a verdict round the reply cannot carry is an error, as a
// check round is.
func TestStepMessageRefusesVerdictRoundOutsideUint32(t *testing.T) {
	t.Parallel()
	for _, round := range []int{1 << 40, -1} {
		row := fullStepRow()
		row.Verdicts = []store.StepVerdict{{Kind: judgeLoopKind, Round: round, Verdict: "pass"}}

		if _, err := stepMessage(row, false); err == nil {
			t.Errorf("stepMessage with verdict round %d: no error, want one", round)
		}
	}
}
