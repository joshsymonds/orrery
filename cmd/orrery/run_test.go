package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
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
	// runHelperEnv picks the fake command TestRunHelperProcess runs when the test binary re-runs as that command.
	runHelperEnv = "ORRERY_RUN_TEST_HELPER"
	// runTimeout bounds every wait on a run or on a fake command.
	runTimeout = 10 * time.Second
	// helperExitStatus is the status the exit helper ends with.
	helperExitStatus = 3
	// sigtermStatus is the status of a command ended by SIGTERM: 128 plus the signal.
	sigtermStatus = 128 + int(syscall.SIGTERM)
)

// Pool files the run tests write. Each defines one pool; the tests put them in the project file or the machine file.
const (
	buildsPoolFile = `[pools.builds]
host = "lab"
cap = 2
members = ["builds-1", "builds-2"]
wake = ["wake-builds"]
wake_limit = 5
status = ["status-builds"]
status_limit = 6
admit = ["admit-builds"]
admit_limit = 7
`
	testsPoolFile = `[pools.tests]
host = "lab"
cap = 1
members = ["tests-1"]
wake = ["wake-tests"]
wake_limit = 3
status = ["status-tests"]
status_limit = 4
admit = ["admit-tests"]
admit_limit = 5
`
	badPoolFile = `[pools.builds]
host = "lab"
bogus = 1
`
	hugeCapPoolFile = `[pools.builds]
host = "lab"
cap = 4294967296
members = ["builds-1"]
wake = ["wake-builds"]
wake_limit = 5
status = ["status-builds"]
status_limit = 6
admit = ["admit-builds"]
admit_limit = 7
`
)

// TestRunHelperProcess is not a test. The run tests re-run the test binary with runHelperEnv set to run one of these
// fake commands. Without the variable it does nothing.
func TestRunHelperProcess(_ *testing.T) {
	mode := os.Getenv(runHelperEnv)
	if mode == "" {
		return
	}
	os.Exit(runHelper(mode))
}

// runHelper runs the fake command mode and returns the status it exits with. print writes ran and the ORRERY_
// variables it was given. exit returns helperExitStatus. sleep writes a ready line and runs until it is signaled.
func runHelper(mode string) int {
	switch mode {
	case "print":
		fmt.Fprintln(os.Stdout, "ran")
		for _, entry := range os.Environ() {
			if strings.HasPrefix(entry, "ORRERY_") {
				fmt.Fprintln(os.Stdout, entry)
			}
		}
		return 0
	case "exit":
		return helperExitStatus
	case "sleep":
		fmt.Fprintf(os.Stdout, "ready %d\n", os.Getpid())
		time.Sleep(time.Hour)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		return 1
	}
}

// helperArgv returns the argv that runs the test binary as TestRunHelperProcess in mode. It sets runHelperEnv for the
// rest of the test, since the command inherits the environment.
func helperArgv(t *testing.T, mode string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("find the test binary: %v", err)
	}
	t.Setenv(runHelperEnv, mode)
	return []string{exe, "-test.run=^TestRunHelperProcess$"}
}

// fakePools is an in-process Pools service. Acquire records each request. It refuses with refuse when that is set.
// Otherwise it grants each need its count of members named pool-1 to pool-count, then holds the stream until the
// client ends it.
type fakePools struct {
	enginev1connect.UnimplementedPoolsHandler

	refuse connect.Code
	ended  chan struct{} // closed once an Acquire that granted has returned

	endedOnce sync.Once
	mu        sync.Mutex
	requests  []*v1.AcquireRequest
}

// newFakePools returns a fake that refuses with refuse, or grants when refuse is zero.
func newFakePools(refuse connect.Code) *fakePools {
	return &fakePools{refuse: refuse, ended: make(chan struct{})}
}

// received returns the requests the fake has received.
func (f *fakePools) received() []*v1.AcquireRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*v1.AcquireRequest(nil), f.requests...)
}

