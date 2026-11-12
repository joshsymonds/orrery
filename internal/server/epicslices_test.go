package server

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// sliceRedOnly and sliceRedGreen are synthetic writing step results carrying red evidence, and both red and green.
const (
	sliceRedOnly  = `{"red_evidence":"synthetic red"}`
	sliceRedGreen = `{"red_evidence":"synthetic red","green_evidence":"synthetic green"}`
)

// sliceStep returns a labeled writing step of repo/epic for task "t" that started an hour before epicsNow.
func sliceStep(end, result string, checks ...*enginev1.StepCheck) *enginev1.Step {
	return &enginev1.Step{
		Id: "step", Epic: "repo/epic", Task: "t", End: end, Result: result,
		Checks: checks, Started: epicsAgo(time.Hour),
	}
}

// sliceCheck returns a synthetic check of round named name, passed or failed.
func sliceCheck(round uint32, name string, ok bool) *enginev1.StepCheck {
	return &enginev1.StepCheck{Round: round, Name: name, Ok: ok}
}

// epicSlicesOf serves the epic repo/epic over the labeled steps and returns the slices its Get answers.
func epicSlicesOf(t *testing.T, steps ...*enginev1.Step) []*orreryv1.EpicSlice {
	t.Helper()
	root := t.TempDir()
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, epicsNow())
	client := epicsServe(t, root, &epicsFake{steps: steps})
	return epicsGet(t, client, "repo/epic").GetSlices()
}

// requireSlices fails unless got holds exactly want, in order.
func requireSlices(t *testing.T, got, want []*orreryv1.EpicSlice) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slices = %v, want %v", got, want)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("slice %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestEpicSlicesRingCells(t *testing.T) {
	cases := []struct {
		name string
		step *enginev1.Step
		want *orreryv1.EpicSlice
	}{
		{
			name: "working alone",
			step: sliceStep("", `{}`),
			want: &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", Working: true, At: 2},
		},
		{
			name: "red test",
			step: sliceStep("", sliceRedOnly),
			want: &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", RedTest: true, Working: true, At: 2},
		},
		{
			name: "tests pass",
			step: sliceStep("", sliceRedGreen),
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "dispatched", RedTest: true, Working: true, TestsPass: true, At: 3,
			},
		},
		{
			name: "tests pass without a red test",
			step: sliceStep("", `{"green_evidence":"synthetic green"}`),
			want: &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", Working: true, TestsPass: true, At: 3},
		},
		{
			name: "checks with a gate in the last round",
			step: sliceStep("", sliceRedGreen, sliceCheck(1, "schema", true), sliceCheck(2, "gate", true)),
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "dispatched", RedTest: true, Working: true, TestsPass: true, Checks: true, At: 4,
			},
		},
		{
			name: "gate accepted",
			step: sliceStep("done", sliceRedGreen, sliceCheck(1, "gate", true)),
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "done", RedTest: true, Working: true, TestsPass: true, Checks: true,
				GateAccepted: true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireSlices(t, epicSlicesOf(t, tc.step), []*orreryv1.EpicSlice{tc.want})
		})
	}
}

func TestEpicSlicesChecksReadTheLastRoundOnly(t *testing.T) {
	cases := []struct {
		name         string
		end          string
		checks       []*enginev1.StepCheck
		want         bool
		gateAccepted bool
	}{
		{
			name:         "done with no gate",
			end:          "done",
			checks:       []*enginev1.StepCheck{sliceCheck(1, "schema", true), sliceCheck(1, "build", true)},
			want:         false,
			gateAccepted: true,
		},
		{
			name: "last round without a gate after a passing gate round",
			checks: []*enginev1.StepCheck{
				sliceCheck(1, "gate", true), sliceCheck(2, "schema", true),
			},
			want: false,
		},
		{
			name: "last round with a failed check beside its gate",
			checks: []*enginev1.StepCheck{
				sliceCheck(1, "gate", true), sliceCheck(2, "gate", true), sliceCheck(2, "test", false),
			},
			want: false,
		},
		{
			name: "last round's gate failed",
			checks: []*enginev1.StepCheck{
				sliceCheck(1, "gate", true), sliceCheck(2, "gate", false),
			},
			want: false,
		},
		{
			name: "earlier round failed, last round passes with its gate",
			checks: []*enginev1.StepCheck{
				sliceCheck(1, "gate", false), sliceCheck(2, "gate", true),
			},
			want: true,
		},
		{
			name:   "no checks",
			checks: nil,
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := epicSlicesOf(t, sliceStep(tc.end, sliceRedGreen, tc.checks...))
			if len(got) != 1 {
				t.Fatalf("slices = %v, want one", got)
			}
			if got[0].GetChecks() != tc.want {
				t.Errorf("checks = %v, want %v", got[0].GetChecks(), tc.want)
			}
			if got[0].GetGateAccepted() != tc.gateAccepted {
				t.Errorf("gate_accepted = %v, want %v", got[0].GetGateAccepted(), tc.gateAccepted)
			}
		})
	}
}

