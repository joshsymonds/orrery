package enginehost

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// releaseTestSteps are the release steps of the release tests: a step with an undo, then an undo-none step.
func releaseTestSteps() []store.EpicReleaseStep {
	return []store.EpicReleaseStep{
		{Step: "push tag", Target: "origin", Undo: "delete tag"},
		{Step: "publish", Target: "registry", Undo: "none"},
	}
}

// releaseTestRepo is the repository of the stopped releases that never read a git repository. releaseTestBase is the
// commit they are based at. Neither names anything on disk: the store keeps both as text.
const (
	releaseTestRepo = "/nonexistent/synthetic-repo"
	releaseTestBase = "0123456789abcdef0123456789abcdef01234567"
)

// stoppedReleaseEpic creates epicServiceID clean at repo, based at releaseTestBase, and takes repo's release slot. Its
// release node run stops at publish, the second step, which failed after the first step passed.
func stoppedReleaseEpic(t *testing.T, repo string) *store.Store {
	t.Helper()
	ctx := t.Context()
	st := epicTestStore(t)
	now := time.Unix(turnStarted, 0)
	row := store.EpicRow{
		ID:           epicServiceID,
		Repository:   repo,
		Base:         releaseTestBase,
		Phase:        store.PhaseClean,
		Created:      now,
		Changed:      now,
		ReleaseSteps: releaseTestSteps(),
	}
	if err := st.CreateEpic(ctx, row); err != nil {
		t.Fatalf("CreateEpic clean: %v", err)
	}
	if _, err := st.TakeReleaseSlot(ctx, epicServiceID, now); err != nil {
		t.Fatalf("TakeReleaseSlot: %v", err)
	}
	run := store.NodeRun{Path: "release", ID: "id-release", Node: "release", Kind: "release", Attempt: 1}
	if err := st.StartNodes(ctx, epicServiceID, []store.NodeRun{run}, nil, time.Unix(turnStarted, 0)); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	passed := store.ReleaseRun{
		Part: store.ReleasePartStep, Position: 1, Attempt: 1, Step: "push tag", Evidence: "run-push",
		Passed: true, At: time.Unix(turnStarted, 0),
	}
	failed := store.ReleaseRun{
		Part: store.ReleasePartStep, Position: 2, Attempt: 1, Step: "publish", Evidence: "run-publish",
		Reason: "publish rejected", At: time.Unix(turnFreed, 0),
	}
	for _, release := range []store.ReleaseRun{passed, failed} {
		if err := st.RecordRelease(ctx, epicServiceID, release); err != nil {
			t.Fatalf("RecordRelease %s %d: %v", release.Part, release.Position, err)
		}
	}
	if err := st.StopRelease(
		ctx, epicServiceID, "release", 1, "publish rejected", time.Unix(turnFreed, 0),
	); err != nil {
		t.Fatalf("StopRelease: %v", err)
	}
	return st
}

func TestEpicReleaseResumesStoppedReleaseAtNamedUndoNone(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := stoppedReleaseEpic(t, releaseTestRepo)
	client := newEpicsClient(t, startEpicServer(t, st))

	released, err := client.ReleaseEpic(ctx, connect.NewRequest(&v1.ReleaseEpicRequest{
		Id: epicServiceID, UndoNone: []int32{2},
	}))
	if err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	if phase := released.Msg.GetEpic().GetPhase(); phase != store.PhaseReleasing {
		t.Fatalf("phase = %q in the ReleaseEpic reply, want %s", phase, store.PhaseReleasing)
	}
	if parked := released.Msg.GetEpic().GetParked(); parked != "" {
		t.Fatalf("parked = %q in the ReleaseEpic reply, want unparked", parked)
	}
	if got := getEpicOK(t, client); !proto.Equal(got, released.Msg.GetEpic()) {
		t.Fatalf("GetEpic = %v, want the released epic %v", got, released.Msg.GetEpic())
	}
	runs := released.Msg.GetEpic().GetReleases()
	if len(runs) != 3 {
		t.Fatalf("releases = %d runs in the ReleaseEpic reply, want 3", len(runs))
	}
	named := runs[2]
	if named.GetPart() != store.ReleasePartNamed || named.GetPosition() != 2 || named.GetAttempt() != 2 ||
		named.GetStep() != "publish" || named.GetReason() != "owner named it" {
		t.Fatalf("last release = %v, want the named run of publish at position 2, attempt 2", named)
	}
}

