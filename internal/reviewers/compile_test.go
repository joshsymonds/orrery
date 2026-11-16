package reviewers

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
)

// one merges a single entry and returns the reviewer it declares.
func one(t *testing.T, entry Entry) Reviewer {
	t.Helper()
	rs, err := Merge([]Entry{entry})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("Merge returned %d reviewers, want 1", len(rs))
	}
	return rs[0]
}

// modelEntry is a model reviewer that names every key a model reviewer may set.
func modelEntry() Entry {
	return Entry{
		Source:  project,
		Index:   0,
		Name:    "alpha",
		Stage:   []string{StageTask},
		Must:    []string{"the diff holds"},
		MustNot: []string{"the diff touches vendor/"},
		Prompt:  str("review it"),
		Model:   str("m-1"),
		Tools:   []string{"read", "grep"},
		Effort:  str("high"),
	}
}

func TestAgentName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		entry Entry
		want  string
	}{
		{name: "a model reviewer runs its orrery-named agent", entry: modelEntry(), want: "orrery-reviewer-alpha"},
		{name: "an agent reviewer runs the agent it names", entry: base(project, 0, "alpha"), want: "code-reviewer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := one(t, tc.entry).AgentName(); got != tc.want {
				t.Errorf("AgentName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAgents(t *testing.T) {
	t.Parallel()
	bare := modelEntry()
	bare.Tools = nil
	bare.Effort = nil
	emptyTools := modelEntry()
	emptyTools.Tools = []string{}
	namesOutput := modelEntry()
	namesOutput.Tools = []string{"read", "StructuredOutput"}
	tests := []struct {
		name  string
		entry Entry
		// want is the decoded definition under the agent name; nil when the reviewer names an agent instead.
		want map[string]any
	}{
		{
			name:  "a model reviewer carries its prompt, model, tools with StructuredOutput, and effort",
			entry: modelEntry(),
			want: map[string]any{"orrery-reviewer-alpha": map[string]any{
				"description": "orrery reviewer alpha",
				"prompt":      "review it",
				"model":       "m-1",
				"tools":       []any{"read", "grep", "StructuredOutput"},
				"effort":      "high",
			}},
		},
		{
			name:  "tools and effort are absent when unset",
			entry: bare,
			want: map[string]any{"orrery-reviewer-alpha": map[string]any{
				"description": "orrery reviewer alpha",
				"prompt":      "review it",
				"model":       "m-1",
			}},
		},
		{
			name:  "an empty tools list that is set stays in the definition, holding StructuredOutput",
			entry: emptyTools,
			want: map[string]any{"orrery-reviewer-alpha": map[string]any{
				"description": "orrery reviewer alpha",
				"prompt":      "review it",
				"model":       "m-1",
				"tools":       []any{"StructuredOutput"},
				"effort":      "high",
			}},
		},
		{
			name:  "a tools list that names StructuredOutput keeps it once",
			entry: namesOutput,
			want: map[string]any{"orrery-reviewer-alpha": map[string]any{
				"description": "orrery reviewer alpha",
				"prompt":      "review it",
				"model":       "m-1",
				"tools":       []any{"read", "StructuredOutput"},
				"effort":      "high",
			}},
		},
		{name: "an agent reviewer has no inline definition", entry: base(project, 0, "alpha"), want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := one(t, tc.entry).Agents()
			if err != nil {
				t.Fatalf("Agents: %v", err)
			}
			if tc.want == nil {
				if got != "" {
					t.Errorf("Agents() = %q, want empty", got)
				}
				return
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(got), &decoded); err != nil {
				t.Fatalf("Agents() = %q is not JSON: %v", got, err)
			}
			if !reflect.DeepEqual(decoded, tc.want) {
				t.Errorf("Agents() decodes to %v, want %v", decoded, tc.want)
			}
		})
	}
}

func TestAgentsEncodingIsDeterministic(t *testing.T) {
	t.Parallel()
	got, err := one(t, modelEntry()).Agents()
	if err != nil {
		t.Fatalf("Agents: %v", err)
	}
	const want = `{"orrery-reviewer-alpha":{"description":"orrery reviewer alpha","effort":"high",` +
		`"model":"m-1","prompt":"review it","tools":["read","grep","StructuredOutput"]}}`
	if got != want {
		t.Errorf("Agents() = %s, want %s", got, want)
	}
}

func TestGate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		entry Entry
		want  gates.Gate
	}{
		{
			name:  "a model reviewer gates on its orrery agent with its limit and assertions in order",
			entry: func() Entry { e := modelEntry(); e.Limit = secs(120); return e }(),
			want: gates.Gate{
				Name:  "alpha",
				Agent: "orrery-reviewer-alpha",
				Limit: 120 * time.Second,
				Assertions: []gates.Assertion{
					{Text: "the diff holds", Kind: gates.KindMust},
					{Text: "the diff touches vendor/", Kind: gates.KindMustNot},
				},
			},
		},
		{
			name:  "an agent reviewer gates on the agent it names with the default limit",
			entry: base(project, 0, "alpha"),
			want: gates.Gate{
				Name:       "alpha",
				Agent:      "code-reviewer",
				Limit:      1800 * time.Second,
				Assertions: []gates.Assertion{{Text: "the diff holds", Kind: gates.KindMust}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := one(t, tc.entry)
			got := r.Gate()
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Gate() = %+v, want %+v", got, tc.want)
			}
			var musts, mustNots []string
			for _, a := range got.Assertions {
				if a.Kind == gates.KindMust {
					musts = append(musts, a.Text)
				} else {
					mustNots = append(mustNots, a.Text)
				}
			}
			defined, err := gates.Define(r.Name, nil, r.Limit, nil, r.AgentName(), musts, mustNots)
			if err != nil {
				t.Fatalf("gates.Define refuses the reviewer's gate: %v", err)
			}
			if !reflect.DeepEqual(got, defined) {
				t.Errorf("Gate() = %+v, gates.Define = %+v", got, defined)
			}
		})
	}
}
