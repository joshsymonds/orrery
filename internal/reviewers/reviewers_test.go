package reviewers

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const project = "project.toml"

func str(v string) *string { return &v }
func on(v bool) *bool      { return &v }
func secs(v int64) *int64  { return &v }

// base is a valid agent reviewer at the task stage with one must, the shape most rules start from.
func base(source string, index int, name string) Entry {
	return Entry{
		Source: source,
		Index:  index,
		Name:   name,
		Stage:  []string{StageTask},
		Must:   []string{"the diff holds"},
		Agent:  str("code-reviewer"),
	}
}

// names returns the names of rs in order.
func names(rs []Reviewer) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}

func TestMergeRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		layers [][]Entry
		want   []Reviewer
	}{
		{
			name:   "unset keys take their defaults",
			layers: [][]Entry{{base(project, 0, "alpha")}},
			want: []Reviewer{{
				Name: "alpha", Enabled: true, Stage: []string{StageTask}, Must: []string{"the diff holds"},
				Agent: "code-reviewer", Limit: 1800, Verify: true,
			}},
		},
		{
			name: "set keys replace the earlier value",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  0,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"the diff holds"},
						Model:  str("m-1"),
						Prompt: str("old"),
						Effort: str("low"),
						Limit:  secs(60),
						Verify: on(false),
					},
				},
				{{Source: "epic_create", Index: 0, Name: "alpha", Enabled: on(false), Prompt: str("new"),
					Effort: str("max"), Limit: secs(120), Verify: on(true)}},
			},
			want: []Reviewer{{
				Name: "alpha", Enabled: false, Stage: []string{StageTask}, Must: []string{"the diff holds"},
				Prompt: "new", Model: "m-1", Effort: "max", Limit: 120, Verify: true,
			}},
		},
		{
			name: "arrays replace whole",
			layers: [][]Entry{
				{{Source: project, Index: 0, Name: "alpha", Stage: []string{StageTask, StageEpic},
					Must: []string{"a", "b"}, MustNot: []string{"c"}, Agent: str("code-reviewer")}},
				{{Source: "epic_create", Index: 0, Name: "alpha", Stage: []string{StageEpic}, Must: []string{"d"}}},
			},
			want: []Reviewer{{
				Name: "alpha", Enabled: true, Stage: []string{StageEpic}, Must: []string{"d"}, MustNot: []string{"c"},
				Agent: "code-reviewer", Limit: 1800, Verify: true,
			}},
		},
		{
			name: "an empty array is set and replaces",
			layers: [][]Entry{
				{
					{
						Source:  project,
						Index:   0,
						Name:    "alpha",
						Stage:   []string{StageTask},
						Must:    []string{"the diff holds"},
						MustNot: []string{"c"},
						Agent:   str("code-reviewer"),
					},
				},
				{{Source: "epic_create", Index: 0, Name: "alpha", Must: []string{}}},
			},
			want: []Reviewer{{
				Name: "alpha", Enabled: true, Stage: []string{StageTask}, Must: []string{}, MustNot: []string{"c"},
				Agent: "code-reviewer", Limit: 1800, Verify: true,
			}},
		},
		{
			name: "agent displaces model, tools and effort",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  0,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"the diff holds"},
						Prompt: str("review it"),
						Model:  str("m-1"),
						Tools:  []string{"read"},
						Effort: str("high"),
					},
				},
				{{Source: "epic_create", Index: 0, Name: "alpha", Agent: str("code-reviewer")}},
			},
			want: []Reviewer{{
				Name: "alpha", Enabled: true, Stage: []string{StageTask}, Must: []string{"the diff holds"},
				Prompt: "review it", Agent: "code-reviewer", Limit: 1800, Verify: true,
			}},
		},
		{
			name: "model displaces agent",
			layers: [][]Entry{
				{base(project, 0, "alpha")},
				{{Source: "epic_create", Index: 0, Name: "alpha", Model: str("m-1"), Prompt: str("review it")}},
			},
			want: []Reviewer{{
				Name: "alpha", Enabled: true, Stage: []string{StageTask}, Must: []string{"the diff holds"},
				Prompt: "review it", Model: "m-1", Limit: 1800, Verify: true,
			}},
		},
		{
			name: "a disabled reviewer is not checked for an agent or a model",
			layers: [][]Entry{
				{{Source: project, Index: 0, Name: "alpha", Enabled: on(false)}},
			},
			want: []Reviewer{{Name: "alpha", Enabled: false, Limit: 1800, Verify: true}},
		},
		{
			name: "a limit at the bound is kept",
			layers: [][]Entry{
				{{Source: project, Index: 0, Name: "alpha", Stage: []string{StageTask}, Must: []string{"x"},
					Agent: str("a"), Limit: secs(9223372036)}},
			},
			want: []Reviewer{{
				Name: "alpha", Enabled: true, Stage: []string{StageTask}, Must: []string{"x"},
				Agent: "a", Limit: 9223372036, Verify: true,
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Merge(tc.layers...)
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Merge =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

func TestMergeOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		layers [][]Entry
		want   []string
	}{
		{
			name:   "one layer keeps its order",
			layers: [][]Entry{{base(project, 0, "gamma"), base(project, 1, "alpha"), base(project, 2, "beta")}},
			want:   []string{"gamma", "alpha", "beta"},
		},
		{
			name: "an override keeps the place its name first took and new names append",
			layers: [][]Entry{
				{base(project, 0, "beta"), base(project, 1, "alpha")},
				{base("epic_create", 0, "gamma"), base("epic_create", 1, "alpha")},
				{base("orrery defaults", 0, "delta")},
			},
			want: []string{"beta", "alpha", "gamma", "delta"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Merge(tc.layers...)
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			if !reflect.DeepEqual(names(got), tc.want) {
				t.Errorf("order = %v, want %v", names(got), tc.want)
			}
		})
	}
}

// refusal is one entry or reviewer the merge must refuse, with the exact error text.
type refusal struct {
	name   string
	layers [][]Entry
	want   string
}

func checkRefusals(t *testing.T, tests []refusal) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Merge(tc.layers...)
			if err == nil {
				t.Fatalf("Merge accepted the reviewers, want refusal %q", tc.want)
			}
			var refused *Error
			if !errors.As(err, &refused) {
				t.Fatalf("error %T is not *Error", err)
			}
			if got := err.Error(); got != tc.want {
				t.Errorf("error = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEntryRefusals(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 49)
	checkRefusals(t, []refusal{
		{
			name: "name does not match the pattern",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "Alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Agent:  str("a"),
					},
				},
			},
			want: "project.toml: reviewers[1] Alpha: name: must match [a-z][a-z0-9_]*",
		},
		{
			name: "name is over 48 bytes",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   long,
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Agent:  str("a"),
					},
				},
			},
			want: fmt.Sprintf("project.toml: reviewers[1] %s: name: is over 48 bytes", long),
		},
		{
			name:   "name appears twice in one layer",
			layers: [][]Entry{{base(project, 0, "alpha"), base(project, 1, "alpha")}},
			want:   "project.toml: reviewers[1] alpha: name: appears twice in one layer",
		},
		{
			name: "agent sits beside model",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Agent:  str("a"),
						Model:  str("m"),
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: model: cannot sit beside agent",
		},
		{
			name: "agent sits beside tools",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Agent:  str("a"),
						Tools:  []string{"read"},
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: tools: cannot sit beside agent",
		},
		{
			name: "agent sits beside effort",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Agent:  str("a"),
						Effort: str("high"),
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: effort: cannot sit beside agent",
		},
		{
			name: "stage names an unknown stage",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{"tasks"},
						Must:   []string{"x"},
						Agent:  str("a"),
					},
				},
			},
			want: `project.toml: reviewers[1] alpha: stage: element "tasks" must be task, epic or release`,
		},
		{
			name: "stage repeats a stage",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask, StageTask},
						Must:   []string{"x"},
						Agent:  str("a"),
					},
				},
			},
			want: `project.toml: reviewers[1] alpha: stage: repeats "task"`,
		},
		{
			name: "stage is empty",
			layers: [][]Entry{
				{{Source: project, Index: 1, Name: "alpha", Stage: []string{}, Must: []string{"x"}, Agent: str("a")}},
			},
			want: "project.toml: reviewers[1] alpha: stage: is empty",
		},
		{
			name: "must has an empty element",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x", ""},
						Agent:  str("a"),
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: must: has an empty element",
		},
		{
			name: "must_not has an empty element",
			layers: [][]Entry{
				{
					{
						Source:  project,
						Index:   1,
						Name:    "alpha",
						Stage:   []string{StageTask},
						MustNot: []string{""},
						Agent:   str("a"),
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: must_not: has an empty element",
		},
		{
			name: "tools has an empty element",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Model:  str("m"),
						Prompt: str("p"),
						Tools:  []string{""},
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: tools: has an empty element",
		},
		{
			name: "agent is empty",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Agent:  str(""),
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: agent: is empty",
		},
		{
			name: "model is empty",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Model:  str(""),
						Prompt: str("p"),
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: model: is empty",
		},
		{
			name: "prompt is empty",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Agent:  str("a"),
						Prompt: str(""),
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: prompt: is empty",
		},
		{
			name: "effort is not a listed level",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Model:  str("m"),
						Prompt: str("p"),
						Effort: str("ultra"),
					},
				},
			},
			want: `project.toml: reviewers[1] alpha: effort: "ultra" is not low, medium, high, xhigh or max`,
		},
		{
			name: "limit is below one second",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Agent:  str("a"),
						Limit:  secs(0),
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: limit: must be at least 1 second",
		},
		{
			name: "limit is over the bound",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  1,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Agent:  str("a"),
						Limit:  secs(9223372037),
					},
				},
			},
			want: "project.toml: reviewers[1] alpha: limit: must be at most 9223372036 seconds",
		},
	})
}

