package enginehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

// The paths the step facts tests change: a text file the step owns, a binary file it owns too, and a file it does not
// own.
const (
	stepFactsText    = stepDoneOwned
	stepFactsBlob    = "pkg/blob.bin"
	stepFactsOutside = "other/outside.txt"
)

// stepFactsRepo returns a repo whose start revision holds the owned text file with three lines.
func stepFactsRepo(t *testing.T) (dir, start string) {
	t.Helper()
	dir, _ = stepDoneRepo(t)
	start = stepGitTestCommit(t, dir, map[string]string{stepFactsText: "one\ntwo\nthree\n"})
	return dir, start
}

// stepFactsReturn is a return with status that names commit.
func stepFactsReturn(status, commit string) session.TurnEnded {
	raw := fmt.Sprintf(`{"status":%q,"commit":%q}`, status, commit)
	return session.TurnEnded{StructuredOutput: json.RawMessage(raw)}
}

// stepFactsWriting returns a configure func that makes the fixture's step an owned writing step in dir, starting at
// start, which owns the text and blob paths and runs no gate.
func stepFactsWriting(dir, start string) func(*stepRunConfig) {
	configure := stepDoneWriting(dir, start, nil, time.Minute, &fakeGatePools{})
	return func(cfg *stepRunConfig) {
		configure(cfg)
		cfg.owned = append(cfg.owned, stepFactsBlob)
	}
}

// stepFactsRun builds a fixture configured by configure, hands each of returns to its run in order, and returns once
// the run has ended.
func stepFactsRun(t *testing.T, configure func(*stepRunConfig), returns ...session.TurnEnded) *stepLoopFixture {
	t.Helper()
	f := newStepDoneFixture(t, configure)
	_, done := f.start(t)
	for _, ret := range returns {
		f.run.Return(ret)
	}
	waitRunEnd(t, done)
	return f
}

// stepFactsWantNone asserts that the row of f ends with end and holds no line counts.
func stepFactsWantNone(t *testing.T, f *stepLoopFixture, end string) {
	t.Helper()
	row := f.row(t)
	if row.End != end {
		t.Fatalf("end = %q, want %q", row.End, end)
	}
	if row.Lines != (store.StepLines{}) {
		t.Errorf("lines = %+v, want uncounted zeros", row.Lines)
	}
}

func TestStepEndFactsCountOwnedDone(t *testing.T) {
	t.Parallel()
	dir, start := stepFactsRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{
		stepFactsText: "one\nTWO\nthree\nfour\n",
		stepFactsBlob: "\x00binary\n",
	})
	f := stepFactsRun(t, stepFactsWriting(dir, start), stepFactsReturn(stepLoopDone, commit))

	row := f.row(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
	// The text file loses "two" and gains "TWO" and "four"; the binary file is one file with no lines.
	want := store.StepLines{Counted: true, Files: 2, Added: 2, Removed: 1}
	if row.Lines != want {
		t.Errorf("lines = %+v, want %+v", row.Lines, want)
	}
}

