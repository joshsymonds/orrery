package stepmcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

const captureCommit = "fedcba9876543210fedcba9876543210fedcba98"

// fakeCaptureEvidence is the fake Evidence handler of these tests. Its CaptureEvidence and AttachEvidence answer with
// the configured replies and record each request; its other RPCs are fakeEvidence's.
type fakeCaptureEvidence struct {
	*fakeEvidence
	captureMu    sync.Mutex
	captures     []*v1.CaptureEvidenceRequest
	attaches     []*v1.AttachEvidenceRequest
	captureReply *v1.CaptureEvidenceResponse
	attachReply  *v1.AttachEvidenceResponse
	captureErr   error
	attachErr    error
}

func (f *fakeCaptureEvidence) CaptureEvidence(
	_ context.Context,
	req *connect.Request[v1.CaptureEvidenceRequest],
) (*connect.Response[v1.CaptureEvidenceResponse], error) {
	f.captureMu.Lock()
	defer f.captureMu.Unlock()
	f.captures = append(f.captures, req.Msg)
	if f.captureErr != nil {
		return nil, f.captureErr
	}
	return connect.NewResponse(f.captureReply), nil
}

func (f *fakeCaptureEvidence) AttachEvidence(
	_ context.Context,
	req *connect.Request[v1.AttachEvidenceRequest],
) (*connect.Response[v1.AttachEvidenceResponse], error) {
	f.captureMu.Lock()
	defer f.captureMu.Unlock()
	f.attaches = append(f.attaches, req.Msg)
	if f.attachErr != nil {
		return nil, f.attachErr
	}
	return connect.NewResponse(f.attachReply), nil
}

// received returns the capture and attach requests the fake received.
func (f *fakeCaptureEvidence) received() ([]*v1.CaptureEvidenceRequest, []*v1.AttachEvidenceRequest) {
	f.captureMu.Lock()
	defer f.captureMu.Unlock()
	return f.captures, f.attaches
}

// newCaptureFake returns a fake whose captures answer with capture-7 and whose attaches answer with no runs.
func newCaptureFake() *fakeCaptureEvidence {
	return &fakeCaptureEvidence{
		fakeEvidence: &fakeEvidence{},
		captureReply: &v1.CaptureEvidenceResponse{Capture: "capture-7"},
		attachReply:  &v1.AttachEvidenceResponse{},
	}
}

func TestCaptureToolSendsArgumentsUnchangedAndReturnsCaptureID(t *testing.T) {
	fake := newCaptureFake()
	p := newPeerWithEvidence(t, nil, nil, fake)

	outcome := p.tool("evidence_capture",
		`{"commit":"`+captureCommit+`","argv":["go","test","./..."],"runner":"local",`+
			`"briefs":["run-1","run-2"],"files":["out/roundtrip.log"]}`)

	if outcome.isError {
		t.Fatalf("evidence_capture returned an error: %s", outcome.text)
	}
	captures, _ := fake.received()
	want := &v1.CaptureEvidenceRequest{
		Commit: captureCommit,
		Argv:   []string{"go", "test", "./..."},
		Runner: "local",
		Briefs: []string{"run-1", "run-2"},
		Files:  []string{"out/roundtrip.log"},
	}
	if len(captures) != 1 || !proto.Equal(captures[0], want) {
		t.Fatalf("CaptureEvidence requests = %v, want %v", captures, want)
	}
	if id := decodeObject(t, outcome.text)["capture"]; id != "capture-7" {
		t.Fatalf("evidence_capture result = %s, want the capture id capture-7", outcome.text)
	}
}

func TestCaptureToolSaysItsEndArrivesAsAnEvent(t *testing.T) {
	p := newPeerWithEvidence(t, nil, nil, newCaptureFake())

	outcome := p.tool("evidence_capture", `{"commit":"`+captureCommit+`","argv":["go"],"briefs":["run-1"]}`)

	if outcome.isError {
		t.Fatalf("evidence_capture returned an error: %s", outcome.text)
	}
	next, _ := decodeObject(t, outcome.text)["next"].(string)
	if !strings.Contains(next, "end arrives in this conversation as an orrery-event") {
		t.Fatalf("evidence_capture next = %q, want a line saying its end arrives as an event", next)
	}
}

func TestCaptureToolRefusalIsToolError(t *testing.T) {
	const refusal = "runner: no such runner"
	fake := newCaptureFake()
	fake.captureErr = connect.NewError(connect.CodeInvalidArgument, errors.New(refusal))
	p := newPeerWithEvidence(t, nil, nil, fake)

	outcome := p.tool("evidence_capture",
		`{"commit":"`+captureCommit+`","argv":["go"],"runner":"nope","briefs":["run-1"]}`)

	if !outcome.isError {
		t.Fatalf("evidence_capture returned %q without isError", outcome.text)
	}
	if want := "invalid_argument: " + refusal; outcome.text != want {
		t.Fatalf("evidence_capture error text = %q, want %q", outcome.text, want)
	}
	if captures, _ := fake.received(); len(captures) != 1 {
		t.Fatalf("CaptureEvidence was called %d times, want once", len(captures))
	}
}

