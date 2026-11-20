package capture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// runNoTimeout is the Timeout of a command whose test does not end it by its timeout. It is far longer than the
	// test binary's timeout, so it never fires during a test, however slowly the machine runs the script.
	runNoTimeout = 24 * time.Hour
	// runShortTimeout is the Timeout of a command whose test ends it by its timeout. The tests fire it on a fake
	// clock, so its length only names the timer.
	runShortTimeout = 2 * time.Second
)

const (
	runInitReply = `{"type":"control_response","response":{"subtype":"success","request_id":"req_init"}}`
	runInitLine  = `{"type":"control_request","request_id":"req_init","request":{"subtype":"initialize",` +
		`"supportedDialogKinds":null,"hooks":{},"systemPrompt":[]}}` + "\n"
	runResultLine  = `{"type":"result","subtype":"success"}`
	runTrailerLine = `{"type":"system","subtype":"trailer"}`
	runPromptOne   = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"ONE"}]},` +
		`"uuid":"00000000-0000-4000-8000-000000000001"}` + "\n"
	runPromptTwo = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"TWO"}]},` +
		`"uuid":"00000000-0000-4000-8000-000000000002"}` + "\n"
	runInterruptLine = `{"type":"control_request","request_id":"req_interrupt","request":{"subtype":"interrupt"}}` +
		"\n"
	runWriteRequest = `{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool",` +
		`"tool_name":"Write","input":{"file_path":"/home/user/note.txt","content":"ok"},"tool_use_id":"toolu_1"}}` +
		"\n"
	runQuestionRequest = `{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool",` +
		`"tool_name":"AskUserQuestion","input":{"questions":[{"question":"Which letter do you prefer?",` +
		`"header":"Letter","multiSelect":false,"options":[{"label":"A","description":"first"},` +
		`{"label":"B","description":"second"}]}]},"tool_use_id":"toolu_1"}}` + "\n"
	runAllowReply = `{"type":"control_response","response":{"subtype":"success","request_id":"req_1",` +
		`"response":{"behavior":"allow","updatedInput":{"file_path":"/home/user/note.txt","content":"ok"}}}}` + "\n"
	runDenyReply = `{"type":"control_response","response":{"subtype":"success","request_id":"req_1",` +
		`"response":{"behavior":"deny","message":"The probe denies this tool call."}}}` + "\n"
	runQuestionReply = `{"type":"control_response","response":{"subtype":"success","request_id":"req_1",` +
		`"response":{"behavior":"allow","updatedInput":{"answers":{"Which letter do you prefer?":"A"},` +
		`"questions":[{"question":"Which letter do you prefer?","header":"Letter","multiSelect":false,` +
		`"options":[{"label":"A","description":"first"},{"label":"B","description":"second"}]}]}}}}` + "\n"
	runModeReply = `{"type":"control_response","response":{"subtype":"success","request_id":"req_mode"}}`
	runModeLine  = `{"type":"control_request","request_id":"req_mode","request":{"subtype":"set_permission_mode",` +
		`"mode":"acceptEdits"}}` + "\n"
	runMalformedEnvelope = `{"type":"control_request","request_id":5,"request":{"subtype":"made_up"}}` + "\n"
	runMalformedRequest  = `{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool",` +
		`"tool_name":5,"input":{},"tool_use_id":"toolu_1"}}` + "\n"
	runNoQuestionsRequest = `{"type":"control_request","request_id":"req_1","request":{"subtype":"can_use_tool",` +
		`"tool_name":"AskUserQuestion","input":{"questions":[]},"tool_use_id":"toolu_1"}}` + "\n"
)

// runModeScript answers the initialize and set_permission_mode requests, prints one result for each user
// line, and prints a trailer after stdin closes.
const runModeScript = `while IFS= read -r line; do
  case "$line" in
    *'"subtype":"initialize"'*) printf '%s\n' '` + runInitReply + `' ;;
    *'"subtype":"set_permission_mode"'*) printf '%s\n' '` + runModeReply + `' ;;
    *'"type":"user"'*) printf '%s\n' '` + runResultLine + `' ;;
  esac
done
printf '%s\n' '` + runTrailerLine + `'
`

