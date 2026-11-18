package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/engine/wire"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// cronInput is the part of a PreToolUse hook's stdin that a cron call needs. Claude Code spells these fields in
// snake_case, which the wire tags name.
type cronInput struct {
	ToolName  string          `json:"toolName"  wire:"tool_name"`
	ToolUseID string          `json:"toolUseId" wire:"tool_use_id"`
	ToolInput json.RawMessage `json:"toolInput" wire:"tool_input"`
}

// cronDecision is the PreToolUse decision the cron hook writes to stdout.
type cronDecision struct {
	HookSpecificOutput cronOutput `json:"hookSpecificOutput"`
}

// cronOutput is the hookSpecificOutput of a cronDecision.
type cronOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// Cron answers one PreToolUse hook for a cron tool. It reads the hook's JSON from stdin, asks client to take the call
// over with a deadline of wait, and writes one deny decision to stdout whose reason is orreryd's answer. Any failure,
// an unreadable or malformed hook input, an RPC error or a deadline that passes with no answer, writes a deny whose
// reason says the job was not scheduled. Cron always writes exactly one decision, so the hook never denies silently.
// It returns an error only when the decision cannot be written to stdout.
func Cron(
	ctx context.Context, stdin io.Reader, stdout io.Writer, client enginev1connect.CronsClient, wait time.Duration,
) error {
	return writeDeny(stdout, cronReason(ctx, stdin, client, wait))
}

// cronReason returns the reason of the deny decision for the hook input on stdin.
func cronReason(ctx context.Context, stdin io.Reader, client enginev1connect.CronsClient, wait time.Duration) string {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return notScheduled("could not read the hook input", err, "")
	}
	var in cronInput
	if err = wire.UnmarshalControlJSON(raw, &in); err != nil {
		return notScheduled("could not parse the hook input", err, "")
	}

	callCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	resp, err := client.CronCall(callCtx, connect.NewRequest(&v1.CronCallRequest{
		ToolName:  in.ToolName,
		ToolUseId: in.ToolUseID,
		ToolInput: string(in.ToolInput),
	}))
	if err != nil {
		return notScheduled("could not reach orreryd", err, in.ToolName)
	}
	return resp.Msg.GetReason()
}

// notScheduled returns the reason of a call that got no answer. It says the job was not scheduled, names cause, and
// tells the agent to call the tool again. tool is the tool's name, or empty when the hook input did not name one.
func notScheduled(what string, cause error, tool string) string {
	again := "Call the tool again."
	if tool != "" {
		again = "Call " + tool + " again."
	}
	return fmt.Sprintf("orrery %s, so the job was not scheduled (%v). %s", what, cause, again)
}

// writeDeny writes the deny decision with reason to stdout as one JSON line.
func writeDeny(stdout io.Writer, reason string) error {
	decision := cronDecision{HookSpecificOutput: cronOutput{
		HookEventName:            "PreToolUse",
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}}
	if err := json.NewEncoder(stdout).Encode(decision); err != nil {
		return fmt.Errorf("write decision: %w", err)
	}
	return nil
}
