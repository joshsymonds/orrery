package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// fakeRefusal is the message every refusal of the fake Evidence service carries.
const fakeRefusal = "fake evidence refused the request"

// fakeEvidence is an in-process Evidence service for the evidence tests. It records each call as its arguments and
// answers with run, runs and data. Its calls take the codes in refusals in order: a call whose code is not 0 is refused
// with it, and a call past the end of refusals is answered.
type fakeEvidence struct {
	run      *v1.EvidenceRun
	runs     []*v1.EvidenceRun
	data     []byte
	refusals []connect.Code

	mu    sync.Mutex
	calls []string
}

// received returns the calls the fake has received, in order.
func (f *fakeEvidence) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// answer records call and returns the error the fake refuses it with, when its turn in refusals is a refusal.
func (f *fakeEvidence) answer(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	turn := len(f.calls) - 1
	if turn >= len(f.refusals) || f.refusals[turn] == 0 {
		return nil
	}
	return connect.NewError(f.refusals[turn], errors.New(fakeRefusal))
}

// RunEvidence implements enginev1connect.EvidenceHandler.
func (f *fakeEvidence) RunEvidence(
	_ context.Context,
	req *connect.Request[v1.RunEvidenceRequest],
) (*connect.Response[v1.RunEvidenceResponse], error) {
	call := fmt.Sprintf("run epic=%q item=%q revision=%q phase=%q",
		req.Msg.GetEpic(), req.Msg.GetItem(), req.Msg.GetRevision(), req.Msg.GetPhase())
	if err := f.answer(call); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.RunEvidenceResponse{Run: f.run}), nil
}

// ListEvidence implements enginev1connect.EvidenceHandler.
func (f *fakeEvidence) ListEvidence(
	_ context.Context,
	req *connect.Request[v1.ListEvidenceRequest],
) (*connect.Response[v1.ListEvidenceResponse], error) {
	if err := f.answer(fmt.Sprintf("list epic=%q", req.Msg.GetEpic())); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ListEvidenceResponse{Runs: f.runs}), nil
}

// GetEvidenceArtifact implements enginev1connect.EvidenceHandler.
func (f *fakeEvidence) GetEvidenceArtifact(
	_ context.Context,
	req *connect.Request[v1.GetEvidenceArtifactRequest],
) (*connect.Response[v1.GetEvidenceArtifactResponse], error) {
	call := fmt.Sprintf("get run=%q name=%q", req.Msg.GetRun(), req.Msg.GetName())
	if err := f.answer(call); err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.GetEvidenceArtifactResponse{Data: f.data}), nil
}

// serveEvidence serves fake as Evidence on the session socket beside a daemon socket in a temporary directory. It
// returns the daemon socket path that `--socket` names. The server is closed at cleanup.
func serveEvidence(t *testing.T, fake *fakeEvidence) string {
	t.Helper()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	ln, err := net.Listen("unix", protocol.SessionSocketPath(daemon))
	if err != nil {
		t.Fatalf("listen on the session socket: %v", err)
	}
	mux := http.NewServeMux()
	path, handler := enginev1connect.NewEvidenceHandler(fake)
	mux.Handle(path, handler)
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close evidence server: %v", err)
		}
	})
	return daemon
}

// TestEvidenceRunPrintsTheRun sends the four arguments of `evidence run` to RunEvidence and prints the run's id, item,
// phase and state on one line.
func TestEvidenceRunPrintsTheRun(t *testing.T) {
	t.Parallel()
	fake := &fakeEvidence{run: &v1.EvidenceRun{Id: "run-1", Item: "R1", Phase: "red", State: "ended"}}
	daemon := serveEvidence(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout,
		"--socket", daemon, "evidence", "run", "EPIC", "R1", "abc123", "red"))
	if res.code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
	want := []string{`run epic="EPIC" item="R1" revision="abc123" phase="red"`}
	if got := fake.received(); !slices.Equal(got, want) {
		t.Errorf("orreryd received %q, want %q", got, want)
	}
	if got, want := stdout.String(), "run-1 R1 red ended\n"; got != want {
		t.Errorf("stdout %q, want %q", got, want)
	}
	if res.stderr != "" {
		t.Errorf("stderr %q, want none", res.stderr)
	}
}

