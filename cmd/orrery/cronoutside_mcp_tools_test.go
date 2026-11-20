package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// cronMCPToolsStart runs `orrery --socket socket mcp` with ORRERY_SESSION set to cronStoreSession, with pipes for stdin
// and stdout. The command closes stdout when it returns.
func cronMCPToolsStart(t *testing.T, socket string) *mcpRun {
	t.Helper()
	getenv := envFrom(map[string]string{protocol.EnvSession: cronStoreSession})
	stdinR, stdinW := mustPipe(t)
	stdoutR, stdoutW := mustPipe(t)
	r := &mcpRun{t: t, stdin: stdinW, stdout: stdoutR, lines: bufio.NewReader(stdoutR), done: make(chan int, 1)}
	t.Cleanup(func() {
		closeFile(t, stdinW)
		closeFile(t, stdoutR)
	})
	go func() {
		code := run([]string{"--socket", socket, "mcp"}, stdinR, stdoutW, &r.stderr, getenv)
		closeFile(t, stdoutW)
		closeFile(t, stdinR)
		r.done <- code
	}()
	return r
}

// cronMCPToolsHandshake completes the MCP handshake of cmd, so that the next request may be a call.
func cronMCPToolsHandshake(t *testing.T, cmd *mcpRun) {
	t.Helper()
	cmd.send(mcpInitialize)
	requireResult(t, cmd.receive(), 1)
	cmd.send(mcpInitialized)
}

// cronMCPToolsNames returns the names of the tools cmd lists, in the reply to request id.
func cronMCPToolsNames(t *testing.T, cmd *mcpRun, id int) []string {
	t.Helper()
	cmd.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`, id))
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(requireResult(t, cmd.receive(), id), &listed); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// cronMCPToolsCall sends a tools/call request for the tool name with arguments, as request id, and returns the text of
// the one content block of its reply and whether the reply is a tool error.
func cronMCPToolsCall(t *testing.T, cmd *mcpRun, id int, name, arguments string) (string, bool) {
	t.Helper()
	cmd.send(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, name, arguments,
	))
	var called struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(requireResult(t, cmd.receive(), id), &called); err != nil {
		t.Fatalf("decode tools/call result: %v", err)
	}
	if len(called.Content) != 1 {
		t.Fatalf("%s result has %d content blocks, want 1", name, len(called.Content))
	}
	return called.Content[0].Text, called.IsError
}

// cronMCPToolsJob returns a recurring job with id, stored under session.
func cronMCPToolsJob(id, session string) store.CronJob {
	return store.CronJob{
		ID: id, Session: session, ToolUseID: "toolu_" + id, Cron: "*/5 * * * *", Prompt: "check the build " + id,
		Recurring: true, Created: cronStoreNow(),
	}
}

// cronMCPToolsStore stores job in st.
func cronMCPToolsStore(t *testing.T, st *store.Store, job store.CronJob) {
	t.Helper()
	if _, _, err := st.CreateCronJob(t.Context(), job); err != nil {
		t.Fatalf("CreateCronJob %s: %v", job.ID, err)
	}
}

// TestCronOutsideMCPToolsListAndDelete stores two jobs of the session, and drives `orrery mcp` through a listing of the
// tools, a cron_list that names both jobs, a cron_delete of one, and a cron_list that no longer names it. The store
// keeps only the other job.
func TestCronOutsideMCPToolsListAndDelete(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	first := cronMCPToolsJob("cron-00000001", cronStoreSession)
	second := cronMCPToolsJob("cron-00000002", cronStoreSession)
	cronMCPToolsStore(t, st, first)
	cronMCPToolsStore(t, st, second)

	cmd := cronMCPToolsStart(t, socket)
	cronMCPToolsHandshake(t, cmd)
	names := cronMCPToolsNames(t, cmd, 2)
	for _, want := range []string{"cron_list", "cron_delete"} {
		if !slices.Contains(names, want) {
			t.Errorf("tools/list = %v, want it to name %s", names, want)
		}
	}

	listed, isError := cronMCPToolsCall(t, cmd, 3, "cron_list", "{}")
	if isError {
		t.Fatalf("cron_list is a tool error: %q", listed)
	}
	for _, id := range []string{first.ID, second.ID} {
		if !strings.Contains(listed, id) {
			t.Errorf("cron_list = %q, want it to name job %s", listed, id)
		}
	}

	deleted, isError := cronMCPToolsCall(t, cmd, 4, "cron_delete", fmt.Sprintf(`{"id":%q}`, first.ID))
	if want := "orrery canceled job " + first.ID + "; it will not fire again."; isError || deleted != want {
		t.Fatalf("cron_delete = %q (error %t), want %q without error", deleted, isError, want)
	}

	relisted, isError := cronMCPToolsCall(t, cmd, 5, "cron_list", "{}")
	if isError {
		t.Fatalf("cron_list after the delete is a tool error: %q", relisted)
	}
	if strings.Contains(relisted, first.ID) {
		t.Errorf("cron_list after the delete = %q, still names canceled job %s", relisted, first.ID)
	}
	if !strings.Contains(relisted, second.ID) {
		t.Errorf("cron_list after the delete = %q, does not name job %s", relisted, second.ID)
	}

	if code := cmd.finish(); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, cmd.stderr.String())
	}
	left := cronStoreJobs(t, st, cronStoreSession)
	if len(left) != 1 || left[0].ID != second.ID {
		t.Fatalf("session holds %v after the cancel, want only job %s", left, second.ID)
	}
}

// TestCronOutsideMCPToolsDeleteOtherSessionRefused runs cron_delete through `orrery mcp` for a job that session-9
// stores. The call is a tool error whose text is the refusal and names the job, and the job stays stored.
func TestCronOutsideMCPToolsDeleteOtherSessionRefused(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	other := cronMCPToolsJob("cron-00000009", "session-9")
	cronMCPToolsStore(t, st, other)

	cmd := cronMCPToolsStart(t, socket)
	cronMCPToolsHandshake(t, cmd)
	text, isError := cronMCPToolsCall(t, cmd, 2, "cron_delete", fmt.Sprintf(`{"id":%q}`, other.ID))
	if !isError {
		t.Fatalf("cron_delete = %q, want a tool error", text)
	}
	if !strings.HasPrefix(text, "orrery refused this call") || !strings.Contains(text, other.ID) {
		t.Fatalf("cron_delete = %q, want a refusal naming job %s", text, other.ID)
	}
	if code := cmd.finish(); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, cmd.stderr.String())
	}
	kept := cronStoreJobs(t, st, "session-9")
	if len(kept) != 1 || kept[0].ID != other.ID {
		t.Fatalf("session-9 holds %v after the refused call, want only job %s", kept, other.ID)
	}
}
