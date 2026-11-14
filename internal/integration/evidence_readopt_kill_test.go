package integration_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// The test in this file SIGKILLs orreryd twice while an evidence run works: once while its capture runs and once
// while its judge step runs. Each restart is on the same store and state directory. The capture's gate appends a
// line to a runs file each time it runs, then holds until a release file exists. The judge's fake claude holds its
// turn until fakeclaude.release exists in the judge step's worktree, then ends it with a verdict that holds the
// item's one assertion. The test checks that the capture ran once, that the judge is the one judge step the run
// started, and that the run ends passed with its verdict recorded once.

// evidenceKillEpic is the id of the epic the test creates. Its repository prefix has the form a contract requires.
const evidenceKillEpic = "evidence-0123456/kill-test"

// evidenceKillConclusion is the conclusion of the item, which is the one assertion the judge checks. The fake claude's
// JUDGEHOLDS case answers it, and the judge's prompt names it.
const evidenceKillConclusion = "JUDGEHOLDS"

// evidenceKillAgent is the agent the judge runs as. It is the built-in general-purpose agent, which needs no definition
// in the repository: a read-only step runs in a worktree under the state directory, where a project agent is not read.
const evidenceKillAgent = "general-purpose"

// evidenceKillRig is a repository whose project file declares the default gate pool, with the base commit an epic
// starts from. The paths are the ones the test releases and counts through.
type evidenceKillRig struct {
	f              *engineFixture
	scope          string
	stateDir       string
	repo           string
	base           string
	capture        []string
	captureRuns    string
	captureRelease string
}

// newEvidenceKillRig builds the rig under the fixture's root. The repository ignores the fake claude's files, so they
// never show as changes to it, and it holds the project file and the commit the epic starts from.
func newEvidenceKillRig(t *testing.T) *evidenceKillRig {
	t.Helper()
	f, scope, _ := scopedFixture(t)
	// The agent resolution reads HOME, so it must exist even though the judge's agent is built in.
	if err := os.Mkdir(filepath.Join(f.root, "home"), 0o700); err != nil {
		t.Fatalf("make home: %v", err)
	}
	// The machine's pool file would join the project's pools, and it may define the default gate pool too. The test
	// config directory does not exist, so the project file alone defines the pools.
	f.env = append(f.env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"XDG_CONFIG_HOME="+filepath.Join(f.root, "config"))
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("read host name: %v", err)
	}
	captureRuns := filepath.Join(f.root, "capture.runs")
	captureRelease := filepath.Join(f.root, "capture.release")
	script := fmt.Sprintf("echo run >> '%s'; while [ ! -e '%s' ]; do sleep 0.05; done", captureRuns, captureRelease)

	repo := filepath.Join(f.root, "repo")
	stepReadoptGit(t, f.root, "init", "-q", "-b", "main", repo)
	stepReadoptWrite(t, filepath.Join(repo, ".gitignore"), "fakeclaude.*\n")
	stepReadoptWrite(t, filepath.Join(repo, "README"), "base\n")
	stepReadoptWrite(t, filepath.Join(repo, ".orrery", "project.toml"), fmt.Sprintf(
		"[pools.gate]\nhost = %q\ncap = 1\nmembers = [\"m1\"]\n"+
			"wake = [%q, \"-c\", \"exit 0\"]\nwake_limit = 60\n"+
			"status = [%q, \"-c\", \"exit 0\"]\nstatus_limit = 60\n"+
			"admit = [%q, \"-c\", \"exit 0\"]\nadmit_limit = 60\n",
		host, f.shell, f.shell, f.shell))
	stepReadoptGit(t, repo, "add", "-A")
	stepReadoptGit(t, repo, "commit", "-q", "-m", "base")

	rig := &evidenceKillRig{
		f:              f,
		scope:          scope,
		stateDir:       filepath.Dir(f.dbPath),
		repo:           repo,
		base:           stepReadoptGit(t, repo, "rev-parse", "HEAD"),
		capture:        []string{f.shell, "-c", script},
		captureRuns:    captureRuns,
		captureRelease: captureRelease,
	}
	// A capture still held at the end of a failed test exits at once, so its holder is not left waiting. The holders
	// are cleared last, after every daemon has stopped. A failed run can start judge steps the test never names, so
	// every holder in the state directory is cleared, not only those the test names.
	t.Cleanup(func() { clearHolders(t, rig.stateDir) })
	t.Cleanup(func() { stepReadoptTouch(t, captureRelease) })
	return rig
}