// TestEvidenceListPrintsRunsAndArtifacts sends the epic to ListEvidence and prints one line per run, in the order
// orreryd gives, each followed by an indented line per artifact. An empty verdict prints as -, and the revision is
// cut to its first 12 characters.
func TestEvidenceListPrintsRunsAndArtifacts(t *testing.T) {
	t.Parallel()
	fake := &fakeEvidence{runs: []*v1.EvidenceRun{
		{
			Id:       "run-1",
			Item:     "R1",
			Phase:    "red",
			Source:   "captured",
			State:    "ended",
			Verdict:  "holds",
			Passed:   true,
			Revision: "0123456789abcdef0123456789abcdef01234567",
			Artifacts: []*v1.EvidenceArtifact{
				{Path: "out/a.png", State: "kept", Size: 12},
				{Path: "out/b.txt", State: "missing", Size: 0},
			},
		},
		{Id: "run-2", Item: "R1", Phase: "green", Source: "supplied", State: "ended", Revision: "abc"},
	}}
	daemon := serveEvidence(t, fake)
	var stdout bytes.Buffer
	res := awaitOrrery(t, startOrrery(t, &stdout, "--socket", daemon, "evidence", "list", "EPIC"))
	if res.code != exitOK {
		t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
	want := []string{`list epic="EPIC"`}
	if got := fake.received(); !slices.Equal(got, want) {
		t.Errorf("orreryd received %q, want %q", got, want)
	}
	wantOut := "run-1 R1 red captured ended holds passed 0123456789ab\n" +
		"  out/a.png kept 12\n" +
		"  out/b.txt missing 0\n" +
		"run-2 R1 green supplied ended - not passed abc\n"
	if got := stdout.String(); got != wantOut {
		t.Errorf("stdout %q, want %q", got, wantOut)
	}
	if res.stderr != "" {
		t.Errorf("stderr %q, want none", res.stderr)
	}
}

// TestEvidenceGetWritesTheArtifactBytes writes the bytes of the artifact named, or of the run's log when no name is
// given, to stdout unchanged. The bytes are not text, so any rewriting shows.
func TestEvidenceGetWritesTheArtifactBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"named artifact", []string{"get", "run-1", "out/a.png"}, `get run="run-1" name="out/a.png"`},
		{"run log", []string{"get", "run-1"}, `get run="run-1" name=""`},
	}
	data := []byte{0x00, 0xff, '\n', '\r', 'x', 0x80, 0x00, '\t'}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeEvidence{data: data}
			daemon := serveEvidence(t, fake)
			var stdout bytes.Buffer
			args := append([]string{"--socket", daemon, "evidence"}, tc.args...)
			res := awaitOrrery(t, startOrrery(t, &stdout, args...))
			if res.code != exitOK {
				t.Fatalf("exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
			}
			if got := fake.received(); !slices.Equal(got, []string{tc.want}) {
				t.Errorf("orreryd received %q, want [%s]", got, tc.want)
			}
			if !bytes.Equal(stdout.Bytes(), data) {
				t.Errorf("stdout % x, want % x", stdout.Bytes(), data)
			}
			if res.stderr != "" {
				t.Errorf("stderr %q, want none", res.stderr)
			}
		})
	}
}

