package enginehost

import (
	"errors"
	"strings"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// epicTestRequest returns a valid contract for the git repository at repo. Every refusal test mutates its own copy.
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
				{Id: "R1", Text: "Synthetic requirement.", Evidence: "Synthetic check.", Kind: "check"},
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
			req := epicTestRequest(repo)
			tc.mutate(req)
			err := checkEpic(t.Context(), req)
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
	repo := stepGitTestRepo(t)
	if err := checkEpic(t.Context(), epicTestRequest(repo)); err != nil {
		t.Fatalf("checkEpic refused a valid contract: %v", err)
	}
}

func TestCheckEpicRefusesContractHead(t *testing.T) {
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

func TestCheckEpicRefusesFailures(t *testing.T) {
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
					&v1.EpicRequirement{Id: "R1", Text: "Synthetic.", Evidence: "Synthetic.", Kind: "review"})
			},
			want: "requirements[1].id: duplicate",
		},
		{
			name:   "requirement evidence empty",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Requirements[0].Evidence = " " },
			want:   "requirements[0].evidence: empty",
		},
		{
			name:   "requirement kind bad",
			mutate: func(req *v1.CreateEpicRequest) { req.Contract.Requirements[0].Kind = "manual" },
			want:   "requirements[0].kind: not check or review",
		},
	})
}

func TestCheckEpicRefusesPremisesAndAdditions(t *testing.T) {
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
			name: "failures before requirements",
			mutate: func(req *v1.CreateEpicRequest) {
				req.Contract.Failures[0].Rating = "minor"
				req.Contract.Requirements = nil
			},
			want: "failures[0].rating: not limited, serious or severe",
		},
	})
}