// clearHolders clears every holder under the state directory: the steps' holders and the gates' holders. The state
// directory is the test's own, so each holder in it is one the test started.
func clearHolders(t *testing.T, stateDir string) {
	t.Helper()
	for _, dir := range []string{filepath.Join(stateDir, "holders"), filepath.Join(stateDir, "gates", "holders")} {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read holders %s: %v", dir, err)
		}
		for _, entry := range entries {
			clearHolder(t, filepath.Join(dir, entry.Name()))
		}
	}
}

// evidence returns the Evidence client of the session socket.
func (r *evidenceKillRig) evidence() enginev1connect.EvidenceClient {
	return enginev1connect.NewEvidenceClient(cli.SessionHTTPClient(r.f.sessions), cli.SessionBaseURL)
}

// epics returns the Epics client of the session socket.
func (r *evidenceKillRig) epics() enginev1connect.EpicsClient {
	return enginev1connect.NewEpicsClient(cli.SessionHTTPClient(r.f.sessions), cli.SessionBaseURL)
}

// steps returns the Steps client of the session socket.
func (r *evidenceKillRig) steps() enginev1connect.StepsClient {
	return enginev1connect.NewStepsClient(cli.SessionHTTPClient(r.f.sessions), cli.SessionBaseURL)
}

// createEpic stores the rig's epic: one Requirement whose evidence is the capture, judged against its conclusion.
func (r *evidenceKillRig) createEpic(t *testing.T) {
	t.Helper()
	_, err := r.epics().CreateEpic(t.Context(), connect.NewRequest(&v1.CreateEpicRequest{
		Contract: &v1.EpicContract{
			Id:         evidenceKillEpic,
			Repository: r.repo,
			Title:      "Evidence kill test",
			Intent:     "A SIGKILL during a capture and during its judge loses nothing and runs nothing twice.",
			Care:       "severe",
			Requirements: []*v1.EpicRequirement{{
				Id:   "R1",
				Text: "The capture runs once and its judge holds once.",
				Item: &v1.EvidenceItem{
					Capture:    r.capture,
					Limit:      300,
					Conclusion: evidenceKillConclusion,
					Judge:      evidenceKillAgent,
				},
			}},
			Base: r.base,
		},
	}))
	if err != nil {
		t.Fatalf("create epic %s: %v", evidenceKillEpic, err)
	}
}

// runEvidence starts the run of the epic's R1 at the base commit in phase green, and returns its id.
func (r *evidenceKillRig) runEvidence(t *testing.T) string {
	t.Helper()
	resp, err := r.evidence().RunEvidence(t.Context(), connect.NewRequest(&v1.RunEvidenceRequest{
		Epic:     evidenceKillEpic,
		Item:     "R1",
		Revision: r.base,
		Phase:    "green",
	}))
	if err != nil {
		t.Fatalf("run evidence: %v", err)
	}
	return resp.Msg.GetRun().GetId()
}

// runs returns the evidence runs the epic holds, as ListEvidence reports them.
func (r *evidenceKillRig) runs(t *testing.T) []*v1.EvidenceRun {
	t.Helper()
	resp, err := r.evidence().ListEvidence(
		t.Context(), connect.NewRequest(&v1.ListEvidenceRequest{Epic: evidenceKillEpic}),
	)
	if err != nil {
		t.Fatalf("list evidence of %s: %v", evidenceKillEpic, err)
	}
	return resp.Msg.GetRuns()
}

// run returns the row of the evidence run id, failing the test when the epic does not list it.
func (r *evidenceKillRig) run(t *testing.T, id string) *v1.EvidenceRun {
	t.Helper()
	for _, run := range r.runs(t) {
		if run.GetId() == id {
			return run
		}
	}
	t.Fatalf("evidence run %s is not listed for %s", id, evidenceKillEpic)
	return nil
}

// stepRow returns the row of step id as the Steps service reports it.
func (r *evidenceKillRig) stepRow(t *testing.T, id string) *v1.Step {
	t.Helper()
	resp, err := r.steps().StepStatus(t.Context(), connect.NewRequest(&v1.StepStatusRequest{Id: id}))
	if err != nil {
		t.Fatalf("read step %s: %v", id, err)
	}
	return resp.Msg.GetStep()
}

// judgeSteps returns the labeled steps of run's evidence task, which hold the run's judge steps.
func (r *evidenceKillRig) judgeSteps(t *testing.T, run string) []*v1.Step {
	t.Helper()
	resp, err := r.steps().ListSteps(t.Context(), connect.NewRequest(&v1.ListStepsRequest{
		Epic: evidenceKillEpic,
		Task: "evidence-" + run,
	}))
	if err != nil {
		t.Fatalf("list judge steps of run %s: %v", run, err)
	}
	return resp.Msg.GetSteps()
}

