package enginehost

import (
	"context"
	"maps"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic ids of the steering-rule world: the conversation that steers the epic, a conversation that does not, a step,
// and a pane with empty state.
const (
	steerOnlySteerer = "steerer-1"
	steerOnlyOther   = "other-1"
	steerOnlyStep    = "step-1"
	steerOnlyPaneID  = "orrery-empty"
	steerOnlyPane    = "%3"
)

// unsteeredSteering is the SteeringSessions of a create that starts no session: Start returns no id, so the epic has no
// steerer.
type unsteeredSteering struct{ fixedSteering }

// Start returns no id and starts no session.
func (unsteeredSteering) Start(context.Context, store.EpicRow) (string, error) { return "", nil }

// steerOnlyWorld returns a client of the epic epicServiceID, created by the conversation steerOnlySteerer. The
// store also holds steerOnlyOther, a conversation that does not steer the epic, steerOnlyStep, a step, and a pane with
// empty state on steerSocket.
func steerOnlyWorld(t *testing.T) enginev1connect.EpicsClient {
	t.Helper()
	st := epicTestStore(t)
	engines := []store.EngineOp{
		store.EngineAdd{ID: steerOnlySteerer, Dir: "/work/steerer"},
		store.EngineAdd{ID: steerOnlyOther, Dir: "/work/other"},
		store.EngineAdd{ID: steerOnlyStep, Dir: "/wt/step", State: "idle", Kind: store.KindStep},
	}
	if err := st.ApplyEngine(t.Context(), engines, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	pane := store.Pane{ID: steerOnlyPaneID, Pane: steerOnlyPane}
	if err := st.SyncPane(t.Context(), "-S "+steerSocket, pane, fixedClock()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	client := newEpicsClient(t, serveEpicsWith(t, st, fixedSteering{}))
	if err := steererSetCreate(t, st, client, stepGitTestRepo(t), sessionHeader(steerOnlySteerer)); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	return client
}

// steerOnlyRequest returns a request of msg that sends the caller headers header.
func steerOnlyRequest[T any](header http.Header, msg *T) *connect.Request[T] {
	req := connect.NewRequest(msg)
	maps.Copy(req.Header(), header)
	return req
}

// steerOnlyCall is one changing epic call the steering rule covers. call sends it for the epic id with the caller headers
// header and returns its error.
type steerOnlyCall struct {
	name string
	call func(ctx context.Context, client enginev1connect.EpicsClient, header http.Header, id string) error
}

// steerOnlyCalls returns the nine changing epic calls: epic_answer, epic_kick, epic_pause, epic_resume, epic_stop,
// epic_retry, epic_release, epic_close and epic_amend.
func steerOnlyCalls() []steerOnlyCall {
	return []steerOnlyCall{
		{"AnswerEpic", func(ctx context.Context, c enginev1connect.EpicsClient, h http.Header, id string) error {
			_, err := c.AnswerEpic(ctx, steerOnlyRequest(h, &v1.AnswerEpicRequest{
				EpicId: id, Id: "note-1", Text: "Synthetic note.",
			}))
			return err
		}},
		{"KickEpic", func(ctx context.Context, c enginev1connect.EpicsClient, h http.Header, id string) error {
			_, err := c.KickEpic(ctx, steerOnlyRequest(h, &v1.KickEpicRequest{Id: id, Text: "Synthetic kick."}))
			return err
		}},
		{"PauseEpic", func(ctx context.Context, c enginev1connect.EpicsClient, h http.Header, id string) error {
			_, err := c.PauseEpic(ctx, steerOnlyRequest(h, &v1.PauseEpicRequest{Id: id}))
			return err
		}},
		{"ResumeEpic", func(ctx context.Context, c enginev1connect.EpicsClient, h http.Header, id string) error {
			_, err := c.ResumeEpic(ctx, steerOnlyRequest(h, &v1.ResumeEpicRequest{Id: id}))
			return err
		}},
		{"StopEpic", func(ctx context.Context, c enginev1connect.EpicsClient, h http.Header, id string) error {
			_, err := c.StopEpic(ctx, steerOnlyRequest(h, &v1.StopEpicRequest{Id: id}))
			return err
		}},
		{"RetryEpic", func(ctx context.Context, c enginev1connect.EpicsClient, h http.Header, id string) error {
			_, err := c.RetryEpic(ctx, steerOnlyRequest(h, &v1.RetryEpicRequest{Id: id}))
			return err
		}},
		{"ReleaseEpic", func(ctx context.Context, c enginev1connect.EpicsClient, h http.Header, id string) error {
			_, err := c.ReleaseEpic(ctx, steerOnlyRequest(h, &v1.ReleaseEpicRequest{Id: id}))
			return err
		}},
		{"CloseEpic", func(ctx context.Context, c enginev1connect.EpicsClient, h http.Header, id string) error {
			_, err := c.CloseEpic(ctx, steerOnlyRequest(h, &v1.CloseEpicRequest{Id: id, Kind: "abandoned"}))
			return err
		}},
		{"AmendEvidence", func(ctx context.Context, c enginev1connect.EpicsClient, h http.Header, id string) error {
			_, err := c.AmendEvidence(ctx, steerOnlyRequest(h, &v1.AmendEvidenceRequest{
				Id: id, Requirement: "R1", Item: amendTestItem(), Reason: "Synthetic reason.",
			}))
			return err
		}},
	}
}

// steerOnlyCaller is a caller the steering rule lets change an epic, with the headers its calls send.
type steerOnlyCaller struct {
	name   string
	header http.Header
}

// steerOnlyAllowedCallers returns the callers the steering rule does not refuse: the steering conversation, a call with no
// headers from the owner, a pane with empty state, and a step.
func steerOnlyAllowedCallers() []steerOnlyCaller {
	return []steerOnlyCaller{
		{name: "steering conversation", header: sessionHeader(steerOnlySteerer)},
		{name: "no headers", header: nil},
		{name: "empty-state pane", header: paneHeader(steerOnlyPane)},
		{name: "step session", header: sessionHeader(steerOnlyStep)},
	}
}

// steerOnlySnapshot returns the epic id read through client with its triggers, tells and actions, so a call that wrote
// anything changes it.
func steerOnlySnapshot(t *testing.T, client enginev1connect.EpicsClient, id string) *v1.StoredEpic {
	t.Helper()
	got, err := client.GetEpic(t.Context(), connect.NewRequest(&v1.GetEpicRequest{Id: id, History: true}))
	if err != nil {
		t.Fatalf("GetEpic %s: %v", id, err)
	}
	return got.Msg.GetEpic()
}

// wantSteerRefusal asserts that err is FailedPrecondition and that its message names the session caller, the steerer and
// epic_steer.
func wantSteerRefusal(t *testing.T, err error, caller, steerer string) {
	t.Helper()
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v (%v), want FailedPrecondition", got, err)
	}
	for _, want := range []string{caller, steerer, "epic_steer"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

// TestSteerOnlyRefusesNonSteeringConversation checks that each changing call from a conversation that does not steer the
// epic is refused, naming the steerer and epic_steer, and that the epic is left as it was.
func TestSteerOnlyRefusesNonSteeringConversation(t *testing.T) {
	t.Parallel()
	for _, c := range steerOnlyCalls() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			client := steerOnlyWorld(t)
			before := steerOnlySnapshot(t, client, epicServiceID)

			err := c.call(t.Context(), client, sessionHeader(steerOnlyOther), epicServiceID)
			wantSteerRefusal(t, err, steerOnlyOther, steerOnlySteerer)
			if after := steerOnlySnapshot(t, client, epicServiceID); !proto.Equal(before, after) {
				t.Errorf("epic after the refused call = %v, want it unchanged from %v", after, before)
			}
		})
	}
}

// TestSteerOnlyAcceptsSteererOwnerPaneAndStep checks that each changing call from the steering conversation, from a call
// with no headers, from a pane with empty state, and from a step is not refused by the steering rule.
func TestSteerOnlyAcceptsSteererOwnerPaneAndStep(t *testing.T) {
	t.Parallel()
	for _, c := range steerOnlyCalls() {
		for _, caller := range steerOnlyAllowedCallers() {
			t.Run(c.name+" from "+caller.name, func(t *testing.T) {
				t.Parallel()
				client := steerOnlyWorld(t)

				err := c.call(t.Context(), client, caller.header, epicServiceID)
				if err != nil && strings.Contains(err.Error(), "epic_steer") {
					t.Fatalf("call refused by the steering rule: %v", err)
				}
			})
		}
	}
}

// TestSteerOnlyLeavesReadsOpen checks that a conversation that does not steer the epic still reads it: GetEpic, ListEpics
// and FollowEpics answer.
func TestSteerOnlyLeavesReadsOpen(t *testing.T) {
	t.Parallel()
	client := steerOnlyWorld(t)
	header := sessionHeader(steerOnlyOther)

	get := steerOnlyRequest(header, &v1.GetEpicRequest{Id: epicServiceID})
	if _, err := client.GetEpic(t.Context(), get); err != nil {
		t.Errorf("GetEpic from a non-steering conversation: %v", err)
	}
	list := steerOnlyRequest(header, &v1.ListEpicsRequest{})
	if _, err := client.ListEpics(t.Context(), list); err != nil {
		t.Errorf("ListEpics from a non-steering conversation: %v", err)
	}
	follow := steerOnlyRequest(header, &v1.FollowEpicsRequest{})
	if _, err := client.FollowEpics(t.Context(), follow); err != nil {
		t.Errorf("FollowEpics from a non-steering conversation: %v", err)
	}
}

// TestSteerOnlyUnknownEpicFallsToNotFound checks that a changing call from a conversation that does not steer an epic
// that is not stored is the handler's own NotFound, not the steering refusal.
func TestSteerOnlyUnknownEpicFallsToNotFound(t *testing.T) {
	t.Parallel()
	for _, c := range steerOnlyCalls() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			client := steerOnlyWorld(t)

			err := c.call(t.Context(), client, sessionHeader(steerOnlyOther), "no-such-epic")
			if got := connect.CodeOf(err); got != connect.CodeNotFound {
				t.Fatalf("code = %v (%v), want NotFound", got, err)
			}
		})
	}
}

// TestSteerOnlyUnsteeredEpicNamesNoConversation checks that a changing call from a conversation against an epic no
// conversation steers is refused, naming that no conversation steers it and epic_steer.
func TestSteerOnlyUnsteeredEpicNamesNoConversation(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	other := store.EngineAdd{ID: steerOnlyOther, Dir: "/work/other"}
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{other}, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	client := newEpicsClient(t, serveEpicsWith(t, st, unsteeredSteering{}))
	if err := steererSetCreate(t, st, client, stepGitTestRepo(t), nil); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}

	_, err := client.PauseEpic(t.Context(), steerOnlyRequest(sessionHeader(steerOnlyOther), &v1.PauseEpicRequest{
		Id: epicServiceID,
	}))
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v (%v), want FailedPrecondition", got, err)
	}
	for _, want := range []string{"no conversation steers it", "epic_steer"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not contain %q", err, want)
		}
	}
}
