package enginehost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// epicTestBase returns the commit the synthetic repository at repo is at: the base a valid contract starts from. It
// panics when git cannot resolve HEAD, because the fixtures that call it run where no test can fail with the error.
func epicTestBase(repo string) string {
	base, err := stepGitResolve(context.Background(), repo, "HEAD")
	if err != nil {
		panic(fmt.Sprintf("epicTestBase: %v", err))
	}
	return base
}

// epicTestGates returns a command gate and an agent gate, the gates a valid contract defines.
func epicTestGates() []*v1.EpicGate {
	return []*v1.EpicGate{
		{Name: "check", Argv: []string{"scripts/check"}, Limit: 60},
		{Name: "review", Agent: "reviewer", Limit: 300, Must: []string{"Synthetic statement holds."}},
	}
}

// epicTestItem returns a valid evidence item: a capture of a synthetic script, with the conclusion and judge it needs.
func epicTestItem() *v1.EvidenceItem {
	return &v1.EvidenceItem{
		Capture:    []string{"scripts/check"},
		Limit:      60,
		Conclusion: "Synthetic conclusion.",
		Judge:      "reviewer",
	}
}

// epicTestGetenv reads the environment of a machine with no pool file: HOME names a directory that does not exist,
// and no other variable is set.
func epicTestGetenv(key string) string {
	if key == "HOME" {
		return "/nonexistent/orrery-test-home"
	}
	return ""
}

// epicTestRequest returns a valid contract for the git repository at repo, based at the repository's HEAD. Every
// refusal test mutates its own copy.
func epicTestRequest(repo string) *v1.CreateEpicRequest {
	return &v1.CreateEpicRequest{
		Contract: &v1.EpicContract{
			Id:         "demo-repo-0a1b2c3/epic-check",
			Repository: repo,
			Title:      "Epic check",
			Approved:   "Synthetic approval sentence.",
			Intent:     "Synthetic intent.",
			Failures: []*v1.EpicFailure{
				{Id: "F1", Text: "Synthetic failure.", Rating: "serious", Response: "Synthetic response."},
			},
			Care: "serious",
			Additions: []*v1.EpicAddition{
				{Location: "F1", What: "Synthetic addition.", Why: "Synthetic reason.", Cost: "Synthetic cost."},
				{Location: "docs/guide.md", What: "Synthetic note.", Why: "Synthetic reason.", Cost: "None."},
			},
			Requirements: []*v1.EpicRequirement{
				{Id: "R1", Text: "Synthetic requirement.", Item: epicTestItem()},
			},
			Premises: []*v1.EpicPremise{
				{Id: "P1", Text: "Synthetic premise.", IfWrong: "Synthetic consequence."},
			},
			Exclusions: []string{"Synthetic exclusion."},
			Approach:   "Synthetic approach.",
			Alternatives: []*v1.EpicAlternative{
				{Alternative: "Synthetic alternative.", WhyNot: "Synthetic reason.", Reconsider: "Synthetic trigger."},
			},
			ReleaseSteps: []*v1.EpicReleaseStep{
				{Step: "Synthetic release.", Target: "Synthetic target.", Undo: "Synthetic undo."},
			},
			ReleaseThen: "Synthetic release line.",
			Checks:      "go test ./...",
			Base:        epicTestBase(repo),
			Gates:       epicTestGates(),
			DoneGates:   []string{"check"},
			ReviewGates: []string{"review"},
		},
		Decisions: []*v1.EpicDecisionInput{{Id: "D1", Text: "Synthetic decision."}},
	}
}

// epicRefusal is one mutation of the valid contract and the exact message checkEpic refuses it with.
type epicRefusal struct {
	name   string
	mutate func(req *v1.CreateEpicRequest)
	want   string
}

// runEpicRefusals runs each refusal on a fresh valid contract for repo and asserts that checkEpic returns an
// *epicFieldError with exactly the refusal's message.
func runEpicRefusals(t *testing.T, repo string, refusals []epicRefusal) {
	t.Helper()
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := epicTestRequest(repo)
			tc.mutate(req)
			err := checkEpic(t.Context(), req, epicTestGetenv)
			if err == nil {
				t.Fatalf("checkEpic accepted the contract, want refusal %q", tc.want)
			}
			var fieldErr *epicFieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("checkEpic error is %T, want *epicFieldError", err)
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("checkEpic = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckEpicAcceptsValidContract(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	if err := checkEpic(t.Context(), epicTestRequest(repo), epicTestGetenv); err != nil {
		t.Fatalf("checkEpic refused a valid contract: %v", err)
	}
}

// TestCheckEpicResolvesDoneGateFromProjectFile asserts a done gate the epic does not define resolves from the
// repository's project file.
func TestCheckEpicResolvesDoneGateFromProjectFile(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	stepGitTestWrite(t, repo, ".orrery/project.toml", "[gates.project_check]\nargv = [\"scripts/check\"]\nlimit = 60\n")
	req := epicTestRequest(repo)
	req.Contract.DoneGates = []string{"project_check"}
	if err := checkEpic(t.Context(), req, epicTestGetenv); err != nil {
		t.Fatalf("checkEpic refused a done gate the project file defines: %v", err)
	}
}

