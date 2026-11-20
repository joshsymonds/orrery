package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// cronStoreSession is the session ORRERY_SESSION names. The hook stores its jobs under it.
const cronStoreSession = "session-7"

// cronStoreClaudeSession is the session_id the hook input carries. No job goes under it.
const cronStoreClaudeSession = "claude-8f3a"

// The tools the cron hook is called for.
const (
	cronToolCreate = "CronCreate"
	cronToolDelete = "CronDelete"
	cronToolList   = "CronList"
)

// cronRefusalPrefix begins every refusal the cron answers give.
const cronRefusalPrefix = "orrery refused this call, so nothing was scheduled or canceled: "

// cronStoreOffset is the offset, in seconds, of the zone the cron commands read times in: UTC+9, never UTC.
const cronStoreOffset = 9 * 3600

// cronStoreCreateInput is the tool_input of a CronCreate call for a recurring job that fires every five minutes.
const cronStoreCreateInput = `{"cron":"*/5 * * * *","prompt":"check the build"}`

// cronStoreNow is the fixed time the cron commands read as now: 2026-11-18 10:02:30 in a fixed zone that is not UTC.
func cronStoreNow() time.Time {
	return time.Date(2026, time.November, 18, 10, 2, 30, 0, time.FixedZone("JST", cronStoreOffset))
}

// openCronStore opens a store in a temporary directory. The store closes at cleanup.
func openCronStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return st
}

// serveCronAnswers serves the cron answers of st on the session socket beside a daemon socket, and returns the daemon
// socket path that `--socket` names. The server stops at cleanup.
func serveCronAnswers(t *testing.T, st *store.Store) string {
	t.Helper()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveCronAnswersAt(t, st, daemon)
	return daemon
}

// serveCronAnswersAt serves the cron answers of st on the session socket beside daemon, the socket that `--socket`
// names. The server stops at cleanup.
func serveCronAnswersAt(t *testing.T, st *store.Store, daemon string) {
	t.Helper()
	ln, err := enginehost.Listen(protocol.SessionSocketPath(daemon))
	if err != nil {
		t.Fatalf("enginehost.Listen: %v", err)
	}
	answers := enginehost.NewCronAnswers(enginehost.CronAnswersConfig{Store: st, Now: cronStoreNow})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- enginehost.Serve(ctx, ln, nil, nil, nil, nil, nil, nil, nil, answers, nil, nil) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("enginehost.Serve: %v", err)
		}
	})
}

// cronHookInput returns the stdin of the PreToolUse hook for a call of tool with toolInput. Its session_id is
// cronStoreClaudeSession, not the session ORRERY_SESSION names.
func cronHookInput(t *testing.T, tool, toolUseID, toolInput string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      cronStoreClaudeSession,
		"tool_name":       tool,
		"tool_use_id":     toolUseID,
		"tool_input":      json.RawMessage(toolInput),
	})
	if err != nil {
		t.Fatalf("marshal hook input: %v", err)
	}
	return string(raw)
}

// runCronHook runs `orrery --socket socket hook cron` for a call of tool with toolInput, with ORRERY_SESSION set to
// cronStoreSession. It checks that the command exits 0, and returns the reason of its deny decision.
func runCronHook(t *testing.T, socket, tool, toolUseID, toolInput string) string {
	t.Helper()
	getenv := envFrom(map[string]string{protocol.EnvSession: cronStoreSession})
	var stdout, stderr bytes.Buffer
	args := []string{"--socket", socket, "hook", "cron"}
	input := hookStdin(t, cronHookInput(t, tool, toolUseID, toolInput))
	if code := run(args, input, &stdout, &stderr, getenv); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr.String())
	}
	return decodeCronDeny(t, stdout.String())
}

// cronStoreJobs returns the jobs of session in st.
func cronStoreJobs(t *testing.T, st *store.Store, session string) []store.CronJob {
	t.Helper()
	jobs, err := st.CronJobs(t.Context(), session)
	if err != nil {
		t.Fatalf("CronJobs %s: %v", session, err)
	}
	return jobs
}

