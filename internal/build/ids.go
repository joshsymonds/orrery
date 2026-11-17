package build

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
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

// StepID returns the id of the step of unit for attempt and rerun of task seq of epic, at gatePos and findingPos.
// A unit with no review gate or finding passes zero for them. The same inputs give the same id, so a step started
// again under its id is that step.
func StepID(epic string, seq int64, unit string, attempt, rerun, gatePos, findingPos int64) string {
	return digest(
		idNamespace, epic, taskLabel(seq), unit,
		decimal(attempt), decimal(rerun), decimal(gatePos), decimal(findingPos),
	)[:idLength]
}

// RunID returns the id of the evidence run of phase for attempt and rerun of task seq of epic. The same inputs give
// the same id, so a run started again under its id is that run.
func RunID(epic string, seq int64, phase string, attempt, rerun int64) string {
	return digest(idNamespace, epic, taskLabel(seq), phase, decimal(attempt), decimal(rerun))[:idLength]
}

// TaskDir returns the directory the worktrees of task seq of epic are added under: a name of taskDirLength hex
// digits under the task worktrees directory of stateDir.
func TaskDir(stateDir, epic string, seq int64) string {
	return filepath.Join(stateDir, worktreeDir, digest(epic, taskLabel(seq))[:taskDirLength])
}

// ImplementWorktree returns the path of the worktree every implementer step of task seq of epic runs in, under the
// task's directory. It is the task's, not one step's, so a later attempt or rerun continues from the earlier work.
func ImplementWorktree(stateDir, epic string, seq int64) string {
	return filepath.Join(TaskDir(stateDir, epic, seq), "implement")
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
