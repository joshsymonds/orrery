package interpret

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// The migrations the collision tests' project declares, and the paths of its migrations: the one the base holds, the
// one origin's main adds after it, and the one the epic or its task adds with the same version, and that one renumbered
// after origin's.
const (
	collisionProject = "[migrations]\ngoose = \"db/migrations\"\n"
	collisionBase    = "db/migrations/00001_base.sql"
	collisionOrigin  = "db/migrations/00002_origin.sql"
	collisionEpic    = "db/migrations/00002_epic.sql"
	collisionMoved   = "db/migrations/00003_epic.sql"
)

// collisionWire is the migration part of a release run's Result as the tests read it from the store.
type collisionWire struct {
	mergeWire
	Branch     string   `json:"branch"`
	Migrations string   `json:"migrations"`
	Collisions []string `json:"collisions"`
}

// declareMigrations commits the project file that declares the collision tests' migrations, and their base migration,
// to main in repo, and leaves main checked out. It returns the commit.
func declareMigrations(t *testing.T, repo string) string {
	t.Helper()
	nodeWrite(t, repo, ".orrery/project.toml", collisionProject)
	nodeWrite(t, repo, collisionBase, "-- base\n")
	nodeGit(t, repo, "add", ".orrery/project.toml", collisionBase)
	nodeGit(t, repo, "commit", "-q", "-m", "declare migrations")
	return nodeGit(t, repo, "rev-parse", "HEAD")
}

// collisionLines are the Migration collisions lines of a candidate whose collisionEpic takes the version of origin's
// collisionOrigin, renumbered after it.
func collisionLines(side string) []string {
	return []string{
		"- " + collisionEpic + " collides with origin's " + collisionOrigin + ": both are version 2",
		"Renumber " + side + "'s new migrations, in order:",
		"- " + collisionEpic + " to " + collisionMoved,
		"Rename each file with git mv, change every reference to its old name or version, make again what the " +
			"repository generates from its migrations, and list each changed path in other_changes.",
	}
}

// renumbered moves collisionEpic to collisionMoved in the worktree wt and amends HEAD with the move, so HEAD keeps its
// parents and message. It returns the amended HEAD.
func renumbered(t *testing.T, wt string) string {
	t.Helper()
	nodeGit(t, wt, "mv", collisionEpic, collisionMoved)
	nodeGit(t, wt, "commit", "-q", "--amend", "--no-edit")
	return nodeGit(t, wt, "rev-parse", "HEAD")
}

// renumberChanges are the other_changes of a return that renumbered collisionEpic, keyed as the integrator named by
// key lists them: its old path and its new one.
func renumberChanges(key string) []map[string]any {
	return []map[string]any{
		{"path": collisionEpic, key: "renumbered after origin's 00002_origin.sql"},
		{"path": collisionMoved, key: "renumbered after origin's 00002_origin.sql"},
	}
}

