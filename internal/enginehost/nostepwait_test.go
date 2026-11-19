package enginehost

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/emptypb"
)

// TestNoStepWaitRPCUnimplemented calls the WaitSteps procedure, which the Steps service no longer serves, and expects
// Unimplemented.
func TestNoStepWaitRPCUnimplemented(t *testing.T) {
	t.Parallel()
	path, _ := startServerWithSteps(t, nil, &fakeStepCommands{})
	client := connect.NewClient[emptypb.Empty, emptypb.Empty](
		unixHTTPClient(t, path),
		"http://steps.invalid/orrery.engine.v1.Steps/WaitSteps",
	)

	_, err := client.CallUnary(context.Background(), connect.NewRequest(&emptypb.Empty{}))
	if got := connect.CodeOf(err); got != connect.CodeUnimplemented {
		t.Fatalf("WaitSteps code = %v, want %v (error %v)", got, connect.CodeUnimplemented, err)
	}
}
