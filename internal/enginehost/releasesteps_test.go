package enginehost

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/releasestep"
)

// TestProjectRelease_RepoStepsAllNameKinds checks that orrery's own project file releases by the four built-in kinds in
// order, so its release needs no agent step and no judge, and that every step passes the contract's checks.
func TestProjectRelease_RepoStepsAllNameKinds(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "..", ".orrery", "project.toml"))
	if err != nil {
		t.Fatal(err)
	}
	steps, _, _, err := parseProjectRelease(data)
	if err != nil {
		t.Fatalf("parseProjectRelease error = %v, want nil", err)
	}
	kinds := make([]string, 0, len(steps))
	for _, step := range steps {
		kinds = append(kinds, step.GetKind())
	}
	if !slices.Equal(kinds, releasestep.Kinds()) {
		t.Errorf("kinds of the repo's release steps = %q, want %q", kinds, releasestep.Kinds())
	}
	if err = checkEpicReleaseSteps(steps); err != nil {
		t.Errorf("checkEpicReleaseSteps of the repo's release steps = %v, want nil", err)
	}
}

// kindProjectFile is a project file whose release gives an update step, which names a kind with string and list
// parameters, and an agent step after it.
const kindProjectFile = `[release]
then = "Synthetic release line."

[[release.steps]]
step = "Synthetic update step."
target = "Synthetic machine."
undo = "Synthetic undo."
kind = "update"
branch = "main"
flake = "~/flake"
run = ["update"]
units = ["one", "two"]

[[release.steps]]
step = "Synthetic agent step."
undo = "Synthetic agent undo."
`

// kindProjectSteps returns the steps kindProjectFile holds.
func kindProjectSteps() []*v1.EpicReleaseStep {
	return []*v1.EpicReleaseStep{
		{
			Step: "Synthetic update step.", Target: "Synthetic machine.", Undo: "Synthetic undo.", Kind: "update",
			Params: &v1.EpicReleaseParams{Branch: "main", Flake: "~/flake", Run: []string{"update"},
				Units: []string{"one", "two"}},
		},
		{Step: "Synthetic agent step.", Undo: "Synthetic agent undo."},
	}
}

func TestProjectRelease_ReadsKindAndParams(t *testing.T) {
	t.Parallel()
	steps, _, _, err := parseProjectRelease([]byte(kindProjectFile))
	if err != nil {
		t.Fatalf("parseProjectRelease error = %v, want nil", err)
	}
	want := kindProjectSteps()
	if len(steps) != len(want) {
		t.Fatalf("got %d steps, want %d", len(steps), len(want))
	}
	for i := range want {
		if !proto.Equal(steps[i], want[i]) {
			t.Errorf("step %d = %v, want %v", i, steps[i], want[i])
		}
	}
}

func TestProjectRelease_RefusesMalformedKind(t *testing.T) {
	t.Parallel()
	const head = "[release]\n[[release.steps]]\nstep = \"publish\"\nundo = \"unpublish\"\n"
	cases := []struct {
		name, text, field, reason string
	}{
		{
			name:  "unknown kind",
			text:  head + "kind = \"deploy\"\n",
			field: "release.steps[0].kind",
			reason: `unknown kind "deploy": a release step's kind is one of push-main, tag, pin-flake, update, ` +
				"or empty for an agent step",
		},
		{
			name:   "a parameter the kind needs is missing",
			text:   head + "kind = \"push-main\"\n",
			field:  "release.steps[0].branch",
			reason: "missing: a push-main step needs it",
		},
		{
			name:   "a parameter the kind does not read",
			text:   head + "kind = \"tag\"\nprefix = \"v\"\nbranch = \"main\"\n",
			field:  "release.steps[0].branch",
			reason: "not a parameter of a tag step",
		},
		{
			name:   "a parameter on a step with no kind",
			text:   head + "branch = \"main\"\n",
			field:  "release.steps[0].branch",
			reason: "a step with no kind sets no parameters",
		},
		{
			name:   "run not an array",
			text:   head + "run = \"update\"\n",
			field:  "release.steps[0].run",
			reason: reasonNotStrings,
		},
		{
			name:   "units with an element that is not a string",
			text:   head + "units = [\"one\", 2]\n",
			field:  "release.steps[0].units",
			reason: reasonNotStrings,
		},
		{
			name:   "kind not a string",
			text:   head + "kind = 1\n",
			field:  "release.steps[0].kind",
			reason: reasonNotString,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, _, err := parseProjectRelease([]byte(tc.text))
			var fieldErr *epicFieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("parseProjectRelease error = %v, want *epicFieldError", err)
			}
			if fieldErr.field != tc.field || fieldErr.reason != tc.reason {
				t.Errorf("refused %q: %q, want %q: %q", fieldErr.field, fieldErr.reason, tc.field, tc.reason)
			}
		})
	}
}

func TestCreateEpic_ProjectReleaseSteps_KindsAreStoredAndRead(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stepGitTestCommit(t, repo, map[string]string{".orrery/project.toml": kindProjectFile})

	createEpicOK(t, st, client, noReleaseEpicCreate(repo))

	want := &v1.EpicContract{ReleaseSteps: kindProjectSteps(), ReleaseThen: "Synthetic release line."}
	if got := releaseOf(getEpicOK(t, client).GetContract()); !proto.Equal(got, want) {
		t.Fatalf("GetEpic release = %v, want the project's %v", got, want)
	}
}

func TestCreateEpic_ContractReleaseStepKindIsChecked(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		step *v1.EpicReleaseStep
		want string
	}{
		{
			name: "unknown kind",
			step: &v1.EpicReleaseStep{Step: "Synthetic.", Undo: "Synthetic undo.", Kind: "deploy"},
			want: `release_steps[0].kind: unknown kind "deploy": a release step's kind is one of push-main, tag, ` +
				"pin-flake, update, or empty for an agent step",
		},
		{
			name: "a parameter the kind needs is missing",
			step: &v1.EpicReleaseStep{
				Step: "Synthetic.", Undo: "Synthetic undo.", Kind: "pin-flake",
				Params: &v1.EpicReleaseParams{Branch: "main", Prefix: "v", Flake: "/flake", Input: "orrery"},
			},
			want: "release_steps[0].params.url: missing: a pin-flake step needs it",
		},
		{
			name: "a url with no tag slot",
			step: &v1.EpicReleaseStep{
				Step: "Synthetic.", Undo: "Synthetic undo.", Kind: "pin-flake",
				Params: &v1.EpicReleaseParams{
					Branch: "main", Prefix: "v", Flake: "/flake", Input: "orrery", Url: "github:owner/repo",
				},
			},
			want: "release_steps[0].params.url: holds {tag} not exactly once",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			client := newEpicsClient(t, startEpicServer(t, st))
			repo := stepGitTestRepo(t)
			req := fullEpicRequest(repo, epicServiceID)
			req.Contract.ReleaseSteps = []*v1.EpicReleaseStep{tc.step}
			acceptEpicContract(t, st, req.Contract)

			_, err := client.CreateEpic(t.Context(), connect.NewRequest(req.request()))
			wantConnectError(t, err, connect.CodeInvalidArgument, tc.want)
			wantNoEpicStored(t, st)
		})
	}
}