const (
	runUserResult  = `      printf '%s\n' '` + runResultLine + `'`
	runUserRequest = `      cat "$1"
` + runUserResult
	runUserAnswer = `      cat "$1"
      IFS= read -r answer
` + runUserResult
	runUserInterrupt = `      printf '%s\n' '{"type":"stream_event","event":{"type":"message_start"}}'
      printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta"}}'
      IFS= read -r interrupt
      printf '%s\n' '{"type":"control_response","response":{"subtype":"success","request_id":"req_interrupt"}}'
` + runUserResult
)

// runSilentScript records its pid in the file named by $1 and then never reads or writes.
const runSilentScript = `printf '%s\n' "$$" > "$1"
while :; do sleep 0.1; done
`

// runScript returns a fake probe. It answers the initialize request, runs user for each user line, and
// prints a trailer after stdin closes, so a test can see whether lines after the last result arrive.
func runScript(user string) string {
	return `while IFS= read -r line; do
  case "$line" in
    *'"subtype":"initialize"'*) printf '%s\n' '` + runInitReply + `' ;;
    *'"type":"user"'*)
` + user + `
      ;;
  esac
done
printf '%s\n' '` + runTrailerLine + `'
`
}

// runPIDScript is runScript that also records its pid in the file named by $2, so a test can check that
// the process is gone once Run returns. The request file is $1, as with runUserRequest.
func runPIDScript(user string) string {
	return `printf '%s\n' "$$" > "$2"` + "\n" + runScript(user)
}

// runCommand returns a command that runs script with sh, followed by args, with Timeout runNoTimeout. The
// script is written to a fresh directory for the test.
func runCommand(t *testing.T, script string, args ...string) Command {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "child.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	return Command{
		Path:    shell,
		Args:    append([]string{scriptPath}, args...),
		Dir:     dir,
		Timeout: runNoTimeout,
	}
}

// runWriteFile writes content to request.json in a fresh directory and returns its path.
func runWriteFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write request.json: %v", err)
	}
	return path
}

// runExecute runs the probe and fails the test on a run error.
func runExecute(t *testing.T, probe Probe, cmd Command, sinks Sinks) Outcome {
	t.Helper()
	outcome, err := Run(t.Context(), probe, cmd, sinks)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return outcome
}

// runRequireCompleted fails the test unless every prompt got its result and the process exited cleanly.
func runRequireCompleted(t *testing.T, outcome Outcome) {
	t.Helper()
	if !outcome.Completed || outcome.TimedOut || outcome.ExitErr != nil {
		t.Fatalf("outcome = %+v, want completed without timeout or exit error", outcome)
	}
}

// runCompare fails the test when got differs from want.
func runCompare(t *testing.T, sink, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s sink:\n got: %q\nwant: %q", sink, got, want)
	}
}

// runWaitGone waits until pid no longer exists. A pidfd for the process becomes readable when the process
// exits, so the wait blocks in the kernel until then. It has no deadline of its own.
func runWaitGone(t *testing.T, pid int) {
	t.Helper()
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return
	}
	if err != nil {
		t.Fatalf("open pidfd of %d: %v", pid, err)
	}
	defer func() {
		if err := unix.Close(fd); err != nil {
			t.Errorf("close pidfd of %d: %v", pid, err)
		}
	}()
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		_, err := unix.Poll(fds, -1)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			t.Fatalf("wait on pidfd of %d: %v", pid, err)
		}
		return
	}
}

// runWatchDir starts an inotify watch on dir for files that are closed after a write or moved into dir, the
// two ways a fake probe publishes a file there. The watch is closed when the test ends.
func runWatchDir(t *testing.T, dir string) int {
	t.Helper()
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		t.Fatalf("inotify_init1: %v", err)
	}
	t.Cleanup(func() {
		if err := unix.Close(fd); err != nil {
			t.Errorf("close inotify fd: %v", err)
		}
	})
	if _, err := unix.InotifyAddWatch(fd, dir, unix.IN_CLOSE_WRITE|unix.IN_MOVED_TO); err != nil {
		t.Fatalf("watch %s: %v", dir, err)
	}
	return fd
}

