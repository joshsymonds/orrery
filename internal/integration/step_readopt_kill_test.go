package integration_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/holder"
)

// The tests in this file SIGKILL orreryd while a writing step runs, then restart it on the same store and state
// directory. The fake claude holds the step's turn until fakeclaude.release exists in the step's worktree, then makes
// an empty commit and returns it as done. The step's named gate appends a line to a runs file each time it runs, then
// holds one member of a pool until a release file exists. Each test checks that the step ends done with the fake's
// return, that its gate ran once and recorded one passing check, and that the member the gate holds stays granted until
// the gate exits.

// stepReadoptPrompt is the prompt that makes the fake claude end its turn with a done return.
const stepReadoptPrompt = "STEPDONE"

// stepReadoptSchema is the schema a step's done return must match.
const stepReadoptSchema = `{"type":"object","properties":{"status":{"type":"string"},"commit":{"type":"string"}},` +
	`"required":["status","commit"]}`

// stepReadoptAgent is the agent the step runs as. Its definition is a project agent in the step's worktree.
const stepReadoptAgent = "implementer"

// stepReadoptPool is the pool the gate holds a member of, and stepReadoptGate is the gate that needs it.
const (
	stepReadoptPool = "gatepool"
	stepReadoptGate = "unit"
)

// stepReadoptRig is a repository whose linked worktree a writing step runs in. The repository declares one pool with
// one member in its project file, and its gates file defines the gate the step names. The paths are the ones the test
// releases and counts through.
type stepReadoptRig struct {
	f           *engineFixture
	scope       string
	stateDir    string
	worktree    string
	gatesFile   string
	turnRelease string
	gateRuns    string
	gateRelease string
}

// newStepReadoptRig builds the rig under the fixture's root. The repository ignores the fake claude's files, so the
// step's clean check passes, and it holds the project file, the agent and the commit the test starts from.
func newStepReadoptRig(t *testing.T) *stepReadoptRig {
	t.Helper()
	f, scope, _ := scopedFixture(t)
	// A project agent is read only from a directory under HOME, so the repository and its worktree live under it.
	home := filepath.Join(f.root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatalf("make home: %v", err)
	}
	f.env = append(f.env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("read host name: %v", err)
	}
	gateRuns := filepath.Join(f.root, "gate.runs")
	gateRelease := filepath.Join(f.root, "gate.release")
	gatesFile := filepath.Join(f.root, "gates.toml")
	script := fmt.Sprintf("echo run >> '%s'; while [ ! -e '%s' ]; do sleep 0.05; done", gateRuns, gateRelease)
	stepReadoptWrite(t, gatesFile, fmt.Sprintf("[gates.%s]\nargv = [%q, \"-c\", %q]\nlimit = 300\nneeds = { %s = 1 }\n",
		stepReadoptGate, f.shell, script, stepReadoptPool))

	repo := filepath.Join(home, "repo")
	stepReadoptGit(t, home, "init", "-q", "-b", "main", repo)
	stepReadoptWrite(t, filepath.Join(repo, ".gitignore"), "fakeclaude.*\n")
	stepReadoptWrite(t, filepath.Join(repo, "README"), "base\n")
	stepReadoptWrite(t, filepath.Join(repo, ".orrery", "project.toml"), fmt.Sprintf(
		"[pools.%s]\nhost = %q\ncap = 1\nmembers = [\"m1\"]\n"+
			"wake = [%q, \"-c\", \"exit 0\"]\nwake_limit = 60\n"+
			"status = [%q, \"-c\", \"exit 0\"]\nstatus_limit = 60\n"+
			"admit = [%q, \"-c\", \"exit 0\"]\nadmit_limit = 60\n",
		stepReadoptPool, host, f.shell, f.shell, f.shell))
	stepReadoptWrite(t, filepath.Join(repo, ".claude", "agents", stepReadoptAgent+".md"),
		"---\nname: "+stepReadoptAgent+"\ndescription: Implements a step\n---\n")
	stepReadoptGit(t, repo, "add", "-A")
	stepReadoptGit(t, repo, "commit", "-q", "-m", "base")

	worktree := filepath.Join(home, "worktree")
	stepReadoptGit(t, repo, "worktree", "add", "--detach", worktree, "HEAD")
	resolved, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatalf("resolve worktree: %v", err)
	}
	rig := &stepReadoptRig{
		f:           f,
		scope:       scope,
		stateDir:    filepath.Dir(f.dbPath),
		worktree:    resolved,
		gatesFile:   gatesFile,
		turnRelease: filepath.Join(resolved, "fakeclaude.release"),
		gateRuns:    gateRuns,
		gateRelease: gateRelease,
	}
	// A gate still held at the end of a failed test exits at once, so its holder is cleared and not left waiting.
	t.Cleanup(func() { stepReadoptTouch(t, rig.gateRelease) })
	return rig
}

