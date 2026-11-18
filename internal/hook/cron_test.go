package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/engine/wire"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// noWait is the wait the cron tests inject. It has passed by the time Cron starts, so Cron arms no timer, and a client
// that waits on its context returns as soon as it is called.
const noWait = 0

const (
	cronHookInput = `{"hook_event_name":"PreToolUse","tool_name":"CronCreate","tool_use_id":"toolu_01",` +
		`"tool_input":{"cron":"*/5 * * * *","prompt":"check the build"}}`
	cronToolInput = `{"cron":"*/5 * * * *","prompt":"check the build"}`
)

// fakeCrons is a CronsClient whose CronCall is call.
type fakeCrons struct {
	call func(context.Context, *connect.Request[v1.CronCallRequest]) (*connect.Response[v1.CronCallResponse], error)
}

// CronCall calls the fake's call.
func (f fakeCrons) CronCall(
	ctx context.Context, req *connect.Request[v1.CronCallRequest],
) (*connect.Response[v1.CronCallResponse], error) {
	return f.call(ctx, req)
}

// answering returns a client that answers every call with reason.
func answering(reason string) enginev1connect.CronsClient {
	return fakeCrons{call: func(context.Context, *connect.Request[v1.CronCallRequest]) (
		*connect.Response[v1.CronCallResponse], error,
	) {
		return connect.NewResponse(&v1.CronCallResponse{Reason: reason}), nil
	}}
}

// decisionOutput is the hookSpecificOutput of the decision Cron writes.
type decisionOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// decodeDeny checks that out is exactly one PreToolUse deny decision and returns its reason. json.Unmarshal refuses
// trailing data after the value, so out holding a second decision fails here too.
func decodeDeny(t *testing.T, out string) string {
	t.Helper()
	var decision decisionOutput
	if err := json.Unmarshal([]byte(out), &decision); err != nil {
		t.Fatalf("stdout %q is not exactly one decision: %v", out, err)
	}
	got := decision.HookSpecificOutput
	if got.HookEventName != "PreToolUse" || got.PermissionDecision != "deny" {
		t.Fatalf("decision = %+v, want a PreToolUse deny", got)
	}
	return got.PermissionDecisionReason
}

func TestCronTakeoverHookPrintsOrreryAnswer(t *testing.T) {
	t.Parallel()
	const answer = "Scheduled job 4 for */5 * * * *.\nCall CronList to see it, or CronDelete to end it."
	var stdout bytes.Buffer
	client := answering(answer)
	if err := Cron(context.Background(), strings.NewReader(cronHookInput), &stdout, client, noWait); err != nil {
		t.Fatalf("Cron: %v", err)
	}
	if got := decodeDeny(t, stdout.String()); got != answer {
		t.Fatalf("reason = %q, want orreryd's answer %q verbatim", got, answer)
	}
}

func TestCronTakeoverHookSendsCall(t *testing.T) {
	t.Parallel()
	var got *v1.CronCallRequest
	client := fakeCrons{call: func(_ context.Context, req *connect.Request[v1.CronCallRequest]) (
		*connect.Response[v1.CronCallResponse], error,
	) {
		got = req.Msg
		return connect.NewResponse(&v1.CronCallResponse{Reason: "ok"}), nil
	}}
	var stdout bytes.Buffer
	if err := Cron(context.Background(), strings.NewReader(cronHookInput), &stdout, client, noWait); err != nil {
		t.Fatalf("Cron: %v", err)
	}
	if got == nil {
		t.Fatal("client received no call")
	}
	if got.GetToolName() != "CronCreate" {
		t.Errorf("tool_name = %q, want CronCreate", got.GetToolName())
	}
	if got.GetToolUseId() != "toolu_01" {
		t.Errorf("tool_use_id = %q, want toolu_01", got.GetToolUseId())
	}
	if got.GetToolInput() != cronToolInput {
		t.Errorf("tool_input = %q, want %q", got.GetToolInput(), cronToolInput)
	}
}

func TestCronTakeoverHookRPCErrorDenies(t *testing.T) {
	t.Parallel()
	rpcErr := connect.NewError(connect.CodeUnavailable, errors.New("orreryd is not running"))
	client := fakeCrons{call: func(context.Context, *connect.Request[v1.CronCallRequest]) (
		*connect.Response[v1.CronCallResponse], error,
	) {
		return nil, rpcErr
	}}
	var stdout bytes.Buffer
	if err := Cron(context.Background(), strings.NewReader(cronHookInput), &stdout, client, noWait); err != nil {
		t.Fatalf("Cron: %v", err)
	}
	const want = "orrery could not reach orreryd, so the job was not scheduled (unavailable: orreryd is not running). " +
		"Call CronCreate again."
	if got := decodeDeny(t, stdout.String()); got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
}

func TestCronTakeoverHookNoAnswerDenies(t *testing.T) {
	t.Parallel()
	// The client answers only when its context ends, so the call is refused by the deadline and nothing else.
	client := fakeCrons{call: func(ctx context.Context, _ *connect.Request[v1.CronCallRequest]) (
		*connect.Response[v1.CronCallResponse], error,
	) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	var stdout bytes.Buffer
	if err := Cron(context.Background(), strings.NewReader(cronHookInput), &stdout, client, noWait); err != nil {
		t.Fatalf("Cron: %v", err)
	}
	const want = "orrery could not reach orreryd, so the job was not scheduled (context deadline exceeded). " +
		"Call CronCreate again."
	if got := decodeDeny(t, stdout.String()); got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
}

func TestCronTakeoverHookBadInputDenies(t *testing.T) {
	t.Parallel()
	// A bad hook input never reaches the client, so the client fails the test if it is called.
	client := fakeCrons{call: func(context.Context, *connect.Request[v1.CronCallRequest]) (
		*connect.Response[v1.CronCallResponse], error,
	) {
		t.Error("client called for a hook input that could not be read")
		return nil, errors.New("client called")
	}}
	// The malformed input's cause is the decoder's own error, so the expected reason takes it from the decoder.
	const malformed = `{"tool_name":`
	var probe cronInput
	parseErr := wire.UnmarshalControlJSON([]byte(malformed), &probe)
	if parseErr == nil {
		t.Fatalf("%q parses, so it is no malformed input", malformed)
	}
	tests := []struct {
		name  string
		stdin io.Reader
		want  string
	}{
		{
			name:  "malformed",
			stdin: strings.NewReader(malformed),
			want: "orrery could not parse the hook input, so the job was not scheduled (" +
				parseErr.Error() + "). Call the tool again.",
		},
		{
			name:  "unreadable",
			stdin: iotest.ErrReader(errors.New("read failed")),
			want:  "orrery could not read the hook input, so the job was not scheduled (read failed). Call the tool again.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			if err := Cron(context.Background(), tt.stdin, &stdout, client, noWait); err != nil {
				t.Fatalf("Cron: %v", err)
			}
			if got := decodeDeny(t, stdout.String()); got != tt.want {
				t.Fatalf("reason = %q, want %q", got, tt.want)
			}
		})
	}
}