// runAwaitDirChange blocks until the watch on fd reports a file closed after a write or moved into its
// directory. It has no deadline of its own.
func runAwaitDirChange(t *testing.T, fd int) {
	t.Helper()
	var buf [4096]byte
	for {
		_, err := unix.Read(fd, buf[:])
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			t.Fatalf("read inotify events: %v", err)
		}
		return
	}
}

// runReadPID returns the pid that a fake probe wrote into path.
func runReadPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse pid %q: %v", data, err)
	}
	return pid
}

func TestRunTwoPromptsCopiesBothStreamsExactly(t *testing.T) {
	var in, out bytes.Buffer
	probe := Probe{Name: "two-turns", PermissionMode: "default", Prompts: []string{"ONE", "TWO"}}
	cmd := runCommand(t, runScript(runUserResult))

	outcome := runExecute(t, probe, cmd, Sinks{In: &in, Out: &out})

	runRequireCompleted(t, outcome)
	if len(outcome.Unanswered) != 0 {
		t.Errorf("Unanswered = %q, want none", outcome.Unanswered)
	}
	runCompare(t, "In", in.String(), runInitLine+runPromptOne+runPromptTwo)
	runCompare(t, "Out", out.String(),
		runInitReply+"\n"+runResultLine+"\n"+runResultLine+"\n"+runTrailerLine+"\n")
}

func TestRunResumeNumbersPromptUUIDsFromOwnRange(t *testing.T) {
	var in, out bytes.Buffer
	probe := Probe{Name: "chained", PermissionMode: "default", ResumeOf: "two-turns", Prompts: []string{"ONE", "TWO"}}
	cmd := runCommand(t, runScript(runUserResult))

	outcome := runExecute(t, probe, cmd, Sinks{In: &in, Out: &out})

	runRequireCompleted(t, outcome)
	// The repository's leak rule admits only the ordinary uuid form as a literal, so the resume form is
	// built from its format; it must differ from every uuid an ordinary probe sends.
	if fmt.Sprintf(runResumeUUIDFormat, 1) == fmt.Sprintf(runUUIDFormat, 1) {
		t.Fatalf("resume uuid format %q numbers like the ordinary one", runResumeUUIDFormat)
	}
	prompt := func(text string, n int) string {
		return `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"` + text + `"}]},` +
			`"uuid":"` + fmt.Sprintf(runResumeUUIDFormat, n) + `"}` + "\n"
	}
	runCompare(t, "In", in.String(), runInitLine+prompt("ONE", 1)+prompt("TWO", 2))
}

func TestRunAnswersPermissionRequests(t *testing.T) {
	tests := []struct {
		name    string
		answer  Answer
		request string
		want    string
	}{
		{name: "allow", answer: AnswerAllow, request: runWriteRequest, want: runAllowReply},
		{name: "deny", answer: AnswerDeny, request: runWriteRequest, want: runDenyReply},
		{name: "question", answer: AnswerQuestion, request: runQuestionRequest, want: runQuestionReply},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in, out bytes.Buffer
			probe := Probe{
				Name:           tc.name,
				PermissionMode: "default",
				PromptTool:     true,
				Prompts:        []string{"ONE"},
				Answer:         tc.answer,
			}
			cmd := runCommand(t, runScript(runUserAnswer), runWriteFile(t, tc.request))

			outcome := runExecute(t, probe, cmd, Sinks{In: &in, Out: &out})

			runRequireCompleted(t, outcome)
			runCompare(t, "In", in.String(), runInitLine+runPromptOne+tc.want)
		})
	}
}