// TestReleaseMigrationCollisionBriefsTheIntegrator: origin's main and the epic's tip each add a migration of version 2
// under the directory origin's project file declares, and git merges the two with no conflict. The prepare records the
// directory and the collision with its renumber, and the release integrator's prompt carries them under Migration
// collisions. A commit of git's merge as it stands is refused for the collision; the same merge with the epic's
// migration renumbered passes, and is recorded as the candidate.
func TestReleaseMigrationCollisionBriefsTheIntegrator(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	declareMigrations(t, w.repo)
	remote := mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, collisionEpic)
	origin := mergeOriginCommit(t, remote, collisionOrigin)
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	var got collisionWire
	if err := json.Unmarshal([]byte(run.Result), &got); err != nil {
		t.Fatalf("Result %q: %v", run.Result, err)
	}
	if !got.Behind || got.Tip != tip || got.Origin != origin || got.Candidate != "" ||
		got.Migrations != "db/migrations" || !slices.Equal(got.Collisions, collisionLines(sideCandidate)) {
		t.Fatalf("Result = %+v, want the tip behind origin with the collision of %s and its renumber", got,
			collisionEpic)
	}
	id := w.integratorID()
	prompt := w.started(id).Prompt
	requireSections(t, prompt, integratorSections()...)
	want := "\n## Migration collisions\n" + strings.Join(collisionLines(sideCandidate), "\n") + "\n"
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt lacks %q:\n%s", want, prompt)
	}

	wt := w.worktree()
	merge := w.commitCleanMerge()
	failures := w.checkReturn(id, cleanReturn(merge))
	if want := "migrations: " + collisionEpic + " collides with origin's " + collisionOrigin; len(failures) != 1 ||
		!strings.HasPrefix(failures[0], want) {
		t.Errorf("return check of the merge with the collision = %q, want the one line %q", failures, want)
	}
	moved := renumbered(t, wt)
	ret := cleanReturn(moved)
	ret["other_changes"] = renumberChanges("change")
	if failures = w.checkReturn(id, ret); len(failures) != 0 {
		t.Fatalf("return check of the renumbered merge = %q, want no failure", failures)
	}
	w.endStep(id, "done", "done", ret)
	if logged := w.pass(); logged != "" {
		t.Fatalf("pass logged: %s", logged)
	}
	candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
	if len(candidates) != 1 || candidates[0].Head != moved || !slices.Equal(candidates[0].Commits, []string{moved}) {
		t.Errorf("candidate runs = %+v, want the renumbered merge %s", candidates, moved)
	}
}

// TestReleaseMigrationCollisionInTheTipStartsTheIntegrator: the epic's tip already holds origin's main, and both add a
// migration of version 2, so the tip itself cannot ship. The prepare records no candidate: it records origin's commit
// with the tip not behind, and the collision. The release integrator starts on the tip with no merge in progress, its
// prompt saying origin's commit is already in the tip and giving the renumber's message. A commit on the tip alone
// that renumbers the epic's migration passes, and is recorded as the candidate; one with origin's commit as a second
// parent is refused for its shape.
func TestReleaseMigrationCollisionInTheTipStartsTheIntegrator(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	declareMigrations(t, w.repo)
	remote := mergeRemote(t, w.repo)
	origin := mergeOriginCommit(t, remote, collisionOrigin)
	nodeGit(t, w.repo, "pull", "-q", "--ff-only", "origin", "main")
	tip := nodeEpicBranch(t, w.repo, collisionEpic)
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	var got collisionWire
	if err := json.Unmarshal([]byte(run.Result), &got); err != nil {
		t.Fatalf("Result %q: %v", run.Result, err)
	}
	if got.Behind || got.Tip != tip || got.Origin != origin || got.Branch != "refs/heads/main" || got.Candidate != "" ||
		run.Revision != "" || !slices.Equal(got.Collisions, collisionLines(sideCandidate)) {
		t.Fatalf("Result = %+v with revision %q, want the tip holding origin's commit, the collision and no "+
			"candidate", got, run.Revision)
	}
	id := w.integratorID()
	start := w.started(id)
	wt := w.worktree()
	if head := nodeGit(t, wt, "rev-parse", "HEAD"); head != tip || start.Revision != tip {
		t.Errorf("worktree HEAD = %s and start revision %s, want the tip %s", head, start.Revision, tip)
	}
	mergeHead := nodeGit(t, wt, "rev-parse", "--path-format=absolute", "--git-path", "MERGE_HEAD")
	if _, err := os.Stat(filepath.Clean(mergeHead)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s = %v, want no merge in progress", mergeHead, err)
	}
	for _, want := range []string{
		"Origin's commit: " + origin + " from origin's refs/heads/main, already in the release tip, so there is no " +
			"merge and no MERGE_HEAD",
		"Commit message: " + renumberMessage(tip, origin),
		"\n## Migration collisions\n- " + collisionEpic + " collides with origin's " + collisionOrigin,
	} {
		if !strings.Contains(start.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, start.Prompt)
		}
	}

	nodeGit(t, wt, "mv", collisionEpic, collisionMoved)
	nodeGit(t, wt, "commit", "-q", "-m", renumberMessage(tip, origin))
	moved := nodeGit(t, wt, "rev-parse", "HEAD")
	ret := cleanReturn(moved)
	ret["other_changes"] = renumberChanges("change")
	if failures := w.checkReturn(id, ret); len(failures) != 0 {
		t.Fatalf("return check of the renumber = %q, want no failure", failures)
	}

	shaped := nodeGit(t, wt, "commit-tree", moved+"^{tree}", "-p", tip, "-p", origin, "-m", "two parents")
	nodeGit(t, wt, "reset", "-q", "--soft", shaped)
	if failures := w.checkReturn(id, cleanReturn(shaped)); !hasLine(failures, "parents: commit "+shaped) {
		t.Errorf("return check of a commit with origin's commit as a parent = %q, want a parents line", failures)
	}
	nodeGit(t, wt, "reset", "-q", "--soft", moved)

	w.endStep(id, "done", "done", ret)
	if logged := w.pass(); logged != "" {
		t.Fatalf("pass logged: %s", logged)
	}
	candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
	if len(candidates) != 1 || candidates[0].Head != moved || candidates[0].Start != tip ||
		!slices.Equal(candidates[0].Commits, []string{moved}) {
		t.Errorf("candidate runs = %+v, want the renumber %s made on the tip %s", candidates, moved, tip)
	}
}

