package view_test

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/view"
)

func ids(panes []protocol.Pane) []string {
	out := make([]string, 0, len(panes))
	for _, p := range panes {
		out = append(out, p.ID)
	}
	return out
}

func dirs(groups []view.Group) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.Dir)
	}
	return out
}

func TestUnseen(t *testing.T) {
	cases := []struct {
		name string
		pane protocol.Pane
		want bool
	}{
		{"unset state is never unseen", protocol.Pane{State: "", Changed: 5, Seen: 1}, false},
		{"changed equal to seen", protocol.Pane{State: protocol.StateWaiting, Changed: 4, Seen: 4}, false},
		{"changed less than seen", protocol.Pane{State: protocol.StateDone, Changed: 2, Seen: 4}, false},
		{"changed greater than seen", protocol.Pane{State: protocol.StateDone, Changed: 5, Seen: 4}, true},
		{"never seen, changed set", protocol.Pane{State: protocol.StateWorking, Changed: 1, Seen: 0}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := view.Unseen(tc.pane); got != tc.want {
				t.Fatalf("Unseen(%+v) = %v, want %v", tc.pane, got, tc.want)
			}
		})
	}
}

func TestGroupsGroupsByCwd(t *testing.T) {
	panes := []protocol.Pane{
		{ID: "a1", Cwd: "/home/user/project-a", State: protocol.StateDone, Session: "s", PaneIndex: 0},
		{ID: "b1", Cwd: "/home/user/project-b", State: protocol.StateDone, Session: "s", PaneIndex: 1},
		{ID: "a2", Cwd: "/home/user/project-a", State: protocol.StateDone, Session: "s", PaneIndex: 2},
		{ID: "a3", Cwd: "/home/user/project-a/sub", State: protocol.StateDone, Session: "s", PaneIndex: 3},
	}
	groups := view.Groups(panes)
	want := []string{"/home/user/project-a", "/home/user/project-a/sub", "/home/user/project-b"}
	if got := dirs(groups); !slices.Equal(got, want) {
		t.Fatalf("group dirs = %v, want %v", got, want)
	}
	if got := ids(groups[0].Panes); !slices.Equal(got, []string{"a1", "a2"}) {
		t.Fatalf("project-a panes = %v, want [a1 a2]", got)
	}
	if got := ids(groups[1].Panes); !slices.Equal(got, []string{"a3"}) {
		t.Fatalf("project-a/sub panes = %v, want [a3]", got)
	}
	if got := ids(groups[2].Panes); !slices.Equal(got, []string{"b1"}) {
		t.Fatalf("project-b panes = %v, want [b1]", got)
	}
}

func TestGroupsExcludesShellSessions(t *testing.T) {
	panes := []protocol.Pane{
		{ID: "agent", Cwd: "/home/user/project-a", State: protocol.StateWaiting, Session: "work"},
		{
			ID:      "shell",
			Cwd:     "/home/user/project-a",
			State:   protocol.StateWaiting,
			Session: protocol.ShellSessionPrefix + "1",
		},
		{
			ID:      "lone-shell",
			Cwd:     "/home/user/project-b",
			State:   protocol.StateWaiting,
			Session: protocol.ShellSessionPrefix + "2",
		},
	}
	groups := view.Groups(panes)
	if got := dirs(groups); !slices.Equal(got, []string{"/home/user/project-a"}) {
		t.Fatalf("group dirs = %v, want only project-a", got)
	}
	if got := ids(groups[0].Panes); !slices.Equal(got, []string{"agent"}) {
		t.Fatalf("project-a panes = %v, want [agent]", got)
	}
}

func TestGroupsWaitingLeadsGroupAndGroups(t *testing.T) {
	panes := []protocol.Pane{
		{
			ID:      "b-unseen-done",
			Cwd:     "/home/user/project-b",
			State:   protocol.StateDone,
			Changed: 3,
			Seen:    1,
			Session: "s",
		},
		{ID: "a-seen-done", Cwd: "/home/user/project-a", State: protocol.StateDone, Changed: 1, Seen: 1, Session: "s"},
		{
			ID:      "a-seen-waiting",
			Cwd:     "/home/user/project-a",
			State:   protocol.StateWaiting,
			Changed: 1,
			Seen:    1,
			Session: "s",
		},
	}
	groups := view.Groups(panes)
	if got := dirs(groups); !slices.Equal(got, []string{"/home/user/project-a", "/home/user/project-b"}) {
		t.Fatalf("group dirs = %v, want project-a first", got)
	}
	if got := ids(groups[0].Panes); !slices.Equal(got, []string{"a-seen-waiting", "a-seen-done"}) {
		t.Fatalf("project-a panes = %v, want waiting pane first", got)
	}
}

