package stepmcp

import (
	"context"
	"sync"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// defaultHostNamed is the synthetic host a test names in a tool call. Each tool passes the host on unchanged.
const defaultHostNamed = "synthetic-host-a"

// defaultHostSteps is the Steps service of the default-host tests. It records each StartStep request.
type defaultHostSteps struct {
	enginev1connect.StepsClient

	mu     sync.Mutex
	starts []*v1.StartStepRequest
}

func (s *defaultHostSteps) StartStep(
	_ context.Context,
	req *connect.Request[v1.StartStepRequest],
) (*connect.Response[v1.StartStepResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, req.Msg)
	return connect.NewResponse(&v1.StartStepResponse{Id: "s1"}), nil
}

// TestDefaultHostEpicCreateToolSendsHost checks that epic_create sends the host it is given as CreateEpicRequest.Host,
// and sends an empty host when none is given.
func TestDefaultHostEpicCreateToolSendsHost(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{name: "named", args: `{"id":"epic-one","host":"` + defaultHostNamed + `"}`, want: defaultHostNamed},
		{name: "omitted", args: `{"id":"epic-one"}`, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeEpics{createReply: &v1.CreateEpicResponse{}}
			p := newPeer(t, nil, fake)
			outcome := p.tool("epic_create", tc.args)
			if outcome.isError {
				t.Fatalf("epic_create refused: %s", outcome.text)
			}
			if len(fake.creates) != 1 {
				t.Fatalf("CreateEpic calls = %d, want 1", len(fake.creates))
			}
			if host := fake.creates[0].GetHost(); host != tc.want {
				t.Errorf("CreateEpic host = %q, want %q", host, tc.want)
			}
		})
	}
}

// TestDefaultHostDesignCreateToolSendsHost checks that design_create sends the host it is given as
// CreateDesignRequest.Host, and sends an empty host when none is given.
func TestDefaultHostDesignCreateToolSendsHost(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{
			name: "named",
			args: `{"id":"design-one","title":"Design one","host":"` + defaultHostNamed + `"}`,
			want: defaultHostNamed,
		},
		{name: "omitted", args: `{"id":"design-one","title":"Design one"}`, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeDesigns{}
			p := designPeer(t, fake)
			outcome := p.tool("design_create", tc.args)
			if outcome.isError {
				t.Fatalf("design_create refused: %s", outcome.text)
			}
			if len(fake.creates) != 1 {
				t.Fatalf("CreateDesign calls = %d, want 1", len(fake.creates))
			}
			if host := fake.creates[0].GetHost(); host != tc.want {
				t.Errorf("CreateDesign host = %q, want %q", host, tc.want)
			}
		})
	}
}

// TestDefaultHostStepStartToolSendsHost checks that step_start sends the host it is given as StartStepRequest.Host, and
// sends an empty host when none is given.
func TestDefaultHostStepStartToolSendsHost(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{
			name: "named",
			args: `{"prompt":"check the change","workspace":"synthetic-workspace","host":"` + defaultHostNamed + `"}`,
			want: defaultHostNamed,
		},
		{name: "omitted", args: `{"prompt":"check the change","workspace":"synthetic-workspace"}`, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			steps := &defaultHostSteps{}
			p := newPeer(t, steps, nil)
			outcome := p.tool("step_start", tc.args)
			if outcome.isError {
				t.Fatalf("step_start refused: %s", outcome.text)
			}
			if len(steps.starts) != 1 {
				t.Fatalf("StartStep calls = %d, want 1", len(steps.starts))
			}
			if host := steps.starts[0].GetHost(); host != tc.want {
				t.Errorf("StartStep host = %q, want %q", host, tc.want)
			}
		})
	}
}
