package enginehost

import (
	"fmt"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// releaseProjectFile is a project file whose release gives a then and two steps, in file order.
const releaseProjectFile = `[release]
then = "Synthetic project release line."

[[release.steps]]
step = "Synthetic project step one."
target = "Synthetic target one."
undo = "Synthetic undo one."

[[release.steps]]
step = "Synthetic project step two."
target = "Synthetic target two."
undo = "Synthetic undo two."
`

// releaseProjectRelease is the release releaseProjectFile holds.
func releaseProjectRelease() *v1.EpicContract {
	return &v1.EpicContract{
		ReleaseSteps: []*v1.EpicReleaseStep{
			{Step: "Synthetic project step one.", Target: "Synthetic target one.", Undo: "Synthetic undo one."},
			{Step: "Synthetic project step two.", Target: "Synthetic target two.", Undo: "Synthetic undo two."},
		},
		ReleaseThen: "Synthetic project release line.",
	}
}

// releaseOf returns a contract holding only the release of contract, so a test compares the release alone.
func releaseOf(contract *v1.EpicContract) *v1.EpicContract {
	return &v1.EpicContract{ReleaseSteps: contract.GetReleaseSteps(), ReleaseThen: contract.GetReleaseThen()}
}

// noReleaseEpicCreate returns the epic create of fullEpicRequest with its release cleared, so the contract names no
// step and no then. Its base is the commit repo holds at HEAD, so a test commits its project file first.
func noReleaseEpicCreate(repo string) *epicCreate {
	req := fullEpicRequest(repo, epicServiceID)
	req.Contract.ReleaseSteps = nil
	req.Contract.ReleaseThen = ""
	return req
}

func TestCreateEpic_ProjectReleaseSteps_TakenWhenContractNamesNone(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stepGitTestCommit(t, repo, map[string]string{".orrery/project.toml": releaseProjectFile})
	req := noReleaseEpicCreate(repo)

	createEpicOK(t, st, client, req)

	if got := releaseOf(getEpicOK(t, client).GetContract()); !proto.Equal(got, releaseProjectRelease()) {
		t.Fatalf("GetEpic release = %v, want the project's %v", got, releaseProjectRelease())
	}
}

func TestCreateEpic_ProjectReleaseSteps_ContractKeepsItsOwnStepAndThen(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stepGitTestCommit(t, repo, map[string]string{".orrery/project.toml": releaseProjectFile})
	req := fullEpicRequest(repo, epicServiceID)
	req.Contract.ReleaseSteps = []*v1.EpicReleaseStep{
		{Step: "Synthetic own step.", Target: "Synthetic own target.", Undo: "Synthetic own undo."},
	}
	req.Contract.ReleaseThen = "Synthetic own then."
	want := &v1.EpicContract{
		ReleaseSteps: req.Contract.GetReleaseSteps(),
		ReleaseThen:  req.Contract.GetReleaseThen(),
	}

	createEpicOK(t, st, client, req)

	if got := releaseOf(getEpicOK(t, client).GetContract()); !proto.Equal(got, want) {
		t.Fatalf("GetEpic release = %v, want the contract's own %v", got, want)
	}
}

func TestCreateEpic_ProjectReleaseSteps_ReadsTheBaseNotTheWorkingTree(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stepGitTestCommit(t, repo, map[string]string{".orrery/project.toml": releaseProjectFile})
	stepGitTestWrite(t, repo, ".orrery/project.toml", "[release]\n"+
		"then = \"Synthetic working tree line.\"\n\n"+
		"[[release.steps]]\n"+
		"step = \"Synthetic working tree step.\"\n"+
		"target = \"Synthetic working tree target.\"\n"+
		"undo = \"Synthetic working tree undo.\"\n")
	req := noReleaseEpicCreate(repo)

	createEpicOK(t, st, client, req)

	if got := releaseOf(getEpicOK(t, client).GetContract()); !proto.Equal(got, releaseProjectRelease()) {
		t.Fatalf("GetEpic release = %v, want the base's %v", got, releaseProjectRelease())
	}
}

func TestCreateEpic_ProjectReleaseSteps_LaterCommitDoesNotChangeTheEpic(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stepGitTestCommit(t, repo, map[string]string{".orrery/project.toml": releaseProjectFile})
	req := noReleaseEpicCreate(repo)
	createEpicOK(t, st, client, req)

	stepGitTestCommit(t, repo, map[string]string{".orrery/project.toml": "[release]\n" +
		"then = \"Synthetic later line.\"\n\n" +
		"[[release.steps]]\n" +
		"step = \"Synthetic later step.\"\n" +
		"target = \"Synthetic later target.\"\n" +
		"undo = \"Synthetic later undo.\"\n"})

	if got := releaseOf(getEpicOK(t, client).GetContract()); !proto.Equal(got, releaseProjectRelease()) {
		t.Fatalf("GetEpic after a later commit = %v, want the release at create %v", got, releaseProjectRelease())
	}
}

func TestCreateEpic_ProjectReleaseSteps_CopiedStepWarnsAndIsStored(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stepGitTestCommit(t, repo, map[string]string{".orrery/project.toml": "[release]\n" +
		"then = \"Synthetic tag line.\"\n\n" +
		"[[release.steps]]\n" +
		"step = \"Tag v0.86.3 at the synthetic release candidate.\"\n" +
		"target = \"Synthetic target.\"\n" +
		"undo = \"Synthetic undo.\"\n"})
	req := noReleaseEpicCreate(repo)
	acceptEpicContract(t, st, req.Contract)

	created, err := client.CreateEpic(t.Context(), connect.NewRequest(req.request()))
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	wantWarnings := []string{"release_steps[0].step: " + fmt.Sprintf(epicReasonReleaseVersion, "v0.86.3")}
	if got := created.Msg.GetWarnings(); !slices.Equal(got, wantWarnings) {
		t.Fatalf("CreateEpic warnings = %q, want %q", got, wantWarnings)
	}
	want := &v1.EpicContract{
		ReleaseSteps: []*v1.EpicReleaseStep{{
			Step:   "Tag v0.86.3 at the synthetic release candidate.",
			Target: "Synthetic target.",
			Undo:   "Synthetic undo.",
		}},
		ReleaseThen: "Synthetic tag line.",
	}
	if got := releaseOf(getEpicOK(t, client).GetContract()); !proto.Equal(got, want) {
		t.Fatalf("GetEpic release = %v, want %v", got, want)
	}
}

func TestCreateEpic_ProjectReleaseSteps_NoFileOrNoReleaseStoresNone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		files map[string]string
	}{
		{name: "no project file"},
		{
			name: "no release table",
			files: map[string]string{
				".orrery/project.toml": "[gates.project_check]\nargv = [\"scripts/check\"]\nlimit = 60\n",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			client := newEpicsClient(t, startEpicServer(t, st))
			repo := stepGitTestRepo(t)
			if tc.files != nil {
				stepGitTestCommit(t, repo, tc.files)
			}
			req := noReleaseEpicCreate(repo)

			createEpicOK(t, st, client, req)

			if got := releaseOf(getEpicOK(t, client).GetContract()); !proto.Equal(got, &v1.EpicContract{}) {
				t.Fatalf("GetEpic release = %v, want none", got)
			}
		})
	}
}

