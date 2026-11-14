package enginehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceServiceCall is one call the fake made: its method and the arguments it was given.
type evidenceServiceCall struct {
	method string
	args   []string
}

// evidenceServiceFake is an EvidenceCommands that answers with the values the test sets and records every call.
type evidenceServiceFake struct {
	run  store.EvidenceRun
	runs []store.EvidenceRun
	data []byte
	hash string
	err  error

	mu    sync.Mutex
	calls []evidenceServiceCall
}

func (f *evidenceServiceFake) Run(_ context.Context, epic, item, revision, phase string) (store.EvidenceRun, error) {
	f.record("Run", epic, item, revision, phase)
	return f.run, f.err
}

func (f *evidenceServiceFake) Runs(_ context.Context, epic string) ([]store.EvidenceRun, error) {
	f.record("Runs", epic)
	return f.runs, f.err
}

func (f *evidenceServiceFake) Artifact(_ context.Context, run, name string) ([]byte, string, error) {
	f.record("Artifact", run, name)
	return f.data, f.hash, f.err
}

// record appends a call to method with args.
func (f *evidenceServiceFake) record(method string, args ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, evidenceServiceCall{method: method, args: args})
}

// recorded returns the calls the fake has made, in order.
func (f *evidenceServiceFake) recorded() []evidenceServiceCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// evidenceServiceStartServer serves evidence on a socket in a temporary directory and returns the socket's path. The
// server stops at cleanup.
func evidenceServiceStartServer(t *testing.T, evidence EvidenceCommands) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, nil, nil, nil, nil, evidence) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve after cleanup: %v", err)
		}
	})
	return path
}

// evidenceServiceNewClient returns an Evidence client that dials the socket at path.
func evidenceServiceNewClient(t *testing.T, path string) enginev1connect.EvidenceClient {
	t.Helper()
	return enginev1connect.NewEvidenceClient(unixHTTPClient(t, path), "http://evidence.invalid")
}

// evidenceServiceRuns returns two synthetic runs of epic-1 in order: the first ended, with a kept artifact and a missing
// one, and the second still capturing, with no end time.
func evidenceServiceRuns() []store.EvidenceRun {
	const created int64 = 1700000000
	const ended int64 = 1700000100
	const capturing int64 = 1700000200
	first := store.EvidenceRun{
		ID: "run-1", Epic: "epic-1", Item: "R1", Revision: "abc123", Phase: "green", Source: "captured",
		State: "ended", Exit: 2, TimedOut: true, LogHash: "loghash", LogBytes: 17,
		Artifacts: []store.EvidenceArtifact{
			{Path: "out/a.txt", State: "kept", Hash: "hashA", Blob: "blobA", Size: 5},
			{Path: "out/b.txt", State: "missing"},
		},
		JudgeStep: "step-9", Verdict: "pass", JudgeReason: "synthetic reason", Passed: true,
		Created: time.Unix(created, 0), Ended: time.Unix(ended, 0),
	}
	second := store.EvidenceRun{
		ID: "run-2", Epic: "epic-1", Item: "R1", Revision: "abc123", Phase: "red", Source: "supplied",
		State: "capturing", Created: time.Unix(capturing, 0),
	}
	return []store.EvidenceRun{first, second}
}

// evidenceServiceWant returns the messages evidenceServiceRuns maps to, in the same order.
func evidenceServiceWant() []*v1.EvidenceRun {
	return []*v1.EvidenceRun{
		{
			Id: "run-1", Epic: "epic-1", Item: "R1", Revision: "abc123", Phase: "green", Source: "captured",
			State: "ended", Exit: 2, TimedOut: true, LogHash: "loghash", LogBytes: 17,
			Artifacts: []*v1.EvidenceArtifact{
				{Path: "out/a.txt", State: "kept", Hash: "hashA", Size: 5},
				{Path: "out/b.txt", State: "missing"},
			},
			JudgeStep: "step-9", Verdict: "pass", JudgeReason: "synthetic reason", Passed: true,
			Created: 1700000000, Ended: 1700000100,
		},
		{
			Id: "run-2", Epic: "epic-1", Item: "R1", Revision: "abc123", Phase: "red", Source: "supplied",
			State: "capturing", Created: 1700000200,
		},
	}
}