// Acquire implements enginev1connect.PoolsHandler.
func (f *fakePools) Acquire(
	ctx context.Context,
	req *connect.Request[v1.AcquireRequest],
	stream *connect.ServerStream[v1.AcquireResponse],
) error {
	f.mu.Lock()
	f.requests = append(f.requests, req.Msg)
	f.mu.Unlock()
	if f.refuse != 0 {
		return connect.NewError(f.refuse, errors.New("fake pools refused the request"))
	}
	granted := &v1.AcquireGranted{GrantId: "grant-1"}
	for _, need := range req.Msg.GetNeeds() {
		members := make([]string, 0, need.GetCount())
		for i := range need.GetCount() {
			members = append(members, fmt.Sprintf("%s-%d", need.GetPool(), i+1))
		}
		granted.Pools = append(granted.Pools, &v1.PoolMembers{Pool: need.GetPool(), Members: members})
	}
	err := stream.Send(&v1.AcquireResponse{Event: &v1.AcquireResponse_Granted{Granted: granted}})
	if err != nil {
		return err
	}
	<-ctx.Done()
	f.endedOnce.Do(func() { close(f.ended) })
	return nil
}

// startPools serves fake as Pools on the session socket beside a daemon socket in a temporary directory. It returns
// the daemon socket path that `--socket` names. The server is closed at cleanup.
func startPools(t *testing.T, fake *fakePools) string {
	t.Helper()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	ln, err := net.Listen("unix", protocol.SessionSocketPath(daemon))
	if err != nil {
		t.Fatalf("listen on the session socket: %v", err)
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
	return daemon
}

// isolate points HOME, XDG_CONFIG_HOME and git's configuration at fresh directories, so a run reads nothing from the
// machine the test runs on. It returns the machine's orrery config directory, where a machine pool file goes.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	config := filepath.Join(home, "config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return config
}

// setupProject isolates the environment and makes a fresh directory the working directory. A non-empty projectText
// is written as the project file and a non-empty machineText as the machine file. It returns the directory with its
// symlinks resolved, as the run's project path is.
func setupProject(t *testing.T, projectText, machineText string) string {
	t.Helper()
	config := isolate(t)
	dir := t.TempDir()
	if projectText != "" {
		writeRunFile(t, filepath.Join(dir, ".orrery", "project.toml"), projectText)
	}
	if machineText != "" {
		writeRunFile(t, filepath.Join(config, "orrery", "pools.toml"), machineText)
	}
	t.Chdir(dir)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// writeRunFile creates path with text, along with any missing parent directories.
func writeRunFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runResult is what one `orrery` command line returned.
type runResult struct {
	code   int
	stderr string
}

// startOrrery runs `orrery args` on its own goroutine with stdin from /dev/null, its stdout written to stdout, and its
// stderr captured. The result arrives on the returned channel.
func startOrrery(t *testing.T, stdout io.Writer, args ...string) <-chan runResult {
	t.Helper()
	out := make(chan runResult, 1)
	go func() {
		stdin, err := os.Open(os.DevNull)
		if err != nil {
			out <- runResult{code: -1, stderr: err.Error()}
			return
		}
		var stderr bytes.Buffer
		code := run(args, stdin, stdout, &stderr, os.Getenv)
		_ = stdin.Close()
		out <- runResult{code: code, stderr: stderr.String()}
	}()
	return out
}

// awaitOrrery returns the result from ch. It fails the test when none arrives within runTimeout.
func awaitOrrery(t *testing.T, ch <-chan runResult) runResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(runTimeout):
		t.Fatal("orrery run did not return")
		return runResult{}
	}
}

// runCommandArgs returns the command line `run` takes for pool needs and a fake command in mode, with the needs in the
// order given.
func runCommandArgs(t *testing.T, mode string, needs ...string) []string {
	t.Helper()
	args := []string{"run"}
	for _, need := range needs {
		args = append(args, "--needs", need)
	}
	args = append(args, "--")
	return append(args, helperArgv(t, mode)...)
}

// buildsDefinition is the definition buildsPoolFile makes, as the request carries it.
func buildsDefinition() *v1.PoolDefinition {
	return &v1.PoolDefinition{
		Name: "builds", Host: "lab", Cap: 2, Members: []string{"builds-1", "builds-2"},
		Wake: []string{"wake-builds"}, WakeLimitSeconds: 5,
		Status: []string{"status-builds"}, StatusLimitSeconds: 6,
		Admit: []string{"admit-builds"}, AdmitLimitSeconds: 7,
	}
}