func TestGroupsPaneRankOrder(t *testing.T) {
	panes := []protocol.Pane{
		{
			ID:        "seen-done",
			Cwd:       "/home/user/project-a",
			State:     protocol.StateDone,
			Changed:   1,
			Seen:      1,
			Session:   "s",
			PaneIndex: 0,
		},
		{ID: "stateless", Cwd: "/home/user/project-a", State: "", Changed: 9, Seen: 1, Session: "s", PaneIndex: 1},
		{
			ID:        "seen-working",
			Cwd:       "/home/user/project-a",
			State:     protocol.StateWorking,
			Changed:   1,
			Seen:      1,
			Session:   "s",
			PaneIndex: 2,
		},
		{
			ID:        "unseen-done",
			Cwd:       "/home/user/project-a",
			State:     protocol.StateDone,
			Changed:   5,
			Seen:      1,
			Session:   "s",
			PaneIndex: 3,
		},
		{
			ID:        "unseen-working",
			Cwd:       "/home/user/project-a",
			State:     protocol.StateWorking,
			Changed:   5,
			Seen:      1,
			Session:   "s",
			PaneIndex: 4,
		},
		{
			ID:        "seen-waiting",
			Cwd:       "/home/user/project-a",
			State:     protocol.StateWaiting,
			Changed:   1,
			Seen:      1,
			Session:   "s",
			PaneIndex: 5,
		},
	}
	groups := view.Groups(panes)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	want := []string{"seen-waiting", "unseen-done", "unseen-working", "seen-working", "seen-done", "stateless"}
	if got := ids(groups[0].Panes); !slices.Equal(got, want) {
		t.Fatalf("pane order = %v, want %v", got, want)
	}
}

func TestGroupsTieOrder(t *testing.T) {
	panes := []protocol.Pane{
		{
			ID:          "host2-s1-w0-p0",
			Host:        "host-2",
			Session:     "s1",
			WindowIndex: 0,
			PaneIndex:   0,
			Cwd:         "/x",
			State:       protocol.StateDone,
			Changed:     1,
			Seen:        1,
		},
		{
			ID:          "host1-s2-w0-p0",
			Host:        "host-1",
			Session:     "s2",
			WindowIndex: 0,
			PaneIndex:   0,
			Cwd:         "/x",
			State:       protocol.StateDone,
			Changed:     1,
			Seen:        1,
		},
		{
			ID:          "host1-s1-w1-p0",
			Host:        "host-1",
			Session:     "s1",
			WindowIndex: 1,
			PaneIndex:   0,
			Cwd:         "/x",
			State:       protocol.StateDone,
			Changed:     1,
			Seen:        1,
		},
		{
			ID:          "host1-s1-w0-p1",
			Host:        "host-1",
			Session:     "s1",
			WindowIndex: 0,
			PaneIndex:   1,
			Cwd:         "/x",
			State:       protocol.StateDone,
			Changed:     1,
			Seen:        1,
		},
		{
			ID:          "host1-s1-w0-p0",
			Host:        "host-1",
			Session:     "s1",
			WindowIndex: 0,
			PaneIndex:   0,
			Cwd:         "/x",
			State:       protocol.StateDone,
			Changed:     1,
			Seen:        1,
		},
	}
	groups := view.Groups(panes)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	want := []string{"host1-s1-w0-p0", "host1-s1-w0-p1", "host1-s1-w1-p0", "host1-s2-w0-p0", "host2-s1-w0-p0"}
	if got := ids(groups[0].Panes); !slices.Equal(got, want) {
		t.Fatalf("pane order = %v, want %v", got, want)
	}
}

func TestGroupsOrdersGroupsByLowestRankThenDir(t *testing.T) {
	panes := []protocol.Pane{
		{ID: "c-done", Cwd: "/home/user/project-c", State: protocol.StateDone, Changed: 1, Seen: 1, Session: "s"},
		{ID: "b-done", Cwd: "/home/user/project-b", State: protocol.StateDone, Changed: 1, Seen: 1, Session: "s"},
		{ID: "a-done", Cwd: "/home/user/project-a", State: protocol.StateDone, Changed: 1, Seen: 1, Session: "s"},
		{ID: "d-unseen", Cwd: "/home/user/project-d", State: protocol.StateDone, Changed: 5, Seen: 1, Session: "s"},
	}
	groups := view.Groups(panes)
	want := []string{"/home/user/project-d", "/home/user/project-a", "/home/user/project-b", "/home/user/project-c"}
	if got := dirs(groups); !slices.Equal(got, want) {
		t.Fatalf("group dirs = %v, want %v", got, want)
	}
}

func TestGroupsDoesNotModifyInput(t *testing.T) {
	panes := []protocol.Pane{
		{ID: "done", Cwd: "/home/user/project-a", State: protocol.StateDone, Changed: 1, Seen: 1, Session: "s"},
		{
			ID:      "shell",
			Cwd:     "/home/user/project-a",
			State:   protocol.StateWaiting,
			Session: protocol.ShellSessionPrefix + "1",
		},
		{ID: "waiting", Cwd: "/home/user/project-b", State: protocol.StateWaiting, Changed: 1, Seen: 1, Session: "s"},
	}
	before := slices.Clone(panes)
	_ = view.Groups(panes)
	if !slices.Equal(panes, before) {
		t.Fatalf("input changed: got %+v, want %+v", panes, before)
	}
}
