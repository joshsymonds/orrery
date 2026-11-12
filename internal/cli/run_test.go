package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
)

const (
	// runHelperEnv picks the helper TestRunHelperProcess runs when the test binary re-runs as a helper process.
	runHelperEnv = "ORRERY_RUN_TEST_HELPER"
	// runSocketEnv names the socket the client helper acquires its grant from.
	runSocketEnv = "ORRERY_RUN_TEST_SOCKET"
	// runTimeout bounds every wait on a process or on the fake server.
	runTimeout = 10 * time.Second
	// pollInterval is how often a wait checks whether a process has exited.
	pollInterval = 10 * time.Millisecond
	// helperExitStatus is the status the exit helper ends with.
	helperExitStatus = 3
	// killedStatus is the status of a process ended by SIGKILL: 128 plus the signal.
	killedStatus = 128 + int(syscall.SIGKILL)
	// stoppedStatus is the status of a process ended by SIGTERM: 128 plus the signal.
	stoppedStatus = 128 + int(syscall.SIGTERM)
	// usageStatus, notFoundStatus and cannotRunStatus are the statuses of a command that never ran.
	usageStatus     = 2
	notFoundStatus  = 127
	cannotRunStatus = 126
)

// TestRunHelperProcess is not a test. Tests re-run the test binary with runHelperEnv set to run one of these helpers
// as the command or as the client. Without the variable it does nothing.
func TestRunHelperProcess(_ *testing.T) {
	mode := os.Getenv(runHelperEnv)
	if mode == "" {
		return
	}
	os.Exit(runHelper(mode))
}

// runHelper runs the helper mode and returns the status it exits with.
func runHelper(mode string) int {
	switch mode {
	case "env":
		return helperEnv()
	case "exit":
		return helperExitStatus
	case "kill":
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		return 0
	case "sleep":
		fmt.Fprintf(os.Stdout, "ready %d\n", os.Getpid())
		time.Sleep(time.Hour)
		return 0
	case "client":
		return helperClient()
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		return 1
	}
}

// helperEnv prints each variable the helper runs with, then its process group and its process id.
func helperEnv() int {
	for _, entry := range os.Environ() {
		fmt.Fprintln(os.Stdout, entry)
	}
	pgid, err := syscall.Getpgid(0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "pgid %d pid %d\n", pgid, os.Getpid())
	return 0
}

// helperClient is the client the kill test runs in a subprocess. It acquires from the socket in runSocketEnv and runs
// the sleep helper under the grant.
func helperClient() int {
	argv, err := helperArgv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return cli.RunWithGrant(context.Background(), cli.RunConfig{
		Pools:   enginev1connect.NewPoolsClient(cli.SessionHTTPClient(os.Getenv(runSocketEnv)), cli.SessionBaseURL),
		Request: testRequest(),
		Argv:    argv,
		Environ: []string{runHelperEnv + "=sleep"},
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	})
}

// helperArgv returns the argv that runs the test binary as TestRunHelperProcess.
func helperArgv() ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find the test binary: %w", err)
	}
	return []string{exe, "-test.run=^TestRunHelperProcess$"}, nil
}

// poolsFake is an in-process Pools with one scripted Acquire. It records each request, sends its events in order, then
// returns fail if it is set, or otherwise returns, or when hold is set keeps the stream open until the client closes it.
type poolsFake struct {
	fail   error
	events []*v1.AcquireResponse
	hold   bool
	held   chan struct{} // closed once the stream is held open
	ended  chan struct{} // closed when Acquire returns

	mu       sync.Mutex
	requests []*v1.AcquireRequest
}

// newPoolsFake returns a fake that sends events in order, then behaves as described on poolsFake.
func newPoolsFake(fail error, hold bool, events ...*v1.AcquireResponse) *poolsFake {
	return &poolsFake{
		fail:   fail,
		events: events,
		hold:   hold,
		held:   make(chan struct{}),
		ended:  make(chan struct{}),
	}
}

// received returns the requests received so far.
func (f *poolsFake) received() []*v1.AcquireRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*v1.AcquireRequest(nil), f.requests...)
}

// Acquire implements enginev1connect.PoolsHandler.
func (f *poolsFake) Acquire(
	ctx context.Context,
	req *connect.Request[v1.AcquireRequest],
	stream *connect.ServerStream[v1.AcquireResponse],
) error {
	defer close(f.ended)
	f.mu.Lock()
	f.requests = append(f.requests, req.Msg)
	f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	for _, event := range f.events {
		if err := stream.Send(event); err != nil {
			return err
		}
	}
	if !f.hold {
		return nil
	}
	close(f.held)
	<-ctx.Done()
	return nil
}

