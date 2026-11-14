package route_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
)

const testEpic = "synthetic-epic"

// The lines a route writes, as the notice log holds them. The epic lines name the epic and carry no pane or session.
const (
	paneAwayLine   = `{"time":1700000000,"host":"synthetic-host","pane":"%synthetic-pane","state":"waiting","route":"desk+phone","reason":"away"}`
	epicAwayLine   = `{"time":1700000000,"host":"synthetic-host","epic":"synthetic-epic","state":"waiting","route":"desk+phone","reason":"away"}`
	epicResentLine = `{"time":1700000000,"host":"synthetic-host","epic":"synthetic-epic","state":"waiting","route":"phone","reason":"resent after restart"}`
)

// epicNotice is a sourced notice keyed by testEpic, with no pane or session. Its desk and push builds succeed.
func epicNotice() route.Notice {
	return route.Notice{
		Key:    notice.Key{Epic: testEpic},
		State:  stateWaiting,
		Source: route.Source{Kind: testSourceKind, Data: []byte(testSourceData)},
		Desk: func(context.Context) (deskstream.Notice, error) {
			return deskPost(stateWaiting), nil
		},
		Push: func(context.Context) (push.Notification, error) {
			return push.Notification{Epic: testEpic, Summary: testSummary, State: protocol.State(stateWaiting)}, nil
		},
	}
}

// lineJSON is line as the notice log writes it.
func lineJSON(t *testing.T, line route.Line) string {
	t.Helper()
	data, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("encode line: %v", err)
	}
	return string(data)
}

// linesOf returns the lines f has recorded so far.
func linesOf(f *fake) []route.Line {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.lines)
}

// checkOnlyLine asserts that lines holds exactly one line and that the line is encoded as want.
func checkOnlyLine(t *testing.T, lines []route.Line, want string) {
	t.Helper()
	if len(lines) != 1 {
		t.Fatalf("lines = %+v, want exactly one", lines)
	}
	if got := lineJSON(t, lines[0]); got != want {
		t.Errorf("line = %s, want %s", got, want)
	}
}

func TestPaneLineHasNoEpicKey(t *testing.T) {
	f := newFake()
	r := newRouter(t, f)

	r.Notify(t.Context(), f.notice(testPane, stateWaiting, false))
	r.Wait()

	checkOnlyLine(t, linesOf(f), paneAwayLine)
}

func TestEpicNoticeWritesLineNamingEpicAndStoresRow(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openStore(t, path)
	f := newFake()
	r := newRestartRouter(t, f, nil)
	r.Attach(ctx, s)

	r.Notify(ctx, epicNotice())
	r.Wait()

	checkOnlyLine(t, linesOf(f), epicAwayLine)
	rows, err := s.Notices(ctx)
	if err != nil {
		t.Fatalf("list notices: %v", err)
	}
	if len(rows) != 1 || rows[0].Epic != testEpic || rows[0].Pane != "" || rows[0].Session != "" {
		t.Errorf("rows = %+v, want one row keyed by epic %q alone", rows, testEpic)
	}
}

func TestEpicNoticeSurvivesRestartUnderItsEpic(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openStore(t, path)
	first := newFake()
	r1 := newRestartRouter(t, first, nil)
	r1.Attach(ctx, s)
	r1.Notify(ctx, epicNotice())
	r1.Wait()
	reopened := reopenStore(t, s, path)

	restarted := newFake()
	rebuilds := map[string]route.Rebuild{testSourceKind: rebuildAs(epicNotice())}
	r2 := restoreAt(t, restarted, rebuilds, reopened)
	r2.Wait()

	checkOnlyLine(t, linesOf(restarted), epicResentLine)
	rows, err := reopened.Notices(ctx)
	if err != nil {
		t.Fatalf("list notices: %v", err)
	}
	if len(rows) != 1 || rows[0].Epic != testEpic {
		t.Errorf("rows = %+v, want one row still keyed by epic %q", rows, testEpic)
	}
}
