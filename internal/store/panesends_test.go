package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// assertPaneSends fails unless the reader at path lists the sends to pane in [from, to] as exactly want, in order.
func assertPaneSends(t *testing.T, path, pane string, from, to int64, want []PaneSend) {
	t.Helper()
	got, err := NewReader(path).PaneSends(context.Background(), pane, from, to)
	if err != nil {
		t.Fatalf("PaneSends(%q, %d, %d): %v", pane, from, to, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PaneSends(%q, %d, %d):\n got %+v\nwant %+v", pane, from, to, got, want)
	}
}

func TestPromptClientPaneSendsReadBackByPaneAndRangeInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	record := func(text, client string, at int64) {
		t.Helper()
		if err := s.RecordPaneSend(ctx, "orrery-1", text, client, time.Unix(at, 0)); err != nil {
			t.Fatalf("RecordPaneSend %q: %v", text, err)
		}
	}
	record("late", "phone", 300)
	record("first", "web", 100)
	record("second", "phone", 100)
	record("middle", "web", 200)

	assertPaneSends(t, path, "orrery-1", 0, 1000, []PaneSend{
		{Text: "first", Client: "web", At: 100},
		{Text: "second", Client: "phone", At: 100},
		{Text: "middle", Client: "web", At: 200},
		{Text: "late", Client: "phone", At: 300},
	})
}

func TestPromptClientPaneSendsLeaveOtherPanesAndOutsideRangeAbsent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	for _, send := range []struct {
		pane, text string
		at         int64
	}{
		{"orrery-1", "before", 99},
		{"orrery-1", "from", 100},
		{"orrery-1", "to", 200},
		{"orrery-1", "after", 201},
		{"orrery-2", "other pane", 150},
	} {
		if err := s.RecordPaneSend(ctx, send.pane, send.text, "web", time.Unix(send.at, 0)); err != nil {
			t.Fatalf("RecordPaneSend %q: %v", send.text, err)
		}
	}

	assertPaneSends(t, path, "orrery-1", 100, 200, []PaneSend{
		{Text: "from", Client: "web", At: 100},
		{Text: "to", Client: "web", At: 200},
	})
}

func TestPromptClientPaneSendsCutTextAndClientAtRuneStart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	at := time.Unix(10, 0)
	for _, tc := range []struct {
		name, text, client, wantText, wantClient string
	}{
		{
			"long ascii", strings.Repeat("a", 5000), strings.Repeat("c", 100),
			strings.Repeat("a", 4096), strings.Repeat("c", 64),
		},
		// The two-byte é spans bytes 4095 and 4096, so the cut at 4096 falls inside it and drops it.
		{"text rune split at the cap", strings.Repeat("a", 4095) + "é" + "b", "web", strings.Repeat("a", 4095), "web"},
		// The é spans bytes 4094 and 4095, so it fits whole below the cap.
		{"text rune ends at the cap", strings.Repeat("a", 4094) + "é" + "bc", "web", strings.Repeat("a", 4094) + "é", "web"},
		// The é spans bytes 63 and 64 of the client, so the cut at 64 drops it.
		{"client rune split at the cap", "hi", strings.Repeat("c", 63) + "é" + "d", "hi", strings.Repeat("c", 63)},
	} {
		if err := s.RecordPaneSend(ctx, tc.name, tc.text, tc.client, at); err != nil {
			t.Fatalf("RecordPaneSend %s: %v", tc.name, err)
		}
		assertPaneSends(t, path, tc.name, 0, 100, []PaneSend{{Text: tc.wantText, Client: tc.wantClient, At: 10}})
	}
}

func TestPromptClientPaneSendsRefuseEmptyPaneTextOrClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	at := time.Unix(1700000000, 0)
	for _, tc := range []struct {
		name, pane, text, client string
	}{
		{"empty pane", "", "hello", "web"},
		{"empty text", "orrery-1", "", "web"},
		{"empty client", "orrery-1", "hello", ""},
	} {
		if err := s.RecordPaneSend(ctx, tc.pane, tc.text, tc.client, at); !errors.Is(err, ErrEmptyPaneSend) {
			t.Errorf("RecordPaneSend with %s: got %v, want ErrEmptyPaneSend", tc.name, err)
		}
	}
	const count = `SELECT COUNT(*) FROM pane_sends`
	var rows int
	if err := s.db.QueryRowContext(ctx, count).Scan(&rows); err != nil {
		t.Fatalf("count pane sends: %v", err)
	}
	if rows != 0 {
		t.Fatalf("pane sends after the refusals = %d rows, want none", rows)
	}
}

func TestPromptClientPaneSendsMissingStoreIsUnavailable(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "missing")
	path := filepath.Join(dir, "orrery.db")

	_, err := NewReader(path).PaneSends(context.Background(), "orrery-1", 0, 100)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("PaneSends of missing store: got %v, want ErrUnavailable", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store's directory was created or unreadable: %v", statErr)
	}
}
