package main

import (
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The epic and its texts are invented, and its repository is a scratch one under t.TempDir().
const (
	buildWiringEpic = "orrery-5d6e7f8/build-wiring"
	buildWiringHost = "synthetic-host"
)

// buildWiringGit runs git in dir with a test identity and returns its trimmed output. It fails the test on error.
func buildWiringGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test",
		"-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// buildWiringRepo makes a scratch repository under t.TempDir() with one commit, and returns its path and that commit.
func buildWiringRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	buildWiringGit(t, dir, "init", "-q", "-b", "main")
	buildWiringGit(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	return dir, buildWiringGit(t, dir, "rev-parse", "HEAD")
}

// buildWiringPlan is the planner's return for one task, task 1, which builds at check and whose evidence is its
// conclusion.
func buildWiringPlan() store.EpicPlan {
	return store.EpicPlan{
		Tasks: []store.EpicTaskInput{{
			Title: "Wire the build", Deliverable: "the build runs", Files: []string{"wiring.go"},
			Requirements: []string{"R1"}, Gate: "check",
			Evidence: evidence.Item{
				Capture: []string{"just check"}, Limit: 60, Conclusion: "the build runs", Judge: "the judge",
			},
		}},
		Outline: "One task",
		Outcome: "the build runs",
	}
}

// createBuildWiringEpic stores the synthetic accepted epic over repo at base, with a command gate check and an agent gate
// review, and no planned task. Its one pending trigger is the one a planner turn claims.
func createBuildWiringEpic(t *testing.T, st *store.Store, repo, base string) {
	t.Helper()
	created := time.Unix(plannerWiringStamp, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID:          buildWiringEpic,
		Repository:  repo,
		Base:        base,
		Title:       "Wire the build",
		Approved:    "approved text",
		Intent:      "build the next task",
		Care:        "serious",
		Approach:    "one implementer",
		ReleaseThen: "release it",
		Checks:      "just check",
		Phase:       "accepted",
		Created:     created,
		Changed:     created,
		Gates: []store.EpicGate{
			{
				Name: "check", Argv: []string{"/opt/tools/run", "-c", "check"}, Limit: 60,
				Needs: []store.EpicGateNeed{{Pool: "gate", Count: 1}},
			},
			{Name: "review", Agent: "reviewer", Limit: 60, Must: []string{"the diff is correct"}},
		},
		Reviewers: []reviewers.Reviewer{{
			Name: "review", Enabled: true, Stage: []string{reviewers.StageTask}, Agent: "reviewer", Limit: 60,
			Must: []string{"the diff is correct"}, Verify: true,
		}},
		Requirements: []store.EpicRequirement{
			{ID: "R1", Text: "the build runs", Evidence: "wiring test", Kind: "test"},
		},
	}); err != nil {
		t.Fatalf("create epic: %v", err)
	}
}

// planBuildWiringTask completes one planner turn of the synthetic epic, which plans its task 1.
func planBuildWiringTask(t *testing.T, st *store.Store) {
	t.Helper()
	created := time.Unix(plannerWiringStamp, 0)
	if _, err := st.ClaimTurn(t.Context(), buildWiringEpic, created); err != nil {
		t.Fatalf("claim turn: %v", err)
	}
	if err := st.CompleteTurn(t.Context(), buildWiringEpic, 1, buildWiringPlan(), created); err != nil {
		t.Fatalf("complete turn: %v", err)
	}
}