func TestStepEndFactsCountLaterReturnReplaces(t *testing.T) {
	t.Parallel()
	dir, start := stepFactsRepo(t)
	// The first commit changes the owned text and adds a file outside owned, so its owned check fails and it is sent
	// back.
	first := stepGitTestCommit(t, dir, map[string]string{
		stepFactsText:    "one\nTWO\nthree\nfour\n",
		stepFactsOutside: "stray\n",
	})
	// The second commit, which HEAD is when the run starts, removes that file and changes the owned text once more.
	stepGitTestRun(t, dir, "rm", "-q", stepFactsOutside)
	second := stepGitTestCommit(t, dir, map[string]string{stepFactsText: "one\nTWO\nthree\nfour\nfive\n"})
	f := stepFactsRun(t, stepFactsWriting(dir, start),
		stepFactsReturn(stepLoopDone, first), stepFactsReturn(stepLoopDone, second))

	if got := len(f.sessions.continuedTexts()); got != 1 {
		t.Fatalf("Continue called %d times, want 1 for the first return", got)
	}
	row := f.row(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
	// The second commit's counts replace the first's, which held two files and three added lines.
	want := store.StepLines{Counted: true, Files: 1, Added: 3, Removed: 1}
	if row.Lines != want {
		t.Errorf("lines = %+v, want the second commit's %+v", row.Lines, want)
	}
}

func TestStepEndFactsCountNone(t *testing.T) {
	t.Parallel()
	t.Run("read_only", func(t *testing.T) {
		t.Parallel()
		// The checkout holds a real change from start, so a read-only step that counted it would show counts.
		dir, start := stepFactsRepo(t)
		commit := stepGitTestCommit(t, dir, map[string]string{stepFactsText: "one\nTWO\nthree\n"})
		f := stepFactsRun(t, func(cfg *stepRunConfig) {
			cfg.writing = false
			cfg.dir = dir
			cfg.start = start
		}, stepFactsReturn(stepLoopDone, commit))
		stepFactsWantNone(t, f, stepLoopEndDone)
	})
	t.Run("unowned", func(t *testing.T) {
		t.Parallel()
		dir, start := stepFactsRepo(t)
		commit := stepGitTestCommit(t, dir, map[string]string{stepFactsText: "one\nTWO\nthree\n"})
		configure := stepDoneUnownedWriting(dir, start, nil, &fakeGatePools{})
		f := stepFactsRun(t, configure, stepFactsReturn(stepLoopDone, commit))
		stepFactsWantNone(t, f, stepLoopEndDone)
	})
	t.Run("not_done", func(t *testing.T) {
		t.Parallel()
		dir, start := stepFactsRepo(t)
		commit := stepGitTestCommit(t, dir, map[string]string{stepFactsText: "one\nTWO\nthree\n"})
		f := stepFactsRun(t, stepFactsWriting(dir, start), stepFactsReturn("PARTIAL", commit))
		stepFactsWantNone(t, f, stepLoopEndReturned)
	})
}

func TestStepEndFactsCountFailure(t *testing.T) {
	t.Parallel()
	failing := func(context.Context, string, string, string) (store.StepLines, error) {
		return store.StepLines{}, errors.New("boom")
	}
	t.Run("first_return", func(t *testing.T) {
		t.Parallel()
		dir, start := stepFactsRepo(t)
		commit := stepGitTestCommit(t, dir, map[string]string{stepFactsText: "one\nTWO\nthree\n"})
		var logged bytes.Buffer
		f := stepFactsRun(t, func(cfg *stepRunConfig) {
			stepFactsWriting(dir, start)(cfg)
			cfg.countLines = failing
			cfg.logger = slog.New(slog.NewTextHandler(&logged, nil))
		}, stepFactsReturn(stepLoopDone, commit))

		stepFactsWantNone(t, f, stepLoopEndDone)
		want := `level=WARN msg="count lines of step" step=` + stepLoopID + " err=boom"
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log %q does not hold the record %q", logged.String(), want)
		}
	})
	t.Run("later_return", func(t *testing.T) {
		t.Parallel()
		dir, start := stepFactsRepo(t)
		first := stepGitTestCommit(t, dir, map[string]string{
			stepFactsText:    "one\nTWO\nthree\n",
			stepFactsOutside: "stray\n",
		})
		stepGitTestRun(t, dir, "rm", "-q", stepFactsOutside)
		second := stepGitTestCommit(t, dir, map[string]string{stepFactsText: "one\nTWO\nthree\nfour\n"})
		// The first return's count succeeds and the second's fails, so only the second's failure can clear the counts.
		calls := 0
		counting := func(ctx context.Context, repo, base, rev string) (store.StepLines, error) {
			calls++
			if calls > 1 {
				return store.StepLines{}, errors.New("boom")
			}
			return stepGitLines(ctx, hostexec.NewLocal(), repo, base, rev)
		}
		var logged bytes.Buffer
		f := stepFactsRun(t, func(cfg *stepRunConfig) {
			stepFactsWriting(dir, start)(cfg)
			cfg.countLines = counting
			cfg.logger = slog.New(slog.NewTextHandler(&logged, nil))
		}, stepFactsReturn(stepLoopDone, first), stepFactsReturn(stepLoopDone, second))

		if calls != 2 {
			t.Fatalf("countLines ran %d times, want 2", calls)
		}
		stepFactsWantNone(t, f, stepLoopEndDone)
		want := `level=WARN msg="count lines of step" step=` + stepLoopID + " err=boom"
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log %q does not hold the record %q", logged.String(), want)
		}
	})
}