// testsDefinition is the definition testsPoolFile makes, as the request carries it.
func testsDefinition() *v1.PoolDefinition {
	return &v1.PoolDefinition{
		Name: "tests", Host: "lab", Cap: 1, Members: []string{"tests-1"},
		Wake: []string{"wake-tests"}, WakeLimitSeconds: 3,
		Status: []string{"status-tests"}, StatusLimitSeconds: 4,
		Admit: []string{"admit-tests"}, AdmitLimitSeconds: 5,
	}
}

// TestRunUsageErrorsExitTwo refuses each command line whose run arguments are wrong. Each exits 2 with the usage text,
// names the problem on its first line, and runs nothing.
func TestRunUsageErrorsExitTwo(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no needs", []string{"--", "print"}, "--needs"},
		{"no equals", []string{"--needs", "builds", "--", "print"}, "POOL=N"},
		{"empty pool", []string{"--needs", "=1", "--", "print"}, "POOL=N"},
		{"name not a pool name", []string{"--needs", "Builds=1", "--", "print"}, "POOL=N"},
		{"empty count", []string{"--needs", "builds=", "--", "print"}, "POOL=N"},
		{"count not a number", []string{"--needs", "builds=x", "--", "print"}, "POOL=N"},
		{"count zero", []string{"--needs", "builds=0", "--", "print"}, "POOL=N"},
		{"count negative", []string{"--needs", "builds=-1", "--", "print"}, "POOL=N"},
		{"count overflows", []string{"--needs", "builds=4294967296", "--", "print"}, "POOL=N"},
		{"pool named twice", []string{"--needs", "builds=1", "--needs", "builds=2", "--", "print"}, "twice"},
		{"no dashes", []string{"--needs", "builds=1", "print"}, "-- CMD"},
		{"no command after dashes", []string{"--needs", "builds=1", "--"}, "-- CMD"},
		{"argument before dashes", []string{"stray", "--needs", "builds=1", "--", "print"}, "stray"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupProject(t, buildsPoolFile, "")
			var stdout bytes.Buffer
			res := awaitOrrery(t, startOrrery(t, &stdout, append([]string{"run"}, tc.args...)...))
			if res.code != exitUsage {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}
			first, _, _ := strings.Cut(res.stderr, "\n")
			if !strings.HasPrefix(first, "orrery: run: ") || !strings.Contains(first, tc.want) {
				t.Errorf("first stderr line %q, want run's usage error naming %q", first, tc.want)
			}
			if !strings.HasSuffix(res.stderr, usageText) {
				t.Errorf("stderr does not end with the usage text: %q", res.stderr)
			}
			if strings.Contains(stdout.String(), "ran") {
				t.Errorf("the command ran: stdout %q", stdout.String())
			}
		})
	}
}

// TestRunRefusesBadPoolFiles exits ExitNoGrant, with the reason, when a pool file does not parse or a pool's cap does
// not fit the request. The command does not run.
func TestRunRefusesBadPoolFiles(t *testing.T) {
	cases := []struct {
		name string
		file string
		want string
	}{
		{"unknown key", badPoolFile, "unknown key"},
		{"cap too large", hugeCapPoolFile, "too large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupProject(t, tc.file, "")
			var stdout bytes.Buffer
			res := awaitOrrery(t, startOrrery(t, &stdout, runCommandArgs(t, "print", "builds=1")...))
			if res.code != cli.ExitNoGrant {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, cli.ExitNoGrant, res.stderr)
			}
			if !strings.Contains(res.stderr, tc.want) {
				t.Errorf("stderr %q does not say why the pool file was refused", res.stderr)
			}
			if strings.Contains(stdout.String(), "ran") {
				t.Errorf("the command ran: stdout %q", stdout.String())
			}
		})
	}
}

