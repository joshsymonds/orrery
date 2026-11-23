package enginehost

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/reviewers"
)

// epicReviewerTestContract returns a contract for the synthetic repository at repo that declares the given reviewers
// and exclusions. The reviewer merge reads no other field.
func epicReviewerTestContract(repo string, declared []*v1.EpicReviewer, exclusions []string) *v1.EpicContract {
	return &v1.EpicContract{Repository: repo, Reviewers: declared, Exclusions: exclusions}
}

// epicReviewerTestNames returns the names of rs, in order.
func epicReviewerTestNames(rs []reviewers.Reviewer) []string {
	names := make([]string, 0, len(rs))
	for _, rev := range rs {
		names = append(names, rev.Name)
	}
	return names
}

// epicReviewerTestNamed returns the reviewer named name in rs, and fails the test when there is none.
func epicReviewerTestNamed(t *testing.T, rs []reviewers.Reviewer, name string) reviewers.Reviewer {
	t.Helper()
	for _, rev := range rs {
		if rev.Name == name {
			return rev
		}
	}
	t.Fatalf("no reviewer named %q in %v", name, epicReviewerTestNames(rs))
	return reviewers.Reviewer{}
}

// TestEpicReviewersDefaultsAlone asserts a repository with no project file and a contract with no reviewers runs
// orrery's six defaults, with the contract's exclusion generated into conformance.
func TestEpicReviewersDefaultsAlone(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	contract := epicReviewerTestContract(repo, nil, []string{"Synthetic exclusion."})
	rs, err := epicReviewers(t.Context(), hostexec.NewLocal(), contract, epicTestGetenv)
	if err != nil {
		t.Fatalf("epicReviewers refused the defaults: %v", err)
	}
	want := []string{"floor", "conformance", "integration", "security", "test_parsimony", "release"}
	if got := epicReviewerTestNames(rs); !slices.Equal(got, want) {
		t.Fatalf("reviewers = %v, want %v", got, want)
	}
	conformance := epicReviewerTestNamed(t, rs, "conformance")
	const generated = "The change does what this entry rules out: Synthetic exclusion."
	if !slices.Contains(conformance.MustNot, generated) {
		t.Fatalf("conformance must_not = %q, want it to hold %q", conformance.MustNot, generated)
	}
}

// TestReleaseReviewerIsADefaultOfEveryEpic asserts a repository with no project file and a contract with no reviewers
// runs the release reviewer at the release stage, read-only, and that a contract that disables it leaves no reviewer at
// that stage.
func TestReleaseReviewerIsADefaultOfEveryEpic(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	rs, err := epicReviewers(t.Context(), hostexec.NewLocal(), epicReviewerTestContract(repo, nil, nil), epicTestGetenv)
	if err != nil {
		t.Fatalf("epicReviewers refused the defaults: %v", err)
	}
	release := epicReviewerTestNamed(t, rs, "release")
	if !slices.Equal(release.Stage, []string{reviewers.StageRelease}) ||
		!slices.Equal(release.Tools, []string{"Read", "Bash"}) {
		t.Fatalf(
			"release = stage %q tools %q, want the release stage and the read-only tools",
			release.Stage,
			release.Tools,
		)
	}
	disabled := []*v1.EpicReviewer{{Name: "release", Enabled: new(false)}}
	rs, err = epicReviewers(
		t.Context(), hostexec.NewLocal(), epicReviewerTestContract(repo, disabled, nil), epicTestGetenv,
	)
	if err != nil {
		t.Fatalf("epicReviewers refused the contract's disabled release reviewer: %v", err)
	}
	if got := reviewers.ForStage(rs, reviewers.StageRelease); len(got) != 0 {
		t.Fatalf(
			"release-stage reviewers = %v, want none once the contract disables release",
			epicReviewerTestNames(got),
		)
	}
}

// TestEpicReviewersProjectOverridesAndAdds asserts a project file's entry overrides one key of a default, keeping the
// default's other keys, and adds a reviewer after the defaults.
func TestEpicReviewersProjectOverridesAndAdds(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	stepGitTestWrite(t, repo, ".orrery/project.toml", strings.Join([]string{
		"[[reviewers]]",
		`name = "floor"`,
		"limit = 600",
		"",
		"[[reviewers]]",
		`name = "synthetic_check"`,
		`stage = ["task"]`,
		`agent = "code-reviewer"`,
		`must = ["Synthetic statement holds."]`,
		"",
	}, "\n"))
	rs, err := epicReviewers(t.Context(), hostexec.NewLocal(), epicReviewerTestContract(repo, nil, nil), epicTestGetenv)
	if err != nil {
		t.Fatalf("epicReviewers refused the project file: %v", err)
	}
	floor := epicReviewerTestNamed(t, rs, "floor")
	if floor.Limit != 600 {
		t.Fatalf("floor limit = %d, want 600 from the project file", floor.Limit)
	}
	if floor.Model != "opus" {
		t.Fatalf("floor model = %q, want the default opus kept", floor.Model)
	}
	added := epicReviewerTestNamed(t, rs, "synthetic_check")
	if added.Agent != "code-reviewer" || !slices.Equal(added.Stage, []string{"task"}) {
		t.Fatalf("synthetic_check = %+v, want agent code-reviewer at task", added)
	}
	if last := rs[len(rs)-1].Name; last != "synthetic_check" {
		t.Fatalf("last reviewer = %q, want the project's reviewer after the defaults", last)
	}
}

