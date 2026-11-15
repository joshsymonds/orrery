package enginehost

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// parkTestNode starts node check of epicServiceID running at attempt 1 and parks it with reason, which parks the epic.
func parkTestNode(t *testing.T, st *store.Store, node, reason string) {
	t.Helper()
	ctx := t.Context()
	runs := []store.NodeRun{{Path: node, ID: "id-" + node, Node: node, Kind: "gate", Attempt: 1}}
	if err := st.StartNodes(ctx, epicServiceID, runs, nil, time.Unix(turnStarted, 0)); err != nil {
		t.Fatalf("StartNodes %s: %v", node, err)
	}
	if err := st.ParkNode(ctx, epicServiceID, node, 1, "failed", "boom", reason, time.Unix(turnFreed, 0)); err != nil {
		t.Fatalf("ParkNode %s: %v", node, err)
	}
}

// nodeRunOf returns the node run of epicServiceID at path as the store reads it. It fails the test when there is none.
func nodeRunOf(t *testing.T, st *store.Store, path string) store.NodeRun {
	t.Helper()
	flow, err := st.EpicFlow(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	for _, run := range flow.Runs {
		if run.Path == path {
			return run
		}
	}
	t.Fatalf("no node run at %s of epic %s", path, epicServiceID)
	return store.NodeRun{}
}

func TestEpicRetryNamingParkedNodeRerunsItAtNextAttempt(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	parkTestNode(t, st, "check", "workflow node check failed: boom")

	retried, err := client.RetryEpic(ctx, connect.NewRequest(&v1.RetryEpicRequest{Id: epicServiceID, Node: "check"}))
	if err != nil {
		t.Fatalf("RetryEpic naming check: %v", err)
	}
	if parked := retried.Msg.GetEpic().GetParked(); parked != "" {
		t.Fatalf("parked = %q in the RetryEpic reply, want empty", parked)
	}
	if got := getEpicOK(t, client); !proto.Equal(got, retried.Msg.GetEpic()) {
		t.Fatalf("GetEpic = %v, want the retried epic %v", got, retried.Msg.GetEpic())
	}
	run := nodeRunOf(t, st, "check")
	if run.State != store.NodeRunning || run.Attempt != 2 {
		t.Fatalf("node run check = state %q attempt %d, want running at attempt 2", run.State, run.Attempt)
	}
}

func TestEpicRetryNamingUnparkedNodeRefusesAndChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	const reason = "workflow node build failed: boom"
	parkTestNode(t, st, "build", reason)
	runs := []store.NodeRun{{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1}}
	if err := st.StartNodes(ctx, epicServiceID, runs, nil, time.Unix(turnStarted, 0)); err != nil {
		t.Fatalf("StartNodes check: %v", err)
	}

	_, err := client.RetryEpic(ctx, connect.NewRequest(&v1.RetryEpicRequest{Id: epicServiceID, Node: "check"}))
	wantConnectError(t, err, connect.CodeFailedPrecondition, "node check of epic "+epicServiceID+" is not parked")
	if got := getEpicOK(t, client).GetParked(); got != reason {
		t.Fatalf("parked = %q after the refused RetryEpic, want %q", got, reason)
	}
	if run := nodeRunOf(t, st, "check"); run.State != store.NodeRunning || run.Attempt != 1 {
		t.Fatalf("node run check = state %q attempt %d after the refused RetryEpic, want running at attempt 1",
			run.State, run.Attempt)
	}
}

func TestEpicRetryNamingNodeOfUnknownEpicIsNotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))

	_, err := client.RetryEpic(ctx, connect.NewRequest(&v1.RetryEpicRequest{Id: epicServiceID, Node: "check"}))
	wantConnectError(t, err, connect.CodeNotFound, "id: no epic "+epicServiceID)
}