func TestReviewerRefusals(t *testing.T) {
	t.Parallel()
	checkRefusals(t, []refusal{
		{
			name:   "enabled reviewer has no stage",
			layers: [][]Entry{{{Source: project, Index: 0, Name: "alpha", Must: []string{"x"}, Agent: str("a")}}},
			want:   "project.toml: reviewers[0] alpha: stage: needs a stage",
		},
		{
			name: "enabled reviewer has no agent or model",
			layers: [][]Entry{
				{{Source: project, Index: 0, Name: "alpha", Stage: []string{StageTask}, Must: []string{"x"}}},
			},
			want: "project.toml: reviewers[0] alpha: agent: needs an agent or a model",
		},
		{
			name: "model reviewer has no prompt",
			layers: [][]Entry{
				{
					{
						Source: project,
						Index:  0,
						Name:   "alpha",
						Stage:  []string{StageTask},
						Must:   []string{"x"},
						Model:  str("m"),
					},
				},
			},
			want: "project.toml: reviewers[0] alpha: prompt: is required when model is set",
		},
		{
			name: "enabled reviewer has no must or must_not",
			layers: [][]Entry{
				{{Source: project, Index: 0, Name: "alpha", Stage: []string{StageTask}, Agent: str("a")}},
			},
			want: "project.toml: reviewers[0] alpha: must: needs a must or must_not",
		},
		{
			name: "tools displace agent and leave no runner",
			layers: [][]Entry{
				{base(project, 0, "alpha")},
				{{Source: "epic_create", Index: 0, Name: "alpha", Tools: []string{"read"}}},
			},
			want: "epic_create: reviewers[0] alpha: agent: needs an agent or a model",
		},
		{
			name: "effort displaces agent and leaves no runner",
			layers: [][]Entry{
				{base(project, 0, "alpha")},
				{{Source: "epic_create", Index: 0, Name: "alpha", Effort: str("high")}},
			},
			want: "epic_create: reviewers[0] alpha: agent: needs an agent or a model",
		},
		{
			name: "the last entry that set the reviewer is named",
			layers: [][]Entry{
				{base(project, 0, "alpha")},
				{{Source: "epic_create", Index: 3, Name: "alpha", Model: str("m")}},
			},
			want: "epic_create: reviewers[3] alpha: prompt: is required when model is set",
		},
	})
}

