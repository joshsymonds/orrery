package stepmcp

import (
	"context"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// evidenceRunArgs is the arguments of evidence_run, described for its input schema. The handler decodes the arguments
// with protojson, which refuses an unknown key, so the struct is never decoded into.
type evidenceRunArgs struct {
	Epic     string `json:"epic"`
	Item     string `json:"item"`
	Revision string `json:"revision"`
	Phase    string `json:"phase"`
}

// evidenceListArgs is the arguments of evidence_list, described for its input schema as evidenceRunArgs is.
type evidenceListArgs struct {
	Epic string `json:"epic"`
}

// evidenceGetArgs is the arguments of evidence_get, described for its input schema as evidenceRunArgs is.
type evidenceGetArgs struct {
	Run  string `json:"run"`
	Name string `json:"name,omitempty" jsonschema:"the artifact's repository-relative path; omitted for the run's log"`
}

// evidenceTools returns the evidence tools, each calling evidence.
func evidenceTools(evidence enginev1connect.EvidenceClient) ([]Tool, error) {
	runInput, err := argumentSchema[evidenceRunArgs](nil)
	if err != nil {
		return nil, err
	}
	listInput, err := argumentSchema[evidenceListArgs](nil)
	if err != nil {
		return nil, err
	}
	getInput, err := argumentSchema[evidenceGetArgs](nil)
	if err != nil {
		return nil, err
	}
	return []Tool{
		{
			Name: "evidence_run",
			Description: "Run an item's evidence at a revision for one phase, red or green. Returns the evidence run. " +
				"A refusal names the field that failed.",
			InputSchema: runInput,
			Handler:     evidenceRunHandler(evidence),
		},
		{
			Name:        "evidence_list",
			Description: "List an epic's evidence runs.",
			InputSchema: listInput,
			Handler:     evidenceListHandler(evidence),
		},
		{
			Name: "evidence_get",
			Description: "Return one artifact of an evidence run by its path, or the run's log when name is omitted. " +
				"The bytes are base64 in the result.",
			InputSchema: getInput,
			Handler:     evidenceGetHandler(evidence),
		},
	}, nil
}

func evidenceRunHandler(evidence enginev1connect.EvidenceClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.RunEvidenceRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		ran, err := evidence.RunEvidence(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(ran.Msg.GetRun())
	}
}

func evidenceListHandler(evidence enginev1connect.EvidenceClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.ListEvidenceRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		listed, err := evidence.ListEvidence(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(listed.Msg)
	}
}

func evidenceGetHandler(evidence enginev1connect.EvidenceClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var req v1.GetEvidenceArtifactRequest
		if err := decodeProto(call.Arguments, &req); err != nil {
			return ToolResult{}, err
		}
		got, err := evidence.GetEvidenceArtifact(ctx, connect.NewRequest(&req))
		if err != nil {
			return toolFailure(err), nil
		}
		return protoResult(got.Msg)
	}
}
