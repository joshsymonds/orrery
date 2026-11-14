package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/rpcretry"
)

// stepsFake is an in-process Steps service for the step tests. It answers each ResumeStep in order from script, then
// with tail. A cut answer drops the connection once the request is read. It records each ResumeStep it answers as
// "Resume ID".
type stepsFake struct {
	enginev1connect.UnimplementedStepsHandler

	mu       sync.Mutex
	script   []holdAnswer
	tail     holdAnswer
	attempts int      // requests that reached the server, cut or answered
	calls    []string // calls the server answered, as "Resume ID"
}

// arrive counts a request that reached the server and returns the answer it gets.
func (f *stepsFake) arrive() holdAnswer {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if len(f.script) == 0 {
		return f.tail
	}
	ans := f.script[0]
	f.script = f.script[1:]
	return ans
}

// received returns the calls the server answered, in order.
func (f *stepsFake) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// requests returns how many requests reached the server, cut or answered.
func (f *stepsFake) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

// ResumeStep implements enginev1connect.StepsHandler.
func (f *stepsFake) ResumeStep(
	ctx context.Context,
	req *connect.Request[v1.ResumeStepRequest],
) (*connect.Response[v1.ResumeStepResponse], error) {
	f.mu.Lock()
	f.calls = append(f.calls, "Resume "+req.Msg.GetId())
	f.mu.Unlock()
	ans, _ := ctx.Value(answerKey{}).(holdAnswer)
	if ans.err != nil {
		return nil, ans.err
	}
	return connect.NewResponse(&v1.ResumeStepResponse{}), nil
}

// serveStepsFake serves fake as Steps on the session socket beside the daemon socket at daemon. The server is closed at
// cleanup.
func serveStepsFake(t *testing.T, daemon string, fake *stepsFake) {
	t.Helper()
	ln, err := net.Listen("unix", protocol.SessionSocketPath(daemon))
	if err != nil {
		t.Fatalf("listen on the session socket: %v", err)
	}
	mux := http.NewServeMux()
	path, handler := enginev1connect.NewStepsHandler(fake)
	mux.Handle(path, handler)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ans := fake.arrive()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if ans.cut {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), answerKey{}, ans)))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// runStepRetry runs `orrery step` with args against the daemon socket on clock. It returns the exit code main would
// report and what was written to stderr.
func runStepRetry(t *testing.T, clock rpcretry.Clock, daemon string, args ...string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	err := runStepWith(
		t.Context(),
		clock,
		globals{daemonSocket: daemon},
		args,
		&stderr,
		func(string) string { return "" },
	)
	return report(&stderr, err), stderr.String()
}

// TestStepResumeSendsResume resumes s1 against a Steps fake. It exits 0 and writes nothing.
func TestStepResumeSendsResume(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "step", "resume", "s1"))
	if res.code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
	if got := fake.received(); !slices.Equal(got, []string{"Resume s1"}) {
		t.Errorf("orreryd received %q, want [Resume s1]", got)
	}
	if stdout.Len() != 0 || res.stderr != "" {
		t.Errorf("step resume wrote stdout %q and stderr %q, want neither", stdout.String(), res.stderr)
	}
}

// TestStepResumeRefusedExitsOne exits 1 with orreryd's reason on stderr when orreryd refuses the resume. Each refusal
// is sent once and not retried.
func TestStepResumeRefusedExitsOne(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		refuse connect.Code
		reason string
	}{
		{"step not limited", connect.CodeFailedPrecondition, "step is not limited"},
		{"step cap full", connect.CodeResourceExhausted, "session cap reached"},
		{"no such step", connect.CodeNotFound, "no step"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &stepsFake{tail: holdAnswer{err: connectErr(tc.refuse, tc.reason)}}
			daemon := filepath.Join(t.TempDir(), "orreryd.sock")
			serveStepsFake(t, daemon, fake)
			clock := newHoldClock()
			code, stderr := runStepRetry(t, clock, daemon, "resume", "s1")
			if code != exitFailure {
				t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
			}
			if !strings.HasPrefix(stderr, "orrery step: ") || !strings.Contains(stderr, tc.reason) {
				t.Errorf("stderr %q, want the reason %q prefixed with orrery step:", stderr, tc.reason)
			}
			if got := fake.received(); !slices.Equal(got, []string{"Resume s1"}) {
				t.Errorf("orreryd received %q, want [Resume s1]", got)
			}
			if len(clock.waits) != 0 {
				t.Errorf("waits = %v, want no retry", clock.waits)
			}
		})
	}
}

// TestStepUsageErrorsExitTwo refuses each command line that is not step resume ID. Each exits 2 with the usage text and
// sends nothing.
func TestStepUsageErrorsExitTwo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{"no verb", []string{"step"}},
		{"resume without ID", []string{"step", "resume"}},
		{"resume with two IDs", []string{"step", "resume", "a", "b"}},
		{"unknown verb", []string{"step", "stop", "s1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &stepsFake{}
			daemon := filepath.Join(t.TempDir(), "orreryd.sock")
			serveStepsFake(t, daemon, fake)
			var stdout bytes.Buffer
			res := awaitOrrery(t, startOrrery(t, &stdout, append([]string{"--socket", daemon}, tc.args...)...))
			if res.code != exitUsage {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}
			if !strings.HasPrefix(res.stderr, "orrery: step: ") {
				t.Errorf("stderr %q, want a usage error naming step", res.stderr)
			}
			if !strings.HasSuffix(res.stderr, usageText) {
				t.Errorf("stderr does not end with the usage text: %q", res.stderr)
			}
			if got := fake.received(); len(got) != 0 {
				t.Errorf("orreryd received %q, want nothing", got)
			}
		})
	}
}

