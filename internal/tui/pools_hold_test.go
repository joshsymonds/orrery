package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// holdingPools is a Pools client that records each Hold and Free call and applies it to the status it reports, so a
// reload shows the change. holdErr and freeErr fail their calls when set.
type holdingPools struct {
	*fakePools
	holds   []string
	frees   []string
	holdErr error
	freeErr error
}

var _ enginev1connect.PoolsClient = (*holdingPools)(nil)

func (h *holdingPools) Hold(
	_ context.Context,
	req *connect.Request[v1.PoolsHoldRequest],
) (*connect.Response[v1.PoolsHoldResponse], error) {
	member := req.Msg.GetMember()
	h.holds = append(h.holds, member)
	if h.holdErr != nil {
		return nil, h.holdErr
	}
	setMemberHeld(h.status, member, testNowUnix)
	return connect.NewResponse(&v1.PoolsHoldResponse{}), nil
}

func (h *holdingPools) Free(
	_ context.Context,
	req *connect.Request[v1.PoolsFreeRequest],
) (*connect.Response[v1.PoolsFreeResponse], error) {
	member := req.Msg.GetMember()
	h.frees = append(h.frees, member)
	if h.freeErr != nil {
		return nil, h.freeErr
	}
	setMemberHeld(h.status, member, 0)
	return connect.NewResponse(&v1.PoolsFreeResponse{}), nil
}

// setMemberHeld sets the held-since of the pool member named member in status: since when it is held, 0 when it is not.
func setMemberHeld(status *v1.PoolsStatusResponse, member string, since int64) {
	for _, pool := range status.GetPools() {
		for _, m := range pool.GetMembers() {
			if m.GetName() == member {
				m.HeldSince = since
			}
		}
	}
}

// holdPoolsModel returns the tree at the golden size with its pools view's client recording holds and frees.
func holdPoolsModel(t *testing.T) (model, *holdingPools) {
	t.Helper()
	fake := &holdingPools{fakePools: &fakePools{status: poolsStatus()}}
	return poolsModel(t, fake), fake
}

// heldWords returns the words of the frame line under the pools cursor.
func heldWords(t *testing.T, m model) []string {
	t.Helper()
	return strings.Fields(poolsCursorLine(t, m.View().Content))
}

func TestHKeyHoldsFreeMemberAndReloadsView(t *testing.T) {
	m, fake := holdPoolsModel(t)
	m = pressAll(t, m, "p", "j", "j", "j")
	assertPoolsCursor(t, m, "gpu-4")
	reads := fake.calls

	m = press(t, m, "h")
	if !slices.Equal(fake.holds, []string{"gpu-4"}) || len(fake.frees) != 0 {
		t.Fatalf("h on free gpu-4: holds %v, frees %v; want holds [gpu-4] and no frees", fake.holds, fake.frees)
	}
	if fake.calls != reads+1 {
		t.Fatalf("h read Status %d times after the hold, want 1", fake.calls-reads)
	}
	assertPoolsCursor(t, m, "gpu-4")
	if !slices.Contains(heldWords(t, m), "held") {
		t.Fatalf("gpu-4 not shown held after h:\n%s", m.View().Content)
	}
}

func TestHKeyFreesHeldMemberAndReloadsView(t *testing.T) {
	m, fake := holdPoolsModel(t)
	m = pressAll(t, m, "p", "j")
	assertPoolsCursor(t, m, "gpu-2")
	reads := fake.calls

	m = press(t, m, "h")
	if len(fake.holds) != 0 || !slices.Equal(fake.frees, []string{"gpu-2"}) {
		t.Fatalf("h on held gpu-2: holds %v, frees %v; want no holds and frees [gpu-2]", fake.holds, fake.frees)
	}
	if fake.calls != reads+1 {
		t.Fatalf("h read Status %d times after the free, want 1", fake.calls-reads)
	}
	assertPoolsCursor(t, m, "gpu-2")
	if !slices.Contains(heldWords(t, m), "free") {
		t.Fatalf("gpu-2 not shown free after h:\n%s", m.View().Content)
	}
}

