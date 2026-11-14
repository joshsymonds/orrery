package stepmcp

import (
	"context"
	"encoding/json"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/mcp"
)

const (
	evidenceRunSchema = `{"type":"object","properties":{` +
		`"epic":{"type":"string"},"item":{"type":"string"},` +
		`"revision":{"type":"string"},"phase":{"type":"string"}},` +
		`"required":["epic","item","revision","phase"],"additionalProperties":false}`

	evidenceListSchema = `{"type":"object","properties":{"epic":{"type":"string"}},` +
		`"required":["epic"],"additionalProperties":false}`

	evidenceGetSchema = `{"type":"object","properties":{` +
		`"run":{"type":"string"},` +
		`"name":{"type":"string","description":"the artifact's repository-relative path; omitted for the run's log"}},` +
		`"required":["run"],"additionalProperties":false}`
)

// evidenceTools returns the evidence tools, each calling evidence.
func evidenceTools(evidence enginev1connect.EvidenceClient) []mcp.Tool {
	return []mcp.Tool{
		{
			Name: "evidence_run",
			Description: "Run an item's evidence at a revision for one phase, red or green. Returns the evidence run. " +
				"A refusal names the field that failed.",
			InputSchema: json.RawMessage(evidenceRunSchema),
			Handler:     evidenceRunHandler(evidence),
		},
		{
			Name:        "evidence_list",
			Description: "List an epic's evidence runs.",
			InputSchema: json.RawMessage(evidenceListSchema),
			Handler:     evidenceListHandler(evidence),
		},
		{
			Name: "evidence_get",
			Description: "Return one artifact of an evidence run by its path, or the run's log when name is omitted. " +
				"The bytes are base64 in the result.",
			InputSchema: json.RawMessage(evidenceGetSchema),
			Handler:     evidenceGetHandler(evidence),
		},
	}
}

func evidenceRunHandler(evidence enginev1connect.EvidenceClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var req v1.RunEvidenceRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return mcp.ToolResult{}, err
		}
		ran, err := evidence.RunEvidence(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(ran.Msg.GetRun())
	}
}

func evidenceListHandler(evidence enginev1connect.EvidenceClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var req v1.ListEvidenceRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return mcp.ToolResult{}, err
		}
		listed, err := evidence.ListEvidence(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(listed.Msg)
	}
}

func evidenceGetHandler(evidence enginev1connect.EvidenceClient) mcp.ToolHandler {
	return func(ctx context.Context, call mcp.Call) (mcp.ToolResult, error) {
		var req v1.GetEvidenceArtifactRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return mcp.ToolResult{}, err
		}
		got, err := evidence.GetEvidenceArtifact(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(got.Msg)
	}
}
