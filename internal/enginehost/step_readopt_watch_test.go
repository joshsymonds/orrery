package enginehost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// readoptWatchStore stores a writing step whose session ran a tool call and said one line, then ended its turn not
// done, and returns the store and the step's row. When background is set, a background task was listed at that turn
// end and still is.
func readoptWatchStore(t *testing.T, at time.Time, background bool) (*store.Store, store.StepRow) {
	t.Helper()
	s, _ := openStore(t)
	output := `{"status":"BLOCKED"}`
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: stepLoopID, Dir: "/work", State: "working", Kind: store.KindStep},
		store.StepAdd{
			ID: stepLoopID, Agent: "implementer", Prompt: "do it", Schema: stepLoopSchema, Workspace: "/ws",
			Revision: "abc123", DoneValue: stepLoopDone, Owned: []string{"a.go"},
		},
		store.EnginePrompt{ID: stepLoopID, Text: "do it"},
		store.EngineBlock{ID: stepLoopID, Kind: "tool_call", Name: "Bash", Input: `{"command":"go test ./..."}`},
		store.EngineBlock{ID: stepLoopID, Kind: "text", Text: "blocked on the flaky test"},
		store.EngineTurnEnded{ID: stepLoopID, Subtype: "success"},
		store.EngineConsumed{ID: stepLoopID, Consumed: 4, Output: &output, Background: &background},
	}, at); err != nil {
		t.Fatalf("store the step and its session: %v", err)
	}
	row, err := s.Step(t.Context(), stepLoopID)
	if err != nil {
		t.Fatalf("read step row: %v", err)
	}
	return s, row
}

// A step whose turn ended while a background task was listed is restored from that turn end, flagged, and not as a
// running turn, and its watch keeps the blocks its session stored, so a later trip is judged from them.
func TestReadoptRunRestoresBackgroundTurnEndAndSeedsWatch(t *testing.T) {
	t.Parallel()
	at := time.Unix(1_700_000_000, 0)
	for _, background := range []bool{true, false} {
		s, row := readoptWatchStore(t, at, background)
		steps := &Steps{store: s, now: func() time.Time { return at.Add(time.Minute) }}

		run, err := steps.readoptRun(t.Context(), row)
		if err != nil {
			t.Fatalf("readoptRun (background %v): %v", background, err)
		}
		restore := run.restore
		if !restore.Found || restore.Turn.Background != background || restore.Turn.Output != `{"status":"BLOCKED"}` {
			t.Fatalf("restore (background %v) = found %v turn %+v, want the stored turn end found with background %v",
				background, restore.Found, restore.Turn, background)
		}
		want := "1m0s ago call Bash: {\"command\":\"go test ./...\"}\n1m0s ago text: blocked on the flaky test"
		if got := run.watch.recent(at.Add(time.Minute)); got != want {
			t.Fatalf("recent (background %v):\n%s\nwant:\n%s", background, got, want)
		}
	}
}

// readoptBlocksFail is a StepStore with no turn end whose blocks cannot be read.
type readoptBlocksFail struct{ readoptTurnStore }

// StepBlocks fails.
func (readoptBlocksFail) StepBlocks(context.Context, string, int) ([]store.StepBlock, error) {
	return nil, errors.New("events table unreadable")
}

func TestReadoptRunFailsWhenBlocksCannotBeRead(t *testing.T) {
	t.Parallel()
	steps := &Steps{store: readoptBlocksFail{}}
	row := store.StepRow{ID: stepLoopID, Workspace: "/ws", Owned: []string{"a.go"}}
	if _, err := steps.readoptRun(t.Context(), row); err == nil {
		t.Fatal("readoptRun with unreadable blocks: nil error, want the read's error")
	}
}