func TestRunSendsInterruptOnceAfterFirstStreamEvent(t *testing.T) {
	var in, out bytes.Buffer
	probe := Probe{Name: "interrupt", PermissionMode: "default", Prompts: []string{"ONE"}, Interrupt: true}
	cmd := runCommand(t, runScript(runUserInterrupt))

	outcome := runExecute(t, probe, cmd, Sinks{In: &in, Out: &out})

	runRequireCompleted(t, outcome)
	runCompare(t, "In", in.String(), runInitLine+runPromptOne+runInterruptLine)
}

func TestRunListsUnansweredRequests(t *testing.T) {
	tests := []struct {
		name    string
		request string
		want    []string
	}{
		{
			name:    "unknown subtype",
			request: `{"type":"control_request","request_id":"req_2","request":{"subtype":"made_up"}}` + "\n",
			want:    []string{"made_up"},
		},
		{
			name:    "can_use_tool with no answer",
			request: runWriteRequest,
			want:    []string{"can_use_tool Write"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in, out bytes.Buffer
			probe := Probe{Name: tc.name, PermissionMode: "default", PromptTool: true, Prompts: []string{"ONE"}}
			cmd := runCommand(t, runScript(runUserRequest), runWriteFile(t, tc.request))

			outcome := runExecute(t, probe, cmd, Sinks{In: &in, Out: &out})

			if !outcome.Completed || outcome.TimedOut {
				t.Fatalf("outcome = %+v, want completed without timeout", outcome)
			}
			if !slices.Equal(outcome.Unanswered, tc.want) {
				t.Errorf("Unanswered = %q, want %q", outcome.Unanswered, tc.want)
			}
			runCompare(t, "In", in.String(), runInitLine+runPromptOne)
		})
	}
}

func TestRunListsAnsweredRequests(t *testing.T) {
	tests := []struct {
		name           string
		answer         Answer
		want           []string
		wantUnanswered []string
	}{
		{name: "allowed", answer: AnswerAllow, want: []string{"can_use_tool Write"}},
		{name: "denied", answer: AnswerDeny, want: []string{"can_use_tool Write"}},
		{name: "unanswered", wantUnanswered: []string{"can_use_tool Write"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in, out bytes.Buffer
			probe := Probe{
				Name:           tc.name,
				PermissionMode: "default",
				PromptTool:     true,
				Prompts:        []string{"ONE"},
				Answer:         tc.answer,
			}
			cmd := runCommand(t, runScript(runUserRequest), runWriteFile(t, runWriteRequest))

			outcome := runExecute(t, probe, cmd, Sinks{In: &in, Out: &out})

			runRequireCompleted(t, outcome)
			if !slices.Equal(outcome.Answered, tc.want) {
				t.Errorf("Answered = %q, want %q", outcome.Answered, tc.want)
			}
			if !slices.Equal(outcome.Unanswered, tc.wantUnanswered) {
				t.Errorf("Unanswered = %q, want %q", outcome.Unanswered, tc.wantUnanswered)
			}
		})
	}
}

func TestRunAnsweredEmptyWithoutRequest(t *testing.T) {
	var in, out bytes.Buffer
	probe := Probe{Name: "no-request", PermissionMode: "default", Prompts: []string{"ONE"}}
	cmd := runCommand(t, runScript(runUserResult))

	outcome := runExecute(t, probe, cmd, Sinks{In: &in, Out: &out})

	runRequireCompleted(t, outcome)
	if len(outcome.Answered) != 0 {
		t.Errorf("Answered = %q, want none", outcome.Answered)
	}
}

func TestRunChangesModeBeforePrompt(t *testing.T) {
	var in, out bytes.Buffer
	probe := Probe{Name: "mode", PermissionMode: "default", ModeChange: "acceptEdits", Prompts: []string{"ONE"}}
	cmd := runCommand(t, runModeScript)

	outcome := runExecute(t, probe, cmd, Sinks{In: &in, Out: &out})

	runRequireCompleted(t, outcome)
	runCompare(t, "In", in.String(), runInitLine+runModeLine+runPromptOne)
	runCompare(t, "Out", out.String(),
		runInitReply+"\n"+runModeReply+"\n"+runResultLine+"\n"+runTrailerLine+"\n")
}

