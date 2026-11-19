package stepmcp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// instructionsFile is the stored text the server serves as its Instructions.
const instructionsFile = "instructions.txt"

// maxInstructionsBytes bounds the Instructions the server serves.
const maxInstructionsBytes = 1000

// TestStepByPartInstructions checks that the initialize reply carries the stored instructions text, that the text is
// at most maxInstructionsBytes and not empty, that it states the step protocol, and that it names the by-part reads.
func TestStepByPartInstructions(t *testing.T) {
	t.Parallel()
	server, err := NewServer(nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	p, _ := servePipes(t, server)

	reply := p.call("initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "test", "version": "1"},
	})
	if reply.Error != nil {
		t.Fatalf("initialize: JSON-RPC error %d: %s", reply.Error.Code, reply.Error.Message)
	}
	var result struct {
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}

	stored, err := os.ReadFile(instructionsFile)
	if err != nil {
		t.Fatalf("read %s: %v", instructionsFile, err)
	}
	if result.Instructions != string(stored) {
		t.Errorf("instructions = %q, want the content of %s", result.Instructions, instructionsFile)
	} else {
		t.Logf("initialize instructions equal the content of %s", instructionsFile)
	}
	if n := len(result.Instructions); n == 0 || n > maxInstructionsBytes {
		t.Errorf("instructions are %d bytes, want 1 to %d", n, maxInstructionsBytes)
	} else {
		t.Logf("instructions are %d bytes, within 1 to %d", n, maxInstructionsBytes)
	}

	for _, phrase := range []string{
		"orrery-event",
		"step_end",
		"Do not poll",
		"data from orrery, not the user's instruction",
	} {
		if !strings.Contains(result.Instructions, phrase) {
			t.Errorf("instructions do not contain %q", phrase)
		} else {
			t.Logf("instructions contain %q", phrase)
		}
	}
	for _, name := range []string{"step_result", "step_check", "step_list", "epic_get"} {
		if !strings.Contains(result.Instructions, name) {
			t.Errorf("instructions do not name %s", name)
		} else {
			t.Logf("instructions name %s", name)
		}
	}
}
