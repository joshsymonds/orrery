package cli_test

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

// stopGrace is the grace a stopped command gets before SIGKILL when a run does not set one.
const stopGrace = 30 * time.Second

// stopWatch is how long a test watches a stopped command that must still be running.
const stopWatch = 5 * testRetry

// stopScript is the command the stop tests run, under the grant each test names. Under grant-term it exits on SIGTERM.
// Under grant-stubborn it ignores SIGTERM and runs until killed. Under grant-orphan it exits on SIGTERM and leaves a
// child in its group that ignores SIGTERM and runs until killed; the child prints a child line once it ignores
// SIGTERM. Under any other grant it prints the grant and exits 3. The first two print a ready line once their traps are
// set, so a test can send its stop only after that.
const stopScript = `case "$ORRERY_GRANT" in
grant-term) trap 'echo term; exit 0' TERM; echo "ready $$"; read -r line ;;
grant-stubborn) trap '' TERM; echo "ready $$"; read -r line ;;
grant-orphan) trap 'echo term; exit 0' TERM; sh -c 'trap "" TERM; echo "child $$"; exec sleep 1000' & read -r line ;;
*) echo "rerun $ORRERY_GRANT"; exit 3 ;;
esac`

// scriptedStep is one Acquire of a stopPools. It sends before, then waits until after closes when it is set. It returns
// fail when it is set; otherwise it sends then, and holds the stream open until the client closes it when hold is set.
type scriptedStep struct {
	before []*v1.AcquireResponse
	after  <-chan struct{}
	then   []*v1.AcquireResponse
	fail   error
	hold   bool
}

// stopStep is the first Acquire of a stop test: it grants grantID to member m1, waits until ready closes, then reports
// that m1 is held. It holds the stream open until the client closes it.
func stopStep(grantID string, ready <-chan struct{}) scriptedStep {
	stop := &v1.AcquireResponse{Event: &v1.AcquireResponse_Stop{Stop: &v1.AcquireStop{Members: []string{"m1"}}}}
	return scriptedStep{
		before: []*v1.AcquireResponse{grantedWith(grantID, poolMembers("builds", "m1"))},
		after:  ready,
		then:   []*v1.AcquireResponse{stop},
		hold:   true,
	}
}

// rejoinStep is the reclaim after a stop: it waits at position 2, then is granted grantID with member m2 and held.
func rejoinStep(grantID string) scriptedStep {
	return scriptedStep{
		before: []*v1.AcquireResponse{queuedAt(2), grantedWith(grantID, poolMembers("builds", "m2"))},
		hold:   true,
	}
}

// stopPools is a Pools for the stop tests. It answers the Acquires in order from steps, and its last step answers every
// Acquire after the steps run out. It records each request.
type stopPools struct {
	enginev1connect.UnimplementedPoolsHandler

	steps []scriptedStep

	mu       sync.Mutex
	requests []*v1.AcquireRequest
}

// received returns the requests received so far.
func (p *stopPools) received() []*v1.AcquireRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*v1.AcquireRequest(nil), p.requests...)
}

