package build

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// isHex reports whether s is exactly digits lowercase hex digits.
func isHex(s string, digits int) bool {
	return len(s) == digits && strings.Trim(s, "0123456789abcdef") == ""
}

// taskView returns the view of open task 3 of epic acme/epic at build b.
func taskView(b store.TaskBuild) View {
	return View{Epic: "acme/epic", Task: store.EpicTask{Seq: 3, Build: b}}
}

func TestStepIDIsHexAndStable(t *testing.T) {
	t.Parallel()
	got := StepID("acme/epic", 3, StageImplement, 1, 0, 0, 0)
	if !isHex(got, idLength) {
		t.Fatalf("StepID = %q, want %d lowercase hex digits", got, idLength)
	}
	if again := StepID("acme/epic", 3, StageImplement, 1, 0, 0, 0); again != got {
		t.Errorf("StepID for equal inputs = %q, then %q", got, again)
	}
}

func TestStepIDDiffersWhenAnyInputDiffers(t *testing.T) {
	t.Parallel()
	base := StepID("acme/epic", 3, StageImplement, 1, 0, 0, 0)
	variants := []struct {
		name string
		id   string
	}{
		{"epic", StepID("acme/other", 3, StageImplement, 1, 0, 0, 0)},
		{"seq", StepID("acme/epic", 4, StageImplement, 1, 0, 0, 0)},
		{"unit", StepID("acme/epic", 3, StageReview, 1, 0, 0, 0)},
		{"attempt", StepID("acme/epic", 3, StageImplement, 2, 0, 0, 0)},
		{"rerun", StepID("acme/epic", 3, StageImplement, 1, 1, 0, 0)},
		{"gate position", StepID("acme/epic", 3, StageImplement, 1, 0, 1, 0)},
		{"finding position", StepID("acme/epic", 3, StageImplement, 1, 0, 0, 1)},
	}
	for _, v := range variants {
		if v.id == base {
			t.Errorf("StepID with a different %s = base %q, want a different id", v.name, base)
		}
	}
}

func TestRunIDIsHexAndStable(t *testing.T) {
	t.Parallel()
	got := RunID("acme/epic", 3, StageGreen, 1, 0)
	if !isHex(got, idLength) {
		t.Fatalf("RunID = %q, want %d lowercase hex digits", got, idLength)
	}
	if again := RunID("acme/epic", 3, StageGreen, 1, 0); again != got {
		t.Errorf("RunID for equal inputs = %q, then %q", got, again)
	}
}

func TestRunIDDiffersWhenAnyInputDiffers(t *testing.T) {
	t.Parallel()
	base := RunID("acme/epic", 3, StageGreen, 1, 0)
	variants := []struct {
		name string
		id   string
	}{
		{"epic", RunID("acme/other", 3, StageGreen, 1, 0)},
		{"seq", RunID("acme/epic", 4, StageGreen, 1, 0)},
		{"phase", RunID("acme/epic", 3, StageRed, 1, 0)},
		{"attempt", RunID("acme/epic", 3, StageGreen, 2, 0)},
		{"rerun", RunID("acme/epic", 3, StageGreen, 1, 1)},
	}
	for _, v := range variants {
		if v.id == base {
			t.Errorf("RunID with a different %s = base %q, want a different id", v.name, base)
		}
	}
}

func TestTaskDirIsHexNameUnderTaskWorktrees(t *testing.T) {
	t.Parallel()
	dir := TaskDir("/state", "acme/epic", 3)
	if got, want := filepath.Dir(dir), filepath.Join("/state", "task-worktrees"); got != want {
		t.Errorf("TaskDir parent = %q, want %q", got, want)
	}
	if name := filepath.Base(dir); !isHex(name, taskDirLength) {
		t.Errorf("TaskDir name = %q, want %d lowercase hex digits", name, taskDirLength)
	}
	if again := TaskDir("/state", "acme/epic", 3); again != dir {
		t.Errorf("TaskDir for equal inputs = %q, then %q", dir, again)
	}
	if other := TaskDir("/state", "acme/epic", 4); other == dir {
		t.Errorf("TaskDir for seq 4 = TaskDir for seq 3 = %q", dir)
	}
	if other := TaskDir("/state", "acme/other", 3); other == dir {
		t.Errorf("TaskDir for epic acme/other = TaskDir for acme/epic = %q", dir)
	}
}

