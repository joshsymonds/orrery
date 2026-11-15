package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// artifactRecorder is the EvidenceClient of the stderr flag test. GetEvidenceArtifact records each request and answers
// synthetic bytes. ListEvidence answers unimplemented, since the Artifact call never calls it.
type artifactRecorder struct {
	mu       sync.Mutex
	requests []*enginev1.GetEvidenceArtifactRequest
}

func (f *artifactRecorder) ListEvidence(
	context.Context,
	*connect.Request[enginev1.ListEvidenceRequest],
) (*connect.Response[enginev1.ListEvidenceResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("synthetic unused"))
}

func (f *artifactRecorder) GetEvidenceArtifact(
	_ context.Context,
	req *connect.Request[enginev1.GetEvidenceArtifactRequest],
) (*connect.Response[enginev1.GetEvidenceArtifactResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req.Msg)
	return connect.NewResponse(&enginev1.GetEvidenceArtifactResponse{Data: []byte("synthetic log")}), nil
}

// received returns the requests the recorder has received, in order.
func (f *artifactRecorder) received() []*enginev1.GetEvidenceArtifactRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*enginev1.GetEvidenceArtifactRequest(nil), f.requests...)
}

func TestEvidenceArtifactForwardsStderrFlagAndAnswersText(t *testing.T) {
	t.Parallel()
	fake := &artifactRecorder{}
	server := evidenceServer{client: fake, timeout: time.Minute}
	for _, stderr := range []bool{true, false} {
		resp, err := server.Artifact(t.Context(), connect.NewRequest(&orreryv1.EvidenceArtifactRequest{
			Run:    "synthetic-run",
			Stderr: stderr,
		}))
		if err != nil {
			t.Fatalf("Artifact stderr=%v: %v", stderr, err)
		}
		if got := resp.Msg.GetMediaType(); got != mediaTypeText {
			t.Errorf("Artifact stderr=%v media type = %q, want %q", stderr, got, mediaTypeText)
		}
	}
	reqs := fake.received()
	if len(reqs) != 2 {
		t.Fatalf("GetEvidenceArtifact calls = %d, want 2", len(reqs))
	}
	for i, want := range []bool{true, false} {
		if reqs[i].GetRun() != "synthetic-run" || reqs[i].GetName() != "" || reqs[i].GetStderr() != want {
			t.Errorf("call %d = run %q name %q stderr %v, want synthetic-run, the run's log, stderr %v",
				i, reqs[i].GetRun(), reqs[i].GetName(), reqs[i].GetStderr(), want)
		}
	}
}

func TestEpicStepMessageCarriesTailsOnlyForFailedGateCheck(t *testing.T) {
	t.Parallel()
	step := &enginev1.Step{
		Id: "s1",
		Checks: []*enginev1.StepCheck{
			{
				Round: 1, Name: "gate", Ok: false, Detail: "synthetic detail",
				Stdout: "synthetic gate stdout", Stderr: "synthetic gate stderr",
			},
			{
				Round: 2, Name: "gate", Ok: true,
				Stdout: "synthetic passed stdout", Stderr: "synthetic passed stderr",
			},
			{
				Round: 3, Name: "test", Ok: false,
				Stdout: "synthetic test stdout", Stderr: "synthetic test stderr",
			},
		},
	}
	checks := epicStepMessage(step).GetChecks()
	want := []struct {
		round          uint32
		name           string
		ok             bool
		stdout, stderr string
	}{
		{1, "gate", false, "synthetic gate stdout", "synthetic gate stderr"},
		{2, "gate", true, "", ""},
		{3, "test", false, "", ""},
	}
	if len(checks) != len(want) {
		t.Fatalf("checks = %d, want %d", len(checks), len(want))
	}
	for i, w := range want {
		got := checks[i]
		if got.GetRound() != w.round || got.GetName() != w.name || got.GetOk() != w.ok ||
			got.GetStdout() != w.stdout || got.GetStderr() != w.stderr {
			t.Errorf("check %d = round %d name %q ok %v stdout %q stderr %q, want round %d name %q ok %v "+
				"stdout %q stderr %q",
				i, got.GetRound(), got.GetName(), got.GetOk(), got.GetStdout(), got.GetStderr(),
				w.round, w.name, w.ok, w.stdout, w.stderr)
		}
	}
}

func TestEpicRunMessageCarriesStderrBytes(t *testing.T) {
	t.Parallel()
	run := &enginev1.EvidenceRun{Id: "run-1", LogBytes: 2048, StderrBytes: 96}
	got := epicRunMessage(run)
	if got.GetStderrBytes() != 96 || got.GetLogBytes() != 2048 {
		t.Errorf("run stderr_bytes = %d log_bytes = %d, want 96 and 2048", got.GetStderrBytes(), got.GetLogBytes())
	}
}