func TestEpicSlicesEvidenceCountsForNothing(t *testing.T) {
	cases := []struct {
		name   string
		result string
	}{
		{name: "empty", result: ""},
		{name: "unparsable", result: "{not json"},
		{name: "not an object", result: `["red_evidence"]`},
		{name: "NOT RUN and not run", result: `{"red_evidence":"NOT RUN: x","green_evidence":"  not run"}`},
		{name: "blank and Not Run", result: `{"red_evidence":"   ","green_evidence":"Not Run"}`},
		{name: "non-string evidence", result: `{"red_evidence":7,"green_evidence":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := epicSlicesOf(t, sliceStep("", tc.result))
			want := &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", Working: true, At: 2}
			requireSlices(t, got, []*orreryv1.EpicSlice{want})
		})
	}
}

func TestEpicSlicesHueAndRing(t *testing.T) {
	gate := sliceCheck(1, "gate", true)
	cases := []struct {
		name string
		end  string
		held bool
		hue  string
		at   int32
	}{
		{name: "running", end: "", hue: "dispatched", at: 4},
		{name: "held", end: "", held: true, hue: "dispatched", at: 4},
		{name: "done", end: "done", hue: "done", at: 0},
		{name: "failed", end: "failed", hue: "gap", at: 0},
		{name: "returned", end: "returned", hue: "gap", at: 0},
		{name: "checks failed", end: "checks_failed", hue: "gap", at: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := sliceStep(tc.end, sliceRedGreen, gate)
			step.Held = tc.held
			got := epicSlicesOf(t, step)
			if len(got) != 1 {
				t.Fatalf("slices = %v, want one", got)
			}
			if got[0].GetHue() != tc.hue || got[0].GetAt() != tc.at {
				t.Errorf("hue %q at %d, want %q at %d", got[0].GetHue(), got[0].GetAt(), tc.hue, tc.at)
			}
		})
	}
}

func TestEpicSlicesSkipReadOnlyAndUntasked(t *testing.T) {
	gate := sliceCheck(1, "gate", true)
	got := epicSlicesOf(t,
		&enginev1.Step{
			Id: "read", Epic: "repo/epic", Task: "t", ReadOnly: true, End: "done",
			Result: sliceRedGreen, Checks: []*enginev1.StepCheck{gate}, Started: epicsAgo(time.Hour),
		},
		&enginev1.Step{
			Id: "untasked", Epic: "repo/epic", End: "", Result: sliceRedGreen, Started: epicsAgo(time.Hour),
		},
	)
	if len(got) != 0 {
		t.Errorf("slices = %v, want none", got)
	}
}

func TestEpicSlicesOrderByFirstStartAndCellsFromLatest(t *testing.T) {
	gate := sliceCheck(1, "gate", true)
	// The steps arrive out of order. Label b starts first, and its latest writing step is the one that ended done;
	// label a's latest writing step is its only one, since the read-only step between them adds nothing.
	got := epicSlicesOf(t,
		&enginev1.Step{
			Id: "b-latest", Epic: "repo/epic", Task: "b", End: "done", Result: sliceRedGreen,
			Checks: []*enginev1.StepCheck{gate}, Started: epicsAgo(time.Hour),
		},
		&enginev1.Step{
			Id: "a-read", Epic: "repo/epic", Task: "a", ReadOnly: true, End: "done", Result: sliceRedGreen,
			Checks: []*enginev1.StepCheck{gate}, Started: epicsAgo(90 * time.Minute),
		},
		&enginev1.Step{
			Id: "a-only", Epic: "repo/epic", Task: "a", End: "returned", Result: `{}`,
			Started: epicsAgo(2 * time.Hour),
		},
		&enginev1.Step{
			Id: "b-first", Epic: "repo/epic", Task: "b", End: "", Result: `{}`, Started: epicsAgo(3 * time.Hour),
		},
		&enginev1.Step{
			Id: "untasked", Epic: "repo/epic", End: "done", Result: sliceRedGreen,
			Checks: []*enginev1.StepCheck{gate}, Started: epicsAgo(10 * time.Minute),
		},
	)
	requireSlices(t, got, []*orreryv1.EpicSlice{
		{
			Task: "b", Hue: "done", RedTest: true, Working: true, TestsPass: true, Checks: true,
			GateAccepted: true,
		},
		{Task: "a", Hue: "gap", Working: true},
	})
}