// TestEvidenceRefusedExitsOne exits 1 with orreryd's message on stderr when orreryd refuses each subcommand. Each
// refusal is sent once.
func TestEvidenceRefusedExitsOne(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		args   []string
		refuse connect.Code
		call   string
	}{
		{"run refused as invalid", []string{"run", "EPIC", "R1", "abc", "red"}, connect.CodeInvalidArgument,
			`run epic="EPIC" item="R1" revision="abc" phase="red"`},
		{"list of a missing epic", []string{"list", "EPIC"}, connect.CodeNotFound, `list epic="EPIC"`},
		{"get of a missing run", []string{"get", "run-1", "out/a.png"}, connect.CodeNotFound,
			`get run="run-1" name="out/a.png"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeEvidence{refusals: []connect.Code{tc.refuse}}
			daemon := serveEvidence(t, fake)
			var stdout bytes.Buffer
			args := append([]string{"--socket", daemon, "evidence"}, tc.args...)
			res := awaitOrrery(t, startOrrery(t, &stdout, args...))
			if res.code != exitFailure {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitFailure, res.stderr)
			}
			if prefix := "orrery evidence " + tc.args[0] + ": "; !strings.HasPrefix(res.stderr, prefix) ||
				!strings.Contains(res.stderr, fakeRefusal) {
				t.Errorf("stderr %q, want orreryd's message %q prefixed with %q", res.stderr, fakeRefusal, prefix)
			}
			if got := fake.received(); !slices.Equal(got, []string{tc.call}) {
				t.Errorf("orreryd received %q, want [%s]", got, tc.call)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout %q, want none", stdout.String())
			}
		})
	}
}

// TestEvidenceUsageErrorsExitTwo refuses each command line whose arguments are wrong or whose subcommand is unknown.
// Each exits 2 with the usage text and sends nothing.
func TestEvidenceUsageErrorsExitTwo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{"no subcommand", []string{"evidence"}},
		{"run without its phase", []string{"evidence", "run", "EPIC", "R1", "abc"}},
		{"run with an extra argument", []string{"evidence", "run", "EPIC", "R1", "abc", "red", "x"}},
		{"list without an epic", []string{"evidence", "list"}},
		{"list with two epics", []string{"evidence", "list", "EPIC", "X"}},
		{"get without a run", []string{"evidence", "get"}},
		{"get with two names", []string{"evidence", "get", "run-1", "a", "b"}},
		{"unknown subcommand", []string{"evidence", "show", "run-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeEvidence{}
			daemon := serveEvidence(t, fake)
			var stdout bytes.Buffer
			res := awaitOrrery(t, startOrrery(t, &stdout, append([]string{"--socket", daemon}, tc.args...)...))
			if res.code != exitUsage {
				t.Errorf("exit status %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}
			if !strings.HasPrefix(res.stderr, "orrery: evidence") {
				t.Errorf("stderr %q, want a usage error naming evidence", res.stderr)
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

// TestUsageTextListsEvidence lists the three evidence subcommands and their arguments in the usage text.
func TestUsageTextListsEvidence(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"\n  evidence run EPIC ITEM REVISION PHASE",
		"\n  evidence list EPIC",
		"\n  evidence get RUN [NAME]",
	} {
		if !strings.Contains(usageText, line) {
			t.Errorf("usage text does not list %q:\n%s", strings.TrimPrefix(line, "\n"), usageText)
		}
	}
}

// TestUsageTextListsGateTail lists --gate-tail BYTES on the holder gate line, the flag holder takes with --gate-limit.
func TestUsageTextListsGateTail(t *testing.T) {
	t.Parallel()
	const line = "\n  holder --session NAME [--state-dir DIR] --gate-limit SECONDS [--gate-tail BYTES] " +
		"-- PATH [ARGS...]"
	if !strings.Contains(usageText, line) {
		t.Errorf("usage text does not list the gate tail flag:\n%s", usageText)
	}
}

// runEvidenceClock runs `orrery evidence` with args against the daemon socket on clock, as runEvidence does, and writes
// its stdout to stdout. It returns the exit code main would report and what was written to stderr.
func runEvidenceClock(
	t *testing.T,
	clock rpcretry.Clock,
	daemon string,
	stdout io.Writer,
	args ...string,
) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	err := runEvidenceWith(
		t.Context(),
		clock,
		globals{daemonSocket: daemon},
		args,
		stdout,
		&stderr,
		func(string) string { return "" },
	)
	return report(&stderr, err), stderr.String()
}

// TestEvidenceRunRetriesWhileNothingListens sends run to a socket nothing listens on. Each attempt never connects, so
// run is retried until rpcretry.Window has passed on the clock, then exits 1 with the reason.
func TestEvidenceRunRetriesWhileNothingListens(t *testing.T) {
	t.Parallel()
	daemon := filepath.Join(t.TempDir(), "orreryd.sock")
	clock := newHoldClock()
	start := clock.Now()
	var stdout bytes.Buffer
	code, stderr := runEvidenceClock(t, clock, daemon, &stdout, "run", "EPIC", "R1", "abc", "red")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if !strings.HasPrefix(stderr, "orrery evidence run: ") {
		t.Errorf("stderr %q, want the reason prefixed with orrery evidence run:", stderr)
	}
	if elapsed := clock.Now().Sub(start); elapsed != rpcretry.Window {
		t.Errorf("run gave up after %v on the clock, want exactly Window %v", elapsed, rpcretry.Window)
	}
}

// TestEvidenceRunDoesNotRepeatAnsweredUnavailable sends run to an Evidence service that answers Unavailable. Orreryd
// may have stored and started the run before it answered, so the run is sent once, and it exits 1 with orreryd's
// message.
func TestEvidenceRunDoesNotRepeatAnsweredUnavailable(t *testing.T) {
	t.Parallel()
	fake := &fakeEvidence{refusals: []connect.Code{connect.CodeUnavailable}}
	daemon := serveEvidence(t, fake)
	clock := newHoldClock()
	var stdout bytes.Buffer
	code, stderr := runEvidenceClock(t, clock, daemon, &stdout, "run", "EPIC", "R1", "abc", "red")
	if code != exitFailure {
		t.Errorf("exit status %d, want %d; stderr %q", code, exitFailure, stderr)
	}
	if !strings.HasPrefix(stderr, "orrery evidence run: ") || !strings.Contains(stderr, fakeRefusal) {
		t.Errorf("stderr %q, want orreryd's message prefixed with orrery evidence run:", stderr)
	}
	want := []string{`run epic="EPIC" item="R1" revision="abc" phase="red"`}
	if got := fake.received(); !slices.Equal(got, want) {
		t.Errorf("orreryd received %q, want %q", got, want)
	}
	if len(clock.waits) != 0 {
		t.Errorf("run waited %v on the clock, want no retry", clock.waits)
	}
}

// TestEvidenceReadsRetryUnavailable sends list and get to an Evidence service that answers its first call Unavailable.
// Each read is sent again and prints its answer, since a repeated read changes nothing.
func TestEvidenceReadsRetryUnavailable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		call string
		want string
	}{
		{"list", []string{"list", "EPIC"}, `list epic="EPIC"`, "run-1 R1 red captured ended holds passed abc\n"},
		{"get", []string{"get", "run-1"}, `get run="run-1" name=""`, "log\x00bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeEvidence{
				runs: []*v1.EvidenceRun{{
					Id: "run-1", Item: "R1", Phase: "red", Source: "captured", State: "ended",
					Verdict: "holds", Passed: true, Revision: "abc",
				}},
				data:     []byte("log\x00bytes"),
				refusals: []connect.Code{connect.CodeUnavailable},
			}
			daemon := serveEvidence(t, fake)
			clock := newHoldClock()
			var stdout bytes.Buffer
			code, stderr := runEvidenceClock(t, clock, daemon, &stdout, tc.args...)
			if code != exitOK {
				t.Fatalf("exit status %d, want %d; stderr %q", code, exitOK, stderr)
			}
			if want := []string{tc.call, tc.call}; !slices.Equal(fake.received(), want) {
				t.Errorf("orreryd received %q, want %q", fake.received(), want)
			}
			if got := stdout.String(); got != tc.want {
				t.Errorf("stdout %q, want %q", got, tc.want)
			}
		})
	}
}