// TestEpicReviewersContractOverridesAndDisables asserts the contract's layer overrides the project file's value of a
// key, that a key it sets empty stays unset, and that it disables a default.
func TestEpicReviewersContractOverridesAndDisables(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	stepGitTestWrite(t, repo, ".orrery/project.toml", "[[reviewers]]\nname = \"floor\"\nlimit = 600\n")
	declared := []*v1.EpicReviewer{
		{Name: "floor", Limit: new(int64(300)), Tools: []string{}},
		{Name: "security", Enabled: new(false)},
	}
	rs, err := epicReviewers(
		t.Context(), hostexec.NewLocal(), epicReviewerTestContract(repo, declared, nil), epicTestGetenv,
	)
	if err != nil {
		t.Fatalf("epicReviewers refused the contract's reviewers: %v", err)
	}
	floor := epicReviewerTestNamed(t, rs, "floor")
	if floor.Limit != 300 {
		t.Fatalf("floor limit = %d, want 300 from the contract over the project's 600", floor.Limit)
	}
	if !slices.Equal(floor.Tools, []string{"Read", "Bash"}) {
		t.Fatalf("floor tools = %q, want the default kept because an empty list is unset", floor.Tools)
	}
	if security := epicReviewerTestNamed(t, rs, "security"); security.Enabled {
		t.Fatal("security enabled, want the contract to disable it")
	}
}

// TestEpicReviewersRefusesBadProjectEntry asserts a project file's entry the layer cannot hold is refused with an
// error naming the file, the entry and the key.
func TestEpicReviewersRefusesBadProjectEntry(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	stepGitTestWrite(t, repo, ".orrery/project.toml", "[[reviewers]]\nname = \"floor\"\nlimit = \"soon\"\n")
	_, err := epicReviewers(t.Context(), hostexec.NewLocal(), epicReviewerTestContract(repo, nil, nil), epicTestGetenv)
	var refusal *reviewers.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("epicReviewers error = %v (%T), want *reviewers.Error", err, err)
	}
	if want := filepath.Join(".orrery", "project.toml"); !strings.HasSuffix(refusal.Source, want) {
		t.Fatalf("refusal source = %q, want the project file %s", refusal.Source, want)
	}
	if refusal.Index != 0 || refusal.Name != "floor" || refusal.Key != "limit" {
		t.Fatalf("refusal = index %d name %q key %q, want index 0 name floor key limit",
			refusal.Index, refusal.Name, refusal.Key)
	}
}

// TestEpicReviewersRefusesBadContractEntry asserts a contract's reviewer the layer cannot hold is refused with the
// exact message naming epic_create, the entry and the key.
func TestEpicReviewersRefusesBadContractEntry(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	declared := []*v1.EpicReviewer{{Name: "floor", Limit: new(int64(0))}}
	_, err := epicReviewers(
		t.Context(), hostexec.NewLocal(), epicReviewerTestContract(repo, declared, nil), epicTestGetenv,
	)
	var refusal *reviewers.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("epicReviewers error = %v (%T), want *reviewers.Error", err, err)
	}
	if refusal.Source != "epic_create" || refusal.Index != 0 || refusal.Name != "floor" || refusal.Key != "limit" ||
		refusal.Reason != "must be at least 1 second" {
		t.Fatalf("refusal = %+v, want epic_create reviewers[0] floor limit must be at least 1 second", refusal)
	}
}

// TestEpicReviewerMessageKeepsEveryKey asserts the stored epic's message holds every key of a reviewer that has one:
// a model reviewer keeps its model and its tools, and an agent reviewer keeps its agent and no model.
func TestEpicReviewerMessageKeepsEveryKey(t *testing.T) {
	t.Parallel()
	model := reviewers.Reviewer{
		Name:    "synthetic_check",
		Enabled: false,
		Stage:   []string{"task", "epic"},
		Must:    []string{"Synthetic must holds."},
		MustNot: []string{"Synthetic must not holds."},
		Prompt:  "Synthetic prompt.",
		Model:   "synthetic-model",
		Tools:   []string{"Read"},
		Effort:  "high",
		Limit:   120,
		Verify:  false,
	}
	want := &v1.EpicReviewer{
		Name:    "synthetic_check",
		Enabled: new(false),
		Stage:   []string{"task", "epic"},
		Must:    []string{"Synthetic must holds."},
		MustNot: []string{"Synthetic must not holds."},
		Prompt:  new("Synthetic prompt."),
		Model:   new("synthetic-model"),
		Tools:   []string{"Read"},
		Effort:  new("high"),
		Limit:   new(int64(120)),
		Verify:  new(false),
	}
	if got := epicReviewerMessage(model); !proto.Equal(got, want) {
		t.Fatalf("model reviewer message = %v, want %v", got, want)
	}

	agent := reviewers.Reviewer{
		Name:    "synthetic_agent",
		Enabled: true,
		Stage:   []string{"epic"},
		Must:    []string{"Synthetic must holds."},
		Agent:   "code-reviewer",
		Limit:   1800,
		Verify:  true,
	}
	wantAgent := &v1.EpicReviewer{
		Name:    "synthetic_agent",
		Enabled: new(true),
		Stage:   []string{"epic"},
		Must:    []string{"Synthetic must holds."},
		Agent:   new("code-reviewer"),
		Limit:   new(int64(1800)),
		Verify:  new(true),
	}
	if got := epicReviewerMessage(agent); !proto.Equal(got, wantAgent) {
		t.Fatalf("agent reviewer message = %v, want %v", got, wantAgent)
	}
}
