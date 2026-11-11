package push_test

import (
	"encoding/json"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
)

// stopHook is a Claude Code 2.1.321 Stop hook input. background is the raw JSON for the background_tasks member,
// or "" to omit the member.
func stopHook(background string) string {
	member := ""
	if background != "" {
		member = `,"background_tasks":` + background
	}
	return `{"session_id":"3f2c9a1e-0000-4000-8000-000000000001",` +
		`"transcript_path":"/home/dev/.claude/projects/work/3f2c9a1e.jsonl",` +
		`"cwd":"/home/dev/work","hook_event_name":"Stop","stop_hook_active":false,` +
		`"last_assistant_message":"Done."` + member + `}`
}

// permissionHook is a Claude Code 2.1.321 PermissionRequest hook input that also carries subagent tasks.
const permissionHook = `{"session_id":"3f2c9a1e-0000-4000-8000-000000000001",` +
	`"transcript_path":"/home/dev/.claude/projects/work/3f2c9a1e.jsonl",` +
	`"cwd":"/home/dev/work","permission_mode":"default",` +
	`"hook_event_name":"PermissionRequest","tool_name":"Bash",` +
	`"tool_input":{"command":"make install"},` +
	`"background_tasks":[{"id":"a1","type":"subagent","status":"running","description":"search"}]}`

const (
	subagentTasks = `[{"id":"a1","type":"subagent","status":"running","description":"search the tree"}]`
	workflowTasks = `[{"id":"w1","type":"workflow","status":"running","description":"release check"}]`
	shellTasks    = `[{"id":"b1","type":"shell","status":"running","description":"make test","command":"make test"}]`
	monitorTasks  = `[{"id":"m1","type":"monitor","status":"running","description":"watch the log"}]`
	mixedTasks    = `[{"id":"b1","type":"shell","status":"running","description":"make test","command":"make test"},` +
		`{"id":"a1","type":"subagent","status":"running","description":"search the tree"}]`
)

func TestWanted(t *testing.T) {
	cases := []struct {
		name    string
		to      protocol.State
		hook    string
		want    bool
		wantErr bool
	}{
		{
			name: "done with subagent task is not wanted",
			to:   protocol.StateDone, hook: stopHook(subagentTasks), want: false,
		},
		{
			name: "done with workflow task is not wanted",
			to:   protocol.StateDone, hook: stopHook(workflowTasks), want: false,
		},
		{
			name: "done with shell task is wanted",
			to:   protocol.StateDone, hook: stopHook(shellTasks), want: true,
		},
		{
			name: "done with monitor task is wanted",
			to:   protocol.StateDone, hook: stopHook(monitorTasks), want: true,
		},
		{
			name: "done with shell and subagent tasks is not wanted",
			to:   protocol.StateDone, hook: stopHook(mixedTasks), want: false,
		},
		{
			name: "done with empty task array is wanted",
			to:   protocol.StateDone, hook: stopHook(`[]`), want: true,
		},
		{
			name: "done with null task array is wanted",
			to:   protocol.StateDone, hook: stopHook(`null`), want: true,
		},
		{
			name: "done without background_tasks is wanted",
			to:   protocol.StateDone, hook: stopHook(""), want: true,
		},
		{
			name: "done with a string task list errors and is wanted",
			to:   protocol.StateDone, hook: stopHook(`"none"`), want: true, wantErr: true,
		},
		{
			name: "done with non-object tasks errors and is wanted",
			to:   protocol.StateDone, hook: stopHook(`[1]`), want: true, wantErr: true,
		},
		{
			name: "done with a hook that is not an object errors and is wanted",
			to:   protocol.StateDone, hook: `not json`, want: true, wantErr: true,
		},
		{
			name: "waiting is wanted",
			to:   protocol.StateWaiting, hook: permissionHook, want: true,
		},
		{
			name: "waiting with subagent tasks is wanted",
			to:   protocol.StateWaiting, hook: stopHook(subagentTasks), want: true,
		},
		{
			name: "working is not wanted",
			to:   protocol.StateWorking, hook: stopHook(shellTasks), want: false,
		},
		{
			name: "unknown state is not wanted",
			to:   protocol.State("bogus"), hook: stopHook(shellTasks), want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := push.Wanted(tc.to, json.RawMessage(tc.hook))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Wanted error = %v, want error %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("Wanted = %v, want %v", got, tc.want)
			}
		})
	}
}
