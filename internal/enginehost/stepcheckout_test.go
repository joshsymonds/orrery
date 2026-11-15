package enginehost

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepCheckoutRepo returns a scratch repository whose one commit ignores *.log, so a test can write a file git ignores.
func stepCheckoutRepo(t *testing.T) string {
	t.Helper()
	repo := stepGitTestRepo(t)
	stepGitTestWrite(t, repo, ".gitignore", "*.log\n")
	stepGitTestRun(t, repo, "add", ".gitignore")
	stepGitTestRun(t, repo, "commit", "-q", "-m", "ignore logs")
	return repo
}

// stepCheckoutStart starts the read-only step start under f and returns its id and its worktree.
func stepCheckoutStart(t *testing.T, f *stepStartFixture, start StepStart) (string, string) {
	t.Helper()
	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return id, filepath.Join(f.worktrees, id)
}

// stepCheckoutReturn hands the step id under f a return with value, waits until the step ends, and returns its stored row.
func stepCheckoutReturn(t *testing.T, f *stepStartFixture, id string, value json.RawMessage) store.StepRow {
	t.Helper()
	stepsRunOf(t, f.steps, id).Return(session.TurnEnded{StructuredOutput: value})
	stepStartAwait(t, engineOf(f.steps), "step "+id+" to end", func() bool {
		row, err := f.steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return row
}

// stepCheckoutCheckIn returns the check named checkout among checks, and whether there is one.
func stepCheckoutCheckIn(checks []store.StepCheck) (store.StepCheck, bool) {
	for _, c := range checks {
		if c.Name == "checkout" {
			return c, true
		}
	}
	return store.StepCheck{}, false
}

// TestStepCheckoutStartStoresBeforeState reads back the checkout state a read-only start stores: the HEAD it started at
// and a fingerprint of the checkout.
func TestStepCheckoutStartStoresBeforeState(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	repo := stepCheckoutRepo(t)
	head := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id, _ := stepCheckoutStart(t, f, stepStartReadOnly(repo, ""))
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.CheckoutHead != head {
		t.Errorf("CheckoutHead = %q, want the start HEAD %q", row.CheckoutHead, head)
	}
	if row.CheckoutHash == "" {
		t.Error("CheckoutHash is empty, want the fingerprint of the checkout")
	}
}

// TestStepCheckoutMovedReturnEndsChecksFailed starts a read-only step, moves its checkout, and returns. The return must
// end checks_failed at once, with no send-back, and store a failed checkout check whose detail names the move.
func TestStepCheckoutMovedReturnEndsChecksFailed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// move changes the checkout at wt, which the step's checkout is.
		move func(t *testing.T, wt string)
		// detail are the substrings the checkout check's detail must hold.
		detail []string
	}{
		{
			name: "tracked change",
			move: func(t *testing.T, wt string) {
				t.Helper()
				stepGitTestWrite(t, wt, "README", "changed\n")
			},
			detail: []string{"changed paths: README"},
		},
		{
			name: "untracked file",
			move: func(t *testing.T, wt string) {
				t.Helper()
				stepGitTestWrite(t, wt, "notes.txt", "notes\n")
			},
			detail: []string{"changed paths: notes.txt"},
		},
		{
			name: "commit",
			move: func(t *testing.T, wt string) {
				t.Helper()
				stepGitTestWrite(t, wt, "committed.txt", "committed\n")
				stepGitTestRun(t, wt, "add", "committed.txt")
				stepGitTestRun(t, wt, "commit", "-q", "-m", "agent commit")
			},
			detail: []string{"changed paths: committed.txt"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStepStartFixture(t)
			repo := stepCheckoutRepo(t)
			head := stepGitTestRun(t, repo, "rev-parse", "HEAD")
			id, wt := stepCheckoutStart(t, f, stepStartReadOnly(repo, ""))
			tc.move(t, wt)

			row := stepCheckoutReturn(t, f, id, json.RawMessage(`{"status":"done"}`))
			if row.End != stepLoopEndChecksFailed {
				t.Fatalf("end = %q, want %q", row.End, stepLoopEndChecksFailed)
			}
			if row.LoopContinues != 0 {
				t.Errorf("continues = %d, want nothing sent back", row.LoopContinues)
			}
			if len(row.Verdicts) != 0 {
				t.Errorf("stored %d verdicts, want no judge call", len(row.Verdicts))
			}
			check, ok := stepCheckoutCheckIn(row.Checks)
			if !ok {
				t.Fatalf("checks = %+v, want a checkout check", row.Checks)
			}
			if check.OK {
				t.Errorf("checkout check passed, want it failed: %+v", check)
			}
			for _, want := range tc.detail {
				if !strings.Contains(check.Detail, want) {
					t.Errorf("checkout detail = %q, want it to contain %q", check.Detail, want)
				}
			}
			if tc.name == "commit" && !strings.Contains(check.Detail, "HEAD moved from "+head+" to ") {
				t.Errorf("checkout detail = %q, want HEAD's move from %s named", check.Detail, head)
			}
		})
	}
}