// Acquire implements enginev1connect.PoolsHandler.
func (p *stopPools) Acquire(
	ctx context.Context,
	req *connect.Request[v1.AcquireRequest],
	stream *connect.ServerStream[v1.AcquireResponse],
) error {
	p.mu.Lock()
	step := p.steps[min(len(p.requests), len(p.steps)-1)]
	p.requests = append(p.requests, req.Msg)
	p.mu.Unlock()
	if err := sendEvents(stream, step.before); err != nil {
		return err
	}
	if step.after != nil {
		select {
		case <-step.after:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if step.fail != nil {
		return step.fail
	}
	if err := sendEvents(stream, step.then); err != nil {
		return err
	}
	if step.hold {
		<-ctx.Done()
	}
	return nil
}

// sendEvents sends events on stream in order.
func sendEvents(stream *connect.ServerStream[v1.AcquireResponse], events []*v1.AcquireResponse) error {
	for _, event := range events {
		if err := stream.Send(event); err != nil {
			return err
		}
	}
	return nil
}

// serveStopPools serves pools on a unix socket at the session socket path beside a daemon socket in a short temp
// directory. It returns that session socket path. The server is closed at cleanup.
func serveStopPools(t *testing.T, pools *stopPools) string {
	t.Helper()
	socket := protocol.SessionSocketPath(filepath.Join(t.TempDir(), "orreryd.sock"))
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", socket, err)
	}
	mux := http.NewServeMux()
	path, handler := enginev1connect.NewPoolsHandler(pools)
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

// stopConfig returns the RunConfig that runs stopScript through the Pools at socket, with the given output and stdin.
func stopConfig(t *testing.T, socket string, stdout, stderr io.Writer, stdin io.Reader) cli.RunConfig {
	t.Helper()
	cfg := runConfig(t, socket, "env", stdout, stderr)
	cfg.Argv = []string{"sh", "-c", stopScript}
	cfg.Environ = []string{"PATH=" + os.Getenv("PATH")}
	cfg.Stdin = stdin
	return cfg
}

// stdinPipe returns a pipe for a command's stdin. Its write end stays open, so the command waits on its stdin until the
// test ends.
func stdinPipe(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	return r
}

// outputLines sends each line r reads on the returned channel, without its newline. The channel closes at the end of r,
// or at a read error, which a test sees as output that stopped early.
func outputLines(r io.Reader) <-chan string {
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		reader := bufio.NewReader(r)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				lines <- strings.TrimSuffix(line, "\n")
			}
			if err != nil {
				return
			}
		}
	}()
	return lines
}

// nextLine returns the next line lines delivers. It fails the test when none comes within runTimeout.
func nextLine(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line, ok := <-lines:
		if !ok {
			t.Fatal("the command's output ended before the next line")
		}
		return line
	case <-time.After(runTimeout):
		t.Fatal("no output line from the command")
		return ""
	}
}

// awaitStopRequests fails the test unless pools receives n requests within runTimeout.
func awaitStopRequests(t *testing.T, pools *stopPools, n int) {
	t.Helper()
	deadline := time.After(runTimeout)
	for len(pools.received()) < n {
		select {
		case <-deadline:
			t.Fatalf("pools received %d requests, want %d", len(pools.received()), n)
		case <-time.After(pollInterval):
		}
	}
}

// remainingLines returns the lines that out delivers after the test has closed its write end.
func remainingLines(lines <-chan string) []string {
	var rest []string
	for line := range lines {
		rest = append(rest, line)
	}
	return rest
}

func TestRunStopSendsSIGTERMAndRerunsCommand(t *testing.T) {
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{stopStep("grant-term", ready), rejoinStep("grant-rerun")}}
	socket := serveStopPools(t, pools)
	stdinR := stdinPipe(t)
	r, w := stdoutPipe(t)
	lines := outputLines(r)
	var stderr bytes.Buffer
	graces := make(chan time.Duration, 4)
	cfg := stopConfig(t, socket, w, &stderr, stdinR)
	cfg.After = func(d time.Duration) <-chan time.Time {
		graces <- d
		return nil
	}
	status := runAsync(context.Background(), cfg)
	if line := nextLine(t, lines); !strings.HasPrefix(line, "ready ") {
		t.Fatalf("first line = %q, want the ready line", line)
	}
	close(ready)
	if got := awaitStatus(t, status); got != 3 {
		t.Errorf("RunWithGrant = %d, want the rerun's status 3", got)
	}
	// The grace clock starts with SIGTERM and never fires here, so CMD's exit on SIGTERM is the only way it ends.
	if n := len(graces); n != 1 {
		t.Errorf("the grace started %d times, want 1: it starts with SIGTERM", n)
	} else if d := <-graces; d != stopGrace {
		t.Errorf("the grace = %v, want %v", d, stopGrace)
	}
	_ = w.Close()
	if got, want := remainingLines(lines), []string{"term", "rerun grant-rerun"}; !slices.Equal(got, want) {
		t.Errorf("output after the ready line = %q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "m1") {
		t.Errorf("stderr = %q, want it to name the held member m1", stderr.String())
	}
	if !strings.Contains(stderr.String(), "queued at position 2") {
		t.Errorf("stderr = %q, want the rerun to wait at position 2", stderr.String())
	}
	got := pools.received()
	if len(got) != 2 || !proto.Equal(got[0], testRequest()) || !proto.Equal(got[1], reclaimOf("grant-term")) {
		t.Errorf("Acquire requests = %v, want the request then its reclaim by grant_id grant-term", got)
	}
}