// runServe serves fake as Pools on a unix socket at the session socket path beside a daemon socket in a short temp
// directory. It returns that session socket path. The server is closed at cleanup.
func runServe(t *testing.T, fake *poolsFake) string {
	t.Helper()
	socket := protocol.SessionSocketPath(filepath.Join(t.TempDir(), "orreryd.sock"))
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", socket, err)
	}
	mux := http.NewServeMux()
	path, handler := enginev1connect.NewPoolsHandler(fake)
	mux.Handle(path, handler)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close pools server: %v", err)
		}
	})
	return socket
}

// testRequest is the request every run test asks for: one member of the builds pool.
func testRequest() *v1.AcquireRequest {
	return &v1.AcquireRequest{
		Project: "/work/orrery",
		Needs:   []*v1.PoolNeed{{Pool: "builds", Count: 1}},
	}
}

// runConfig returns the RunConfig that asks testRequest of the Pools at socket and runs the helper in mode, with the
// given stdout and stderr.
func runConfig(t *testing.T, socket, mode string, stdout, stderr io.Writer) cli.RunConfig {
	t.Helper()
	argv, err := helperArgv()
	if err != nil {
		t.Fatal(err)
	}
	return cli.RunConfig{
		Pools:   enginev1connect.NewPoolsClient(cli.SessionHTTPClient(socket), cli.SessionBaseURL),
		Request: testRequest(),
		Argv:    argv,
		Environ: []string{runHelperEnv + "=" + mode, "KEEP=1"},
		Stdout:  stdout,
		Stderr:  stderr,
	}
}

// queuedAt returns a queued event at position.
func queuedAt(position int32) *v1.AcquireResponse {
	return &v1.AcquireResponse{Event: &v1.AcquireResponse_Queued{Queued: &v1.AcquireQueued{Position: position}}}
}

// grantedWith returns a granted event with grantID and the members it names.
func grantedWith(grantID string, pools ...*v1.PoolMembers) *v1.AcquireResponse {
	return &v1.AcquireResponse{Event: &v1.AcquireResponse_Granted{
		Granted: &v1.AcquireGranted{GrantId: grantID, Pools: pools},
	}}
}

// poolMembers names the members granted from pool.
func poolMembers(pool string, names ...string) *v1.PoolMembers {
	return &v1.PoolMembers{Pool: pool, Members: names}
}

// runAsync runs RunWithGrant in its own goroutine and returns the channel its status arrives on.
func runAsync(ctx context.Context, cfg cli.RunConfig) <-chan int {
	status := make(chan int, 1)
	go func() { status <- cli.RunWithGrant(ctx, cfg) }()
	return status
}

// awaitStatus returns the status from status. It fails the test when none arrives within runTimeout.
func awaitStatus(t *testing.T, status <-chan int) int {
	t.Helper()
	select {
	case got := <-status:
		return got
	case <-time.After(runTimeout):
		t.Fatal("RunWithGrant did not return")
		return 0
	}
}

// waitClosed fails the test unless ch closes within runTimeout. what says what did not happen.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(runTimeout):
		t.Fatal(what)
	}
}

// stdoutPipe returns a pipe for a command's stdout: the command writes to w and the test reads r.
func stdoutPipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	return r, w
}

// awaitReady returns the first line r reads, which a helper prints once it is running. It fails the test when no line
// comes within runTimeout.
func awaitReady(t *testing.T, r io.Reader) string {
	t.Helper()
	line := make(chan string, 1)
	go func() {
		text, _ := bufio.NewReader(r).ReadString('\n')
		line <- text
	}()
	select {
	case text := <-line:
		return text
	case <-time.After(runTimeout):
		t.Fatal("no ready line from the helper")
		return ""
	}
}

// readyPID returns the process id on the ready line a sleep helper prints.
func readyPID(t *testing.T, ready string) int {
	t.Helper()
	var pid int
	if _, err := fmt.Sscanf(ready, "ready %d", &pid); err != nil {
		t.Fatalf("ready line %q: %v", ready, err)
	}
	return pid
}

// waitGone fails the test unless the process pid exits within runTimeout.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.After(runTimeout)
	for !processExited(t, pid) {
		select {
		case <-deadline:
			t.Fatalf("process %d is still running", pid)
		case <-time.After(pollInterval):
		}
	}
}

// processExited reports whether pid has exited: its /proc entry is gone, or it is a zombie that nothing reaps.
func processExited(t *testing.T, pid int) bool {
	t.Helper()
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return true
	}
	if err != nil {
		t.Fatalf("read process %d: %v", pid, err)
	}
	// The state follows the command name, which is in parentheses and may hold spaces.
	fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	return fields[0] == "Z" || fields[0] == "X"
}