func TestForStage(t *testing.T) {
	t.Parallel()
	rs := []Reviewer{
		{Name: "gamma", Enabled: true, Stage: []string{StageTask, StageEpic}},
		{Name: "alpha", Enabled: true, Stage: []string{StageTask}},
		{Name: "beta", Enabled: true, Stage: []string{StageEpic}},
		{Name: "delta", Enabled: false, Stage: []string{StageTask}},
	}
	tests := []struct {
		name  string
		stage string
		want  []string
	}{
		{name: "task stage keeps merged order and skips disabled", stage: StageTask, want: []string{"gamma", "alpha"}},
		{name: "epic stage keeps merged order", stage: StageEpic, want: []string{"gamma", "beta"}},
		{name: "a stage no reviewer holds is empty", stage: "release", want: []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := names(ForStage(rs, tc.stage)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ForStage(%q) = %v, want %v", tc.stage, got, tc.want)
			}
		})
	}
}

// TestReleaseReviewerStageIsAccepted: a reviewer whose stage is release is accepted by checkStage, and ForStage holds it
// at the release stage alone.
func TestReleaseReviewerStageIsAccepted(t *testing.T) {
	t.Parallel()
	rs, err := Merge([]Entry{{
		Source: project, Index: 0, Name: "alpha", Stage: []string{StageRelease},
		MustNot: []string{"the candidate holds"}, Agent: str("code-reviewer"),
	}})
	if err != nil {
		t.Fatalf("Merge refused a release-stage reviewer: %v", err)
	}
	if got := names(ForStage(rs, StageRelease)); !reflect.DeepEqual(got, []string{"alpha"}) {
		t.Errorf("ForStage(release) = %v, want [alpha]", got)
	}
	if got := ForStage(rs, StageEpic); len(got) != 0 {
		t.Errorf("ForStage(epic) = %v, want none", names(got))
	}
}