// TestSessionsHostRunsBuildAndParksWithoutRoleFile checks that a --sessions orreryd runs the build Runner over its store.
// The synthetic accepted epic's branch is created at its base, and its open task's implementer step cannot read a role
// file, so the epic parks with that error before any step starts. The host then stops without error.
func TestSessionsHostRunsBuildAndParksWithoutRoleFile(t *testing.T) {
	ctx := t.Context()
	repo, base := buildWiringRepo(t)
	env := map[string]string{
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CONFIG_HOME": t.TempDir(),
	}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	st := openPaneStore(t, path)
	createBuildWiringEpic(t, st, repo, base)
	planBuildWiringTask(t, st)
	epic, err := st.Epic(ctx, buildWiringEpic)
	if err != nil {
		t.Fatalf("read epic: %v", err)
	}
	task := epic.Tasks[0]
	if err := st.SetTaskBuild(ctx, buildWiringEpic, task.Seq, task.Build, store.TaskBuild{
		Stage: build.StageImplement, Base: base, Attempt: 1,
	}); err != nil {
		t.Fatalf("set task build: %v", err)
	}

	opts := options{
		socket:     filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap: defaultSessionCap,
		stepCap:    defaultStepCap,
	}
	host, err := startSessions(
		ctx, opts, buildWiringHost, st, &recordingRouter{}, nil, slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}

	row := waitParked(t, st, buildWiringEpic)
	tip, exists, err := build.BranchTip(ctx, repo, buildWiringEpic)
	if err != nil {
		t.Fatalf("branch tip: %v", err)
	}
	if !exists || tip != base {
		t.Errorf("%s = %q (exists %t), want the base %s", build.BranchRef(buildWiringEpic), tip, exists, base)
	}
	if row.Branch != "created" {
		t.Errorf("branch state = %q, want created", row.Branch)
	}
	if !strings.Contains(row.Parked, "no role file") {
		t.Errorf("parked = %q, want the role file error", row.Parked)
	}
	if step := row.Tasks[0].Build.Step; step != "" {
		t.Errorf("task 1 step = %q, want none: no role file means no step starts", step)
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestReadoptCheckComposesPlannerThenBuild checks that a re-adopted step row gets the planner's return check when it is
// the step of a running planner turn, and the build's verifier check when it is a verifier step. Any other row gets none.
func TestReadoptCheckComposesPlannerThenBuild(t *testing.T) {
	ctx := t.Context()
	st := openPaneStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	createBuildWiringEpic(t, st, plannerWiringRepo, "synthetic-base")
	if _, err := st.ClaimTurn(ctx, buildWiringEpic, time.Unix(plannerWiringStamp, 0)); err != nil {
		t.Fatalf("claim turn: %v", err)
	}
	if err := st.SetTurnStep(ctx, buildWiringEpic, 1, "plan-step"); err != nil {
		t.Fatalf("set turn step: %v", err)
	}
	check := readoptCheck(st, planner.NewResolver(nil, t.TempDir()))
	rows := []struct {
		name string
		row  store.StepRow
		want bool
	}{
		{name: "planner turn step", row: store.StepRow{ID: "plan-step"}, want: true},
		{name: "verifier step", row: store.StepRow{ID: "verify-step", Schema: build.VerifierSchema}, want: true},
		{name: "other step", row: store.StepRow{ID: "other-step", Schema: "other"}, want: false},
	}
	for _, c := range rows {
		got, err := check(ctx, c.row)
		if err != nil {
			t.Errorf("readoptCheck(%s) error = %v, want nil", c.name, err)
		}
		if (got != nil) != c.want {
			t.Errorf("readoptCheck(%s) non-nil = %t, want %t", c.name, got != nil, c.want)
		}
	}
}

// TestReadoptGateComposesEvidenceThenBuild checks that a re-adopted step row gets the judge gate of an evidence run when
// it is a judge step, and the build's review gate when it is a task's review step. Any other row gets none.
func TestReadoptGateComposesEvidenceThenBuild(t *testing.T) {
	ctx := t.Context()
	st := openPaneStore(t, filepath.Join(t.TempDir(), "orrery.db"))
	createBuildWiringEpic(t, st, plannerWiringRepo, "synthetic-base")
	if err := st.AddEvidenceRun(ctx, store.EvidenceRun{
		ID:     "run-1",
		Epic:   buildWiringEpic,
		Item:   "T1",
		Phase:  evidence.PhaseGreen,
		Source: evidence.SourceCaptured,
		Spec: evidence.Item{
			Capture: []string{"just check"}, Limit: 60, Conclusion: "the build runs", Judge: "the judge",
		},
		Created: time.Unix(plannerWiringStamp, 0),
	}); err != nil {
		t.Fatalf("add evidence run: %v", err)
	}
	gate := readoptGate(st)
	rows := []struct {
		name  string
		row   store.StepRow
		agent string
	}{
		{
			name:  "evidence judge",
			row:   store.StepRow{ID: "judge-step", Task: "evidence-run-1", GateName: "evidence_judge"},
			agent: "the judge",
		},
		{
			name: "task review",
			row: store.StepRow{
				ID: "review-step", Epic: buildWiringEpic, Task: "T1", ReadOnly: true, GateName: "review",
			},
			agent: "reviewer",
		},
		{
			name: "task zero review",
			row:  store.StepRow{ID: "zero-step", Epic: buildWiringEpic, Task: "T0", ReadOnly: true, GateName: "review"},
		},
	}
	for _, c := range rows {
		got, err := gate(ctx, c.row)
		if err != nil {
			t.Errorf("readoptGate(%s) error = %v, want nil", c.name, err)
			continue
		}
		switch {
		case c.agent == "" && got != nil:
			t.Errorf("readoptGate(%s) = %+v, want nil", c.name, *got)
		case c.agent != "" && got == nil:
			t.Errorf("readoptGate(%s) = nil, want the gate running %s", c.name, c.agent)
		case got != nil && got.Agent != c.agent:
			t.Errorf("readoptGate(%s) runs agent %q, want %q", c.name, got.Agent, c.agent)
		}
	}
}
