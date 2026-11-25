package interpret

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/joshsymonds/orrery/internal/build"
	"github.com/joshsymonds/orrery/internal/compose"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/migrations"
	"github.com/joshsymonds/orrery/internal/store"
)

// The sides whose new migrations a migration plan renumbers, as its lines name them: the release's candidate, and the
// task a task integrator merges.
const (
	sideCandidate = "the candidate"
	sideTask      = "the task"
)

// migrationsSection is the heading of the section of an integrator's prompt that names the migration collisions and the
// renumber that settles them.
const migrationsSection = "Migration collisions"

// migrationCheck is how the commit an integrator returns is checked for migration collisions: the directory of goose
// migrations the project declares, origin's commit its migrations are checked against, and also, the commit whose
// migrations are not the integrator's to move, "" for none. A check with no directory checks nothing.
type migrationCheck struct {
	dir, origin, also string
}

// checkMigrations lists the goose migrations in dir at own, origin and also, "" for no tree, in repo on host, and returns
// what own's new migrations collide with among origin's (migrations.Check). Each tree is read with git ls-tree, so no
// worktree is needed.
func checkMigrations(
	ctx context.Context, host hostexec.Host, repo, dir, own, origin, also string,
) (migrations.Plan, error) {
	ownFiles, err := migrations.List(ctx, host, repo, own, dir)
	if err != nil {
		return migrations.Plan{}, fmt.Errorf("%w", err)
	}
	originFiles, err := migrations.List(ctx, host, repo, origin, dir)
	if err != nil {
		return migrations.Plan{}, fmt.Errorf("%w", err)
	}
	var alsoFiles []migrations.File
	if also != "" {
		if alsoFiles, err = migrations.List(ctx, host, repo, also, dir); err != nil {
			return migrations.Plan{}, fmt.Errorf("%w", err)
		}
	}
	return migrations.Check(ownFiles, originFiles, alsoFiles), nil
}

// batchMigrationsOf returns how the batch of the integrate run run, composed on base, checks its tasks' migrations: the
// directory of goose migrations the project file at base declares (enginehost.ReadProjectMigrations), and origin's
// commit, fetched from origin's default branch into the run's own origin ref (fetchOrigin) for at most
// batchOriginLimit. The declaration is read at base, so a repository that declares no migrations never reads origin,
// and its check has no directory. A read of origin that fails checks nothing: the batch composes without the check,
// the reason is recorded as Unread and logged, and the release checks the candidate against origin again. A
// declaration that does not parse is an error.
func (r *Runner) batchMigrationsOf(
	ctx context.Context, host hostexec.Host, epic store.EpicRow, run store.NodeRun, base string,
) (batchMigrations, error) {
	dir, err := enginehost.ReadProjectMigrations(ctx, host, epic.Repository, base)
	if err != nil {
		return batchMigrations{}, fmt.Errorf("read the migrations the batch base %s declares: %w", base, err)
	}
	if dir == "" {
		return batchMigrations{}, nil
	}
	readCtx, cancel := context.WithTimeout(ctx, batchOriginLimit)
	defer cancel()
	ref := fmt.Sprintf("refs/orrery/%s/integrate/%s-%d-origin", build.Slug(epic.ID), run.ID, run.Attempt)
	_, origin, err := fetchOrigin(readCtx, host, epic, ref)
	if err != nil {
		r.logger.WarnContext(ctx, "batch migrations not checked: origin's main was not read", "epic", epic.ID,
			"node", run.Path, "err", err)
		return batchMigrations{Dir: dir, Unread: err.Error()}, nil
	}
	return batchMigrations{Dir: dir, Origin: origin}, nil
}

// collidingTasks splits tasks, in order, into the tasks the batch composes on base and the tasks it leaves out for a
// migration collision, as checked says to check them: a task whose revision adds a goose migration, one neither origin's
// commit nor base holds, whose version origin's main uses for another file (migrations.Check). Each such task is left
// out with its colliding migrations as its conflicts, so its task integrator rounds take it and renumber them, and with
// a reason that names the collisions and the renumber (migrations.Plan.Summary). A batch that checks nothing composes
// every task.
func collidingTasks(
	ctx context.Context, host hostexec.Host, repo string, checked batchMigrations, base string, tasks []compose.Task,
) ([]compose.Task, []compose.LeftOut, error) {
	if checked.Origin == "" {
		return tasks, nil, nil
	}
	composed := make([]compose.Task, 0, len(tasks))
	var collided []compose.LeftOut
	for _, task := range tasks {
		plan, err := checkMigrations(ctx, host, repo, checked.Dir, task.Revision, checked.Origin, base)
		if err != nil {
			return nil, nil, err
		}
		if len(plan.Collisions) == 0 {
			composed = append(composed, task)
			continue
		}
		paths := make([]string, 0, len(plan.Collisions))
		for _, collision := range plan.Collisions {
			paths = append(paths, collision.Path)
		}
		collided = append(collided, compose.LeftOut{
			Seq: task.Seq,
			Reason: fmt.Sprintf("T%d adds a migration whose version origin's main %s uses for another file: %s",
				task.Seq, checked.Origin, plan.Summary(sideTask)),
			Conflicts: paths,
		})
	}
	return composed, collided, nil
}

// taskMigrationCheck returns how the commit of a task integrator round that merges into candidate is checked for
// migration collisions, from result, the result its integrate run claimed its batch with: against the origin commit the
// batch checked, with candidate's migrations not the round's to move. A batch that checked nothing checks nothing here.
func taskMigrationCheck(result, candidate string) migrationCheck {
	var batch batchResult
	if json.Unmarshal([]byte(result), &batch) != nil || batch.Migrations == nil || batch.Migrations.Origin == "" {
		return migrationCheck{}
	}
	return migrationCheck{dir: batch.Migrations.Dir, origin: batch.Migrations.Origin, also: candidate}
}

// taskCollisions returns the lines of the Migration collisions section of a task integrator round that finishes merge:
// each migration the task's revision adds, one neither origin's commit nor the candidate holds, whose version origin's
// main uses for another file, and the renumber of the task's new migrations after every version origin and the
// candidate use (migrations.Plan.Lines). A merge that checks no migrations, or a task with no collision, has none.
func taskCollisions(ctx context.Context, host hostexec.Host, merge integratorMerge) ([]string, error) {
	check := merge.migrations
	if check.dir == "" {
		return nil, nil
	}
	plan, err := checkMigrations(ctx, host, merge.repo, check.dir, merge.origin, check.origin, check.also)
	if err != nil {
		return nil, err
	}
	return plan.Lines(sideTask), nil
}

// migrationFailure names each new migration of commit whose version origin's main still uses for another file, when
// merge checks its migrations (migrationCheck), as the line the return check fails on. A check git cannot make fails
// with git's error.
func migrationFailure(
	ctx context.Context, host hostexec.Host, merge integratorMerge, commit, _ string, _ integratorReturn,
) string {
	check := merge.migrations
	if check.dir == "" {
		return ""
	}
	plan, err := checkMigrations(ctx, host, merge.repo, check.dir, commit, check.origin, check.also)
	if err != nil {
		return "migrations: " + err.Error()
	}
	return plan.Failure()
}