// TestStepCheckoutUnmovedOrIgnoredReturnKeepsItsChecks starts a read-only step and returns after nothing written, or
// after only a git-ignored file. The return ends as it does without a checkout check: the stored checks are the schema
// check alone.
func TestStepCheckoutUnmovedOrIgnoredReturnKeepsItsChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		write func(t *testing.T, wt string)
	}{
		{name: "nothing written", write: func(*testing.T, string) {}},
		{
			name: "ignored file",
			write: func(t *testing.T, wt string) {
				t.Helper()
				stepGitTestWrite(t, wt, "debug.log", "ignored\n")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStepStartFixture(t)
			repo := stepCheckoutRepo(t)
			id, wt := stepCheckoutStart(t, f, stepStartReadOnly(repo, ""))
			tc.write(t, wt)

			row := stepCheckoutReturn(t, f, id, json.RawMessage(`{"status":"done"}`))
			if row.End != stepDefaultDone {
				t.Errorf("end = %q, want %q", row.End, stepDefaultDone)
			}
			if len(row.Checks) != 1 || row.Checks[0].Name != stepLoopSchemaName || !row.Checks[0].OK {
				t.Errorf("checks = %+v, want only the passing schema check", row.Checks)
			}
		})
	}
}

// TestStepCheckoutNamesAtMostTwentyPaths moves a checkout by more paths than the detail names, and checks the detail
// names the first twenty in order and a count of the rest.
func TestStepCheckoutNamesAtMostTwentyPaths(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	repo := stepCheckoutRepo(t)
	id, wt := stepCheckoutStart(t, f, stepStartReadOnly(repo, ""))
	names := make([]string, 0, 25)
	for i := range 25 {
		name := fmt.Sprintf("untracked-%02d.txt", i)
		names = append(names, name)
		stepGitTestWrite(t, wt, name, "x\n")
	}
	row := stepCheckoutReturn(t, f, id, json.RawMessage(`{"status":"done"}`))
	check, ok := stepCheckoutCheckIn(row.Checks)
	if !ok {
		t.Fatalf("checks = %+v, want a checkout check", row.Checks)
	}
	want := "changed paths: " + strings.Join(names[:20], ", ") + " and 5 more"
	if check.Detail != want {
		t.Errorf("checkout detail = %q, want %q", check.Detail, want)
	}
}

// TestStepCheckoutAgentGateMovedReturnEndsChecksFailed checks an agent gate step the same as an ordinary read-only
// step: a return after its checkout moved ends checks_failed with no verdict.
func TestStepCheckoutAgentGateMovedReturnEndsChecksFailed(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	repo := stepCheckoutRepo(t)
	gate := stepAgentGate("reviewer")
	id, wt := stepCheckoutStart(t, f, StepStart{Prompt: "review", Workspace: repo, ReadOnly: true, AgentGate: &gate})
	stepGitTestWrite(t, wt, "notes.txt", "notes\n")

	row := stepCheckoutReturn(t, f, id, verdictTestJSON(t, "done", verdictTestHolds(), nil))
	if row.End != stepLoopEndChecksFailed {
		t.Fatalf("end = %q, want %q", row.End, stepLoopEndChecksFailed)
	}
	if len(row.Verdicts) != 0 {
		t.Errorf("stored %d verdicts, want no judge call", len(row.Verdicts))
	}
	check, ok := stepCheckoutCheckIn(row.Checks)
	if !ok || check.OK || !strings.Contains(check.Detail, "changed paths: notes.txt") {
		t.Errorf("checks = %+v, want a failed checkout check naming notes.txt", row.Checks)
	}
}

