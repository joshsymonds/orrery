package build

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/store"
)

func TestCutMiddleKeepsBothEndsAndSaysWhatItCut(t *testing.T) {
	t.Parallel()
	if got := cutMiddle("short", 10); got != "short" {
		t.Errorf("cutMiddle(short) = %q, want it whole", got)
	}
	got := cutMiddle("aaaaXXXXXXXXbbbb", 8)
	if got != "aaaa\n[8 bytes cut]\nbbbb" {
		t.Errorf("cutMiddle = %q, want both ends with the cut named", got)
	}
}

func TestRequestedLinesNameEachPathOnceWithItsHolder(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	repo := setup.epic.Repository
	base := commitOff(t, repo, setup.base, "present.go", "package present\n")
	task := store.EpicTask{Seq: 1, Files: []string{"mine.go"}, Status: taskOpen, Build: store.TaskBuild{Base: base}}
	peers := []Peer{{Task: task}, openPeer(2, "owned.go"), acceptedPeer(3, "changed.go")}
	got := requestedLines(
		context.Background(), hostexec.NewLocal(), repo, task, []string{"present.go", "owned.go", ".git/x"},
		[]string{"mine.go", "changed.go", "present.go"}, peers,
	)
	want := []string{
		"- present.go: asked for; exists at the base; free",
		"- owned.go: asked for; new at the base; owned by open T2 (implement)",
		"- .git/x: asked for; new at the base; refused: inside a .git directory",
		"- changed.go: changed outside the task's files; new at the base; changed by accepted T3, not yet integrated",
	}
	if !slices.Equal(got, want) {
		t.Errorf("requestedLines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPeerLinesListOpenPendingIntegratedSinceTheBaseAndFailedTasks(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	repo := setup.epic.Repository
	earlier := commitOff(t, repo, setup.base, "earlier.go", "package earlier\n")
	later := commitOff(t, repo, earlier, "later.go", "package later\n")
	task := store.EpicTask{Seq: 1, Status: taskOpen, Build: store.TaskBuild{Base: earlier}}
	waiter := openPeer(2, "w.go")
	waiter.Task.Title, waiter.Task.Deliverable = "Wait", "waits"
	waiter.Task.Build.Held, waiter.Task.Build.After = HeldWaiting, "3"
	pending := acceptedPeer(3, "p.go")
	pending.Task.Title = "Pending"
	old := Peer{Task: store.EpicTask{Seq: 4, Status: taskDone, Title: "Old"}, State: Integrated, Composed: earlier}
	recent := Peer{Task: store.EpicTask{Seq: 5, Status: taskDone, Title: "Recent"}, State: Integrated, Composed: later}
	failed := Peer{Task: store.EpicTask{
		Seq: 6, Status: taskFailed, Title: "Broke", Build: store.TaskBuild{Failure: "it broke"},
	}, State: Lost}
	got := peerLines(
		context.Background(),
		hostexec.NewLocal(),
		repo,
		task,
		[]Peer{{Task: task}, waiter, pending, old, recent, failed},
	)
	want := []string{
		"- T2 [open, implement, waiting] Wait", "  deliverable: waits", "  files: w.go", "  waits for: T3",
		"- T3 [accepted, not yet integrated] Pending", "  deliverable: ", "  changed: p.go",
		"Integrated since this task's base:", "- T5 Recent, composed as " + later,
		"Failed:", "- T6 Broke", "  failure: it broke",
	}
	if !slices.Equal(got, want) {
		t.Errorf("peerLines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestHistoryOfListsEachDecisionAndWhatCameBack(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 1, Build: store.TaskBuild{Decisions: 2}}
	rows := []store.StepRow{
		{ID: "i0", Schema: ImplementerSchema, End: endReturned, Status: "blocked", Result: `{"summary":"first"}`},
		{ID: "o0", Schema: OrchestratorSchema, End: endDone, Result: orchReturn(t, map[string]any{
			"decision": "rerun", "version": versionWith(map[string]any{
				"changed": []string{"deliverable", "gate"}, "deliverable": "read b.go:3", "gate": "check",
			}),
		})},
		{ID: "i1", Schema: ImplementerSchema, End: endReturned, Status: "blocked", Result: `{"summary":"again"}`},
		{ID: "o1", Schema: OrchestratorSchema, End: endDone, Result: orchReturn(t, map[string]any{
			"decision": "ask", "question": askQuestion(),
		})},
		{ID: "o2", Schema: OrchestratorSchema, End: "failed"},
		{ID: "o3", Schema: OrchestratorSchema},
		{ID: "o4", Schema: OrchestratorSchema, End: endDone, Result: orchReturn(t, map[string]any{
			"decision": "grant", "version": filesVersion("b.go"),
		})},
		{ID: "o5", Schema: OrchestratorSchema, End: endDone, Result: orchReturn(t, map[string]any{
			"decision": "rerun",
		})},
	}
	want := []string{
		"- T1 rerun: the code says so", "  version: changes deliverable, gate",
		"  then: the implementer ended returned, returning blocked: again",
		"- T1 ask (contract-cannot-hold): the code says so", "  question: R1's capture exits 2 as written",
		"  then: no implementer run since",
		"- o2: no decision (failed)",
		"- T1 grant b.go: the code says so", "  version: changes files",
		"  then: no implementer run since",
		"- T1 rerun: the code says so", "  version: changes nothing",
		"  then: no implementer run since",
		"Decisions left before orreryd settles the request without one: 1",
	}
	if got := historyOf(rows, task); !slices.Equal(got, want) {
		t.Errorf("historyOf =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestReturnLinesCarryTheWholeReturnAndEachSendBack(t *testing.T) {
	t.Parallel()
	step := &store.StepRow{
		End: endReturned, Status: "blocked", Result: `{"status":"blocked","summary":"gave up"}`,
		Checks: []store.StepCheck{
			{Round: 1, Name: "gate", OK: false, Detail: "gate exited 1", Stdout: "FAIL thing_test.go:9\n"},
			{Round: 2, Name: "gate", OK: true, Detail: "gate exited 0"},
		},
		Verdicts: []store.StepVerdict{
			{Kind: judgeKind, Round: 2, Verdict: "unmet", Detail: "add a test", Action: judgeContinue},
			{Kind: "monitor", Round: 2, Verdict: "stuck", Action: "continue"},
		},
	}
	want := []string{
		"Step end: returned", "Status: blocked", `Kept return: {"status":"blocked","summary":"gave up"}`,
		"Send-backs:", "- round 1, check gate: gate exited 1", "  stdout:", "    FAIL thing_test.go:9",
		"- round 2, judge unmet: add a test",
	}
	if got := returnLines(step); !slices.Equal(got, want) {
		t.Errorf("returnLines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestReadWorktreeOfAMissingPathSaysItCouldNotBeRead(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gone")
	lines := readWorktree(context.Background(), hostexec.NewLocal(), path, "base").lines()
	if len(lines) != 2 || lines[0] != "Path: "+path ||
		!strings.HasPrefix(lines[1], "The worktree could not be read: ") {
		t.Errorf("lines = %q, want the path and why it could not be read", lines)
	}
}

func TestReadWorktreeShowsUncommittedAndUntrackedWork(t *testing.T) {
	t.Parallel()
	setup := newStartSetup(t)
	path := filepath.Join(t.TempDir(), "implement")
	if err := AddWorktree(
		context.Background(),
		hostexec.NewLocal(),
		setup.epic.Repository,
		path,
		setup.base,
	); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(path, "tracked.go"), "package tracked\n")
	buildRun(t, path, "add", "tracked.go")
	writeFile(t, filepath.Join(path, "untracked.go"), "package untracked\n")
	tree := readWorktree(context.Background(), hostexec.NewLocal(), path, setup.base)
	if !slices.Equal(tree.changed, []string{"tracked.go", "untracked.go"}) {
		t.Errorf("changed = %v, want the staged and the untracked file", tree.changed)
	}
	text := strings.Join(tree.lines(), "\n")
	for _, want := range []string{"HEAD: " + setup.base, "A  tracked.go", "?? untracked.go", "+package tracked"} {
		if !strings.Contains(text, want) {
			t.Errorf("Worktree section lacks %q:\n%s", want, text)
		}
	}
}