// TestStepResumeRetriesUnavailableThenResumes answers the first ResumeStep Unavailable. The retry resumes the step, so
// orrery step exits 0 after one wait and writes nothing.
func TestStepResumeRetriesUnavailableThenResumes(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{script: []holdAnswer{{err: connectErr(connect.CodeUnavailable, "orreryd restarting")}}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	clock := newHoldClock()
	code, stderr := runStepRetry(t, clock, daemon, "resume", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	want := []string{"Resume s1", "Resume s1"}
	if got := fake.received(); !slices.Equal(got, want) {
		t.Errorf("orreryd received %q, want %q", got, want)
	}
	if len(clock.waits) != 1 {
		t.Errorf("waits = %v, want one wait before the retry", clock.waits)
	}
	if stderr != "" {
		t.Errorf("step resume wrote stderr %q, want nothing", stderr)
	}
}

// TestStepResumeCutThenResumes cuts the first ResumeStep mid-call. The retry resumes the step and exits 0.
func TestStepResumeCutThenResumes(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{script: []holdAnswer{{cut: true}}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stderr := runStepRetry(t, newHoldClock(), daemon, "resume", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if got := fake.requests(); got != 2 {
		t.Errorf("orreryd saw %d requests, want the cut and its retry", got)
	}
	if got := fake.received(); !slices.Equal(got, []string{"Resume s1"}) {
		t.Errorf("orreryd answered %q, want [Resume s1]", got)
	}
	if stderr != "" {
		t.Errorf("step resume wrote stderr %q, want nothing", stderr)
	}
}

// TestStepResumeCutThenNotLimitedSucceeds cuts the first ResumeStep mid-call. The retry hears the step is not limited,
// so the earlier attempt resumed it: orrery step exits 0 and writes nothing.
func TestStepResumeCutThenNotLimitedSucceeds(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{script: []holdAnswer{
		{cut: true},
		{err: connectErr(connect.CodeFailedPrecondition, "step is not limited")},
	}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stderr := runStepRetry(t, newHoldClock(), daemon, "resume", "s1")
	if code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if got := fake.requests(); got != 2 {
		t.Errorf("orreryd saw %d requests, want the cut and its retry", got)
	}
	if stderr != "" {
		t.Errorf("step resume wrote stderr %q, want nothing", stderr)
	}
}

// TestStepResumeCutThenOtherRefusalStillFails cuts the first ResumeStep mid-call. A retry refused for a reason other than
// "step is not limited" did not resume the step, so orrery step exits 1 with the reason.
func TestStepResumeCutThenOtherRefusalStillFails(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{script: []holdAnswer{
		{cut: true},
		{err: connectErr(connect.CodeNotFound, "no step")},
	}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	code, stderr := runStepRetry(t, newHoldClock(), daemon, "resume", "s1")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if !strings.HasPrefix(stderr, "orrery step: ") || !strings.Contains(stderr, "no step") {
		t.Errorf("stderr %q, want the reason prefixed with orrery step:", stderr)
	}
}

// TestStepResumeGivesUpAfterWindow answers every ResumeStep Unavailable. The retries stop when rpcretry.Window has
// passed on the clock, and orrery step exits 1 with the reason.
func TestStepResumeGivesUpAfterWindow(t *testing.T) {
	t.Parallel()
	fake := &stepsFake{tail: holdAnswer{err: connectErr(connect.CodeUnavailable, "orreryd restarting")}}
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	serveStepsFake(t, daemon, fake)
	clock := newHoldClock()
	start := clock.Now()
	code, stderr := runStepRetry(t, clock, daemon, "resume", "s1")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if !strings.HasPrefix(stderr, "orrery step: ") || !strings.Contains(stderr, "unavailable") {
		t.Errorf("stderr %q, want the reason unavailable prefixed with orrery step:", stderr)
	}
	if got := clock.Now().Sub(start); got != rpcretry.Window {
		t.Errorf("step gave up after %v on the clock, want exactly Window %v", got, rpcretry.Window)
	}
	if fake.requests() < 2 {
		t.Errorf("orreryd saw %d requests, want retries before the window ends", fake.requests())
	}
}

// TestStepResumeWithoutOrreydExitsOne exits 1 with the reason when nothing listens on the session socket. Each attempt is
// refused at dial and retried until rpcretry.Window has passed on the clock.
func TestStepResumeWithoutOrreydExitsOne(t *testing.T) {
	t.Parallel()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	clock := newHoldClock()
	start := clock.Now()
	code, stderr := runStepRetry(t, clock, daemon, "resume", "s1")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if !strings.HasPrefix(stderr, "orrery step: ") {
		t.Errorf("stderr %q, want the reason prefixed with orrery step:", stderr)
	}
	if elapsed := clock.Now().Sub(start); elapsed != rpcretry.Window {
		t.Errorf("step gave up after %v on the clock, want exactly Window %v", elapsed, rpcretry.Window)
	}
}

// TestUsageTextListsStepResume lists the step resume subcommand and its argument in the usage text.
func TestUsageTextListsStepResume(t *testing.T) {
	t.Parallel()
	if !strings.Contains(usageText, "\n  step resume ID") {
		t.Errorf("usage text does not list step resume:\n%s", usageText)
	}
}
