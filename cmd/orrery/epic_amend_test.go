package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/stepmcp"
)

// amendItemJSON is the evidence item the amend tests ask R2 to hold, as the --item JSON gives it.
const amendItemJSON = `{"capture":["/usr/bin/go","test","-run","QuietStepRestart","./cmd/orreryd/"],"limit":600,` +
	`"conclusion":"a quiet step survives a restart","judge":"haiku-high-ro","kind":"test",` +
	`"tests":["cmd/orreryd/quiet_test.go"]}`

// TestAmendEpicCLISendsTheToolsRequest checks that `orrery epic amend` sends the request the epic_amend tool sends for
// the same input, with and without a reason, that the request carries the item as the JSON gave it, and that the result
// it prints is the tool's: the amendment and the epic.
func TestAmendEpicCLISendsTheToolsRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		args  []string
		input string
	}{
		{
			name:  "with a reason",
			args:  []string{"amend", epicTestID, "R2", "--item", amendItemJSON, "--reason", "the test moved"},
			input: `{"id":"` + epicTestID + `","requirement":"R2","item":` + amendItemJSON + `,"reason":"the test moved"}`,
		},
		{
			name:  "without a reason",
			args:  []string{"amend", epicTestID, "R2", "--item", amendItemJSON},
			input: `{"id":"` + epicTestID + `","requirement":"R2","item":` + amendItemJSON + `}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cliFake := newRecordingEpics()
			stdout, _, err := runEpicCommand(t, cliFake, nil, tc.args...)
			if err != nil {
				t.Fatalf("epicCommand(%v) = %v, want nil", tc.args, err)
			}

			mcpFake := newRecordingEpics()
			result, err := stepmcp.CallEpicTool(t.Context(), mcpFake, "epic_amend", json.RawMessage(tc.input))
			if err != nil {
				t.Fatalf("CallEpicTool(epic_amend) = %v, want nil", err)
			}
			if result.IsError {
				t.Fatalf("CallEpicTool(epic_amend) returned an error result: %v", result.Content)
			}

			got, want := cliFake.recorded(), mcpFake.recorded()
			if len(got) != 1 || len(want) != 1 || !proto.Equal(got[0], want[0]) {
				t.Fatalf("epicCommand(%v) sent %v, want the epic_amend tool's request %v", tc.args, got, want)
			}
			var item v1.EvidenceItem
			if err = protojson.Unmarshal([]byte(amendItemJSON), &item); err != nil {
				t.Fatalf("decode item: %v", err)
			}
			sent, ok := got[0].(*v1.AmendEvidenceRequest)
			if !ok || sent.GetRequirement() != "R2" || !proto.Equal(sent.GetItem(), &item) {
				t.Errorf("request = %v, want R2 with the item %v", got[0], &item)
			}
			var printed struct {
				Amendment struct {
					Requirement string `json:"requirement"`
					State       string `json:"state"`
				} `json:"amendment"`
			}
			if err = json.Unmarshal([]byte(stdout), &printed); err != nil {
				t.Fatalf("stdout %q is not the tool's JSON: %v", stdout, err)
			}
			if printed.Amendment.Requirement != "R2" || printed.Amendment.State != "checking" {
				t.Errorf("printed amendment = %+v, want R2 checking", printed.Amendment)
			}
		})
	}
}

// TestAmendEpicCLIRefusesABadFormAndCallsNothing checks each form `orrery epic amend` refuses as a usage error naming its
// form: no Requirement, no --item, an --item that is not a JSON object, and a stray argument.
func TestAmendEpicCLIRefusesABadFormAndCallsNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{"no Requirement", []string{"amend", epicTestID}},
		{"a flag in place of the Requirement", []string{"amend", epicTestID, "--item", amendItemJSON}},
		{"no item", []string{"amend", epicTestID, "R2"}},
		{"an item that is not JSON", []string{"amend", epicTestID, "R2", "--item", "capture go test"}},
		{"an item that is not an object", []string{"amend", epicTestID, "R2", "--item", `["go"]`}},
		{"an item that is null", []string{"amend", epicTestID, "R2", "--item", "null"}},
		{"a stray argument", []string{"amend", epicTestID, "R2", "--item", amendItemJSON, "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cliFake := newRecordingEpics()
			_, _, err := runEpicCommand(t, cliFake, nil, tc.args...)
			if _, ok := errors.AsType[usageError](err); !ok {
				t.Fatalf("epicCommand(%v) = %v, want a usage error", tc.args, err)
			}
			if !strings.Contains(err.Error(), epicAmendForm) {
				t.Errorf("usage error %q does not name the form %q", err, epicAmendForm)
			}
			if n := len(cliFake.recorded()); n != 0 {
				t.Errorf("epicCommand(%v) made %d calls, want none", tc.args, n)
			}
		})
	}
}