func TestRunStopKillsCommandOnlyAfterGrace(t *testing.T) {
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{stopStep("grant-stubborn", ready), rejoinStep("grant-rerun")}}
	socket := serveStopPools(t, pools)
	stdinR := stdinPipe(t)
	r, w := stdoutPipe(t)
	lines := outputLines(r)
	graces := make(chan time.Duration, 4)
	fire := make(chan time.Time)
	cfg := stopConfig(t, socket, w, io.Discard, stdinR)
	cfg.After = func(d time.Duration) <-chan time.Time {
		graces <- d
		return fire
	}
	status := runAsync(context.Background(), cfg)
	pid := readyPID(t, nextLine(t, lines))
	close(ready)

	select {
	case d := <-graces:
		if d != stopGrace {
			t.Fatalf("the grace = %v, want %v", d, stopGrace)
		}
	case <-time.After(runTimeout):
		t.Fatal("the grace never started after the stop")
	}
	// CMD ignores SIGTERM, so it runs on while the grace has not passed on the test's clock.
	time.Sleep(stopWatch)
	if processExited(t, pid) {
		t.Fatal("CMD exited before the grace passed")
	}
	if n := len(pools.received()); n != 1 {
		t.Fatalf("Acquire called %d times before the grace passed, want 1", n)
	}
	close(fire)
	waitGone(t, pid)
	if got := awaitStatus(t, status); got != 3 {
		t.Errorf("RunWithGrant = %d, want the rerun's status 3", got)
	}
	_ = w.Close()
	if got := remainingLines(lines); !slices.Contains(got, "rerun grant-rerun") {
		t.Errorf("output after the ready line = %q, want the rerun's line", got)
	}
	got := pools.received()
	if len(got) != 2 || !proto.Equal(got[1], reclaimOf("grant-stubborn")) {
		t.Errorf("Acquire requests = %v, want the request then its reclaim by grant_id grant-stubborn", got)
	}
}

func TestRunStopKillsGroupOnlyAfterGrace(t *testing.T) {
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{stopStep("grant-orphan", ready), rejoinStep("grant-rerun")}}
	socket := serveStopPools(t, pools)
	stdinR := stdinPipe(t)
	r, w := stdoutPipe(t)
	lines := outputLines(r)
	// stderr is a file, as in production. With any other writer, exec's Wait also waits for the copy of CMD's output,
	// which the orphan holding the pipe keeps open, so CMD's exit would not be seen.
	errR, errW := stdoutPipe(t)
	go func() { _, _ = io.Copy(io.Discard, errR) }()
	graces := make(chan time.Duration, 4)
	fire := make(chan time.Time)
	cfg := stopConfig(t, socket, w, errW, stdinR)
	cfg.After = func(d time.Duration) <-chan time.Time {
		graces <- d
		return fire
	}
	status := runAsync(context.Background(), cfg)
	// The child line is printed after the child ignores SIGTERM, and after CMD set its own trap, so the stop is sent
	// only once both hold.
	child := childPID(t, nextLine(t, lines))
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	close(ready)

	select {
	case d := <-graces:
		if d != stopGrace {
			t.Fatalf("the grace = %v, want %v", d, stopGrace)
		}
	case <-time.After(runTimeout):
		t.Fatal("the grace never started after the stop")
	}
	// CMD has exited on SIGTERM, but its child ignores SIGTERM and runs on while the grace has not passed on the test's
	// clock. The grant is not reclaimed while the child lives.
	time.Sleep(stopWatch)
	if processExited(t, child) {
		t.Fatal("the child exited before the grace passed")
	}
	if n := len(pools.received()); n != 1 {
		t.Fatalf("Acquire called %d times before the grace passed, want 1: no reclaim while the child lives", n)
	}
	close(fire)
	awaitStopRequests(t, pools, 2)
	if !processExited(t, child) {
		t.Fatal("the reclaim was sent while the child lives: the group was not killed before the reclaim")
	}
	if got := awaitStatus(t, status); got != 3 {
		t.Errorf("RunWithGrant = %d, want the rerun's status 3", got)
	}
	_ = w.Close()
	if got := remainingLines(lines); !slices.Contains(got, "rerun grant-rerun") {
		t.Errorf("output after the child line = %q, want the rerun's line", got)
	}
	got := pools.received()
	if len(got) != 2 || !proto.Equal(got[1], reclaimOf("grant-orphan")) {
		t.Errorf("Acquire requests = %v, want the request then its reclaim by grant_id grant-orphan", got)
	}
}

