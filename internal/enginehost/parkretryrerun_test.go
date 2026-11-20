package enginehost

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// parkNestedRun starts the integrate run at path of epicServiceID running at attempt 1 and parks it with reason, which
// parks the epic.
func parkNestedRun(t *testing.T, st *store.Store, path, reason string) {
	t.Helper()
	ctx := t.Context()
	runs := []store.NodeRun{{Path: path, ID: "id-" + path, Node: "integrate", Kind: "integrate", Attempt: 1}}
	if err := st.StartNodes(ctx, epicServiceID, runs, nil, time.Unix(turnStarted, 0)); err != nil {
		t.Fatalf("StartNodes %s: %v", path, err)
	}
	if err := st.ParkNode(
		ctx,
		epicServiceID,
		path,
		1,
		"failed",
		"boom",
		store.ParkClassNode,
		reason,
		time.Unix(turnFreed, 0),
	); err != nil {
		t.Fatalf("ParkNode %s: %v", path, err)
	}
}

// TestParkRetryRerunEpicRetryNamingParkedPathRerunsItAtNextAttempt checks that epic_retry naming a nested run by the path a
// park reason gives reruns that run at its next attempt and ends the epic's park.
func TestParkRetryRerunEpicRetryNamingParkedPathRerunsItAtNextAttempt(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	parkNestedRun(t, st, "plan/S1/integrate", "workflow node plan/S1/integrate moved main (aaa to bbb)")

	retried, err := client.RetryEpic(
		ctx,
		connect.NewRequest(&v1.RetryEpicRequest{Id: epicServiceID, Node: "plan/S1/integrate"}),
	)
	if err != nil {
		t.Fatalf("RetryEpic naming plan/S1/integrate: %v", err)
	}
	if parked := retried.Msg.GetEpic().GetParked(); parked != "" {
		t.Fatalf("parked = %q in the RetryEpic reply, want empty", parked)
	}
	if got := getEpicOK(t, client); !proto.Equal(got, retried.Msg.GetEpic()) {
		t.Fatalf("GetEpic = %v, want the retried epic %v", got, retried.Msg.GetEpic())
	}
	run := nodeRunOf(t, st, "plan/S1/integrate")
	if run.State != store.NodeRunning || run.Attempt != 2 {
		t.Fatalf("node run plan/S1/integrate = state %q attempt %d, want running at attempt 2", run.State, run.Attempt)
	}
}

// TestParkRetryRerunEpicRetryNamingPathWithNoParkedRunIsRefused checks that epic_retry naming a path that no parked run
// has is refused with a message that names the path, and changes neither the epic's park nor the parked run.
func TestParkRetryRerunEpicRetryNamingPathWithNoParkedRunIsRefused(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	const reason = "workflow node plan/S1/integrate moved main (aaa to bbb)"
	parkNestedRun(t, st, "plan/S1/integrate", reason)

	_, err := client.RetryEpic(
		ctx,
		connect.NewRequest(&v1.RetryEpicRequest{Id: epicServiceID, Node: "plan/S9/integrate"}),
	)
	wantConnectError(t, err, connect.CodeFailedPrecondition,
		"node plan/S9/integrate of epic "+epicServiceID+" is not parked")
	if got := getEpicOK(t, client).GetParked(); got != reason {
		t.Fatalf("parked = %q after the refused RetryEpic, want %q", got, reason)
	}
	if run := nodeRunOf(t, st, "plan/S1/integrate"); run.State != store.NodeParked || run.Attempt != 1 {
		t.Fatalf("node run plan/S1/integrate = state %q attempt %d after the refused RetryEpic, "+
			"want parked at attempt 1", run.State, run.Attempt)
	}
}