// TestStepCheckoutAgentGateUnmovedReturnKeepsItsChecks checks an agent gate step whose checkout did not move ends as it
// does today: done on a holding verdict, with no checkout check stored.
func TestStepCheckoutAgentGateUnmovedReturnKeepsItsChecks(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	repo := stepCheckoutRepo(t)
	gate := stepAgentGate("reviewer")
	id, _ := stepCheckoutStart(t, f, StepStart{Prompt: "review", Workspace: repo, ReadOnly: true, AgentGate: &gate})

	row := stepCheckoutReturn(t, f, id, verdictTestJSON(t, "done", verdictTestHolds(), nil))
	if row.End != stepDefaultDone {
		t.Errorf("end = %q, want %q", row.End, stepDefaultDone)
	}
	if check, ok := stepCheckoutCheckIn(row.Checks); ok {
		t.Errorf("stored checkout check %+v for a checkout that did not move", check)
	}
}

// TestStepCheckoutGitErrorAtReturnFailsClosed removes a read-only step's worktree git link before its return. The
// checkout cannot be read, so the check fails closed and the step ends checks_failed.
func TestStepCheckoutGitErrorAtReturnFailsClosed(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	repo := stepCheckoutRepo(t)
	id, wt := stepCheckoutStart(t, f, stepStartReadOnly(repo, ""))
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatalf("remove worktree git link: %v", err)
	}

	row := stepCheckoutReturn(t, f, id, json.RawMessage(`{"status":"done"}`))
	if row.End != stepLoopEndChecksFailed {
		t.Fatalf("end = %q, want %q", row.End, stepLoopEndChecksFailed)
	}
	check, ok := stepCheckoutCheckIn(row.Checks)
	if !ok || check.OK || !strings.HasPrefix(check.Detail, "read the checkout:") {
		t.Errorf("checks = %+v, want a failed checkout check that says the checkout could not be read", row.Checks)
	}
}

// TestStepCheckoutRunWithoutBeforeStateIsNotCompared runs a read-only step that stores no before state, as a row started
// before this release has none. Its checkout moved, and its return still ends done: the step has nothing to compare with.
func TestStepCheckoutRunWithoutBeforeStateIsNotCompared(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, nil)
	repo := stepCheckoutRepo(t)
	f.run.dir = repo
	stepGitTestWrite(t, repo, "notes.txt", "notes\n")
	_, done := f.start(t)
	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)})
	waitRunEnd(t, done)
	row := f.row(t)
	if row.End != stepLoopEndDone {
		t.Errorf("end = %q, want %q", row.End, stepLoopEndDone)
	}
	if check, ok := stepCheckoutCheckIn(row.Checks); ok {
		t.Errorf("stored checkout check %+v for a row with no before state", check)
	}
}

// TestStepLoopResumedCheckoutFailureEndsWithoutSendBack resumes a re-adopted step whose stored return failed its checkout
// check. The step must end checks_failed, not send the failure back to the session as a retry would.
func TestStepLoopResumedCheckoutFailureEndsWithoutSendBack(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, nil)
	const value = `{"status":"DONE"}`
	f.run.restore = &stepRestore{
		Row: store.StepRow{
			ID: stepLoopID, Result: value, LoopRound: 1,
			Checks: []store.StepCheck{
				{Round: 1, Name: stepLoopSchemaName, OK: true},
				{Round: 1, Name: "checkout", Detail: "changed paths: notes.txt"},
			},
		},
		Turn:  store.StepTurnEnd{Output: value, Returned: true},
		Found: true,
	}
	_, done := f.start(t)
	waitRunEnd(t, done)
	if got := f.sessions.continuedTexts(); len(got) != 0 {
		t.Errorf("continued with %q, want the failed checkout ended without a send-back", got)
	}
	if row := f.row(t); row.End != stepLoopEndChecksFailed {
		t.Errorf("end = %q, want %q", row.End, stepLoopEndChecksFailed)
	}
}
