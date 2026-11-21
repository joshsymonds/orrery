package build

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// idNamespace starts the parts every derived id hashes, so the ids are this package's.
const idNamespace = "orrery-build"

// worktreeDir is the directory under the state directory the task worktrees are added under.
const worktreeDir = "task-worktrees"

// idLength is the number of hex digits of a step or run id, and taskDirLength the number of hex digits of a task's
// worktree directory name.
const (
	idLength      = 32
	taskDirLength = 16
)

// unitScope returns the scope the derived ids of task's units are made under, given the task's epic. A task never sent
// back, at generation 0, is scoped by its epic alone, so its ids are those of its epic. A task sent back n times, at
// generation n, is scoped by its epic and "/reopen-n", so a reopened task restarts at attempt 1 under new ids and never
// adopts the steps or runs an earlier attempt of the task used.
func unitScope(epic string, task store.EpicTask) string {
	if task.Generation == 0 {
		return epic
	}
	return epic + "/reopen-" + decimal(task.Generation)
}

// StepID returns the id of the step of unit for attempt and rerun of task seq, in scope, at gatePos and findingPos. The
// scope is the unit scope of the task (unitScope), which is its epic for a task never sent back. A unit with no review
// gate or finding passes zero for them. The same inputs give the same id, so a step started again under its id is that
// step.
func StepID(scope string, seq int64, unit string, attempt, rerun, gatePos, findingPos int64) string {
	return digest(
		idNamespace, scope, taskLabel(seq), unit,
		decimal(attempt), decimal(rerun), decimal(gatePos), decimal(findingPos),
	)[:idLength]
}

// RunID returns the id of the evidence run of phase for attempt and rerun of task seq, in scope, as StepID's scope
// describes. The same inputs give the same id, so a run started again under its id is that run.
func RunID(scope string, seq int64, phase string, attempt, rerun int64) string {
	return digest(idNamespace, scope, taskLabel(seq), phase, decimal(attempt), decimal(rerun))[:idLength]
}

// TaskDir returns the directory the worktrees of task seq of epic are added under: a name of taskDirLength hex
// digits under the task worktrees directory of stateDir.
func TaskDir(stateDir, epic string, seq int64) string {
	return filepath.Join(stateDir, worktreeDir, digest(epic, taskLabel(seq))[:taskDirLength])
}

// ImplementWorktree returns the path of the worktree every implementer step of task of epic runs in, under the task's
// directory. It is the task's, not one step's, so a later attempt or rerun continues from the earlier work. A task sent
// back n times runs in implement-reopen-n instead, a new worktree added at the reopened build's base, so its implementer
// never starts on the work a send_back dropped; it stays under the task's directory, which the task's cleanup removes
// whole.
func ImplementWorktree(stateDir, epic string, task store.EpicTask) string {
	name := "implement"
	if task.Generation > 0 {
		name += "-reopen-" + decimal(task.Generation)
	}
	return filepath.Join(TaskDir(stateDir, epic, task.Seq), name)
}

// digest returns the lowercase hex sha256 of parts joined with NUL.
func digest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// taskLabel is the label of task seq, T and its seq.
func taskLabel(seq int64) string {
	return "T" + decimal(seq)
}

// decimal renders n in base ten, as the numbers of a derived id are rendered.
func decimal(n int64) string {
	return strconv.FormatInt(n, 10)
}