// evidenceServiceAssertRuns asserts that got holds the messages want holds, in order.
func evidenceServiceAssertRuns(t *testing.T, got, want []*v1.EvidenceRun) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("runs = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("runs[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestEvidenceServiceRunEvidencePassesFieldsAndReturnsRun(t *testing.T) {
	t.Parallel()
	fake := &evidenceServiceFake{run: evidenceServiceRuns()[0]}
	client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, fake))
	resp, err := client.RunEvidence(t.Context(), connect.NewRequest(&v1.RunEvidenceRequest{
		Epic: "epic-1", Item: "R1", Revision: "abc123", Phase: "green",
	}))
	if err != nil {
		t.Fatalf("RunEvidence: %v", err)
	}
	wantCalls := []evidenceServiceCall{{method: "Run", args: []string{"epic-1", "R1", "abc123", "green"}}}
	if got := fake.recorded(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("calls = %v, want %v", got, wantCalls)
	}
	evidenceServiceAssertRuns(t, []*v1.EvidenceRun{resp.Msg.GetRun()}, evidenceServiceWant()[:1])
}

func TestEvidenceServiceListEvidenceReturnsRunsInOrderWithArtifacts(t *testing.T) {
	t.Parallel()
	fake := &evidenceServiceFake{runs: evidenceServiceRuns()}
	client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, fake))
	resp, err := client.ListEvidence(t.Context(), connect.NewRequest(&v1.ListEvidenceRequest{Epic: "epic-1"}))
	if err != nil {
		t.Fatalf("ListEvidence: %v", err)
	}
	wantCalls := []evidenceServiceCall{{method: "Runs", args: []string{"epic-1"}}}
	if got := fake.recorded(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("calls = %v, want %v", got, wantCalls)
	}
	evidenceServiceAssertRuns(t, resp.Msg.GetRuns(), evidenceServiceWant())
}

func TestEvidenceServiceGetEvidenceArtifactReturnsBytesAndHashAndEmptyNameAsksForLog(t *testing.T) {
	t.Parallel()
	fake := &evidenceServiceFake{data: []byte("synthetic bytes"), hash: "hashA"}
	client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, fake))
	for _, name := range []string{"out/a.txt", ""} {
		resp, err := client.GetEvidenceArtifact(t.Context(), connect.NewRequest(&v1.GetEvidenceArtifactRequest{
			Run: "run-1", Name: name,
		}))
		if err != nil {
			t.Fatalf("GetEvidenceArtifact(%q): %v", name, err)
		}
		if string(resp.Msg.GetData()) != "synthetic bytes" || resp.Msg.GetHash() != "hashA" {
			t.Fatalf("GetEvidenceArtifact(%q) = %q, %q; want synthetic bytes, hashA", name, resp.Msg.GetData(),
				resp.Msg.GetHash())
		}
	}
	wantCalls := []evidenceServiceCall{
		{method: "Artifact", args: []string{"run-1", "out/a.txt"}},
		{method: "Artifact", args: []string{"run-1", ""}},
	}
	if got := fake.recorded(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("calls = %v, want %v", got, wantCalls)
	}
}

func TestEvidenceServiceRefusesEmptyFieldsBeforeAnyCall(t *testing.T) {
	t.Parallel()
	fake := &evidenceServiceFake{}
	client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, fake))
	ctx := t.Context()
	for _, tc := range []struct {
		message string
		req     *v1.RunEvidenceRequest
	}{
		{"epic: empty", &v1.RunEvidenceRequest{Item: "R1", Revision: "abc123", Phase: "green"}},
		{"item: empty", &v1.RunEvidenceRequest{Epic: "epic-1", Revision: "abc123", Phase: "green"}},
		{"revision: empty", &v1.RunEvidenceRequest{Epic: "epic-1", Item: "R1", Phase: "green"}},
		{"phase: empty", &v1.RunEvidenceRequest{Epic: "epic-1", Item: "R1", Revision: "abc123"}},
	} {
		_, err := client.RunEvidence(ctx, connect.NewRequest(tc.req))
		wantConnectError(t, err, connect.CodeInvalidArgument, tc.message)
	}
	_, err := client.ListEvidence(ctx, connect.NewRequest(&v1.ListEvidenceRequest{}))
	wantConnectError(t, err, connect.CodeInvalidArgument, "epic: empty")
	_, err = client.GetEvidenceArtifact(ctx, connect.NewRequest(&v1.GetEvidenceArtifactRequest{Name: "out/a.txt"}))
	wantConnectError(t, err, connect.CodeInvalidArgument, "run: empty")
	if got := fake.recorded(); len(got) != 0 {
		t.Fatalf("calls = %v, want none", got)
	}
}