func TestCheckEpicRefusesContractHead(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	notRepo := t.TempDir()
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name:   "contract missing",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract = nil },
			want:   "contract: missing",
		},
		{
			name:   "id bad form",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Id = "demo-repo/epic-check" },
			want:   "id: bad form, want <repo-id>-<7 hex>/<slug>",
		},
		{
			name: "id over 128 bytes",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Id = strings.Repeat("a", epicIDMaxBytes) + "-0a1b2c3/epic-check"
			},
			want: "id: over 128 bytes",
		},
		{
			name:   "repository not absolute",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Repository = "demo-repo" },
			want:   "repository: not absolute",
		},
		{
			name:   "repository not a git work tree",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Repository = notRepo },
			want:   "repository: not inside a git work tree",
		},
		{
			name:   "intent empty",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Intent = " \t " },
			want:   "intent: empty",
		},
		{
			name:   "care bad",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Care = "minor" },
			want:   "care: not limited, serious or severe",
		},
	})
}

func TestCheckEpicRefusesBase(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name:   "base empty",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Base = "" },
			want:   "base: empty",
		},
		{
			name:   "base blank",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Base = " \t " },
			want:   "base: empty",
		},
		{
			name:   "base not a commit",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Base = "0123456789abcdef0123456789abcdef01234567" },
			want:   "base: not a commit in the repository",
		},
	})
}

func TestCheckEpicRefusesGates(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name:   "gate name bad form",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Gates[0].Name = "Check" },
			want:   `gates[0]: gate "Check": name must match [a-z][a-z0-9_]*`,
		},
		{
			name:   "gate argv without a path",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Gates[0].Argv = []string{"check"} },
			want:   `gates[0]: gate "check": argv[0] "check" must be a path with a / in it, never looked up on PATH`,
		},
		{
			name:   "gate limit below one second",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Gates[0].Limit = 0 },
			want:   `gates[0]: gate "check": limit 0 is outside 1 to 9223372036 seconds`,
		},
		{
			name: "gate needs bad pool",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Gates[0].Needs = []*v1.EpicGateNeed{{Pool: "GPU", Count: 1}}
			},
			want: `gates[0]: gate "check": pool "GPU" in needs must match [a-z][a-z0-9_]*`,
		},
		{
			name: "gate argv beside agent",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Gates[1].Argv = []string{"scripts/check"}
			},
			want: `gates[1]: gate "review": argv cannot sit beside agent`,
		},
		{
			name: "gate name duplicate",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Gates = append(req.Contract.Gates,
					&v1.EpicGate{Name: "check", Argv: []string{"scripts/check"}, Limit: 60})
			},
			want: "gates[2].name: duplicate",
		},
	})
}

func TestCheckEpicRefusesDoneAndReviewGates(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name:   "done gate defined nowhere",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.DoneGates = []string{"missing"} },
			want:   `done_gates[0]: gate "missing": unknown gate: searched the epic's gates`,
		},
		{
			name:   "second done gate defined nowhere",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.DoneGates = []string{"check", "missing"} },
			want:   `done_gates[1]: gate "missing": unknown gate: searched the epic's gates`,
		},
		{
			name:   "done gate is an agent gate",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.DoneGates = []string{"review"} },
			want:   "done_gates[0]: not a command gate",
		},
		{
			name:   "review gate defined nowhere",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.ReviewGates = []string{"missing"} },
			want:   `review_gates[0]: gate "missing": unknown gate: searched the epic's gates`,
		},
		{
			name:   "review gate is a command gate",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.ReviewGates = []string{"check"} },
			want:   "review_gates[0]: not an agent gate",
		},
	})
}

func TestCheckEpicRefusesFailures(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name:   "failure id wrong form",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Failures[0].Id = "F0" },
			want:   "failures[0].id: unknown id",
		},
		{
			name: "failure id duplicate",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Failures = append(req.Contract.Failures,
					&v1.EpicFailure{Id: "F1", Text: "Synthetic.", Rating: "limited", Response: "Synthetic."})
			},
			want: "failures[1].id: duplicate",
		},
		{
			name:   "failure rating bad",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Failures[0].Rating = "minor" },
			want:   "failures[0].rating: not limited, serious or severe",
		},
	})
}

