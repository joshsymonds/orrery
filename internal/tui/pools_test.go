package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect"
	"github.com/charmbracelet/x/ansi"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// errNotUsed is what fakePools returns from the methods the pools view does not call.
var errNotUsed = errors.New("the pools view does not call this method")

// fakePools is an in-process enginev1connect.PoolsClient. Status answers with status, or with err when err is set, and
// counts its calls.
type fakePools struct {
	status *v1.PoolsStatusResponse
	err    error
	calls  int
}

var _ enginev1connect.PoolsClient = (*fakePools)(nil)

func (f *fakePools) Acquire(
	context.Context,
	*connect.Request[v1.AcquireRequest],
) (*connect.ServerStreamForClient[v1.AcquireResponse], error) {
	return nil, errNotUsed
}

func (f *fakePools) Status(
	context.Context,
	*connect.Request[v1.PoolsStatusRequest],
) (*connect.Response[v1.PoolsStatusResponse], error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.status), nil
}

func (f *fakePools) Hold(
	context.Context,
	*connect.Request[v1.PoolsHoldRequest],
) (*connect.Response[v1.PoolsHoldResponse], error) {
	return nil, errNotUsed
}

func (f *fakePools) Free(
	context.Context,
	*connect.Request[v1.PoolsFreeRequest],
) (*connect.Response[v1.PoolsFreeResponse], error) {
	return nil, errNotUsed
}

// poolsStatus is the status the pools view tests load. gpu has a granted member, a held member, a stopping member, a
// free asleep member, and two waiters. box-9 is held outside every pool. gpu-2's hold is listed too, so the view must
// not show it twice.
func poolsStatus() *v1.PoolsStatusResponse {
	return &v1.PoolsStatusResponse{
		Pools: []*v1.PoolStatus{{
			Name: "gpu",
			Cap:  2,
			Members: []*v1.PoolMemberStatus{
				{Name: "gpu-1", State: "admitted", Holder: &v1.PoolHolder{
					GrantId: "g1", Project: "/home/user/src/alpha", ClientPid: 4101,
					Command: []string{"claude", "--resume"}, Since: testNowUnix - 720,
				}},
				{Name: "gpu-2", State: "awake", HeldSince: testNowUnix - 180},
				{Name: "gpu-3", State: "admitted", Holder: &v1.PoolHolder{
					GrantId: "g2", Project: "/home/user/src/gamma", ClientPid: 4102,
					Command: []string{"npm", "test"}, Since: testNowUnix - 20, Stopping: true,
				}},
				{Name: "gpu-4", State: "asleep"},
			},
			Waiters: []*v1.PoolWaiter{
				{Position: 1, Project: "/home/user/src/beta", Command: []string{"cargo", "build"},
					Count: 1, Since: testNowUnix - 240},
				{Position: 2, Project: "/home/user/src/delta", Command: []string{"make"},
					Count: 2, Since: testNowUnix - 60},
			},
		}},
		Holds: []*v1.PoolHold{
			{Member: "box-9", Since: testNowUnix - 300},
			{Member: "gpu-2", Since: testNowUnix - 180},
		},
	}
}

// poolsModel returns the tree at the golden size, with keyPanes loaded and pools as its Pools client.
func poolsModel(t *testing.T, pools enginev1connect.PoolsClient) model {
	t.Helper()
	m := loadedModel(t, staticSource)
	m.pools = pools
	return m
}

// poolsCursorLine returns the frame line under the pools cursor, without its escapes. It fails unless exactly one line
// carries the cursor mark.
func poolsCursorLine(t *testing.T, content string) string {
	t.Helper()
	var marked []string
	for _, line := range strings.Split(content, "\n") {
		if text := ansi.Strip(line); strings.HasPrefix(text, "> ") {
			marked = append(marked, text)
		}
	}
	if len(marked) != 1 {
		t.Fatalf("%d lines carry the cursor mark, want 1:\n%s", len(marked), content)
	}
	return marked[0]
}

// assertPoolsCursor fails unless the pools cursor is on the row of member.
func assertPoolsCursor(t *testing.T, m model, member string) {
	t.Helper()
	fields := strings.Fields(poolsCursorLine(t, m.View().Content))
	if len(fields) < 2 || fields[1] != member {
		t.Fatalf("pools cursor on %q, want %q:\n%s", fields, member, m.View().Content)
	}
}

// lastLine returns the last line of the frame.
func lastLine(content string) string {
	lines := strings.Split(content, "\n")
	return lines[len(lines)-1]
}

func TestViewPools(t *testing.T) {
	m := press(t, poolsModel(t, &fakePools{status: poolsStatus()}), "p")
	checkGolden(t, "TestViewPools", m.View().Content)
}

func TestPKeyOpensPoolsViewAndReturnsToPanes(t *testing.T) {
	fake := &fakePools{status: poolsStatus()}
	m := press(t, poolsModel(t, fake), "p")
	content := m.View().Content
	if !strings.Contains(content, "cap 2") {
		t.Fatalf("p did not show the pools view:\n%s", content)
	}
	if strings.Contains(content, "alpha-review") {
		t.Fatalf("pane rows shown in the pools view:\n%s", content)
	}
	if fake.calls != 1 {
		t.Fatalf("p read Status %d times, want 1", fake.calls)
	}

	m = press(t, m, "p")
	content = m.View().Content
	if strings.Contains(content, "cap 2") || !strings.Contains(content, "alpha-review") {
		t.Fatalf("second p did not return to the panes:\n%s", content)
	}
}

