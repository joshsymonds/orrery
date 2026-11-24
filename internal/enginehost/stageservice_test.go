package enginehost

import (
	"context"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// stageTestDesign is the design id the Stage service test's fixed handler answers for.
const stageTestDesign = "synthetic-design-stage"

// fixedStage is a Stage handler whose GetStage answers the design stageTestDesign. Its other RPCs answer unimplemented.
type fixedStage struct {
	enginev1connect.UnimplementedStageHandler
}

// GetStage answers the stage of stageTestDesign, whatever design it is asked for.
func (fixedStage) GetStage(
	_ context.Context,
	_ *connect.Request[v1.GetStageRequest],
) (*connect.Response[v1.GetStageResponse], error) {
	return connect.NewResponse(&v1.GetStageResponse{
		Stage: &v1.DesignStage{Design: stageTestDesign},
	}), nil
}

// startStageServer serves the Stage service with handler, which may be nil, through ServeWith on a sessions socket in a
// temporary directory. It returns the client that dials the socket. The server stops at cleanup.
func startStageServer(t *testing.T, handler enginev1connect.StageHandler) enginev1connect.StageClient {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- ServeWith(ctx, ln, ServeConfig{Stage: handler}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith after cleanup: %v", err)
		}
	})
	return enginev1connect.NewStageClient(unixHTTPClient(t, path), "http://stage.invalid")
}

// TestStageServiceMounted checks that ServeWith mounts the Stage service: a handler given to it answers GetStage, and
// no handler answers GetStage as unimplemented.
func TestStageServiceMounted(t *testing.T) {
	t.Run("given handler answers GetStage", func(t *testing.T) {
		client := startStageServer(t, fixedStage{})
		reply, err := client.GetStage(t.Context(), connect.NewRequest(&v1.GetStageRequest{Design: stageTestDesign}))
		if err != nil {
			t.Fatalf("GetStage: %v", err)
		}
		if got := reply.Msg.GetStage().GetDesign(); got != stageTestDesign {
			t.Errorf("GetStage design = %q, want %q", got, stageTestDesign)
		}
	})
	t.Run("no handler answers unimplemented", func(t *testing.T) {
		client := startStageServer(t, nil)
		_, err := client.GetStage(t.Context(), connect.NewRequest(&v1.GetStageRequest{Design: stageTestDesign}))
		if code := connect.CodeOf(err); code != connect.CodeUnimplemented {
			t.Errorf("GetStage code = %v, want %v (err %v)", code, connect.CodeUnimplemented, err)
		}
	})
}
