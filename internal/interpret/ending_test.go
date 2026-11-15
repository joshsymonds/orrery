package interpret

import (
	"context"
	"strings"
	"testing"
	"time"
)

// endedEpic is one call a recordingEnding received.
type endedEpic struct {
	epic, phase, body string
	at                time.Time
}

// recordingEnding is a test Ending: it records each call in order and ends every epic it is asked to.
type recordingEnding struct {
	calls []endedEpic
}

// EndEpic records the call.
func (r *recordingEnding) EndEpic(_ context.Context, epic, phase, body string, at time.Time) error {
	r.calls = append(r.calls, endedEpic{epic: epic, phase: phase, body: body, at: at})
	return nil
}

// TestRecordingEndingRecordsThroughTheInterface drives the fake as an Ending, the way conclude and release will.
func TestRecordingEndingRecordsThroughTheInterface(t *testing.T) {
	t.Parallel()
	fake := &recordingEnding{}
	var ending Ending = fake
	at := time.Unix(1_700_000_000, 0)
	ctx := t.Context()
	if err := ending.EndEpic(ctx, decideEpic, "ended_with_gaps", "Epic ended with 1 gap.", at); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}
	if err := ending.EndEpic(ctx, decideEpic, "released", "Epic released.", at.Add(time.Minute)); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}
	got := make([]string, 0, len(fake.calls))
	for _, call := range fake.calls {
		got = append(got, call.epic+" "+call.phase+" "+call.body+" "+call.at.UTC().Format(time.RFC3339))
	}
	want := decideEpic + " ended_with_gaps Epic ended with 1 gap. 2023-11-14T22:13:20Z|" +
		decideEpic + " released Epic released. 2023-11-14T22:14:20Z"
	if joined := strings.Join(got, "|"); joined != want {
		t.Fatalf("calls = %q, want %q", joined, want)
	}
}