func TestRunListsMalformedRequestsAndContinues(t *testing.T) {
	tests := []struct {
		name       string
		answer     Answer
		request    string
		wantPrefix string
	}{
		{
			name:       "undecodable envelope",
			answer:     AnswerAllow,
			request:    runMalformedEnvelope,
			wantPrefix: "undecodable: ",
		},
		{
			name:       "malformed can_use_tool",
			answer:     AnswerAllow,
			request:    runMalformedRequest,
			wantPrefix: "can_use_tool: ",
		},
		{
			name:       "AskUserQuestion without questions",
			answer:     AnswerQuestion,
			request:    runNoQuestionsRequest,
			wantPrefix: "can_use_tool AskUserQuestion: capture: AskUserQuestion input has no questions",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var in, out bytes.Buffer
			pidPath := filepath.Join(t.TempDir(), "pid")
			probe := Probe{
				Name:           tc.name,
				PermissionMode: "default",
				PromptTool:     true,
				Prompts:        []string{"ONE"},
				Answer:         tc.answer,
			}
			cmd := runCommand(t, runPIDScript(runUserRequest), runWriteFile(t, tc.request), pidPath)

			outcome, err := Run(t.Context(), probe, cmd, Sinks{In: &in, Out: &out})

			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			runRequireCompleted(t, outcome)
			if len(outcome.Unanswered) != 1 || !strings.HasPrefix(outcome.Unanswered[0], tc.wantPrefix) {
				t.Errorf("Unanswered = %q, want one entry starting with %q", outcome.Unanswered, tc.wantPrefix)
			}
			runCompare(t, "In", in.String(), runInitLine+runPromptOne)
			runWaitGone(t, runReadPID(t, pidPath))
		})
	}
}

// runAwaitPID returns the pid a fake probe writes into path, once the line is complete. It has no deadline of its
// own.
func runAwaitPID(t *testing.T, path string) int {
	t.Helper()
	fd := runWatchDir(t, filepath.Dir(path))
	for {
		if data, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(data), "\n") {
			return runReadPID(t, path)
		}
		runAwaitDirChange(t, fd)
	}
}

func TestRunReturnsOutcomeBesideError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pidPath := filepath.Join(t.TempDir(), "pid")
	cmd := runCommand(t, `printf '%s\n' diag >&2
printf '%s\n' "$$" > "$1"
while :; do sleep 0.1; done
`, pidPath)

	probe := Probe{Name: "stalled", Prompts: []string{"ONE"}}
	done := startRun(ctx, t, probe, cmd, Sinks{In: io.Discard, Out: io.Discard})
	runAwaitPID(t, pidPath)
	cancel()
	got := <-done
	outcome, err := got.outcome, got.err

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if outcome.Stderr != "diag\n" {
		t.Errorf("Stderr = %q, want %q", outcome.Stderr, "diag\n")
	}
	if outcome.ExitErr == nil {
		t.Errorf("ExitErr = nil, want the exit of the stopped process")
	}
}

func TestRunTimesOutAndStopsProcess(t *testing.T) {
	clock := newRunFakeClock()
	pidPath := filepath.Join(t.TempDir(), "pid")
	cmd := runCommand(t, runSilentScript, pidPath)
	cmd.Timeout = runShortTimeout
	cmd.After = clock.After

	probe := Probe{Name: "silent", Prompts: []string{"ONE"}}
	done := startRun(t.Context(), t, probe, cmd, Sinks{In: io.Discard, Out: io.Discard})
	timeout := clock.next(t, runShortTimeout)
	pid := runAwaitPID(t, pidPath)
	timeout.fire <- time.Time{}

	if outcome := awaitRun(t, done); !outcome.TimedOut || outcome.Completed {
		t.Fatalf("outcome = %+v, want TimedOut and not Completed", outcome)
	}
	runWaitGone(t, pid)
}

