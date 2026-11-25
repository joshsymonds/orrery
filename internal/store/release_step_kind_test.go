package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/releasestep"
)

// TestReleaseStepKindAndParamsReadBack checks that a release step's kind and the parameters of that kind are stored
// with the step and read back as they were given, beside a step with no kind, which reads back with none.
func TestReleaseStepKindAndParamsReadBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-kinds"
	row := fullEpic(id)
	row.ReleaseSteps = []EpicReleaseStep{
		{
			Step: "Pin the flake", Target: "nix-config", Undo: "Pin the previous tag", Kind: releasestep.KindPinFlake,
			Params: releasestep.Params{
				Branch: "main", Prefix: "v", Flake: "~/nix-config", Input: "orrery", URL: "github:o/r/{tag}",
			},
		},
		{
			Step: "Update", Target: "vermissian", Undo: "Update to the previous pin", Kind: releasestep.KindUpdate,
			Params: releasestep.Params{
				Branch: "main", Flake: "~/nix-config", Run: []string{"update"}, Units: []string{"orreryd", "serve"},
			},
		},
		{Step: "Install on the phone", Target: "the phone", Undo: "Install the previous build"},
	}
	createEpic(t, s, row)

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !reflect.DeepEqual(got.ReleaseSteps, row.ReleaseSteps) {
		t.Errorf("release steps = %+v, want %+v", got.ReleaseSteps, row.ReleaseSteps)
	}
}

// TestReleaseProofRunsWhileTheCheckOfAKindStepRuns checks that a release step that names a kind is running while the
// check stored under its work id runs, and waiting once that check has ended with no release run recorded for it, the
// same way an agent step's proof follows its step.
func TestReleaseProofRunsWhileTheCheckOfAKindStepRuns(t *testing.T) {
	t.Parallel()
	node := NodeRun{Path: "release", ID: "id-release", Kind: releaseKind, State: NodeRunning, Attempt: 1}
	id := ReleaseStepWorkID(node, nil, 1)
	flow := EpicFlow{
		Epic: EpicRow{ReleaseSteps: []EpicReleaseStep{{
			Step: "push main", Target: "origin", Undo: "revert", Kind: releasestep.KindPushMain,
			Params: releasestep.Params{Branch: "main"},
		}}},
		Runs:   []NodeRun{node},
		Checks: []NodeCheck{{ID: id, State: NodeRunning}},
	}
	if got := ReleaseProofs(flow); len(got) != 1 || got[0].State != ReleaseRunning {
		t.Errorf("ReleaseProofs with the step's check running = %+v, want the step running", got)
	}
	flow.Checks[0].State = NodeEnded
	if got := ReleaseProofs(flow); len(got) != 1 || got[0].State != ReleaseWaiting {
		t.Errorf("ReleaseProofs with the step's check ended = %+v, want the step waiting", got)
	}
}
