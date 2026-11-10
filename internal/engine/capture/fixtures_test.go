package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/wire"
)

// fixtureDir holds the recorded probe fixtures, relative to this package.
const fixtureDir = "testdata"

// TestFixturesDecodeAsWireMessages checks that the recorded fixtures hold: every probe has both fixture
// files, every line decodes to a typed wire message, each stdout fixture meets its probe's expectations
// with no unknown types, and every init line names the same non-empty claude_code_version.
func TestFixturesDecodeAsWireMessages(t *testing.T) {
	t.Parallel()
	if problems := fixtureProblems(fixtureDir); len(problems) > 0 {
		t.Errorf("recorded fixtures in %s are invalid:\n%s", fixtureDir, strings.Join(problems, "\n"))
	}
}

// fixtureProblems returns each way the fixtures in dir fail, or nil when they hold.
func fixtureProblems(dir string) []string {
	var problems, versions []string
	for _, probe := range Probes() {
		for _, suffix := range []string{".in.ndjson", ".out.ndjson"} {
			name := probe.Name + suffix
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: read: %v", name, err))
				continue
			}
			fileVersions, lineProblems := fixtureLines(name, data)
			problems = append(problems, lineProblems...)
			versions = append(versions, fileVersions...)
			if suffix == ".out.ndjson" {
				if len(fileVersions) == 0 {
					problems = append(problems, name+": no system/init line")
				}
				problems = append(problems, fixtureStdout(name, data, probe.Expect)...)
			}
		}
	}
	slices.Sort(versions)
	if distinct := slices.Compact(versions); len(distinct) > 1 {
		problems = append(problems, fmt.Sprintf("fixtures disagree on claude_code_version: %q", distinct))
	}
	return problems
}

// fixtureLines decodes each line of one fixture to a typed wire message. It returns the non-empty
// claude_code_version of each system/init line, and one problem for each line that fails to decode,
// is undeclared, or is an init line with an empty version.
func fixtureLines(name string, data []byte) ([]string, []string) {
	var versions, problems []string
	raws := bytes.Split(data, []byte("\n"))
	if len(raws[len(raws)-1]) == 0 {
		raws = raws[:len(raws)-1]
	}
	for index, raw := range raws {
		system, isInit, err := fixtureDecodeLine(raw)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s:%d: %v", name, index+1, err))
			continue
		}
		if !isInit {
			continue
		}
		if system.ClaudeCodeVersion == "" {
			problems = append(problems, fmt.Sprintf("%s:%d: init line has empty claude_code_version", name, index+1))
			continue
		}
		versions = append(versions, system.ClaudeCodeVersion)
	}
	return versions, problems
}

// fixtureDecodeLine decodes one fixture line to its typed wire message. isInit reports whether the
// line is a system/init message, in which case system holds it.
func fixtureDecodeLine(raw []byte) (system wire.SDKSystemMessage, isInit bool, err error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return system, false, fmt.Errorf("not a JSON object: %w", err)
	}
	switch envelope.Type {
	case "control_request":
		return system, false, fixtureDecodeControlRequest(raw)
	case "control_response":
		return system, false, fixtureDecodeControlResponse(raw)
	}
	var message wire.SDKMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return system, false, fmt.Errorf("decode SDK message: %w", err)
	}
	variant, err := message.Variant()
	if err != nil {
		return system, false, fmt.Errorf("decode %s message: %w", envelope.Type, err)
	}
	if _, unknown := variant.(wire.UnknownMessage); unknown {
		return system, false, fmt.Errorf("type %q is not declared", envelope.Type)
	}
	system, isInit = variant.(wire.SDKSystemMessage)
	return system, isInit, nil
}

// fixtureDecodeControlRequest decodes a control_request line by its request subtype. The interrupt
// request carries only its subtype.
func fixtureDecodeControlRequest(raw []byte) error {
	var request wire.SDKControlRequest
	if err := wire.UnmarshalControlJSON(raw, &request); err != nil {
		return fmt.Errorf("decode control_request: %w", err)
	}
	var body struct {
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal(request.Request, &body); err != nil {
		return fmt.Errorf("decode control_request subtype: %w", err)
	}
	switch body.Subtype {
	case "can_use_tool":
		var permission wire.SDKControlPermissionRequest
		if err := wire.UnmarshalControlJSON(request.Request, &permission); err != nil {
			return fmt.Errorf("decode can_use_tool: %w", err)
		}
	case "initialize":
		var initialize wire.SDKControlInitializeRequest
		if err := wire.UnmarshalControlJSON(request.Request, &initialize); err != nil {
			return fmt.Errorf("decode initialize: %w", err)
		}
	case "set_permission_mode":
		var mode wire.SDKControlSetPermissionModeRequest
		if err := wire.UnmarshalControlJSON(request.Request, &mode); err != nil {
			return fmt.Errorf("decode set_permission_mode: %w", err)
		}
	case "interrupt":
	default:
		return fmt.Errorf("control_request subtype %q is not declared", body.Subtype)
	}
	return nil
}

// fixtureDecodeControlResponse decodes a control_response line, which must be a success response.
func fixtureDecodeControlResponse(raw []byte) error {
	var response wire.SDKControlResponse
	if err := wire.UnmarshalControlJSON(raw, &response); err != nil {
		return fmt.Errorf("decode control_response: %w", err)
	}
	if response.Response.Subtype != "success" {
		return fmt.Errorf("control_response subtype %q is not declared", response.Response.Subtype)
	}
	return nil
}

// fixtureStdout checks one stdout fixture against its probe's expectations and reports unknown types.
func fixtureStdout(name string, data []byte, expect []Expectation) []string {
	report, err := Check(data, expect)
	if err != nil {
		return []string{fmt.Sprintf("%s: check: %v", name, err)}
	}
	var problems []string
	for _, want := range report.Missing {
		problems = append(problems, fmt.Sprintf("%s: missing expectation %+v", name, want))
	}
	if len(report.Unknown) > 0 {
		problems = append(problems, fmt.Sprintf("%s: unknown types %q", name, report.Unknown))
	}
	return problems
}