// TestRunWithoutAfterTimesOutOnTheRealClock checks that a Command with no After arms its Timeout on time.After.
// The probe never answers, so the timeout firing is the only way Run returns; however late it fires, the outcome
// is the same.
func TestRunWithoutAfterTimesOutOnTheRealClock(t *testing.T) {
	cmd := runCommand(t, runSilentScript, filepath.Join(t.TempDir(), "pid"))
	cmd.Timeout = time.Millisecond

	probe := Probe{Name: "silent", Prompts: []string{"ONE"}}
	outcome := runExecute(t, probe, cmd, Sinks{In: io.Discard, Out: io.Discard})
	if !outcome.TimedOut || outcome.Completed {
		t.Fatalf("outcome = %+v, want TimedOut and not Completed", outcome)
	}
}

// runRequestBeforeReplyScript answers initialize at once. Before it answers the mode change or a user line,
// it sends the permission request in the file named by $1 and reads the answer, so the answer is the last
// line Run may send before that reply. A line read in place of the answer ends the script with status 3.
const runRequestBeforeReplyScript = `await_answer() {
  IFS= read -r answer
  case "$answer" in
    *'"type":"control_response"'*) ;;
    *) printf 'read %s in place of the answer\n' "$answer" >&2; exit 3 ;;
  esac
}
while IFS= read -r line; do
  case "$line" in
    *'"subtype":"initialize"'*) printf '%s\n' '` + runInitReply + `' ;;
    *'"subtype":"set_permission_mode"'*) cat "$1"; await_answer; printf '%s\n' '` + runModeReply + `' ;;
    *'"type":"user"'*) cat "$1"; await_answer; printf '%s\n' '` + runResultLine + `' ;;
  esac
done
`

// runLockedWriter is a sink a test reads while Run writes it from its own goroutine. Each Write leaves a token on
// changed, so a test can block until the sink has changed rather than poll it.
type runLockedWriter struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	changed chan struct{}
}

// Write appends data and signals the change.
func (writer *runLockedWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	n, err := writer.buf.Write(data)
	if err != nil {
		return n, fmt.Errorf("record sink write: %w", err)
	}
	select {
	case writer.changesLocked() <- struct{}{}:
	default:
	}
	return n, nil
}

// changes returns the channel that holds a token after each Write. A waiter that takes a token rechecks the
// sink, which already holds every write made before the token was left.
func (writer *runLockedWriter) changes() <-chan struct{} {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.changesLocked()
}

// changesLocked makes the channel on first use, so a zero runLockedWriter works.
func (writer *runLockedWriter) changesLocked() chan struct{} {
	if writer.changed == nil {
		writer.changed = make(chan struct{}, 1)
	}
	return writer.changed
}

// String returns everything written so far.
func (writer *runLockedWriter) String() string {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.buf.String()
}

// TestRunWaitsForEachReplyBeforeItsNextLine has the script send a permission request before each reply it
// owes, and read the answer. Run writes its lines in order on one goroutine, so a Run that sent its next line
// without waiting for the reply would write that line before the answer.
func TestRunWaitsForEachReplyBeforeItsNextLine(t *testing.T) {
	in := &runLockedWriter{}
	probe := Probe{
		Name: "wait", PermissionMode: "default", ModeChange: "acceptEdits", Answer: AnswerAllow,
		Prompts: []string{"ONE", "TWO"},
	}
	cmd := runCommand(t, runRequestBeforeReplyScript, runWriteFile(t, runWriteRequest))

	runRequireCompleted(t, runExecute(t, probe, cmd, Sinks{In: in, Out: io.Discard}))
	runCompare(t, "In", in.String(),
		runInitLine+runModeLine+runAllowReply+runPromptOne+runAllowReply+runPromptTwo+runAllowReply)
}

// runTimer is one timer a fake clock armed: its length, and the channel the test fires it on.
type runTimer struct {
	length time.Duration
	fire   chan time.Time
}