// TestReleaseTipWithoutCollisionIsTheCandidate: a tip that holds origin's main and adds a migration after origin's
// newest is the candidate at once, with the declared directory recorded and no collision.
func TestReleaseTipWithoutCollisionIsTheCandidate(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	declareMigrations(t, w.repo)
	remote := mergeRemote(t, w.repo)
	mergeOriginCommit(t, remote, collisionOrigin)
	nodeGit(t, w.repo, "pull", "-q", "--ff-only", "origin", "main")
	tip := nodeEpicBranch(t, w.repo, collisionMoved)
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	var got collisionWire
	if err := json.Unmarshal([]byte(run.Result), &got); err != nil {
		t.Fatalf("Result %q: %v", run.Result, err)
	}
	if got.Candidate != tip || got.Origin != "" || got.Migrations != "db/migrations" || len(got.Collisions) != 0 ||
		run.Revision != tip {
		t.Errorf("Result = %+v with revision %q, want the tip %s as the candidate with no collision", got,
			run.Revision, tip)
	}
}

// collisionBatch is the integrate run's Result as the collision test reads it from the store: its tasks and the
// migration check it made.
type collisionBatch struct {
	Composed []struct {
		Task int64 `json:"task"`
	} `json:"composed"`
	LeftOut []struct {
		Task      int64    `json:"task"`
		Reason    string   `json:"reason"`
		Conflicts []string `json:"conflicts"`
	} `json:"leftOut"`
	Migrations struct {
		Dir    string `json:"dir"`
		Origin string `json:"origin"`
	} `json:"migrations"`
}