func TestEpicReleaseRefusesMalformedAndUnknownRequests(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	cases := []struct {
		name    string
		request *v1.ReleaseEpicRequest
		code    connect.Code
		message string
	}{
		{
			name:    "empty id",
			request: &v1.ReleaseEpicRequest{},
			code:    connect.CodeInvalidArgument,
			message: "id: empty",
		},
		{
			name:    "undo_none of zero",
			request: &v1.ReleaseEpicRequest{Id: epicServiceID, UndoNone: []int32{0}},
			code:    connect.CodeInvalidArgument,
			message: "undo_none: position 0 is below 1",
		},
		{
			name:    "undo_none of minus one",
			request: &v1.ReleaseEpicRequest{Id: epicServiceID, UndoNone: []int32{2, -1}},
			code:    connect.CodeInvalidArgument,
			message: "undo_none: position -1 is below 1",
		},
		{
			name:    "unknown epic",
			request: &v1.ReleaseEpicRequest{Id: "synthetic-repo-0a1b2c3/no-such-epic", UndoNone: []int32{2}},
			code:    connect.CodeNotFound,
			message: "id: no epic synthetic-repo-0a1b2c3/no-such-epic",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := client.ReleaseEpic(ctx, connect.NewRequest(c.request))
			wantConnectError(t, err, c.code, c.message)
		})
	}
}

// TestEpicReleaseStoreRefusalIsFailedPrecondition checks that a store refusal of ReleaseEpic comes back as
// failed_precondition carrying the store's text. The store's own tests pin each refusal.
func TestEpicReleaseStoreRefusalIsFailedPrecondition(t *testing.T) {
	t.Parallel()
	st := stoppedReleaseEpic(t, releaseTestRepo)
	client := newEpicsClient(t, startEpicServer(t, st))

	_, err := client.ReleaseEpic(t.Context(), connect.NewRequest(&v1.ReleaseEpicRequest{Id: epicServiceID}))
	wantConnectError(t, err, connect.CodeFailedPrecondition,
		"release step 2 of epic "+epicServiceID+" is undo-none and failed; name it to run it again")
}

func TestEpicGetShowsReleaseRunsAndSlotHolder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	repo := stepGitTestRepo(t)
	st := stoppedReleaseEpic(t, repo)
	client := newEpicsClient(t, startEpicServer(t, st))

	stopped := getEpicOK(t, client)
	wantProtos(t, "GetEpic releases", stopped.GetReleases(), []*v1.EpicReleaseRun{
		{
			Part: store.ReleasePartStep, Position: 1, Attempt: 1, Step: "push tag", Evidence: "run-push",
			Passed: true, At: turnStarted,
		},
		{
			Part: store.ReleasePartStep, Position: 2, Attempt: 1, Step: "publish", Evidence: "run-publish",
			Reason: "publish rejected", At: turnFreed,
		},
	})
	if slot := stopped.GetReleaseSlot(); slot != epicServiceID {
		t.Fatalf("release_slot = %q in GetEpic, want the holder %s", slot, epicServiceID)
	}
	wantProtos(t, "GetEpic release proof", stopped.GetReleaseProof(), []*v1.EpicReleaseProof{
		{
			Position: 1, Step: "push tag", Target: "origin", Undo: "delete tag", State: store.ReleasePassed,
			Evidence: "run-push", Attempt: 1, At: turnStarted,
		},
		{
			Position: 2, Step: "publish", Target: "registry", Undo: "none", State: store.ReleaseFailed,
			Evidence: "run-publish", Reason: "publish rejected", Attempt: 1, At: turnFreed,
		},
	})
	if at := stopped.GetPhaseAt(); at != turnFreed {
		t.Fatalf("phase_at = %d in GetEpic, want the stop time %d", at, turnFreed)
	}

	const waitingID = "synthetic-repo-0a1b2c3/epic-waiting"
	createEpicOK(t, client, fullEpicRequest(repo, waitingID))
	waiting, err := client.GetEpic(ctx, connect.NewRequest(&v1.GetEpicRequest{Id: waitingID}))
	if err != nil {
		t.Fatalf("GetEpic %s: %v", waitingID, err)
	}
	if slot := waiting.Msg.GetEpic().GetReleaseSlot(); slot != epicServiceID {
		t.Fatalf("release_slot of waiting %s = %q, want the holder %s", waitingID, slot, epicServiceID)
	}
	if runs := waiting.Msg.GetEpic().GetReleases(); len(runs) != 0 {
		t.Fatalf("releases of waiting %s = %d runs, want none", waitingID, len(runs))
	}
}
