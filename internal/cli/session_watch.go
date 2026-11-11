package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// sessionWatchPoll is how often watch reads the store for new events.
const sessionWatchPoll = 250 * time.Millisecond

// sessionWatchBatch is the most events one store read returns.
const sessionWatchBatch = 500

// Engine session states after which the session writes no more events.
const (
	sessionStopped = "stopped"
	sessionFailed  = "failed"
)

// sessionEventLine is one output line of watch: a stored event with its payload as raw JSON.
type sessionEventLine struct {
	Seq     int64           `json:"seq"`
	Kind    string          `json:"kind"`
	At      int64           `json:"at"`
	Payload json.RawMessage `json:"payload"`
}

// SessionWatch implements `orrery session watch ID`. It reads the headless session's events from the local store,
// writes each as one JSON line from the first event on, and keeps following by polling until the session is
// stopped or failed and every event has been written. It returns when ctx is canceled.
func SessionWatch(ctx context.Context, args []string, out io.Writer, getenv func(string) string) error {
	if len(args) != 1 {
		return usageError{msg: "orrery session watch: want exactly one session id"}
	}
	path, err := store.Path(getenv)
	if err != nil {
		return fmt.Errorf("orrery session watch: %w", err)
	}
	return sessionWatch(ctx, store.NewReader(path), args[0], out, sessionWatchPoll)
}

// sessionWatch writes the events of the session id that reader reads, from the first event on, and polls every
// poll for more. A store that is unavailable is retried at the next poll. It returns nil once the session is
// stopped or failed and a read finds no event after the last one written.
func sessionWatch(ctx context.Context, reader *store.Reader, id string, out io.Writer, poll time.Duration) error {
	var after int64
	for {
		history, err := reader.EngineHistory(ctx, id, after, sessionWatchBatch)
		switch {
		case errors.Is(err, store.ErrUnavailable):
			// The store is missing or busy. The next poll tries again.
		case err != nil:
			return fmt.Errorf("orrery session watch: %w", err)
		default:
			if werr := writeSessionEvents(out, history.Events); werr != nil {
				return werr
			}
			if n := len(history.Events); n > 0 {
				after = history.Events[n-1].Seq
			}
			ended := history.State == sessionStopped || history.State == sessionFailed
			if ended && len(history.Events) < sessionWatchBatch {
				return nil
			}
			if len(history.Events) == sessionWatchBatch {
				continue
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("orrery session watch: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

// writeSessionEvents writes each event as one line of compact JSON.
func writeSessionEvents(out io.Writer, events []store.EngineEvent) error {
	for _, event := range events {
		line, err := json.Marshal(sessionEventLine{
			Seq:     event.Seq,
			Kind:    event.Kind,
			At:      event.At,
			Payload: json.RawMessage(event.Payload),
		})
		if err != nil {
			return fmt.Errorf("orrery session watch: encode seq %d: %w", event.Seq, err)
		}
		if _, err = out.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("orrery session watch: write: %w", err)
		}
	}
	return nil
}