func TestEvidenceServiceRunEvidenceMapsRunErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		err     error
		code    connect.Code
		message string
	}{
		{
			name:    "refused",
			err:     fmt.Errorf("%w: no requirement R9", ErrEvidenceRefused),
			code:    connect.CodeInvalidArgument,
			message: "evidence run is refused: no requirement R9",
		},
		{
			name:    "no epic",
			err:     fmt.Errorf("evidence run of epic epic-1: %w", store.ErrNoEpic),
			code:    connect.CodeNotFound,
			message: "epic: no epic epic-1",
		},
		{
			name:    "other",
			err:     errors.New("synthetic store failure"),
			code:    connect.CodeInternal,
			message: "synthetic store failure",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, &evidenceServiceFake{err: tc.err}))
			_, err := client.RunEvidence(t.Context(), connect.NewRequest(&v1.RunEvidenceRequest{
				Epic: "epic-1", Item: "R1", Revision: "abc123", Phase: "green",
			}))
			wantConnectError(t, err, tc.code, tc.message)
		})
	}
}

func TestEvidenceServiceListEvidenceRefusesExitOutsideInt32(t *testing.T) {
	t.Parallel()
	const outside = 1 << 40
	run := evidenceServiceRuns()[0]
	run.Exit = outside
	client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, &evidenceServiceFake{
		runs: []store.EvidenceRun{run},
	}))
	_, err := client.ListEvidence(t.Context(), connect.NewRequest(&v1.ListEvidenceRequest{Epic: "epic-1"}))
	wantConnectError(t, err, connect.CodeInternal, "exit 1099511627776 is outside the range of int32")
}

func TestEvidenceServiceListEvidenceMapsStoreErrorToInternal(t *testing.T) {
	t.Parallel()
	client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, &evidenceServiceFake{
		err: errors.New("synthetic store failure"),
	}))
	_, err := client.ListEvidence(t.Context(), connect.NewRequest(&v1.ListEvidenceRequest{Epic: "epic-1"}))
	wantConnectError(t, err, connect.CodeInternal, "synthetic store failure")
}

func TestEvidenceServiceGetEvidenceArtifactMapsErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		err     error
		code    connect.Code
		message string
	}{
		{
			name:    "no run",
			err:     fmt.Errorf("evidence run run-9: %w", store.ErrNoEvidenceRun),
			code:    connect.CodeNotFound,
			message: "run: no run run-9",
		},
		{
			name:    "no kept artifact",
			err:     fmt.Errorf("artifact out/b.txt of evidence run run-1: %w", ErrNoEvidenceArtifact),
			code:    connect.CodeNotFound,
			message: "name: no kept artifact out/b.txt",
		},
		{
			name:    "other",
			err:     errors.New("synthetic blob failure"),
			code:    connect.CodeInternal,
			message: "synthetic blob failure",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, &evidenceServiceFake{err: tc.err}))
			_, err := client.GetEvidenceArtifact(t.Context(), connect.NewRequest(&v1.GetEvidenceArtifactRequest{
				Run: "run-9", Name: "out/b.txt",
			}))
			wantConnectError(t, err, tc.code, tc.message)
		})
	}
}

func TestEvidenceServiceAnswersUnavailableWithoutEvidence(t *testing.T) {
	t.Parallel()
	client := evidenceServiceNewClient(t, evidenceServiceStartServer(t, nil))
	ctx := t.Context()
	_, err := client.RunEvidence(ctx, connect.NewRequest(&v1.RunEvidenceRequest{
		Epic: "epic-1", Item: "R1", Revision: "abc123", Phase: "green",
	}))
	wantConnectError(t, err, connect.CodeUnavailable, "evidence unavailable")
	_, err = client.ListEvidence(ctx, connect.NewRequest(&v1.ListEvidenceRequest{Epic: "epic-1"}))
	wantConnectError(t, err, connect.CodeUnavailable, "evidence unavailable")
	_, err = client.GetEvidenceArtifact(ctx, connect.NewRequest(&v1.GetEvidenceArtifactRequest{Run: "run-1"}))
	wantConnectError(t, err, connect.CodeUnavailable, "evidence unavailable")
}
