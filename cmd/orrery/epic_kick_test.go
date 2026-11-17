package main

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestEpicKickSendsTheIDAndTheTextInOneCall checks that `orrery epic kick ID TEXT` makes one KickEpic call carrying the
// id and the text as given, and prints the kicked epic on stdout with nothing on stderr.
func TestEpicKickSendsTheIDAndTheTextInOneCall(t *testing.T) {
	t.Parallel()
	const text = `Try the "sqlite" plan, not JSON.`
	cliFake := newRecordingEpics()

	stdout, stderr, err := runEpicCommand(t, cliFake, nil, "kick", epicTestID, text)
	if err != nil {
		t.Fatalf("epic kick: %v", err)
	}
	requests := cliFake.recorded()
	if len(requests) != 1 {
		t.Fatalf("epic kick made %d calls, want 1", len(requests))
	}
	want := &v1.KickEpicRequest{Id: epicTestID, Text: text}
	if got, ok := requests[0].(*v1.KickEpicRequest); !ok || !proto.Equal(got, want) {
		t.Fatalf("KickEpic request = %v, want %v", requests[0], want)
	}
	var printed v1.StoredEpic
	if err = protojson.Unmarshal([]byte(strings.TrimSpace(stdout)), &printed); err != nil {
		t.Fatalf("stdout %q is not the kicked epic: %v", stdout, err)
	}
	if !proto.Equal(&printed, cliFake.epic) {
		t.Errorf("printed epic = %v, want %v", &printed, cliFake.epic)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// TestEpicKickNamesItsFormInTheUsage checks that the usage text and the forms of `orrery epic` name `epic kick ID TEXT`.
func TestEpicKickNamesItsFormInTheUsage(t *testing.T) {
	t.Parallel()
	if !strings.Contains(epicForms, "kick ID TEXT") {
		t.Errorf("epicForms = %q, want it to name kick ID TEXT", epicForms)
	}
	if !strings.Contains(usageText, "epic kick ID TEXT") {
		t.Errorf("usage text does not name epic kick ID TEXT")
	}
}