func TestCreateEpic_ProjectRelease_MalformedIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		file string
		// names reports whether the contract names its own steps, which the malformed release must still refuse.
		names bool
		want  string
	}{
		{
			name: "unknown key under release",
			file: "[release]\nbogus = \"Synthetic.\"\n\n" +
				"[[release.steps]]\nstep = \"Synthetic step.\"\ntarget = \"Synthetic target.\"\nundo = \"Synthetic undo.\"\n",
			want: ".orrery/project.toml: release.bogus: unknown key",
		},
		{
			name: "step with a blank undo",
			file: "[[release.steps]]\nstep = \"Synthetic step.\"\ntarget = \"Synthetic target.\"\nundo = \" \"\n",
			want: ".orrery/project.toml: release.steps[0].undo: empty",
		},
		{
			name: "release with no steps",
			file: "[release]\nthen = \"Synthetic release line.\"\n",
			want: ".orrery/project.toml: release.steps: missing",
		},
		{
			name:  "unknown key even when the contract names its own steps",
			file:  "[release]\nbogus = \"Synthetic.\"\n",
			names: true,
			want:  ".orrery/project.toml: release.bogus: unknown key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			client := newEpicsClient(t, startEpicServer(t, st))
			repo := stepGitTestRepo(t)
			stepGitTestCommit(t, repo, map[string]string{".orrery/project.toml": tc.file})
			req := fullEpicRequest(repo, epicServiceID)
			if !tc.names {
				req.Contract.ReleaseSteps = nil
				req.Contract.ReleaseThen = ""
			}
			acceptEpicContract(t, st, req.Contract)

			_, err := client.CreateEpic(t.Context(), connect.NewRequest(req.request()))
			wantConnectError(t, err, connect.CodeInvalidArgument, tc.want)
			wantNoEpicStored(t, st)
		})
	}
}