// stepReadoptGit runs git in dir with the test's identity and its own config only, and returns its output. It fails the
// test when git fails.
func stepReadoptGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.CommandContext(t.Context(), "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// stepReadoptWrite writes contents to path, creating its directory.
func stepReadoptWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// stepReadoptTouch creates the empty file at path, or truncates it when it exists.
func stepReadoptTouch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

// fileContains reports whether the file at path holds want. A file that does not exist holds nothing.
func fileContains(t *testing.T, path, want string) bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Contains(string(raw), want)
}

// gateRunCount returns how many times the gate has run: each run appends one line to the runs file.
func gateRunCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read gate runs: %v", err)
	}
	return strings.Count(string(raw), "\n")
}

// startStep starts the rig's writing step through the Steps service on the session socket and returns its id.
func (r *stepReadoptRig) startStep(t *testing.T) string {
	t.Helper()
	resp, err := r.steps().StartStep(t.Context(), connect.NewRequest(&v1.StartStepRequest{
		Agent:     stepReadoptAgent,
		Prompt:    stepReadoptPrompt,
		Schema:    stepReadoptSchema,
		Workspace: r.worktree,
		GateName:  stepReadoptGate,
		GatesFile: r.gatesFile,
	}))
	if err != nil {
		t.Fatalf("start step: %v", err)
	}
	return resp.Msg.GetId()
}

// steps returns the Steps client of the session socket.
func (r *stepReadoptRig) steps() enginev1connect.StepsClient {
	return enginev1connect.NewStepsClient(cli.SessionHTTPClient(r.f.sessions), cli.SessionBaseURL)
}

// step returns the row of step id as the Steps service reports it.
func (r *stepReadoptRig) step(t *testing.T, id string) *v1.Step {
	t.Helper()
	resp, err := r.steps().StepStatus(t.Context(), connect.NewRequest(&v1.StepStatusRequest{Id: id}))
	if err != nil {
		t.Fatalf("read step %s: %v", id, err)
	}
	return resp.Msg.GetStep()
}

// awaitEnd waits until step id has ended, and returns its row.
func (r *stepReadoptRig) awaitEnd(t *testing.T, id string) *v1.Step {
	t.Helper()
	var step *v1.Step
	waitUntil(t, "step "+id+" to end", func() bool {
		step = r.step(t, id)
		return step.GetEnd() != ""
	})
	return step
}

// heldGrant returns the id of the grant that holds the rig's pool member, as Pools.Status reports it, or "" when no
// grant holds it. A pool that no grant uses is not in the status, so it holds nothing.
func (r *stepReadoptRig) heldGrant(t *testing.T) string {
	t.Helper()
	pools := enginev1connect.NewPoolsClient(cli.SessionHTTPClient(r.f.sessions), cli.SessionBaseURL)
	resp, err := pools.Status(t.Context(), connect.NewRequest(&v1.PoolsStatusRequest{}))
	if err != nil {
		t.Fatalf("read pool status: %v", err)
	}
	for _, pool := range resp.Msg.GetPools() {
		if pool.GetName() == stepReadoptPool && len(pool.GetMembers()) == 1 {
			return pool.GetMembers()[0].GetHolder().GetGrantId()
		}
	}
	return ""
}

// gateHolder returns the holder directory of the gate step id runs. The gate's holder is named for its step, under the
// gates folder of the state directory.
func (r *stepReadoptRig) gateHolder(t *testing.T, id string) string {
	t.Helper()
	dir, err := holder.SessionDir(filepath.Join(r.stateDir, "gates", "holders"), id+"-gate")
	if err != nil {
		t.Fatalf("gate holder of %s: %v", id, err)
	}
	return dir
}