func TestHKeyFreesHoldOnMemberOfNoPool(t *testing.T) {
	m, fake := holdPoolsModel(t)
	m = pressAll(t, m, "p", "j", "j", "j", "j")
	assertPoolsCursor(t, m, "box-9")

	press(t, m, "h")
	if len(fake.holds) != 0 || !slices.Equal(fake.frees, []string{"box-9"}) {
		t.Fatalf("h on held box-9: holds %v, frees %v; want no holds and frees [box-9]", fake.holds, fake.frees)
	}
}

func TestHKeyHoldErrorShowsOnStatusLine(t *testing.T) {
	m, fake := holdPoolsModel(t)
	fake.holdErr = errors.New("orreryd gone")
	m = pressAll(t, m, "p", "j", "j", "j", "h")
	if !slices.Equal(fake.holds, []string{"gpu-4"}) {
		t.Fatalf("h on free gpu-4: holds %v, want [gpu-4]", fake.holds)
	}
	if got := lastLine(m.View().Content); got != "error: pools hold: orreryd gone" {
		t.Fatalf("last line = %q, want the hold error", got)
	}
	assertPoolsCursor(t, m, "gpu-4")
	if slices.Contains(heldWords(t, m), "held") {
		t.Fatalf("gpu-4 shown held after a failed hold:\n%s", m.View().Content)
	}
}

func TestHKeyFreeErrorShowsOnStatusLine(t *testing.T) {
	m, fake := holdPoolsModel(t)
	fake.freeErr = errors.New("orreryd gone")
	m = pressAll(t, m, "p", "j", "h")
	if !slices.Equal(fake.frees, []string{"gpu-2"}) {
		t.Fatalf("h on held gpu-2: frees %v, want [gpu-2]", fake.frees)
	}
	if got := lastLine(m.View().Content); got != "error: pools free: orreryd gone" {
		t.Fatalf("last line = %q, want the free error", got)
	}
}

func TestHKeyClearsHoldErrorOnNextHold(t *testing.T) {
	m, fake := holdPoolsModel(t)
	fake.holdErr = errors.New("orreryd gone")
	m = pressAll(t, m, "p", "j", "j", "j", "h")
	if got := lastLine(m.View().Content); got != "error: pools hold: orreryd gone" {
		t.Fatalf("last line = %q, want the hold error", got)
	}

	fake.holdErr = nil
	m = press(t, m, "h")
	if got := lastLine(m.View().Content); strings.HasPrefix(got, "error:") {
		t.Fatalf("hold error still shown after a hold that succeeded: %q", got)
	}
	if !slices.Contains(heldWords(t, m), "held") {
		t.Fatalf("gpu-4 not shown held after the second h:\n%s", m.View().Content)
	}
}

func TestHKeyWithoutClientDoesNothing(t *testing.T) {
	m := press(t, poolsModel(t, nil), "p")
	if _, cmd := m.Update(keyMsg("h")); cmd != nil {
		t.Fatalf("h without a Pools client returned a command, want none")
	}
}

func TestHKeyInPaneTreeCallsNothing(t *testing.T) {
	fake := &holdingPools{fakePools: &fakePools{status: poolsStatus()}}
	m := poolsModel(t, fake)
	before := m.View().Content
	next, cmd := m.Update(keyMsg("h"))
	if cmd != nil {
		t.Fatalf("h in the pane tree returned a command, want none")
	}
	if len(fake.holds) != 0 || len(fake.frees) != 0 {
		t.Fatalf("h in the pane tree called holds %v, frees %v; want none", fake.holds, fake.frees)
	}
	if got := modelOf(t, next).View().Content; got != before {
		t.Fatalf("frame changed after h in the pane tree:\n%s\nwant:\n%s", got, before)
	}
}