func TestPoolsViewListsEachHoldOnce(t *testing.T) {
	m := press(t, poolsModel(t, &fakePools{status: poolsStatus()}), "p")
	content := m.View().Content
	if n := strings.Count(content, "gpu-2"); n != 1 {
		t.Fatalf("gpu-2 appears %d times, want 1 (its member row only):\n%s", n, content)
	}
	if n := strings.Count(content, "box-9"); n != 1 {
		t.Fatalf("box-9 appears %d times, want 1:\n%s", n, content)
	}
}

func TestPoolsCursorMovesOverMemberRowsAndOutsideHolds(t *testing.T) {
	m := press(t, poolsModel(t, &fakePools{status: poolsStatus()}), "p")
	assertPoolsCursor(t, m, "gpu-1")
	steps := []struct {
		key    string
		member string
	}{
		{"j", "gpu-2"},
		{"j", "gpu-3"},
		{"j", "gpu-4"},
		{"j", "box-9"},
		{"j", "box-9"},
		{"k", "gpu-4"},
	}
	for _, step := range steps {
		m = press(t, m, step.key)
		assertPoolsCursor(t, m, step.member)
	}
	m = pressAll(t, m, "k", "k", "k", "k", "k")
	assertPoolsCursor(t, m, "gpu-1")
}

func TestPoolsReloadKeepsCursorOnItsMember(t *testing.T) {
	fake := &fakePools{status: poolsStatus()}
	m := pressAll(t, poolsModel(t, fake), "p", "j")
	assertPoolsCursor(t, m, "gpu-2")

	status := poolsStatus()
	status.Pools[0].Members = status.Pools[0].Members[1:]
	fake.status = status
	m = modelOf(t, settle(t, m, m.loadPools()))
	assertPoolsCursor(t, m, "gpu-2")
}

func TestPoolsStatusErrorShowsOnStatusLineAndKeepsRows(t *testing.T) {
	fake := &fakePools{status: poolsStatus()}
	m := press(t, poolsModel(t, fake), "p")

	fake.err = errors.New("orreryd gone")
	m = modelOf(t, settle(t, m, m.loadPools()))
	if got := lastLine(m.View().Content); got != "error: pools status: orreryd gone" {
		t.Fatalf("last line = %q, want the status error", got)
	}
	if !strings.Contains(m.View().Content, "gpu-1") {
		t.Fatalf("pool rows lost after a status error:\n%s", m.View().Content)
	}

	fake.err = nil
	m = modelOf(t, settle(t, m, m.loadPools()))
	if got := lastLine(m.View().Content); strings.HasPrefix(got, "error:") {
		t.Fatalf("status error still shown after a good read: %q", got)
	}
}

func TestPoolsViewWithoutClientShowsUnavailable(t *testing.T) {
	m := press(t, poolsModel(t, nil), "p")
	if first := strings.Split(m.View().Content, "\n")[0]; first != "pools unavailable" {
		t.Fatalf("first line = %q, want %q:\n%s", first, "pools unavailable", m.View().Content)
	}
	m = press(t, m, "p")
	if !strings.Contains(m.View().Content, "alpha-review") {
		t.Fatalf("p did not return to the panes without a client:\n%s", m.View().Content)
	}
}

func TestPaneActionsDoNothingInPoolsView(t *testing.T) {
	m := press(t, poolsModel(t, &fakePools{status: poolsStatus()}), "p")
	before := m.View().Content
	for _, name := range []string{"x", "c", "s", "N", "X", "D", "enter", "/", "n"} {
		next, cmd := m.Update(keyMsg(name))
		if cmd != nil {
			t.Fatalf("key %q in the pools view returned a command, want none", name)
		}
		if got := modelOf(t, next).View().Content; got != before {
			t.Fatalf("frame changed after %q in the pools view:\n%s\nwant:\n%s", name, got, before)
		}
	}
}

func TestQAndEscCloseTheTreeFromPoolsView(t *testing.T) {
	m := press(t, poolsModel(t, &fakePools{status: poolsStatus()}), "p")
	for _, name := range []string{"q", "esc"} {
		_, cmd := m.Update(keyMsg(name))
		assertQuit(t, cmd)
	}
}

// runTick runs the commands a tick returns. The tick's own timer waits a second before its message arrives.
func runTick(t *testing.T, m model) {
	t.Helper()
	_, cmd := m.Update(tickMsg{})
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("tick returned %T, want tea.BatchMsg", msg)
	}
	for _, c := range batch {
		c()
	}
}

func TestTickReadsStatusOnlyWhilePoolsViewIsShown(t *testing.T) {
	fake := &fakePools{status: poolsStatus()}
	m := press(t, poolsModel(t, fake), "p")
	calls := fake.calls
	runTick(t, m)
	if fake.calls != calls+1 {
		t.Fatalf("tick in the pools view read Status %d times, want 1", fake.calls-calls)
	}

	m = press(t, m, "p")
	calls = fake.calls
	runTick(t, m)
	if fake.calls != calls {
		t.Fatalf("tick in the panes read Status %d times, want 0", fake.calls-calls)
	}
}

func TestHelpListsPKey(t *testing.T) {
	content := pressAll(t, loadedModel(t, staticSource), "?").View().Content
	if !slices.ContainsFunc(strings.Split(content, "\n"), func(line string) bool {
		return strings.HasPrefix(ansi.Strip(line), "p ")
	}) {
		t.Fatalf("help overlay has no line for p:\n%s", content)
	}
}
