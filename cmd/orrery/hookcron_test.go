package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// cronCreateInput is the stdin of the PreToolUse hook for a CronCreate call.
const cronCreateInput = `{"hook_event_name":"PreToolUse","tool_name":"CronCreate","tool_use_id":"toolu_01",` +
	`"tool_input":{"cron":"*/5 * * * *","prompt":"check the build"}}`

// cronCalls is the Crons service behind the test session socket. CronCall records the request headers of each call and
// answers with reason. The other methods are not reached by these tests.
type cronCalls struct {
	enginev1connect.UnimplementedCronsHandler
	reason string
	mu     sync.Mutex
	header http.Header
}

// CronCall records the request headers and answers with the handler's reason.
func (c *cronCalls) CronCall(
	_ context.Context, req *connect.Request[v1.CronCallRequest],
) (*connect.Response[v1.CronCallResponse], error) {
	c.mu.Lock()
	c.header = req.Header().Clone()
	c.mu.Unlock()
	return connect.NewResponse(&v1.CronCallResponse{Reason: c.reason}), nil
}

// lastHeader returns the request headers of the most recent CronCall call.
func (c *cronCalls) lastHeader() http.Header {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.header
}

// startCronDaemon serves crons on sessions.sock in a temporary directory, the session socket that `orrery hook cron`
// dials beside the daemon socket. It returns the daemon socket path that `--socket` names.
func startCronDaemon(t *testing.T, crons enginev1connect.CronsHandler) string {
	t.Helper()
	dir := t.TempDir()
	ln, err := enginehost.Listen(filepath.Join(dir, "sessions.sock"))
	if err != nil {
		t.Fatalf("enginehost.Listen: %v", err)
	}
	path, handler := enginev1connect.NewCronsHandler(crons)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close session server: %v", err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve session socket: %v", err)
		}
	})
	return filepath.Join(dir, "orreryd.sock")
}

// hookStdin returns a file that holds input, opened for reading, as the hook's stdin.
func hookStdin(t *testing.T, input string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatalf("write hook input: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open hook input: %v", err)
	}
	t.Cleanup(func() { closeFile(t, f) })
	return f
}

// decodeCronDeny checks that out is exactly one PreToolUse deny decision and returns its reason.
func decodeCronDeny(t *testing.T, out string) string {
	t.Helper()
	var decision struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &decision); err != nil {
		t.Fatalf("stdout %q is not exactly one decision: %v", out, err)
	}
	got := decision.HookSpecificOutput
	if got.HookEventName != "PreToolUse" || got.PermissionDecision != "deny" {
		t.Fatalf("decision = %+v, want a PreToolUse deny", got)
	}
	return got.PermissionDecisionReason
}

// TestCronTakeoverCommandDeniesWithoutDaemon runs `orrery --socket S hook cron` with no daemon listening at S and
// checks that the command exits 0 and denies the call as not scheduled.
func TestCronTakeoverCommandDeniesWithoutDaemon(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "missing", "orreryd.sock")
	getenv := envFrom(map[string]string{protocol.EnvSession: "session-7"})
	var stdout, stderr bytes.Buffer
	args := []string{"--socket", socket, "hook", "cron"}
	if code := run(args, hookStdin(t, cronCreateInput), &stdout, &stderr, getenv); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr.String())
	}
	const prefix = "orrery could not reach orreryd, so the job was not scheduled ("
	const suffix = "). Call CronCreate again."
	reason := decodeCronDeny(t, stdout.String())
	if !strings.HasPrefix(reason, prefix) || !strings.HasSuffix(reason, suffix) {
		t.Fatalf("reason = %q, want it to start %q and end %q", reason, prefix, suffix)
	}
}

// TestCronTakeoverCommandDeniesWhenFlagsFail runs `orrery -L NAME -S PATH hook cron`, whose global flags are refused as
// mutually exclusive, and checks that the command still exits 0 and denies the call as not scheduled.
func TestCronTakeoverCommandDeniesWhenFlagsFail(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	args := []string{"-L", "name", "-S", "/tmp/tmux.sock", "hook", "cron"}
	if code := run(args, hookStdin(t, cronCreateInput), &stdout, &stderr, envFrom(nil)); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr.String())
	}
	const want = "orrery could not reach orreryd, so the job was not scheduled " +
		"(-L and -S are mutually exclusive). Call CronCreate again."
	if got := decodeCronDeny(t, stdout.String()); got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
}

// TestCronTakeoverCommandSendsSessionHeader runs `orrery --socket S hook cron` in a session that orrery started, and
// checks that the Crons service receives the session as Orrery-Session and that the command prints its reason.
func TestCronTakeoverCommandSendsSessionHeader(t *testing.T) {
	t.Parallel()
	crons := &cronCalls{reason: "Scheduled job 4 for */5 * * * *. Call CronList to see it."}
	socket := startCronDaemon(t, crons)
	getenv := envFrom(map[string]string{protocol.EnvSession: "session-7"})
	var stdout, stderr bytes.Buffer
	args := []string{"--socket", socket, "hook", "cron"}
	if code := run(args, hookStdin(t, cronCreateInput), &stdout, &stderr, getenv); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %q", code, stderr.String())
	}
	header := crons.lastHeader()
	if header == nil {
		t.Fatal("crons service received no CronCall call")
	}
	if got := header.Get(protocol.HeaderSession); got != "session-7" {
		t.Fatalf("%s = %q, want %q", protocol.HeaderSession, got, "session-7")
	}
	if got := decodeCronDeny(t, stdout.String()); got != crons.reason {
		t.Fatalf("reason = %q, want orreryd's %q", got, crons.reason)
	}
}