// TestRunRefusesUndeclaredPool exits ExitNoGrant, naming the pool, when a need names a pool the files do not declare.
// The command does not run.
func TestRunRefusesUndeclaredPool(t *testing.T) {
	setupProject(t, buildsPoolFile, "")
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, runCommandArgs(t, "print", "tests=1")...))
	if res.code != cli.ExitNoGrant {
		t.Errorf("exit status %d, want %d; stderr %q", res.code, cli.ExitNoGrant, res.stderr)
	}
	if !strings.Contains(res.stderr, `"tests"`) || !strings.Contains(res.stderr, "not declared") {
		t.Errorf("stderr %q does not name the undeclared pool", res.stderr)
	}
	if strings.Contains(stdout.String(), "ran") {
		t.Errorf("the command ran: stdout %q", stdout.String())
	}
}

// TestRunSendsProjectPoolsAndNeeds sends the project, the merged pools of both files, and the needs in flag order,
// with this process's id as the client's.
func TestRunSendsProjectPoolsAndNeeds(t *testing.T) {
	dir := setupProject(t, buildsPoolFile, testsPoolFile)
	fake := newFakePools(0)
	daemon := startPools(t, fake)
	var stdout bytes.Buffer
	args := append([]string{"--socket", daemon}, runCommandArgs(t, "print", "tests=1", "builds=2")...)
	res := awaitOrrery(t, startOrrery(t, &stdout, args...))
	if res.code != 0 {
		t.Fatalf("exit status %d, want 0; stderr %q", res.code, res.stderr)
	}
	got := fake.received()
	if len(got) != 1 {
		t.Fatalf("pools received %d requests, want 1", len(got))
	}
	want := &v1.AcquireRequest{
		Project:   dir,
		Pools:     []*v1.PoolDefinition{buildsDefinition(), testsDefinition()},
		Needs:     []*v1.PoolNeed{{Pool: "tests", Count: 1}, {Pool: "builds", Count: 2}},
		ClientPid: int32(os.Getpid()),
		Command:   helperArgv(t, "print"),
	}
	if !proto.Equal(got[0], want) {
		t.Errorf("request\n%v\nwant\n%v", got[0], want)
	}
}

// TestRunSendsCommandArgv sends the argv after -- as the request's command, exactly as given, so status can show what
// the grant runs.
func TestRunSendsCommandArgv(t *testing.T) {
	setupProject(t, buildsPoolFile, "")
	fake := newFakePools(0)
	daemon := startPools(t, fake)
	var stdout bytes.Buffer
	args := append([]string{"--socket", daemon}, runCommandArgs(t, "print", "builds=1")...)
	res := awaitOrrery(t, startOrrery(t, &stdout, args...))
	if res.code != 0 {
		t.Fatalf("exit status %d, want 0; stderr %q", res.code, res.stderr)
	}
	got := fake.received()
	if len(got) != 1 {
		t.Fatalf("pools received %d requests, want 1", len(got))
	}
	want := helperArgv(t, "print")
	if !slices.Equal(got[0].GetCommand(), want) {
		t.Errorf("command %q, want %q", got[0].GetCommand(), want)
	}
}

// TestRunCommandGetsGrant runs the command with the grant's id and each granted pool's members in its environment.
func TestRunCommandGetsGrant(t *testing.T) {
	setupProject(t, buildsPoolFile, testsPoolFile)
	fake := newFakePools(0)
	daemon := startPools(t, fake)
	var stdout bytes.Buffer
	args := append([]string{"--socket", daemon}, runCommandArgs(t, "print", "builds=2", "tests=1")...)
	res := awaitOrrery(t, startOrrery(t, &stdout, args...))
	if res.code != 0 {
		t.Fatalf("exit status %d, want 0; stderr %q", res.code, res.stderr)
	}
	for _, want := range []string{
		"ran",
		"ORRERY_GRANT=grant-1",
		"ORRERY_POOL_BUILDS=builds-1,builds-2",
		"ORRERY_POOL_TESTS=tests-1",
	} {
		if !strings.Contains(stdout.String(), want+"\n") {
			t.Errorf("command output %q does not hold %q", stdout.String(), want)
		}
	}
}

// TestRunExitsWithCommandStatus exits with the status the command exits with.
func TestRunExitsWithCommandStatus(t *testing.T) {
	setupProject(t, buildsPoolFile, "")
	fake := newFakePools(0)
	daemon := startPools(t, fake)
	var stdout bytes.Buffer
	args := append([]string{"--socket", daemon}, runCommandArgs(t, "exit", "builds=1")...)
	res := awaitOrrery(t, startOrrery(t, &stdout, args...))
	if res.code != helperExitStatus {
		t.Errorf("exit status %d, want the command's %d; stderr %q", res.code, helperExitStatus, res.stderr)
	}
}