// TestCronTakeoverCommandCreateStoresJob runs `orrery hook cron` on a CronCreate call and checks that the deny says the
// job is scheduled with its id, cron, prompt and next run, and that the job is stored under ORRERY_SESSION, not under
// the session_id of the hook input.
func TestCronTakeoverCommandCreateStoresJob(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	reason := runCronHook(t, socket, cronToolCreate, "toolu_01", cronStoreCreateInput)
	jobs := cronStoreJobs(t, st, cronStoreSession)
	if len(jobs) != 1 {
		t.Fatalf("session has %d jobs, want 1", len(jobs))
	}
	want := "orrery scheduled this job as " + jobs[0].ID +
		". Cron */5 * * * *, prompt \"check the build\", next run Wed 2026-11-18 10:05 JST."
	if !strings.HasPrefix(reason, want) {
		t.Fatalf("reason = %q, want it to start %q", reason, want)
	}
	if other := cronStoreJobs(t, st, cronStoreClaudeSession); len(other) != 0 {
		t.Fatalf("the hook input's session has %d jobs, want none", len(other))
	}
}

// TestCronTakeoverCommandCreateIsIdempotent runs CronCreate twice with the same tool_use_id, and checks that both answers
// name the one job and that the session keeps one job.
func TestCronTakeoverCommandCreateIsIdempotent(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	first := runCronHook(t, socket, cronToolCreate, "toolu_01", cronStoreCreateInput)
	second := runCronHook(t, socket, cronToolCreate, "toolu_01", cronStoreCreateInput)
	jobs := cronStoreJobs(t, st, cronStoreSession)
	if len(jobs) != 1 {
		t.Fatalf("session has %d jobs, want 1", len(jobs))
	}
	prefix := "orrery scheduled this job as " + jobs[0].ID + "."
	if !strings.HasPrefix(first, prefix) || !strings.HasPrefix(second, prefix) {
		t.Fatalf("answers %q and %q, want both to name job %s", first, second, jobs[0].ID)
	}
}

// TestCronTakeoverCommandDeleteCancels schedules a job, runs CronDelete on its id, and checks that the reason says the
// job is canceled and that the job is gone.
func TestCronTakeoverCommandDeleteCancels(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	runCronHook(t, socket, cronToolCreate, "toolu_01", cronStoreCreateInput)
	jobs := cronStoreJobs(t, st, cronStoreSession)
	if len(jobs) != 1 {
		t.Fatalf("session has %d jobs, want 1", len(jobs))
	}
	id := jobs[0].ID
	reason := runCronHook(t, socket, cronToolDelete, "toolu_02", fmt.Sprintf(`{"id":%q}`, id))
	if want := "orrery canceled job " + id + "; it will not fire again."; reason != want {
		t.Fatalf("reason = %q, want %q", reason, want)
	}
	if left := cronStoreJobs(t, st, cronStoreSession); len(left) != 0 {
		t.Fatalf("session has %d jobs after the cancel, want none", len(left))
	}
}

// TestCronTakeoverCommandListsJobs schedules two jobs for the session and one for another session, runs CronList, and
// checks that the reason lists the session's two job ids and not the other session's.
func TestCronTakeoverCommandListsJobs(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	runCronHook(t, socket, cronToolCreate, "toolu_01", cronStoreCreateInput)
	runCronHook(t, socket, cronToolCreate, "toolu_02", `{"cron":"0 12 * * *","prompt":"lunch","recurring":false}`)
	other := store.CronJob{
		ID: "cron-00000001", Session: "session-9", ToolUseID: "toolu_09", Cron: "*/5 * * * *", Prompt: "other",
		Recurring: true, Created: cronStoreNow(),
	}
	if _, _, err := st.CreateCronJob(t.Context(), other); err != nil {
		t.Fatalf("CreateCronJob: %v", err)
	}
	reason := runCronHook(t, socket, cronToolList, "toolu_03", `{}`)
	if !strings.HasPrefix(reason, "This session's orrery cron jobs:") {
		t.Fatalf("reason = %q, want a list of the session's jobs", reason)
	}
	jobs := cronStoreJobs(t, st, cronStoreSession)
	if len(jobs) != 2 {
		t.Fatalf("session has %d jobs, want 2", len(jobs))
	}
	for _, job := range jobs {
		if !strings.Contains(reason, job.ID) {
			t.Errorf("reason %q does not list job %s", reason, job.ID)
		}
	}
	if strings.Contains(reason, other.ID) {
		t.Errorf("reason %q lists job %s of another session", reason, other.ID)
	}
}