// runFakeClock arms timers that fire only when the test fires them. Each armed timer is sent on armed, in
// the order Run arms them.
type runFakeClock struct {
	armed chan runTimer
}

func newRunFakeClock() *runFakeClock {
	return &runFakeClock{armed: make(chan runTimer, 4)}
}

// After arms a timer of length d.
func (clock *runFakeClock) After(d time.Duration) <-chan time.Time {
	timer := runTimer{length: d, fire: make(chan time.Time, 1)}
	clock.armed <- timer
	return timer.fire
}

// next returns the next timer Run arms, failing the test unless its length is want.
func (clock *runFakeClock) next(t *testing.T, want time.Duration) runTimer {
	t.Helper()
	timer := <-clock.armed
	if timer.length != want {
		t.Fatalf("armed a timer of %v, want %v", timer.length, want)
	}
	return timer
}

// runOutcome is what one Run returned.
type runOutcome struct {
	outcome Outcome
	err     error
}

// startRun runs probe on cmd under ctx in its own goroutine and returns the channel its result arrives on.
func startRun(ctx context.Context, t *testing.T, probe Probe, cmd Command, sinks Sinks) <-chan runOutcome {
	t.Helper()
	done := make(chan runOutcome, 1)
	go func() {
		outcome, err := Run(ctx, probe, cmd, sinks)
		done <- runOutcome{outcome: outcome, err: err}
	}()
	return done
}

// awaitRun returns the outcome of the Run on done, failing the test if it returned an error.
func awaitRun(t *testing.T, done <-chan runOutcome) Outcome {
	t.Helper()
	got := <-done
	if got.err != nil {
		t.Fatalf("Run: %v", got.err)
	}
	return got.outcome
}

// awaitAnswered waits until the In sink holds the answer to the request the script sends after its result.
// Run answers on the goroutine that ends the prompts, so once the answer is in the sink, a Run that closed
// stdin at the last result instead of at the linger has already done so and failed the answer's write. It
// fails the test if Run returns first.
func awaitAnswered(t *testing.T, done <-chan runOutcome, in *runLockedWriter) {
	t.Helper()
	changed := in.changes()
	for !strings.Contains(in.String(), runAllowReply) {
		select {
		case got := <-done:
			t.Fatalf("Run returned %+v before it answered the request sent during the linger", got)
		case <-changed:
		}
	}
}

// runLinger is the Linger of the linger tests. The fake clock fires it, so its length only names the timer.
const runLinger = 400 * time.Millisecond

// runUserResultThenRequest answers a user line with a result and then sends the permission request in the file
// named by $1, which arrives during the linger.
const runUserResultThenRequest = runUserResult + `
      cat "$1"`

// runUserExtraResult answers a user line with two results and then sends the permission request in the file
// named by $1.
const runUserExtraResult = runUserResult + "\n" + runUserResultThenRequest

// TestRunLingerHoldsStdinOpenAfterLastResult checks that stdin stays open from the last result until the linger
// fires: a request that arrives in between is answered, and the trailer the script writes once stdin closes
// comes after the answer.
func TestRunLingerHoldsStdinOpenAfterLastResult(t *testing.T) {
	clock := newRunFakeClock()
	in, out := &runLockedWriter{}, &runLockedWriter{}
	probe := Probe{
		Name: "linger", PermissionMode: "default", Answer: AnswerAllow, Prompts: []string{"ONE"}, Linger: runLinger,
	}
	cmd := runCommand(t, runScript(runUserResultThenRequest), runWriteFile(t, runWriteRequest))
	cmd.After = clock.After

	done := startRun(t.Context(), t, probe, cmd, Sinks{In: in, Out: out})
	clock.next(t, runNoTimeout)
	linger := clock.next(t, runLinger)
	awaitAnswered(t, done, in)
	linger.fire <- time.Time{}

	outcome := awaitRun(t, done)
	runRequireCompleted(t, outcome)
	runCompare(t, "In", in.String(), runInitLine+runPromptOne+runAllowReply)
	if got := out.String(); !strings.HasSuffix(got, runTrailerLine+"\n") {
		t.Errorf("Out = %q, want the trailer last, written once the linger closed stdin", got)
	}
}

