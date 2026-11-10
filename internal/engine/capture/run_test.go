package capture

import (
	"bytes"
	"context"
	"errors"
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
)

const (
	runTestTimeout  = 10 * time.Second
	runShortTimeout = 2 * time.Second
	runGoneTimeout  = 5 * time.Second
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

// runCommand returns a command that runs script with sh, followed by args. The script is written to a
// fresh directory for the test.
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
		Timeout: runTestTimeout,
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

// runWaitGone fails the test unless pid no longer exists within runGoneTimeout.
func runWaitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(runGoneTimeout)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still exists after %v: %v", pid, runGoneTimeout, err)
		}
		time.Sleep(10 * time.Millisecond)
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

func TestRunReturnsOutcomeBesideError(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), runShortTimeout)
	defer cancel()
	cmd := runCommand(t, `printf '%s\n' diag >&2
while :; do sleep 0.1; done
`)

	outcome, err := Run(ctx, Probe{Name: "stalled", Prompts: []string{"ONE"}}, cmd,
		Sinks{In: io.Discard, Out: io.Discard})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want context.DeadlineExceeded", err)
	}
	if outcome.Stderr != "diag\n" {
		t.Errorf("Stderr = %q, want %q", outcome.Stderr, "diag\n")
	}
	if outcome.ExitErr == nil {
		t.Errorf("ExitErr = nil, want the exit of the stopped process")
	}
}

func TestRunTimesOutAndStopsProcess(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "pid")
	cmd := runCommand(t, runSilentScript, pidPath)
	cmd.Timeout = runShortTimeout

	outcome, err := Run(t.Context(), Probe{Name: "silent", Prompts: []string{"ONE"}}, cmd,
		Sinks{In: io.Discard, Out: io.Discard})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !outcome.TimedOut || outcome.Completed {
		t.Fatalf("outcome = %+v, want TimedOut and not Completed", outcome)
	}
	runWaitGone(t, runReadPID(t, pidPath))
}

// runReplyDelay is how long runDelayedScript holds each reply, and runReplyGap the least time Run must
// then leave between the line it waits on and its next line.
const (
	runReplyDelay = 300 * time.Millisecond
	runReplyGap   = 250 * time.Millisecond
)

// runDelayedScript answers initialize at once, and the mode change and each user line only after
// runReplyDelay, so a Run that sends its next line without waiting writes it early.
const runDelayedScript = `while IFS= read -r line; do
  case "$line" in
    *'"subtype":"initialize"'*) printf '%s\n' '` + runInitReply + `' ;;
    *'"subtype":"set_permission_mode"'*) sleep 0.3; printf '%s\n' '` + runModeReply + `' ;;
    *'"type":"user"'*) sleep 0.3; printf '%s\n' '` + runResultLine + `' ;;
  esac
done
`

// runStampedWriter records when each write arrived.
type runStampedWriter struct {
	mu     sync.Mutex
	writes []runStampedWrite
}

type runStampedWrite struct {
	at   time.Time
	data string
}

// Write records data with the time it arrived.
func (writer *runStampedWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.writes = append(writer.writes, runStampedWrite{at: time.Now(), data: string(data)})
	return len(data), nil
}

// when returns the time of the first write that contains fragment.
func (writer *runStampedWriter) when(t *testing.T, fragment string) time.Time {
	t.Helper()
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for _, write := range writer.writes {
		if strings.Contains(write.data, fragment) {
			return write.at
		}
	}
	t.Fatalf("no write contains %s", fragment)
	return time.Time{}
}

func TestRunWaitsForEachReplyBeforeItsNextLine(t *testing.T) {
	in := &runStampedWriter{}
	probe := Probe{Name: "wait", PermissionMode: "default", ModeChange: "acceptEdits", Prompts: []string{"ONE", "TWO"}}

	outcome := runExecute(t, probe, runCommand(t, runDelayedScript), Sinks{In: in, Out: io.Discard})

	runRequireCompleted(t, outcome)
	for _, step := range []struct{ waitedOn, next string }{
		{waitedOn: `"req_mode"`, next: `"text":"ONE"`},
		{waitedOn: `"text":"ONE"`, next: `"text":"TWO"`},
	} {
		if gap := in.when(t, step.next).Sub(in.when(t, step.waitedOn)); gap < runReplyGap {
			t.Errorf("%s written %v after %s, want at least %v (the reply is held %v)",
				step.next, gap, step.waitedOn, runReplyGap, runReplyDelay)
		}
	}
}

// runLinger is the Linger of the linger tests: long enough to observe, short enough to keep the tests fast.
const runLinger = 400 * time.Millisecond

// runUserExtraResult answers a user line with a result, then a second result 100ms later, inside the linger.
const runUserExtraResult = runUserResult + "\n      sleep 0.1\n" + runUserResult

func TestRunLingerHoldsStdinOpenAfterLastResult(t *testing.T) {
	out := &runStampedWriter{}
	var in bytes.Buffer
	probe := Probe{Name: "linger", PermissionMode: "default", Prompts: []string{"ONE"}, Linger: runLinger}

	outcome := runExecute(t, probe, runCommand(t, runScript(runUserResult)), Sinks{In: &in, Out: out})

	runRequireCompleted(t, outcome)
	if gap := out.when(t, runTrailerLine).Sub(out.when(t, runResultLine)); gap < runLinger {
		t.Errorf("trailer written %v after the result, want at least %v (stdin closes after the linger)",
			gap, runLinger)
	}
}

func TestRunLingerCountsExtraResultsAndSendsNothing(t *testing.T) {
	out := &runStampedWriter{}
	var in bytes.Buffer
	probe := Probe{Name: "linger-extra", PermissionMode: "default", Prompts: []string{"ONE"}, Linger: runLinger}

	outcome := runExecute(t, probe, runCommand(t, runScript(runUserExtraResult)), Sinks{In: &in, Out: out})

	runRequireCompleted(t, outcome)
	runCompare(t, "In", in.String(), runInitLine+runPromptOne)
	if gap := out.when(t, runTrailerLine).Sub(out.when(t, runResultLine)); gap < runLinger {
		t.Errorf("trailer written %v after the first result, want at least %v (the extra result must not close stdin)",
			gap, runLinger)
	}
}

func TestRunLingerLongerThanTimeoutStopsProcess(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "pid")
	cmd := runCommand(t, `printf '%s\n' "$$" > "$1"`+"\n"+runScript(runUserResult), pidPath)
	cmd.Timeout = runShortTimeout
	probe := Probe{
		Name:           "linger-timeout",
		PermissionMode: "default",
		Prompts:        []string{"ONE"},
		Linger:         10 * time.Second,
	}

	outcome, err := Run(t.Context(), probe, cmd, Sinks{In: io.Discard, Out: io.Discard})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !outcome.TimedOut {
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