// TestRunRefusedGrantExitsNoGrant exits ExitNoGrant when orreryd refuses the grant. The command does not run.
func TestRunRefusedGrantExitsNoGrant(t *testing.T) {
	setupProject(t, buildsPoolFile, "")
	fake := newFakePools(connect.CodeFailedPrecondition)
	daemon := startPools(t, fake)
	var stdout bytes.Buffer
	args := append([]string{"--socket", daemon}, runCommandArgs(t, "print", "builds=1")...)
	res := awaitOrrery(t, startOrrery(t, &stdout, args...))
	if res.code != cli.ExitNoGrant {
		t.Errorf("exit status %d, want %d; stderr %q", res.code, cli.ExitNoGrant, res.stderr)
	}
	if !strings.Contains(res.stderr, "no grant") {
		t.Errorf("stderr %q does not say no grant", res.stderr)
	}
	if strings.Contains(stdout.String(), "ran") {
		t.Errorf("the command ran: stdout %q", stdout.String())
	}
}

// TestRunWithoutOrreydExitsNoGrant exits ExitNoGrant when nothing listens on the session socket. The command does not
// run.
func TestRunWithoutOrreydExitsNoGrant(t *testing.T) {
	setupProject(t, buildsPoolFile, "")
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	var stdout bytes.Buffer
	args := append([]string{"--socket", daemon}, runCommandArgs(t, "print", "builds=1")...)
	res := awaitOrrery(t, startOrrery(t, &stdout, args...))
	if res.code != cli.ExitNoGrant {
		t.Errorf("exit status %d, want %d; stderr %q", res.code, cli.ExitNoGrant, res.stderr)
	}
	if strings.Contains(stdout.String(), "ran") {
		t.Errorf("the command ran: stdout %q", stdout.String())
	}
}

// TestRunSignalEndsGrant ends the grant and the command when SIGINT, SIGTERM or SIGHUP reaches orrery while the command
// runs. The command is ended by SIGTERM, so run exits with 128 plus SIGTERM.
func TestRunSignalEndsGrant(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			setupProject(t, buildsPoolFile, "")
			fake := newFakePools(0)
			daemon := startPools(t, fake)
			stdoutR, stdoutW, err := os.Pipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}
			t.Cleanup(func() {
				_ = stdoutR.Close()
				_ = stdoutW.Close()
			})
			args := append([]string{"--socket", daemon}, runCommandArgs(t, "sleep", "builds=1")...)
			res := startOrrery(t, stdoutW, args...)
			awaitReadyLine(t, stdoutR)
			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				t.Fatalf("send %v: %v", sig, err)
			}
			got := awaitOrrery(t, res)
			if got.code != sigtermStatus {
				t.Errorf("exit status %d, want %d; stderr %q", got.code, sigtermStatus, got.stderr)
			}
			select {
			case <-fake.ended:
			case <-time.After(runTimeout):
				t.Error("the grant's stream did not end")
			}
		})
	}
}

// awaitReadyLine returns once r yields the ready line a sleep command prints. It fails the test when no line comes
// within runTimeout.
func awaitReadyLine(t *testing.T, r io.Reader) {
	t.Helper()
	line := make(chan string, 1)
	go func() {
		text, err := bufio.NewReader(r).ReadString('\n')
		if err != nil {
			text = ""
		}
		line <- text
	}()
	select {
	case text := <-line:
		if !strings.HasPrefix(text, "ready ") {
			t.Fatalf("command output %q, want its ready line", text)
		}
	case <-time.After(runTimeout):
		t.Fatal("the command did not print its ready line")
	}
}

// TestUsageTextListsRun lists the run subcommand and its arguments in the usage text.
func TestUsageTextListsRun(t *testing.T) {
	if !strings.Contains(usageText, "run --needs POOL=N [--needs POOL=N]... -- CMD [ARGS]") {
		t.Errorf("usage text does not list run:\n%s", usageText)
	}
}
