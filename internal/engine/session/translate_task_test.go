package session

import "testing"

// taskMessageLine is a system line of a session with the given subtype, and fields as the JSON members that follow
// the subtype.
func taskMessageLine(subtype, fields string) []byte {
	return []byte(`{"type":"system","subtype":"` + subtype + `",` +
		`"uuid":"00000000-0000-4000-8000-000000000002","session_id":"00000000-0000-4000-8000-000000000001",` +
		fields + `}`)
}

// requireOneEvent fails the test unless events is exactly one event of type T and returns it.
func requireOneEvent[T Event](t *testing.T, events []Event) T {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("events = %#v, want exactly one %T", events, *new(T))
	}
	event, ok := events[0].(T)
	if !ok {
		t.Fatalf("event = %#v, want %T", events[0], *new(T))
	}
	return event
}

func TestBackgroundTaskRowAgentStartedCarriesToolUseAndSubagentType(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	line := taskMessageLine("task_started",
		`"task_id":"task_agent_1","tool_use_id":"toolu_agent_1","description":"review the diff",`+
			`"task_type":"local_agent","subagent_type":"Explore"`)
	got := requireOneEvent[TaskStarted](t, tr.translate(line))
	want := TaskStarted{
		TaskID:       "task_agent_1",
		ToolUseID:    "toolu_agent_1",
		Description:  "review the diff",
		TaskType:     "local_agent",
		SubagentType: "Explore",
	}
	if got != want {
		t.Errorf("TaskStarted = %#v, want %#v", got, want)
	}
}

func TestBackgroundTaskRowShellStartedHasNoSubagentType(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	line := taskMessageLine("task_started",
		`"task_id":"task_shell_1","tool_use_id":"toolu_shell_1","description":"sleep 60","task_type":"local_bash"`)
	got := requireOneEvent[TaskStarted](t, tr.translate(line))
	want := TaskStarted{
		TaskID:      "task_shell_1",
		ToolUseID:   "toolu_shell_1",
		Description: "sleep 60",
		TaskType:    "local_bash",
	}
	if got != want {
		t.Errorf("TaskStarted = %#v, want %#v", got, want)
	}
}

func TestBackgroundTaskRowProgressCarriesLastTool(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	line := taskMessageLine("task_progress",
		`"task_id":"task_agent_1","tool_use_id":"toolu_agent_1","description":"review the diff",`+
			`"subagent_type":"Explore","usage":{"total_tokens":10,"tool_uses":1,"duration_ms":5},`+
			`"last_tool_name":"Read"`)
	got := requireOneEvent[TaskProgress](t, tr.translate(line))
	want := TaskProgress{
		TaskID:       "task_agent_1",
		ToolUseID:    "toolu_agent_1",
		Description:  "review the diff",
		SubagentType: "Explore",
		LastTool:     "Read",
	}
	if got != want {
		t.Errorf("TaskProgress = %#v, want %#v", got, want)
	}
}

func TestBackgroundTaskRowProgressCarriesSummary(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	line := taskMessageLine("task_progress",
		`"task_id":"task_agent_1","tool_use_id":"toolu_agent_1","description":"review the diff",`+
			`"usage":{"total_tokens":10,"tool_uses":1,"duration_ms":5},"summary":"reading the parser"`)
	got := requireOneEvent[TaskProgress](t, tr.translate(line))
	want := TaskProgress{
		TaskID:      "task_agent_1",
		ToolUseID:   "toolu_agent_1",
		Description: "review the diff",
		Summary:     "reading the parser",
	}
	if got != want {
		t.Errorf("TaskProgress = %#v, want %#v", got, want)
	}
}

func TestBackgroundTaskRowNotificationCarriesStatusAndSummary(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	line := taskMessageLine("task_notification",
		`"task_id":"task_agent_1","tool_use_id":"toolu_agent_1","status":"failed",`+
			`"output_file":"/tmp/out.txt","summary":"the agent hit an error"`)
	got := requireOneEvent[TaskEnded](t, tr.translate(line))
	want := TaskEnded{
		TaskID:    "task_agent_1",
		ToolUseID: "toolu_agent_1",
		Status:    "failed",
		Summary:   "the agent hit an error",
	}
	if got != want {
		t.Errorf("TaskEnded = %#v, want %#v", got, want)
	}
}

func TestBackgroundTaskRowUpdatedYieldsNoEvent(t *testing.T) {
	t.Parallel()
	tr := newTranslator()
	line := taskMessageLine("task_updated",
		`"task_id":"task_agent_1","patch":{"status":"running","is_backgrounded":true}`)
	if events := tr.translate(line); len(events) != 0 {
		t.Errorf("events = %#v, want none", events)
	}
}
