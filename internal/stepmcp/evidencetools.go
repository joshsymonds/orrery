package stepmcp

import (
	"context"
	"errors"

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

// evidenceGetArgs is the arguments of evidence_get, described for its input schema. The handler decodes the arguments
// with decodeArguments, which refuses an unknown key. Lines, before and match shape the text of a UTF-8 answer, and
// evidenceGetText renders it; Lines and Before are never negative, which the handler refuses.
type evidenceGetArgs struct {
	Run    string `json:"run"              jsonschema:"the id of the evidence run"`
	Name   string `json:"name,omitempty"   jsonschema:"the artifact's repository-relative path; omitted for the run's log"`
	Stderr bool   `json:"stderr,omitempty" jsonschema:"read the run's stderr log instead of its stdout log; only without name"`
	Lines  int    `json:"lines,omitempty"  jsonschema:"how many lines to return, 40 by default"`
	Before int    `json:"before,omitempty" jsonschema:"return only lines numbered below this one, to page backward"`
	Match  string `json:"match,omitempty"  jsonschema:"return only lines that contain this text"`
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
			Description: "Return one artifact of an evidence run by its path, or the run's stdout log when name is omitted, " +
				"or its stderr log with stderr. UTF-8 bytes come back as numbered text lines: the last lines lines, " +
				"40 by default; before pages backward to lines numbered below it; match keeps only lines that contain it. " +
				"Other bytes come back as base64.",
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

// evidenceGetHandler answers evidence_get. Its negative lines or before is refused before the artifact is read, and the
// answer is the text evidenceGetText renders.
func evidenceGetHandler(evidence enginev1connect.EvidenceClient) ToolHandler {
	return func(ctx context.Context, call Call) (ToolResult, error) {
		var args evidenceGetArgs
		if err := decodeArguments(call.Arguments, &args); err != nil {
			return ToolResult{}, err
		}
		if args.Lines < 0 {
			return ToolResult{}, errors.New("lines: must not be negative")
		}
		if args.Before < 0 {
			return ToolResult{}, errors.New("before: must not be negative")
		}
		got, err := evidence.GetEvidenceArtifact(ctx, connect.NewRequest(&v1.GetEvidenceArtifactRequest{
			Run:    args.Run,
			Name:   args.Name,
			Stderr: args.Stderr,
		}))
		if err != nil {
			return toolFailure(err), nil
		}
		return ToolResult{Content: []Content{{Type: textContentType, Text: evidenceGetText(args, got.Msg)}}}, nil
	}
}