func TestRunWithGrantGivesCommandItsGrant(t *testing.T) {
	fake := newPoolsFake(nil, true, grantedWith("grant-7",
		poolMembers("builds", "m1", "m2"),
		poolMembers("gpu", "g1"),
	))
	socket := runServe(t, fake)
	var stdout bytes.Buffer
	status := awaitStatus(t, runAsync(context.Background(), runConfig(t, socket, "env", &stdout, io.Discard)))
	if status != 0 {
		t.Fatalf("RunWithGrant = %d, want 0", status)
	}

	lines := strings.Split(stdout.String(), "\n")
	for _, want := range []string{
		"ORRERY_GRANT=grant-7",
		"ORRERY_POOL_BUILDS=m1,m2",
		"ORRERY_POOL_GPU=g1",
		"KEEP=1",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("command environment lacks %q", want)
		}
	}

	out := stdout.String()
	start := strings.Index(out, "pgid ")
	if start < 0 {
		t.Fatalf("command printed no process group: %q", out)
	}
	var pgid, pid int
	if _, err := fmt.Sscanf(out[start:], "pgid %d pid %d", &pgid, &pid); err != nil {
		t.Fatalf("parse process group line: %v", err)
	}
	if pgid != pid {
		t.Errorf("command process group = %d, want its own group %d", pgid, pid)
	}

	got := fake.received()
	if len(got) != 1 || !proto.Equal(got[0], testRequest()) {
		t.Errorf("Acquire requests = %v, want the one request the run was given", got)
	}
}

func TestRunWithGrantReturnsCommandStatus(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want int
	}{
		{name: "exit zero", mode: "env", want: 0},
		{name: "exit status", mode: "exit", want: helperExitStatus},
		{name: "killed by a signal", mode: "kill", want: killedStatus},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := newPoolsFake(nil, true, grantedWith("grant-status", poolMembers("builds", "m1")))
			socket := runServe(t, fake)
			cfg := runConfig(t, socket, tc.mode, io.Discard, io.Discard)
			status := awaitStatus(t, runAsync(context.Background(), cfg))
			if status != tc.want {
				t.Errorf("RunWithGrant = %d, want %d", status, tc.want)
			}
			waitClosed(t, fake.ended, "the stream stayed open after the command exited")
		})
	}
}

func TestRunWithGrantWritesQueuedPositions(t *testing.T) {
	fake := newPoolsFake(nil, true, queuedAt(2), queuedAt(1), grantedWith("grant-queue", poolMembers("builds", "m1")))
	socket := runServe(t, fake)
	var stderr bytes.Buffer
	status := awaitStatus(t, runAsync(context.Background(), runConfig(t, socket, "env", io.Discard, &stderr)))
	if status != 0 {
		t.Fatalf("RunWithGrant = %d, want 0", status)
	}
	first := strings.Index(stderr.String(), "position 2")
	second := strings.Index(stderr.String(), "position 1")
	if first < 0 || second < first {
		t.Errorf("stderr = %q, want position 2 written before position 1", stderr.String())
	}
}