func TestRunStopExitsNoGrantWhenReclaimIsRefused(t *testing.T) {
	tests := []struct {
		name string
		code connect.Code
	}{
		{name: "not found", code: connect.CodeNotFound},
		{name: "failed precondition", code: connect.CodeFailedPrecondition},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ready := make(chan struct{})
			pools := &stopPools{steps: []scriptedStep{
				stopStep("grant-term", ready),
				{fail: connect.NewError(tc.code, errors.New("orreryd refused the grant"))},
			}}
			socket := serveStopPools(t, pools)
			stdinR := stdinPipe(t)
			r, w := stdoutPipe(t)
			lines := outputLines(r)
			var stderr bytes.Buffer
			status := runAsync(context.Background(), stopConfig(t, socket, w, &stderr, stdinR))
			nextLine(t, lines)
			close(ready)
			if got := awaitStatus(t, status); got != cli.ExitNoGrant {
				t.Errorf("RunWithGrant = %d, want ExitNoGrant %d", got, cli.ExitNoGrant)
			}
			_ = w.Close()
			if got := remainingLines(lines); slices.ContainsFunc(got, isReadyLine) {
				t.Errorf("output = %q, want no rerun after the refused reclaim", got)
			}
			if !strings.Contains(stderr.String(), "refused") {
				t.Errorf("stderr = %q, want it to say orreryd refused the reclaim", stderr.String())
			}
		})
	}
}

func TestRunStopExitsWhenContextEndsWhileWaitingAgain(t *testing.T) {
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{
		stopStep("grant-term", ready),
		{before: []*v1.AcquireResponse{queuedAt(1)}, hold: true},
	}}
	socket := serveStopPools(t, pools)
	stdinR := stdinPipe(t)
	r, w := stdoutPipe(t)
	lines := outputLines(r)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := runAsync(ctx, stopConfig(t, socket, w, io.Discard, stdinR))
	nextLine(t, lines)
	close(ready)
	awaitStopRequests(t, pools, 2)
	cancel()
	if got := awaitStatus(t, status); got != cli.ExitNoGrant {
		t.Errorf("RunWithGrant = %d, want ExitNoGrant %d", got, cli.ExitNoGrant)
	}
	_ = w.Close()
	if got := remainingLines(lines); slices.ContainsFunc(got, isReadyLine) {
		t.Errorf("output = %q, want no rerun once the context ended", got)
	}
	if n := len(pools.received()); n != 2 {
		t.Errorf("Acquire called %d times, want 2: the request and its reclaim", n)
	}
}

// isReadyLine reports whether line is the ready line stopScript prints each time it starts under a stoppable grant, so
// a ready line after the first means CMD ran again.
func isReadyLine(line string) bool {
	return strings.HasPrefix(line, "ready ")
}

// childPID returns the process id on the child line the grant-orphan stop script prints.
func childPID(t *testing.T, line string) int {
	t.Helper()
	var pid int
	if _, err := fmt.Sscanf(line, "child %d", &pid); err != nil {
		t.Fatalf("child line %q: %v", line, err)
	}
	return pid
}
