package capture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

// Report is the outcome of checking one probe's stdout. Missing lists each expectation that matched
// fewer lines than it requires, in the order given. Unknown lists the message types the wire package
// does not declare, once each and sorted, as type or type/subtype.
type Report struct {
	Missing []Expectation
	Unknown []string
}

const (
	acceptTypeControlRequest  = "control_request"
	acceptTypeControlResponse = "control_response"
	acceptTypeResult          = "result"
	acceptTypeSystem          = "system"
	acceptSubtypeInit         = "init"
)

// acceptLine is one classified stdout line. Its type and subtype are what Expectation matching sees,
// and its raw bytes are what Expectation.Contains is matched against. Its unknown key is empty when the
// wire package declares the line.
type acceptLine struct {
	typ     string
	subtype string
	unknown string
	raw     []byte
}

// acceptEnvelope is the discriminator pair that every stdout line carries.
type acceptEnvelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

// Check reads stdout as newline-separated stream-json lines and reports which expectations it fails to
// meet and which message types the wire package does not declare. A single trailing empty line is not a
// line. Check errors, naming the 1-based line number, when a line is not a JSON object with a non-empty
// string type. An unknown type never makes Check fail.
func Check(stdout []byte, expect []Expectation) (Report, error) {
	lines, err := acceptLines(stdout)
	if err != nil {
		return Report{}, err
	}
	var report Report
	for _, want := range expect {
		if acceptMatches(lines, want) < max(want.Count, 1) {
			report.Missing = append(report.Missing, want)
		}
	}
	report.Unknown = acceptUnknown(lines)
	return report, nil
}

// SessionID returns the session_id of the first stdout line that is a system init message. It errors
// when no such line exists, when its session_id is missing or empty, or when a line of stdout is not a
// JSON object, naming the 1-based line number as Check does.
func SessionID(stdout []byte) (string, error) {
	lines, err := acceptLines(stdout)
	if err != nil {
		return "", err
	}
	for _, line := range lines {
		if line.typ == acceptTypeSystem && line.subtype == acceptSubtypeInit {
			return acceptInitSessionID(line.raw)
		}
	}
	return "", errors.New("capture: no system init line")
}

// acceptInitSessionID decodes a system init line through the wire package and returns its session_id.
func acceptInitSessionID(raw []byte) (string, error) {
	var message wire.SDKMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return "", fmt.Errorf("capture: decode system init: %w", err)
	}
	variant, err := message.Variant()
	if err != nil {
		return "", fmt.Errorf("capture: decode system init: %w", err)
	}
	init, ok := variant.(wire.SDKSystemMessage)
	if !ok {
		return "", fmt.Errorf("capture: decode system init: got %T", variant)
	}
	if init.SessionID == "" {
		return "", errors.New("capture: system init line has no session_id")
	}
	return init.SessionID, nil
}

// acceptLines classifies each line of stdout, dropping one trailing empty line.
func acceptLines(stdout []byte) ([]acceptLine, error) {
	raws := bytes.Split(stdout, []byte("\n"))
	if len(raws[len(raws)-1]) == 0 {
		raws = raws[:len(raws)-1]
	}
	lines := make([]acceptLine, 0, len(raws))
	for index, raw := range raws {
		line, err := acceptClassify(raw)
		if err != nil {
			return nil, fmt.Errorf("capture: line %d: %w", index+1, err)
		}
		line.raw = raw
		lines = append(lines, line)
	}
	return lines, nil
}

// acceptClassify decodes one stdout line into its type, subtype, and unknown key.
func acceptClassify(raw []byte) (acceptLine, error) {
	var envelope acceptEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return acceptLine{}, fmt.Errorf("not a JSON object: %w", err)
	}
	if envelope.Type == "" {
		return acceptLine{}, errors.New("missing non-empty string type")
	}
	switch envelope.Type {
	case acceptTypeControlRequest:
		return acceptControlRequest(raw)
	case acceptTypeControlResponse:
		return acceptControlResponse(raw)
	default:
		return acceptSDKLine(raw, envelope)
	}
}

// acceptControlRequest classifies a control_request line by its request subtype.
func acceptControlRequest(raw []byte) (acceptLine, error) {
	var request wire.SDKControlRequest
	if err := wire.UnmarshalControlJSON(raw, &request); err != nil {
		return acceptLine{}, fmt.Errorf("decode control_request: %w", err)
	}
	var body struct {
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(request.Request, &body); err != nil {
		return acceptLine{}, fmt.Errorf("decode control_request subtype: %w", err)
	}
	line := acceptLine{typ: acceptTypeControlRequest, subtype: body.Subtype}
	if !slices.Contains([]string{"can_use_tool", "elicitation", "request_user_dialog"}, body.Subtype) {
		line.unknown = "control_request/" + body.Subtype
	}
	return line, nil
}

// acceptControlResponse classifies a control_response line by its response subtype.
func acceptControlResponse(raw []byte) (acceptLine, error) {
	var response wire.SDKControlResponse
	if err := wire.UnmarshalControlJSON(raw, &response); err != nil {
		return acceptLine{}, fmt.Errorf("decode control_response: %w", err)
	}
	subtype := response.Response.Subtype
	line := acceptLine{typ: acceptTypeControlResponse, subtype: subtype}
	if subtype != "success" {
		line.unknown = "control_response/" + subtype
	}
	return line, nil
}

// acceptSDKLine classifies a line of the SDK message union. Variant returns UnknownMessage for a type,
// or a system or result subtype, that the wire package does not declare.
func acceptSDKLine(raw []byte, envelope acceptEnvelope) (acceptLine, error) {
	var message wire.SDKMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return acceptLine{}, fmt.Errorf("decode SDK message: %w", err)
	}
	variant, err := message.Variant()
	if err != nil {
		return acceptLine{}, fmt.Errorf("decode %s message: %w", envelope.Type, err)
	}
	line := acceptLine{typ: envelope.Type, subtype: envelope.Subtype}
	if _, unknown := variant.(wire.UnknownMessage); unknown {
		line.unknown = envelope.Type
		if envelope.Type == acceptTypeSystem || envelope.Type == acceptTypeResult {
			line.unknown += "/" + envelope.Subtype
		}
	}
	return line, nil
}

// acceptMatches counts the lines that satisfy the type, subtype, and text rule of want.
func acceptMatches(lines []acceptLine, want Expectation) int {
	count := 0
	for _, line := range lines {
		if line.typ != want.Type || (want.Subtype != "" && line.subtype != want.Subtype) {
			continue
		}
		if want.Contains != "" && !bytes.Contains(line.raw, []byte(want.Contains)) {
			continue
		}
		count++
	}
	return count
}

// acceptUnknown returns the distinct unknown keys of lines, sorted.
func acceptUnknown(lines []acceptLine) []string {
	var unknown []string
	for _, line := range lines {
		if line.unknown != "" {
			unknown = append(unknown, line.unknown)
		}
	}
	slices.Sort(unknown)
	return slices.Compact(unknown)
}