func TestRunWithGrantExitsNoGrant(t *testing.T) {
	tests := []struct {
		name       string
		serve      bool
		fail       error
		events     []*v1.AcquireResponse
		wantStderr string
	}{
		{
			name:       "acquire is refused",
			serve:      true,
			fail:       connect.NewError(connect.CodeFailedPrecondition, errors.New("pool is owned by another host")),
			wantStderr: "pool is owned by another host",
		},
		{
			name:       "stream ends before a grant",
			serve:      true,
			events:     []*v1.AcquireResponse{queuedAt(1)},
			wantStderr: "the stream ended",
		},
		{
			name:       "orreryd is not listening",
			serve:      false,
			wantStderr: "orrery run: no grant:",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "orreryd.sock")
			if tc.serve {
				socket = runServe(t, newPoolsFake(tc.fail, false, tc.events...))
			}
			var stdout, stderr bytes.Buffer
			status := awaitStatus(t, runAsync(context.Background(), runConfig(t, socket, "env", &stdout, &stderr)))
			if status != cli.ExitNoGrant {
				t.Errorf("RunWithGrant = %d, want ExitNoGrant %d", status, cli.ExitNoGrant)
			}
			if stdout.Len() != 0 {
				t.Errorf("command ran without a grant: stdout %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}

func TestRunWithGrantExitsNoGrantWhenContextEndsInQueue(t *testing.T) {
	fake := newPoolsFake(nil, true, queuedAt(1))
	socket := runServe(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-fake.held:
			cancel()
		case <-ctx.Done():
		}
	}()
	var stdout, stderr bytes.Buffer
	status := awaitStatus(t, runAsync(ctx, runConfig(t, socket, "env", &stdout, &stderr)))
	if status != cli.ExitNoGrant {
		t.Errorf("RunWithGrant = %d, want ExitNoGrant %d", status, cli.ExitNoGrant)
	}
	if stdout.Len() != 0 {
		t.Errorf("command ran without a grant: stdout %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "canceled") {
		t.Errorf("stderr = %q, want it to say the context was canceled", stderr.String())
	}
}

func TestRunWithGrantExitsBeforeAcquiring(t *testing.T) {
	tests := []struct {
		name       string
		argv       []string
		want       int
		wantStderr string
	}{
		{name: "no command", argv: nil, want: usageStatus, wantStderr: "no command"},
		{
			name:       "command not found",
			argv:       []string{"orrery-no-such-command"},
			want:       notFoundStatus,
			wantStderr: "orrery-no-such-command",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := newPoolsFake(nil, false, grantedWith("grant-never", poolMembers("builds", "m1")))
			socket := runServe(t, fake)
			var stdout, stderr bytes.Buffer
			cfg := runConfig(t, socket, "env", &stdout, &stderr)
			cfg.Argv = tc.argv
			status := awaitStatus(t, runAsync(context.Background(), cfg))
			if status != tc.want {
				t.Errorf("RunWithGrant = %d, want %d", status, tc.want)
			}
			if n := len(fake.received()); n != 0 {
				t.Errorf("Acquire called %d times, want 0", n)
			}
			if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}

func TestRunWithGrantExitsCannotRunWhenCommandFailsToStart(t *testing.T) {
	fake := newPoolsFake(nil, true, grantedWith("grant-bad", poolMembers("builds", "m1")))
	socket := runServe(t, fake)
	notProgram := filepath.Join(t.TempDir(), "notprogram")
	if err := os.WriteFile(notProgram, []byte("not a program\n"), 0o700); err != nil {
		t.Fatalf("write %s: %v", notProgram, err)
	}
	var stderr bytes.Buffer
	cfg := runConfig(t, socket, "env", io.Discard, &stderr)
	cfg.Argv = []string{notProgram}
	status := awaitStatus(t, runAsync(context.Background(), cfg))
	if status != cannotRunStatus {
		t.Errorf("RunWithGrant = %d, want %d", status, cannotRunStatus)
	}
	if !strings.Contains(stderr.String(), "start") {
		t.Errorf("stderr = %q, want it to say the command did not start", stderr.String())
	}
	waitClosed(t, fake.ended, "the stream stayed open after the command failed to start")
}

func TestRunWithGrantStopsCommandWhenContextEnds(t *testing.T) {
	fake := newPoolsFake(nil, true, grantedWith("grant-stop", poolMembers("builds", "m1")))
	socket := runServe(t, fake)
	ready, w := stdoutPipe(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := runAsync(ctx, runConfig(t, socket, "sleep", w, io.Discard))
	awaitReady(t, ready)
	cancel()
	if got := awaitStatus(t, status); got != stoppedStatus {
		t.Errorf("RunWithGrant = %d, want the command's SIGTERM status %d", got, stoppedStatus)
	}
	waitClosed(t, fake.ended, "the stream stayed open after the command was stopped")
}

func TestRunWithGrantStopsCommandWhenGrantEnds(t *testing.T) {
	fake := newPoolsFake(nil, false, grantedWith("grant-ended", poolMembers("builds", "m1")))
	socket := runServe(t, fake)
	var stderr bytes.Buffer
	status := awaitStatus(t, runAsync(context.Background(), runConfig(t, socket, "sleep", io.Discard, &stderr)))
	if status != cli.ExitNoGrant {
		t.Errorf("RunWithGrant = %d, want ExitNoGrant %d", status, cli.ExitNoGrant)
	}
	if !strings.Contains(stderr.String(), "the grant ended while CMD ran") {
		t.Errorf("stderr = %q, want it to say the grant ended while the command ran", stderr.String())
	}
}

func TestRunWithGrantDiesWithItsClient(t *testing.T) {
	fake := newPoolsFake(nil, true, grantedWith("grant-kill", poolMembers("builds", "m1")))
	socket := runServe(t, fake)
	argv, err := helperArgv()
	if err != nil {
		t.Fatal(err)
	}
	ready, w := stdoutPipe(t)
	client := exec.Command(argv[0], argv[1:]...)
	client.Env = append(os.Environ(), runHelperEnv+"=client", runSocketEnv+"="+socket)
	client.Stdout = w
	client.Stderr = os.Stderr
	if err := client.Start(); err != nil {
		t.Fatalf("start client: %v", err)
	}
	t.Cleanup(func() { _ = client.Process.Kill() })

	pid := readyPID(t, awaitReady(t, ready))
	if err := client.Process.Kill(); err != nil {
		t.Fatalf("kill client: %v", err)
	}
	_ = client.Wait()

	waitClosed(t, fake.ended, "the server saw no end of the stream after its client was killed")
	waitGone(t, pid)
}
