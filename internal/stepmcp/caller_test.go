package stepmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// headerEpics answers GetEpic and records the headers of the request it receives.
type headerEpics struct {
	enginev1connect.UnimplementedEpicsHandler
	mu     sync.Mutex
	header http.Header
}

func (h *headerEpics) GetEpic(
	_ context.Context,
	req *connect.Request[v1.GetEpicRequest],
) (*connect.Response[v1.GetEpicResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.header = req.Header().Clone()
	return connect.NewResponse(&v1.GetEpicResponse{Epic: &v1.StoredEpic{Phase: "accepted"}}), nil
}

// recordedHeader returns the headers of the last request the handler received, or nil when it received none.
func (h *headerEpics) recordedHeader() http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.header
}

// envOf returns a getenv that reads vars and reports every other name as unset.
func envOf(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestCallerInterceptorStampsEpicCallsWithTheirSession(t *testing.T) {
	t.Parallel()
	const tmuxValue = "/tmp/tmux-1000/default,1,0"
	tests := []struct {
		name string
		env  map[string]string
		// want maps each caller header the handler must see to its value. A caller header not named here must be absent.
		want map[string]string
	}{
		{
			name: "orrery session",
			env:  map[string]string{"ORRERY_SESSION": "demo-session"},
			want: map[string]string{"Orrery-Session": "demo-session"},
		},
		{
			name: "orrery session takes precedence over tmux",
			env: map[string]string{
				"ORRERY_SESSION": "demo-session", "TMUX": tmuxValue, "TMUX_PANE": "%3",
			},
			want: map[string]string{"Orrery-Session": "demo-session"},
		},
		{
			name: "tmux and pane without orrery session",
			env:  map[string]string{"TMUX": tmuxValue, "TMUX_PANE": "%3"},
			want: map[string]string{"Orrery-Tmux": tmuxValue, "Orrery-Tmux-Pane": "%3"},
		},
		{
			name: "tmux alone",
			env:  map[string]string{"TMUX": tmuxValue},
		},
		{
			name: "pane alone",
			env:  map[string]string{"TMUX_PANE": "%3"},
		},
		{
			name: "nothing set",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &headerEpics{}
			path, handler := enginev1connect.NewEpicsHandler(svc)
			mux := http.NewServeMux()
			mux.Handle(path, handler)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)
			client := enginev1connect.NewEpicsClient(
				srv.Client(), srv.URL, connect.WithInterceptors(CallerInterceptor(envOf(tc.env))),
			)

			result, err := CallEpicTool(
				context.Background(), client, "epic_get", json.RawMessage(`{"id":"`+testEpicID+`"}`),
			)
			if err != nil {
				t.Fatalf("CallEpicTool(epic_get) returned error: %v", err)
			}
			if result.IsError {
				t.Fatalf("CallEpicTool(epic_get) = %+v, want a result without error", result)
			}
			header := svc.recordedHeader()
			if header == nil {
				t.Fatal("GetEpic was not called")
			}
			for _, name := range []string{"Orrery-Session", "Orrery-Tmux", "Orrery-Tmux-Pane"} {
				values := header.Values(name)
				want, wanted := tc.want[name]
				switch {
				case !wanted && len(values) > 0:
					t.Errorf("%s = %q, want no %s header", name, values, name)
				case wanted && !slices.Equal(values, []string{want}):
					t.Errorf("%s = %q, want %q", name, values, want)
				}
			}
		})
	}
}