// TestIntegrateMigrationCollisionGoesToTheTaskIntegrator: the epic's base declares its migrations, and origin's main
// has added a migration of version 2 since. Task 1 adds another of version 2 and task 2 changes other code. The batch
// composes task 2 and leaves task 1 out for the collision, naming it and the renumber, with its migration as its
// conflict, and records the origin commit it checked against. Task 1's task integrator round starts on the merge, which
// has no conflict, and its prompt carries the collision under Migration collisions. A merged commit that keeps the
// collision is refused; one that renumbers the migration passes, and the round is recorded merged.
func TestIntegrateMigrationCollisionGoesToTheTaskIntegrator(t *testing.T) {
	t.Parallel()
	var remote, origin string
	w := newIntegrateWorld(t, madeBy(func(t *testing.T) (string, string) {
		t.Helper()
		repo, _ := nodeRepo(t)
		base := declareMigrations(t, repo)
		remote = mergeRemote(t, repo)
		origin = mergeOriginCommit(t, remote, collisionOrigin)
		return repo, base
	}))
	w.mergeIdentity()
	w.runner.stateDir = t.TempDir()
	w.runner.getenv = integratorRoles(t)
	first := w.commit(w.base, collisionEpic, "-- epic\n")
	second := w.commit(w.base, "b.go", "two\n")
	w.pass()
	w.planTurn()
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()

	run := runAt(t, w.s, integrateBatch)
	var batch collisionBatch
	if err := json.Unmarshal([]byte(run.Result), &batch); err != nil {
		t.Fatalf("Result %q: %v", run.Result, err)
	}
	wantReason := "T1 adds a migration whose version origin's main " + origin + " uses for another file: " +
		"00002_epic.sql collides with origin's 00002_origin.sql; renumber the task's new migrations to 00003 in order"
	if len(batch.Composed) != 1 || batch.Composed[0].Task != 2 || len(batch.LeftOut) != 1 ||
		batch.LeftOut[0].Task != 1 || batch.LeftOut[0].Reason != wantReason ||
		!slices.Equal(batch.LeftOut[0].Conflicts, []string{collisionEpic}) ||
		batch.Migrations.Dir != "db/migrations" || batch.Migrations.Origin != origin {
		t.Fatalf("batch = %+v, want task 2 composed and task 1 left out with %q, checked against %s", batch,
			wantReason, origin)
	}

	id := workID(t, w.s, integrateBatch, "task integrator T1 1")
	i := slices.IndexFunc(w.fakes.steps.calls, func(start enginehost.StepStart) bool { return start.ID == id })
	if i < 0 {
		t.Fatalf("no task integrator round of task 1 among %+v", w.fakes.steps.calls)
	}
	start := w.fakes.steps.calls[i]
	if unmerged := nodeGit(t, start.Workspace, "ls-files", "-u"); unmerged != "" {
		t.Errorf("unmerged paths = %q, want none: task 1 merges with no conflict", unmerged)
	}
	if want := "\n## Migration collisions\n" + strings.Join(collisionLines(sideTask), "\n") +
		"\n\n## Previous attempt\n"; !strings.Contains(start.Prompt, want) {
		t.Errorf("prompt lacks %q:\n%s", want, start.Prompt)
	}

	nodeGit(t, start.Workspace, "commit", "-q", "-m", taskMergeMessage(1, first))
	kept := nodeGit(t, start.Workspace, "rev-parse", "HEAD")
	merged := func(commit string, changes []map[string]any) []string {
		encoded, err := json.Marshal(map[string]any{
			"status": "done", "verdict": "merged", "commit": commit, "explanation": "task 1 merged",
			"other_changes": changes,
		})
		if err != nil {
			t.Fatalf("encode return: %v", err)
		}
		return start.ReturnCheck(encoded)
	}
	failures := merged(kept, []map[string]any{})
	if want := "migrations: " + collisionEpic + " collides with origin's " + collisionOrigin; len(failures) != 1 ||
		!strings.HasPrefix(failures[0], want) {
		t.Errorf("return check of the merge with the collision = %q, want the one line %q", failures, want)
	}
	moved := renumbered(t, start.Workspace)
	if failures = merged(moved, renumberChanges("reason")); len(failures) != 0 {
		t.Fatalf("return check of the renumbered merge = %q, want no failure", failures)
	}
	endTaskStep(t, w.s, id, "done", "done", map[string]any{
		"status": "done", "verdict": "merged", "commit": moved, "explanation": "task 1 merged",
		"other_changes": renumberChanges("reason"),
	})
	w.pass()
	flow, err := w.s.EpicFlow(t.Context(), runEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	j := slices.IndexFunc(flow.Rounds, func(record store.IntegrateRound) bool {
		return record.Task == 1 && record.Round == 1
	})
	if j < 0 || flow.Rounds[j].Verdict != "merged" || flow.Rounds[j].Head != moved {
		t.Errorf("rounds = %+v, want round 1 of task 1 merged at the renumbered %s", flow.Rounds, moved)
	}
}