// requireDone fails unless step ended done, its return names the worktree's HEAD, and it recorded one passing gate
// check.
func (r *stepReadoptRig) requireDone(t *testing.T, step *v1.Step) {
	t.Helper()
	if step.GetEnd() != "done" {
		t.Fatalf("step %s ended %q (%s), want done", step.GetId(), step.GetEnd(), step.GetReason())
	}
	var ret struct {
		Status string `json:"status"`
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal([]byte(step.GetResult()), &ret); err != nil {
		t.Fatalf("decode return %q: %v", step.GetResult(), err)
	}
	head := stepReadoptGit(t, r.worktree, "rev-parse", "HEAD")
	if ret.Status != "done" || ret.Commit != head {
		t.Fatalf("return = %+v, want status done at HEAD %s", ret, head)
	}
	gateChecks := 0
	for _, check := range step.GetChecks() {
		if check.GetName() != "gate" {
			continue
		}
		gateChecks++
		if !check.GetOk() {
			t.Fatalf("gate check failed: %s", check.GetDetail())
		}
	}
	if gateChecks != 1 {
		t.Fatalf("gate checks of step %s = %d, want 1", step.GetId(), gateChecks)
	}
}

// TestStepReadoptSigkillDuringTurnEndsDone proves that orreryd killed while a writing step's turn runs, before the turn
// returns, re-adopts the step after the restart. The turn's return reaches the restarted orreryd, the gate runs once,
// and the step ends done.
func TestStepReadoptSigkillDuringTurnEndsDone(t *testing.T) {
	t.Parallel()
	r := newStepReadoptRig(t)
	daemon := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	id := r.startStep(t)
	t.Cleanup(func() { clearHolder(t, filepath.Join(r.stateDir, "holders", id)) })
	waitUntil(t, "the fake claude to read the step's prompt", func() bool {
		return fileContains(t, filepath.Join(r.worktree, "fakeclaude.stdin"), stepReadoptPrompt)
	})
	pid := fakeClaudePID(t, r.worktree)

	killOrreryd(t, daemon)
	restarted := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	if end := r.step(t, id).GetEnd(); end != "" {
		t.Fatalf("step %s ended %q after the restart, want it still running its turn", id, end)
	}
	requireProcessRunning(t, pid)

	stepReadoptTouch(t, r.turnRelease)
	waitUntil(t, "the gate to start", func() bool { return gateRunCount(t, r.gateRuns) > 0 })
	if n := gateRunCount(t, r.gateRuns); n != 1 {
		t.Fatalf("gate runs of step %s = %d, want 1", id, n)
	}
	stepReadoptTouch(t, r.gateRelease)
	r.requireDone(t, r.awaitEnd(t, id))
	if n := gateRunCount(t, r.gateRuns); n != 1 {
		t.Fatalf("gate runs of step %s = %d after it ended, want 1", id, n)
	}
	if n := promptsWritten(t, r.worktree, stepReadoptPrompt); n != 1 {
		t.Fatalf("fake claude read the step prompt %d times, want 1", n)
	}
	waitGone(t, pid)
	wantExit(t, restarted.stop(t), 0)
}

// TestStepReadoptSigkillDuringGateHoldsMemberOnce proves that orreryd killed while a writing step's gate runs, holding
// a pool member, leaves the member granted to the same grant after the restart. The gate is not run again, its result
// lands once, and the member is free once the gate exits.
func TestStepReadoptSigkillDuringGateHoldsMemberOnce(t *testing.T) {
	t.Parallel()
	r := newStepReadoptRig(t)
	// The turn returns at once, so the gate is the next thing the step runs.
	stepReadoptTouch(t, r.turnRelease)
	daemon := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	id := r.startStep(t)
	t.Cleanup(func() { clearHolder(t, filepath.Join(r.stateDir, "holders", id)) })
	t.Cleanup(func() { clearHolder(t, r.gateHolder(t, id)) })
	waitUntil(t, "the gate to start", func() bool { return gateRunCount(t, r.gateRuns) > 0 })
	granted := r.heldGrant(t)
	if granted == "" {
		t.Fatalf("member of %s holds no grant before the kill, want the gate's", stepReadoptPool)
	}

	killOrreryd(t, daemon)
	restarted := r.f.startOrreryd(t, true, "--holder-scope", r.scope)
	if end := r.step(t, id).GetEnd(); end != "" {
		t.Fatalf("step %s ended %q after the restart, want it still running its gate", id, end)
	}
	if got := r.heldGrant(t); got != granted {
		t.Fatalf("member holder after the restart = %q, want the grant %q the gate held", got, granted)
	}
	if n := gateRunCount(t, r.gateRuns); n != 1 {
		t.Fatalf("gate runs of step %s = %d before its release, want 1", id, n)
	}

	stepReadoptTouch(t, r.gateRelease)
	r.requireDone(t, r.awaitEnd(t, id))
	if n := gateRunCount(t, r.gateRuns); n != 1 {
		t.Fatalf("gate runs of step %s = %d after it ended, want 1", id, n)
	}
	if held := r.heldGrant(t); held != "" {
		t.Fatalf("member still held by grant %q after the gate exited, want it free", held)
	}
	wantExit(t, restarted.stop(t), 0)
}