func TestCheckEpicRefusesRequirements(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name:   "no requirements",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Requirements = nil },
			want:   "requirements: none",
		},
		{
			name:   "requirement id wrong form",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Requirements[0].Id = "R0" },
			want:   "requirements[0].id: unknown id",
		},
		{
			name: "requirement id duplicate",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements = append(req.Contract.Requirements,
					&v1.EpicRequirement{Id: "R1", Text: "Synthetic.", Item: epicTestItem()})
			},
			want: "requirements[1].id: duplicate",
		},
		{
			name:   "requirement item missing",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Requirements[0].Item = nil },
			want:   "requirements[0].item: missing",
		},
		{
			name: "second requirement item missing",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements = append(req.Contract.Requirements,
					&v1.EpicRequirement{Id: "R2", Text: "Synthetic."})
			},
			want: "requirements[1].item: missing",
		},
		{
			name: "item names no capture or supplied files",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item = &v1.EvidenceItem{Conclusion: "Synthetic.", Judge: "reviewer"}
			},
			want: "requirements[0].item.capture: none, name a capture or supplied files",
		},
		{
			name: "item judge blank",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Judge = " "
			},
			want: "requirements[0].item.judge: empty or blank",
		},
		{
			name: "item artifact not normalized",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item.Artifacts = []string{"../out.txt"}
			},
			want: "requirements[0].item.artifacts[0]: not a normalized repository-relative file path",
		},
		{
			name:   "evidence set beside item",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Requirements[0].Evidence = "Synthetic check." },
			want:   "requirements[0].evidence: set beside an item",
		},
		{
			name:   "kind set beside item",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Requirements[0].Kind = "check" },
			want:   "requirements[0].kind: set beside an item",
		},
	})
}

func TestCheckEpicRefusesPremisesAndAdditions(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name:   "premise id wrong form",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Premises[0].Id = "P0" },
			want:   "premises[0].id: unknown id",
		},
		{
			name: "premise id duplicate",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Premises = append(req.Contract.Premises,
					&v1.EpicPremise{Id: "P1", Text: "Synthetic.", IfWrong: "Synthetic."})
			},
			want: "premises[1].id: duplicate",
		},
		{
			name:   "addition names unknown R",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Additions[0].Location = "R9" },
			want:   "additions[0].location: unknown id",
		},
		{
			name:   "addition names unknown F",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Additions[0].Location = "F9" },
			want:   "additions[0].location: unknown id",
		},
		{
			name:   "addition names unknown P",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Additions[0].Location = "P9" },
			want:   "additions[0].location: unknown id",
		},
	})
}

func TestCheckEpicRefusesReleaseAndDecisions(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name:   "release step undo empty",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.ReleaseSteps[0].Undo = " " },
			want:   "release_steps[0].undo: empty",
		},
		{
			name:   "seed decision id empty",
			mutate: func(req *v1.CreateEpicRequest) { req.Decisions[0].Id = "" },
			want:   "decisions[0].id: empty",
		},
		{
			name: "seed decision id duplicate",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Decisions = append(req.Decisions, &v1.EpicDecisionInput{Id: "D1", Text: "Synthetic."})
			},
			want: "decisions[1].id: duplicate",
		},
		{
			name:   "seed decision text empty",
			mutate: func(req *v1.CreateEpicRequest) { req.Decisions[0].Text = "" },
			want:   "decisions[0].text: empty",
		},
	})
}

// TestCheckEpicRefusesFirstFailureOnly asserts the refusals are checked in order and only the first is reported.
func TestCheckEpicRefusesFirstFailureOnly(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runEpicRefusals(t, repo, []epicRefusal{
		{
			name: "id before repository",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Id = "demo-repo/epic-check"
				req.Contract.Repository = "demo-repo"
			},
			want: "id: bad form, want <repo-id>-<7 hex>/<slug>",
		},
		{
			name: "repository before intent",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Repository = "demo-repo"
				req.Contract.Intent = ""
			},
			want: "repository: not absolute",
		},
		{
			name: "intent before care",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Intent = ""
				req.Contract.Care = "minor"
			},
			want: "intent: empty",
		},
		{
			name: "head before base",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Intent = ""
				req.Contract.Base = ""
			},
			want: "intent: empty",
		},
		{
			name: "base before gates",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Base = ""
				req.Contract.Gates[0].Name = "Check"
			},
			want: "base: empty",
		},
		{
			name: "gates before done gates",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Gates[0].Name = "Check"
				req.Contract.DoneGates = []string{"missing"}
			},
			want: `gates[0]: gate "Check": name must match [a-z][a-z0-9_]*`,
		},
		{
			name: "done gates before failures",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.DoneGates = []string{"missing"}
				req.Contract.Failures[0].Rating = "minor"
			},
			want: `done_gates[0]: gate "missing": unknown gate: searched the epic's gates`,
		},
		{
			name: "failures before requirements",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Failures[0].Rating = "minor"
				req.Contract.Requirements = nil
			},
			want: "failures[0].rating: not limited, serious or severe",
		},
		{
			name: "requirement item before evidence",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Requirements[0].Item = nil
				req.Contract.Requirements[0].Evidence = "Synthetic check."
			},
			want: "requirements[0].item: missing",
		},
	})
}