// TestRunLingerCountsExtraResultsAndSendsNothing checks that a second result during the linger sends nothing and
// leaves stdin open: the request after it is answered, and nothing else is sent.
func TestRunLingerCountsExtraResultsAndSendsNothing(t *testing.T) {
	clock := newRunFakeClock()
	in, out := &runLockedWriter{}, &runLockedWriter{}
	probe := Probe{
		Name: "linger-extra", PermissionMode: "default", Answer: AnswerAllow, Prompts: []string{"ONE"},
		Linger: runLinger,
	}
	cmd := runCommand(t, runScript(runUserExtraResult), runWriteFile(t, runWriteRequest))
	cmd.After = clock.After

	done := startRun(t.Context(), t, probe, cmd, Sinks{In: in, Out: out})
	clock.next(t, runNoTimeout)
	linger := clock.next(t, runLinger)
	awaitAnswered(t, done, in)
	linger.fire <- time.Time{}

	runRequireCompleted(t, awaitRun(t, done))
	runCompare(t, "In", in.String(), runInitLine+runPromptOne+runAllowReply)
	if got := out.String(); !strings.HasSuffix(got, runTrailerLine+"\n") {
		t.Errorf("Out = %q, want the trailer last, written once the linger closed stdin", got)
	}
}

func TestRunLingerLongerThanTimeoutStopsProcess(t *testing.T) {
	clock := newRunFakeClock()
	pidPath := filepath.Join(t.TempDir(), "pid")
	cmd := runCommand(t, `printf '%s\n' "$$" > "$1"`+"\n"+runScript(runUserResult), pidPath)
	cmd.Timeout = runShortTimeout
	cmd.After = clock.After
	probe := Probe{
		Name:           "linger-timeout",
		PermissionMode: "default",
		Prompts:        []string{"ONE"},
		Linger:         10 * time.Second,
	}

	done := startRun(t.Context(), t, probe, cmd, Sinks{In: io.Discard, Out: io.Discard})
	timeout := clock.next(t, runShortTimeout)
	clock.next(t, probe.Linger)
	timeout.fire <- time.Time{}

	if outcome := awaitRun(t, done); !outcome.TimedOut {
		t.Fatalf("outcome = %+v, want TimedOut while lingering", outcome)
	}
	runWaitGone(t, runReadPID(t, pidPath))
}

// runEnvRecord writes what the child sees to the file named by $1: the variable set only in the
// command's environment, the variable set only in the test process, and the working directory.
const runEnvRecord = `printf '%s\n' "$ORRERY_TEST_MARK" "${ORRERY_TEST_PARENT-unset}" "$(pwd -P)" > "$1"` + "\n"

func TestRunStartsChildWithCommandEnvAndDir(t *testing.T) {
	t.Setenv("ORRERY_TEST_PARENT", "from-parent")
	seenPath := filepath.Join(t.TempDir(), "seen")
	var in, out bytes.Buffer
	probe := Probe{Name: "env", PermissionMode: "default", Prompts: []string{"ONE"}}
	cmd := runCommand(t, runEnvRecord+runScript(runUserResult), seenPath)
	cmd.Env = []string{"ORRERY_TEST_MARK=from-command"}

	outcome := runExecute(t, probe, cmd, Sinks{In: &in, Out: &out})

	runRequireCompleted(t, outcome)
	seen, err := os.ReadFile(seenPath)
	if err != nil {
		t.Fatalf("read seen file: %v", err)
	}
	wantDir, err := filepath.EvalSymlinks(cmd.Dir)
	if err != nil {
		t.Fatalf("resolve Dir: %v", err)
	}
	want := "from-command\nunset\n" + wantDir + "\n"
	if string(seen) != want {
		t.Errorf("child saw %q, want %q", seen, want)
	}
}
