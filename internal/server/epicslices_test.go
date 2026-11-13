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
			want: &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", Working: true, At: 1},
		},
		{
			name: "red test",
			step: sliceStep("", sliceRedOnly),
			want: &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", RedTest: true, Working: true, At: 3},
		},
		{
			name: "tests pass",
			step: sliceStep("", sliceRedGreen),
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "dispatched", RedTest: true, Working: true, TestsPass: true, At: 4,
			},
		},
		{
			name: "tests pass without a red test",
			step: sliceStep("", `{"green_evidence":"synthetic green"}`),
			want: &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", Working: true, TestsPass: true, At: 1},
		},
		{
			name: "checks with a gate in the last round",
			step: sliceStep("", sliceRedGreen, sliceCheck(1, "schema", true), sliceCheck(2, "gate", true)),
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "dispatched", RedTest: true, Working: true, TestsPass: true, Checks: true, At: 5,
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

// TestEpicSlicesRingRows serves each row of chart-spec.md section 2's example table through Get and checks its
// booleans, at, held and error_at. Working is true for every listed step, so the table's "none" rows are the steps whose
// result carries no evidence, and the table's working-only rows are the steps whose result has no more than that.
func TestEpicSlicesRingRows(t *testing.T) {
	gate := sliceCheck(1, "gate", true)
	gateFailed := sliceCheck(1, "gate", false)
	cases := []struct {
		name   string
		end    string
		held   bool
		result string
		checks []*enginev1.StepCheck
		want   *orreryv1.EpicSlice
	}{
		{
			name:   "running, just started",
			result: `{}`,
			want:   &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", Working: true, At: 1},
		},
		{
			name:   "running, red evidence given",
			result: sliceRedOnly,
			want:   &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", RedTest: true, Working: true, At: 3},
		},
		{
			name:   "running, tests pass",
			result: sliceRedGreen,
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "dispatched", RedTest: true, Working: true, TestsPass: true, At: 4,
			},
		},
		{
			name:   "running, checks passed",
			result: sliceRedGreen,
			checks: []*enginev1.StepCheck{gate},
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "dispatched", RedTest: true, Working: true, TestsPass: true, Checks: true, At: 5,
			},
		},
		{
			name:   "running, green evidence but no red",
			result: `{"green_evidence":"synthetic green"}`,
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "dispatched", Working: true, TestsPass: true, At: 1,
			},
		},
		{
			name:   "held after tests pass",
			held:   true,
			result: sliceRedGreen,
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "dispatched", RedTest: true, Working: true, TestsPass: true, At: 4, Held: true,
			},
		},
		{
			name:   "held, just started",
			held:   true,
			result: `{}`,
			want:   &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", Working: true, At: 1, Held: true},
		},
		{
			name:   "running, nothing reached",
			result: "",
			want:   &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", Working: true, At: 1},
		},
		{
			name:   "ended done",
			end:    "done",
			result: sliceRedGreen,
			checks: []*enginev1.StepCheck{gate},
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "done", RedTest: true, Working: true, TestsPass: true, Checks: true,
				GateAccepted: true,
			},
		},
		{
			name:   "ended done, checks not evidenced",
			end:    "done",
			result: sliceRedGreen,
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "done", RedTest: true, Working: true, TestsPass: true, GateAccepted: true,
			},
		},
		{
			name:   "ended checks_failed after tests pass",
			end:    "checks_failed",
			result: sliceRedGreen,
			checks: []*enginev1.StepCheck{gateFailed},
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "gap", RedTest: true, Working: true, TestsPass: true, ErrorAt: 4,
			},
		},
		{
			name:   "ended checks_failed, no green evidence",
			end:    "checks_failed",
			result: sliceRedOnly,
			checks: []*enginev1.StepCheck{gateFailed},
			want:   &orreryv1.EpicSlice{Task: "t", Hue: "gap", RedTest: true, Working: true, ErrorAt: 4},
		},
		{
			name:   "ended goal_unmet",
			end:    "goal_unmet",
			result: sliceRedGreen,
			checks: []*enginev1.StepCheck{gate},
			want: &orreryv1.EpicSlice{
				Task: "t", Hue: "gap", RedTest: true, Working: true, TestsPass: true, Checks: true, ErrorAt: 5,
			},
		},
		{
			name:   "ended returned",
			end:    "returned",
			result: sliceRedOnly,
			want:   &orreryv1.EpicSlice{Task: "t", Hue: "gap", RedTest: true, Working: true, ErrorAt: 3},
		},
		{
			name:   "ended failed, no red test",
			end:    "failed",
			result: `{}`,
			want:   &orreryv1.EpicSlice{Task: "t", Hue: "gap", Working: true, ErrorAt: 1},
		},
		{
			name:   "ended, nothing reached",
			end:    "stopped",
			result: "",
			want:   &orreryv1.EpicSlice{Task: "t", Hue: "gap", Working: true, ErrorAt: 1},
		},
		{
			name:   "ended with the held flag still set",
			end:    "returned",
			held:   true,
			result: sliceRedOnly,
			want:   &orreryv1.EpicSlice{Task: "t", Hue: "gap", RedTest: true, Working: true, ErrorAt: 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step := sliceStep(tc.end, tc.result, tc.checks...)
			step.Held = tc.held
			requireSlices(t, epicSlicesOf(t, step), []*orreryv1.EpicSlice{tc.want})
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
			want := &orreryv1.EpicSlice{Task: "t", Hue: "dispatched", Working: true, At: 1}
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
		{name: "running", end: "", hue: "dispatched", at: 5},
		{name: "held", end: "", held: true, hue: "dispatched", at: 5},
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
		{Task: "a", Hue: "gap", Working: true, ErrorAt: 1},
	})
}
