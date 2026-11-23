package enginehost

import (
	"errors"
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/reviewers"
)

// runContractBodyRefusals runs each refusal on a fresh valid contract for repo and asserts that checkContractBody
// returns an *epicFieldError with exactly the refusal's message.
func runContractBodyRefusals(t *testing.T, repo string, refusals []epicRefusal) {
	t.Helper()
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := epicTestRequest(repo)
			tc.mutate(req)
			err := checkContractBody(t.Context(), hostexec.NewLocal(), req.Contract, epicTestGetenv)
			if err == nil {
				t.Fatalf("checkContractBody accepted the contract, want refusal %q", tc.want)
			}
			var fieldErr *epicFieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("checkContractBody error is %T, want *epicFieldError", err)
			}
			got := err.Error()
			if got != tc.want {
				t.Fatalf("checkContractBody = %q, want %q", got, tc.want)
			}
			t.Logf("checkContractBody refused the contract: %s", got)
		})
	}
}

// TestContractRecordBodyRefusesFields asserts that a body the checks of propose and revise share refuses each field
// under the name and reason CreateEpic gives: the id form, a gate gates.Define refuses, a failure's id, and a capture
// argv element holding white space.
func TestContractRecordBodyRefusesFields(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	runContractBodyRefusals(t, repo, []epicRefusal{
		{
			name: "id without a repository prefix",
			mutate: func(req *epicCreate) {
				req.Contract.Id = "epic-check"
			},
			want: "id: bad form, want <repo-id>-<7 hex>/<slug>",
		},
		{
			name: "gate name gates.Define refuses",
			mutate: func(req *epicCreate) {
				req.Contract.Gates[0].Name = "Check"
			},
			want: `gates[0]: gate "Check": name must match [a-z][a-z0-9_]*`,
		},
		{
			name: "failure id outside F<n>",
			mutate: func(req *epicCreate) {
				req.Contract.Failures[0].Id = "X1"
			},
			want: "failures[0].id: unknown id",
		},
		{
			name: "capture element holds white space",
			mutate: func(req *epicCreate) {
				req.Contract.Requirements[0].Item.Capture = []string{"scripts/in-devenv", "go test -run X ./pkg/"}
			},
			want: "requirements[0].item.capture[1]: holds white space: split the element into argv elements, " +
				"or pass the line to a shell after -c",
		},
	})
}

// TestContractRecordBodyRefusesReviewer asserts that a contract reviewer the merge cannot hold is refused with the
// reviewers error CreateEpic gets: it names epic_create, the entry, and the key.
func TestContractRecordBodyRefusesReviewer(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	req := epicTestRequest(repo)
	req.Contract.Reviewers = []*v1.EpicReviewer{{Name: "floor", Limit: new(int64(0))}}
	err := checkContractBody(t.Context(), hostexec.NewLocal(), req.Contract, epicTestGetenv)
	var refusal *reviewers.Error
	if !errors.As(err, &refusal) {
		t.Fatalf("checkContractBody error = %v (%T), want *reviewers.Error", err, err)
	}
	if refusal.Source != "epic_create" || refusal.Index != 0 || refusal.Name != "floor" || refusal.Key != "limit" ||
		refusal.Reason != "must be at least 1 second" {
		t.Fatalf("refusal = %+v, want epic_create reviewers[0] floor limit must be at least 1 second", refusal)
	}
}

// TestContractRecordBodyAcceptsBodies asserts that a body passes whose base is empty or names no commit, since the
// body never reads the base; whose capture runs a go test whose -run pattern matches a test at the base, since the go
// test check belongs to CreateEpic; and whose release step says merge, which a release step may do.
func TestContractRecordBodyAcceptsBodies(t *testing.T) {
	t.Parallel()
	repo := contractLintRunRepo(t)
	for _, tc := range []struct {
		name   string
		mutate func(req *epicCreate)
	}{
		{name: "valid body", mutate: func(*epicCreate) {}},
		{name: "base empty", mutate: func(req *epicCreate) { req.Contract.Base = "" }},
		{name: "base names no commit", mutate: func(req *epicCreate) { req.Contract.Base = "no-such-commit" }},
		{
			name: "release step says merge",
			mutate: func(req *epicCreate) {
				req.Contract.ReleaseSteps = []*v1.EpicReleaseStep{
					{Step: "Merge the release candidate into main.", Target: "origin", Undo: "Synthetic undo."},
				}
			},
		},
		{
			name: "go test -run matches a test at the base",
			mutate: func(req *epicCreate) {
				req.Contract.Requirements[0].Item.Capture = []string{"/usr/bin/env", "go", "test", "-run", "X", "./pkg/"}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := epicTestRequest(repo)
			tc.mutate(req)
			if err := checkContractBody(t.Context(), hostexec.NewLocal(), req.Contract, epicTestGetenv); err != nil {
				t.Fatalf("checkContractBody refused a valid body: %v", err)
			}
			t.Logf("checkContractBody accepted the body: %s", tc.name)
		})
	}
}