// awaitJudge waits until run is judging with a judge step, and returns the step's id.
func (r *evidenceKillRig) awaitJudge(t *testing.T, run string) string {
	t.Helper()
	var judge string
	waitUntil(t, "run "+run+" to judge", func() bool {
		stored := r.run(t, run)
		judge = stored.GetJudgeStep()
		return stored.GetState() == "judging" && judge != ""
	})
	return judge
}

// requireCaptureWorktreesEmpty fails unless no capture worktree is left under the state directory.
func (r *evidenceKillRig) requireCaptureWorktreesEmpty(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(r.stateDir, "evidence-worktrees"))
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read capture worktrees: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("capture worktrees left under the state directory = %d, want none", len(entries))
	}
}

// TestEvidenceReadoptSigkillDuringCaptureAndJudge proves that orreryd killed while an evidence run's capture runs,
// and again while its judge step runs, loses nothing and runs nothing twice. The restarted orreryd re-adopts the
// capture without running it again, then re-adopts the judge step without starting another, and the run ends passed
// with its verdict recorded.
func TestEvidenceReadoptSigkillDuringCaptureAndJudge(t *testing.T) {
	t.Parallel()
	r := newEvidenceKillRig(t)
	daemon := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	r.createEpic(t)
	run := r.runEvidence(t)
	waitUntil(t, "the capture to start", func() bool { return gateRunCount(t, r.captureRuns) > 0 })

	killOrreryd(t, daemon)
	daemon = r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	if state := r.run(t, run).GetState(); state != "capturing" {
		t.Fatalf("state of run %s after the restart = %q, want capturing", run, state)
	}
	if n := gateRunCount(t, r.captureRuns); n != 1 {
		t.Fatalf("capture runs of run %s before its release = %d, want 1", run, n)
	}
	stepReadoptTouch(t, r.captureRelease)

	judge := r.awaitJudge(t, run)
	judgeDir := r.stepRow(t, judge).GetWorktree()
	if judgeDir == "" {
		t.Fatalf("judge step %s names no worktree", judge)
	}
	waitUntil(t, "the judge to read its prompt", func() bool {
		return fileContains(t, filepath.Join(judgeDir, "fakeclaude.stdin"), evidenceKillConclusion)
	})
	judgePID := fakeClaudePID(t, judgeDir)

	killOrreryd(t, daemon)
	daemon = r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	if got := r.run(t, run).GetJudgeStep(); got != judge {
		t.Fatalf("judge step of run %s after the restart = %q, want %q", run, got, judge)
	}
	if end := r.stepRow(t, judge).GetEnd(); end != "" {
		t.Fatalf("judge step %s ended %q after the restart, want it still running", judge, end)
	}
	requireProcessRunning(t, judgePID)
	if n := len(r.judgeSteps(t, run)); n != 1 {
		t.Fatalf("judge steps of run %s after the restart = %d, want 1", run, n)
	}
	if n := gateRunCount(t, r.captureRuns); n != 1 {
		t.Fatalf("capture runs of run %s after the judge restart = %d, want 1", run, n)
	}

	stepReadoptTouch(t, filepath.Join(judgeDir, "fakeclaude.release"))
	waitUntil(t, "run "+run+" to end", func() bool { return r.run(t, run).GetState() == "ended" })
	stored := r.run(t, run)
	if stored.GetVerdict() != "holds" || !stored.GetPassed() || stored.GetExit() != 0 {
		t.Fatalf("run %s verdict %q passed %t exit %d, want holds, passed, exit 0; reason %q",
			run, stored.GetVerdict(), stored.GetPassed(), stored.GetExit(), stored.GetJudgeReason())
	}
	if n := len(r.runs(t)); n != 1 {
		t.Fatalf("evidence runs of %s = %d, want 1", evidenceKillEpic, n)
	}
	if n := gateRunCount(t, r.captureRuns); n != 1 {
		t.Fatalf("capture runs of run %s after it ended = %d, want 1", run, n)
	}
	judges := r.judgeSteps(t, run)
	if len(judges) != 1 || judges[0].GetId() != judge || judges[0].GetEnd() != "done" {
		t.Fatalf("judge steps of run %s = %+v, want the one step %s ended done", run, judges, judge)
	}
	if n := promptsWritten(t, judgeDir, evidenceKillConclusion); n != 1 {
		t.Fatalf("fake claude read the judge prompt %d times, want 1", n)
	}
	r.requireCaptureWorktreesEmpty(t)
	waitGone(t, judgePID)
	wantExit(t, daemon.stop(t), 0)
}