func TestCaptureToolMissingArgumentsAreToolErrors(t *testing.T) {
	cases := []struct {
		name      string
		arguments string
		missing   string
	}{
		{name: "no commit", arguments: `{"argv":["go"],"briefs":["run-1"]}`, missing: "commit is required"},
		{
			name:      "empty commit",
			arguments: `{"commit":"","argv":["go"],"briefs":["run-1"]}`,
			missing:   "commit is required",
		},
		{
			name:      "no argv",
			arguments: `{"commit":"` + captureCommit + `","briefs":["run-1"]}`,
			missing:   "argv is required",
		},
		{
			name:      "empty argv",
			arguments: `{"commit":"` + captureCommit + `","argv":[],"briefs":["run-1"]}`,
			missing:   "argv is required",
		},
		{
			name:      "no briefs",
			arguments: `{"commit":"` + captureCommit + `","argv":["go"]}`,
			missing:   "briefs is required",
		},
		{
			name:      "empty briefs",
			arguments: `{"commit":"` + captureCommit + `","argv":["go"],"briefs":[]}`,
			missing:   "briefs is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newCaptureFake()
			p := newPeerWithEvidence(t, nil, nil, fake)

			outcome := p.tool("evidence_capture", tc.arguments)

			if !outcome.isError {
				t.Fatalf("evidence_capture returned %q without isError", outcome.text)
			}
			if outcome.text != tc.missing {
				t.Fatalf("evidence_capture error text = %q, want %q", outcome.text, tc.missing)
			}
			if captures, _ := fake.received(); len(captures) != 0 {
				t.Fatalf("CaptureEvidence was called %d times, want none", len(captures))
			}
		})
	}
}

func TestEvidenceAttachSendsArgumentsUnchangedAndReturnsRuns(t *testing.T) {
	attached := &v1.AttachEvidenceResponse{Runs: []*v1.EvidenceRun{sampleEvidenceRun()}}
	fake := newCaptureFake()
	fake.attachReply = attached
	p := newPeerWithEvidence(t, nil, nil, fake)

	outcome := p.tool("evidence_attach", `{"capture":"capture-7","briefs":["run-1","run-2"]}`)

	if outcome.isError {
		t.Fatalf("evidence_attach returned an error: %s", outcome.text)
	}
	_, attaches := fake.received()
	want := &v1.AttachEvidenceRequest{Capture: "capture-7", Briefs: []string{"run-1", "run-2"}}
	if len(attaches) != 1 || !proto.Equal(attaches[0], want) {
		t.Fatalf("AttachEvidence requests = %v, want %v", attaches, want)
	}
	var got v1.AttachEvidenceResponse
	unmarshalResult(t, outcome.text, &got)
	if !proto.Equal(&got, attached) {
		t.Fatalf("evidence_attach result = %s, want the runs it moved", outcome.text)
	}
}

func TestEvidenceAttachRefusalIsToolError(t *testing.T) {
	const refusal = "capture: no such capture"
	fake := newCaptureFake()
	fake.attachErr = connect.NewError(connect.CodeInvalidArgument, errors.New(refusal))
	p := newPeerWithEvidence(t, nil, nil, fake)

	outcome := p.tool("evidence_attach", `{"capture":"capture-9","briefs":["run-1"]}`)

	if !outcome.isError {
		t.Fatalf("evidence_attach returned %q without isError", outcome.text)
	}
	if want := "invalid_argument: " + refusal; outcome.text != want {
		t.Fatalf("evidence_attach error text = %q, want %q", outcome.text, want)
	}
	if _, attaches := fake.received(); len(attaches) != 1 {
		t.Fatalf("AttachEvidence was called %d times, want once", len(attaches))
	}
}

func TestEvidenceAttachMissingArgumentsAreToolErrors(t *testing.T) {
	cases := []struct {
		name      string
		arguments string
		missing   string
	}{
		{name: "no capture", arguments: `{"briefs":["run-1"]}`, missing: "capture is required"},
		{name: "empty capture", arguments: `{"capture":"","briefs":["run-1"]}`, missing: "capture is required"},
		{name: "no briefs", arguments: `{"capture":"capture-7"}`, missing: "briefs is required"},
		{name: "empty briefs", arguments: `{"capture":"capture-7","briefs":[]}`, missing: "briefs is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newCaptureFake()
			p := newPeerWithEvidence(t, nil, nil, fake)

			outcome := p.tool("evidence_attach", tc.arguments)

			if !outcome.isError {
				t.Fatalf("evidence_attach returned %q without isError", outcome.text)
			}
			if outcome.text != tc.missing {
				t.Fatalf("evidence_attach error text = %q, want %q", outcome.text, tc.missing)
			}
			if _, attaches := fake.received(); len(attaches) != 0 {
				t.Fatalf("AttachEvidence was called %d times, want none", len(attaches))
			}
		})
	}
}