func TestSpendFirstAttemptMovesToSecond(t *testing.T) {
	t.Parallel()
	v := taskView(store.TaskBuild{
		Stage: StageGreen, Base: "base", Attempt: 1, Rerun: 2, NoVerdict: 1, Step: "step", RedRun: "red-run",
		GreenRun: "green-run", Revision: "rev", Refs: "refs", Failure: "old",
	})
	got := spend(v, "the judge found a defect")
	want := Action{Kind: ActFail, Reason: "the judge found a defect"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("spend on attempt 1 = %+v, want %+v", got, want)
	}
}

func TestSpendSecondAttemptFails(t *testing.T) {
	t.Parallel()
	v := taskView(store.TaskBuild{Stage: StageGreen, Base: "base", Attempt: 2, RedRun: "red-run", Failure: "old"})
	got := spend(v, "the judge found a defect")
	want := Action{Kind: ActFail, Reason: "the judge found a defect"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("spend on attempt 2 = %+v, want %+v", got, want)
	}
}

func TestSilentRerunFirstReruns(t *testing.T) {
	t.Parallel()
	v := taskView(store.TaskBuild{Stage: StageGreen, Base: "base", Attempt: 1, Rerun: 1, Revision: "rev"})
	got, again := silentRerun(v)
	want := Action{Kind: ActMove, Next: store.TaskBuild{
		Stage: StageGreen, Base: "base", Attempt: 1, Rerun: 2, NoVerdict: 1, Revision: "rev",
	}}
	if !again || !reflect.DeepEqual(got, want) {
		t.Errorf("silentRerun with no_verdict 0 = %+v, %t, want %+v, true", got, again, want)
	}
}

// TestSilentRerunSecondLeavesTheUnitToDecide: a second no verdict in a row is not rerun; the unit decides what it means,
// so nothing parks.
func TestSilentRerunSecondLeavesTheUnitToDecide(t *testing.T) {
	t.Parallel()
	v := taskView(store.TaskBuild{Stage: StageGreen, Base: "base", Attempt: 1, Rerun: 1, NoVerdict: 1})
	got, again := silentRerun(v)
	if again || !reflect.DeepEqual(got, Action{}) {
		t.Errorf("silentRerun with no_verdict 1 = %+v, %t, want the zero action and false", got, again)
	}
}

func TestNewFillsDefaults(t *testing.T) {
	t.Parallel()
	runner := New(Config{})
	if runner.tick != 5*time.Second {
		t.Errorf("default tick = %v, want 5s", runner.tick)
	}
	for _, tick := range []time.Duration{0, -time.Second} {
		if got := New(Config{Tick: tick}).tick; got != 5*time.Second {
			t.Errorf("tick %v = %v, want the 5s default", tick, got)
		}
	}
	if got := New(Config{Tick: 2 * time.Second}).tick; got != 2*time.Second {
		t.Errorf("configured tick = %v, want 2s", got)
	}
	if age := time.Since(runner.now()); age < 0 || age > time.Minute {
		t.Errorf("default clock is %v before now, want time.Now", age)
	}
	if got := runner.getenv("PATH"); got == "" || got != os.Getenv("PATH") {
		t.Errorf("default getenv(PATH) = %q, want os.Getenv's %q", got, os.Getenv("PATH"))
	}
	if runner.logger.Enabled(context.Background(), slog.LevelError) {
		t.Error("default logger is enabled at error level, want a discarding logger")
	}
	fixed := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if got := New(Config{Now: func() time.Time { return fixed }}).now(); !got.Equal(fixed) {
		t.Errorf("configured clock = %v, want %v", got, fixed)
	}
	logger := slog.New(slog.DiscardHandler)
	if got := New(Config{Logger: logger}).logger; got != logger {
		t.Errorf("configured logger = %p, want %p", got, logger)
	}
	getenv := func(string) string { return "configured" }
	if got := New(Config{Getenv: getenv}).getenv("ANY"); got != "configured" {
		t.Errorf("configured getenv = %q, want %q", got, "configured")
	}
}
