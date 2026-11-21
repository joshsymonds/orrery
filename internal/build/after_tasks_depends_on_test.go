package build

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// afterRerun returns a decoded rerun of an orchestrator whose after_tasks names the tasks after.
func afterRerun(t *testing.T, after ...int64) orchestratorReturn {
	t.Helper()
	raw := orchReturn(t, map[string]any{"decision": "rerun", "after_tasks": after})
	ret, err := decodeOrchestratorReturn([]byte(raw))
	if err != nil {
		t.Fatalf("decodeOrchestratorReturn(%s) error = %v", raw, err)
	}
	return ret
}

// TestAfterTasksRefusesCycleThroughDependsOn checks that a rerun of task 3 may not name an open task that reaches task 3
// through depends_on, alone or with after_tasks, since that task waits on task 3 as one that waits through after_tasks
// does. An open task that depends on an unrelated task is not refused.
func TestAfterTasksRefusesCycleThroughDependsOn(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 3, Files: []string{"a.go"}, Status: taskOpen}
	cases := []struct {
		name  string
		peers []Peer
		want  []string
	}{
		{
			name: "an open task whose depends_on names this task is refused",
			peers: func() []Peer {
				dependent := openPeer(4, "x.go")
				dependent.Task.DependsOn = []int64{3}
				return []Peer{dependent}
			}(),
			want: []string{"after_tasks[0]: T4 waits on this task"},
		},
		{
			name: "an open task whose depends_on names a task that waits on this task through after_tasks is refused",
			peers: func() []Peer {
				dependent := openPeer(4, "x.go")
				dependent.Task.DependsOn = []int64{5}
				waiter := openPeer(5, "y.go")
				waiter.Task.Build.Held, waiter.Task.Build.After = HeldWaiting, "3"
				return []Peer{dependent, waiter}
			}(),
			want: []string{"after_tasks[0]: T4 waits on this task"},
		},
		{
			name: "an open task whose depends_on names an unrelated task is not refused",
			peers: func() []Peer {
				dependent := openPeer(4, "x.go")
				dependent.Task.DependsOn = []int64{6}
				return []Peer{dependent, openPeer(6, "z.go")}
			}(),
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			peers := append([]Peer{{Task: task, State: Pending}}, tc.peers...)
			got := stateRefusals(afterRerun(t, 4), task, peers, false)
			if !slices.Equal(got, tc.want) {
				t.Errorf("stateRefusals = %q, want %q", got, tc.want)
			}
		})
	}
}