// TestCronTakeoverCommandRefusesInvalidCron runs CronCreate with a cron whose minute is out of range, and checks that the
// refusal names the minute field and that nothing is stored.
func TestCronTakeoverCommandRefusesInvalidCron(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	reason := runCronHook(t, socket, cronToolCreate, "toolu_01", `{"cron":"61 * * * *","prompt":"check"}`)
	if !strings.HasPrefix(reason, cronRefusalPrefix) || !strings.Contains(reason, "minute") {
		t.Fatalf("reason = %q, want a refusal naming the minute field", reason)
	}
	if jobs := cronStoreJobs(t, st, cronStoreSession); len(jobs) != 0 {
		t.Fatalf("refused call stored %d jobs, want none", len(jobs))
	}
}

// TestCronTakeoverCommandRefusesUnknownID runs CronDelete on an id the session does not have, and checks that the refusal
// says the session has no such job.
func TestCronTakeoverCommandRefusesUnknownID(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	socket := serveCronAnswers(t, st)
	reason := runCronHook(t, socket, cronToolDelete, "toolu_01", `{"id":"cron-00000000"}`)
	const want = cronRefusalPrefix + "orrery has no job cron-00000000 in this session, so nothing was canceled. " +
		"CronList lists this session's jobs."
	if reason != want {
		t.Fatalf("reason = %q, want %q", reason, want)
	}
}

// TestCronTakeoverCommandNoDaemonStoresNothing runs CronCreate with no orreryd listening, and checks that the deny says
// the job was not scheduled and that nothing is stored, not even under its tool_use_id. Once a daemon listens, the same
// CronCreate with the same tool_use_id is scheduled.
func TestCronTakeoverCommandNoDaemonStoresNothing(t *testing.T) {
	t.Parallel()
	st := openCronStore(t)
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	reason := runCronHook(t, daemon, cronToolCreate, "toolu_01", cronStoreCreateInput)
	if !strings.Contains(reason, "the job was not scheduled") {
		t.Fatalf("reason = %q, want it to say the job was not scheduled", reason)
	}
	if jobs := cronStoreJobs(t, st, cronStoreSession); len(jobs) != 0 {
		t.Fatalf("session has %d jobs after the refused call, want none", len(jobs))
	}
	if jobs := cronStoreJobs(t, st, cronStoreClaudeSession); len(jobs) != 0 {
		t.Fatalf("the hook input's session has %d jobs after the refused call, want none", len(jobs))
	}
	all, err := st.AllCronJobs(t.Context())
	if err != nil {
		t.Fatalf("AllCronJobs: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("refused call stored %d jobs, want none", len(all))
	}
	serveCronAnswersAt(t, st, daemon)
	reason = runCronHook(t, daemon, cronToolCreate, "toolu_01", cronStoreCreateInput)
	if !strings.HasPrefix(reason, "orrery scheduled this job as ") {
		t.Fatalf("reason = %q, want it to start %q", reason, "orrery scheduled this job as ")
	}
	if jobs := cronStoreJobs(t, st, cronStoreSession); len(jobs) != 1 {
		t.Fatalf("session has %d jobs after the scheduled call, want 1", len(jobs))
	}
}
