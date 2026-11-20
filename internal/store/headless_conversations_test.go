package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestStepEndFactsHeadlessListSkipsStepSessionWithRecentEvents pins that a listed headless row is a conversation: an
// engine session with a recent event is listed, and a step session with recent events is not.
func TestStepEndFactsHeadlessListSkipsStepSessionWithRecentEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	recent := engineNowAt().Add(-time.Hour)
	cutoff := engineNowAt().Add(-14 * 24 * time.Hour).Unix()
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e-talk", Dir: "/t", State: "idle", Kind: KindEngine},
		EngineBlock{ID: "e-talk", Kind: "text", Role: "assistant", MessageID: "m0", Text: "talk reply"},
		EngineAdd{ID: "step-end", Dir: "/s", State: "idle", Kind: KindStep},
		EngineBlock{ID: "step-end", Kind: "text", Role: "assistant", MessageID: "m1", Text: "step reply"},
	}, recent); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, err := NewReader(path).HeadlessSessions(ctx, cutoff)
	if err != nil {
		t.Fatalf("HeadlessSessions: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, row := range got {
		ids = append(ids, row.ID)
	}
	if want := []string{"e-talk"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("HeadlessSessions ids:\n got %v\nwant %v", ids, want)
	}
}
