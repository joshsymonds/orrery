package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The sources a task spec version can come from. SourcePlanner is the source of version 1, the planner's epic_tasks row,
// which is never stored as a version row. SourceOrchestrator writes a rerun, grant or re-brief, SourceGrant a rule's
// grant of files, SourceApproach the planner's recarve, and SourceCarry the work a climb could not carry onto its base.
// SourceSendBack writes the record of a planner's send_back, which changes no field. A version of source rescue is still
// read from its stored row, though no version is written with that source.
const (
	SourcePlanner      = "planner"
	SourceOrchestrator = "orchestrator"
	SourceGrant        = "grant"
	SourceApproach     = "approach"
	SourceCarry        = "carry"
	SourceSendBack     = "send_back"
)

// The states a task spec version is in. VersionApplied is the state of a version the task's spec takes, and the
// planner's read overlays applied versions only. VersionChecking is a version whose new evidence is judged at the
// task's base, and VersionRefused one that did not take.
const (
	VersionApplied  = "applied"
	VersionChecking = "checking"
	VersionRefused  = "refused"
)

// ErrVersionNotChecking reports that a task has no version in state checking under the number a settle names, so the
// settle wrote nothing.
var ErrVersionNotChecking = errors.New("version not checking")

// TaskVersion is one stored version of a task's spec: the number the row stores, 2 or more, the source that wrote
// it, the epic decision that did (Decision, empty for orreryd's rule grant), the step that did, and why. A field the
// version does not change is nil. Files is the task's whole file list after the version, TestChanges the whole new
// list, and Deliverable, Gate and Evidence the value the version sets. State is applied, checking or refused. RedRun
// and Refusal are as the row stores them. Created is when the version was stored, and Settled when it left checking,
// zero while it is checking.
type TaskVersion struct {
	Version     int64
	Source      string
	Decision    string
	Step        string
	Reason      string
	State       string
	Deliverable *string
	Files       *[]string
	TestChanges *[]string
	Gate        *string
	Evidence    *evidence.Item
	RedRun      string
	Refusal     string
	Created     time.Time
	Settled     time.Time
}

// Checking returns the task's version in state checking, and false when it has none. A version that changes the
// evidence is checking until its red run settles it.
func (task EpicTask) Checking() (TaskVersion, bool) {
	for _, version := range task.Versions {
		if version.State == VersionChecking {
			return version, true
		}
	}
	return TaskVersion{}, false
}

// Source returns the source of the task's current spec version: the source of its last applied version in Versions, and
// SourcePlanner when none is applied. A checking or refused version does not count.
func (task EpicTask) Source() string {
	source := SourcePlanner
	for _, version := range task.Versions {
		if version.State == VersionApplied {
			source = version.Source
		}
	}
	return source
}

// ApproachStands reports whether a change of approach stands for a task whose versions are versions, in version order: a
// change of approach in any state stands, unless a send_back stored after it reopened the task's ladder, which ends the
// change of approach it reopens from. A reopened task that climbs onto rung 4 again writes a new change of approach.
func ApproachStands(versions []TaskVersion) bool {
	standing := false
	for _, version := range versions {
		switch version.Source {
		case SourceApproach:
			standing = true
		case SourceSendBack:
			standing = false
		}
	}
	return standing
}

// Changed returns the names of the fields the version changes, in the order deliverable, files, test_changes, gate,
// evidence.
func (v TaskVersion) Changed() []string {
	var changed []string
	if v.Deliverable != nil {
		changed = append(changed, "deliverable")
	}
	if v.Files != nil {
		changed = append(changed, "files")
	}
	if v.TestChanges != nil {
		changed = append(changed, "test_changes")
	}
	if v.Gate != nil {
		changed = append(changed, "gate")
	}
	if v.Evidence != nil {
		changed = append(changed, "evidence")
	}
	return changed
}

// TaskVersionInput is a version a writer stores: its Source and Step, the Reason it gives, and the fields it changes. A
// nil Deliverable, TestChanges, Gate or Evidence leaves that field unchanged, and a non-nil one sets it, even to empty.
// Files are the files the version adds to the task, after its last file, and Remove the files it removes from the
// task. The version stores the task's whole file list after it: the task's current list minus Remove, then Files.
type TaskVersionInput struct {
	Source      string
	Step        string
	Reason      string
	Deliverable *string
	Files       []string
	Remove      []string
	TestChanges *[]string
	Gate        *string
	Evidence    *evidence.Item
}

// changesAny reports whether in changes a field. A version that changes none is not stored.
func (in TaskVersionInput) changesAny() bool {
	return in.Deliverable != nil || in.changesFiles() || in.TestChanges != nil || in.Gate != nil || in.Evidence != nil
}

// changesFiles reports whether in adds or removes a file.
func (in TaskVersionInput) changesFiles() bool {
	return len(in.Files) > 0 || len(in.Remove) > 0
}

// appendTaskVersion stores in as the next version of task seq of epic, in the caller's write, applied by the epic
// decision decision, or by none when decision is empty. The version stores the task's whole file list after it, as
// TaskVersionInput describes. A version that changes the evidence is stored checking, unsettled, and leaves the task's
// files as they are until SettleTaskVersion settles it applied. Every other version is applied and settled at at, and
// changes the task's files in this write: it deletes its Remove files and adds its Files after the task's last file. It
// writes nothing when in changes no field. An input that removes a file the task does not own, or leaves the task no
// files, is an error that writes nothing.
func appendTaskVersion(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq int64,
	decision string,
	in TaskVersionInput,
	at time.Time,
) error {
	if !in.changesAny() {
		return nil
	}
	highest, err := queries.MaxEpicTaskVersion(ctx, sqlcgen.MaxEpicTaskVersionParams{Epic: epic, Task: seq})
	if err != nil {
		return fmt.Errorf("read versions of task %d of epic %s: %w", seq, epic, err)
	}
	state, settled := storedState(in, at)
	params := sqlcgen.InsertEpicTaskVersionParams{
		Epic: epic, Task: seq, Version: highest + 1, Source: in.Source, Decision: decision, Step: in.Step,
		Reason: in.Reason, State: state, Created: at.Unix(), Settled: settled,
	}
	if in.changesFiles() {
		encoded, filesErr := writeTaskFiles(ctx, queries, epic, seq, in, state == VersionApplied)
		if filesErr != nil {
			return filesErr
		}
		params.Files = nullText(encoded)
	}
	if in.Deliverable != nil {
		params.Deliverable = nullText(*in.Deliverable)
	}
	if in.TestChanges != nil {
		encoded, encodeErr := encodeList(*in.TestChanges)
		if encodeErr != nil {
			return fmt.Errorf("encode test changes of task %d of epic %s: %w", seq, epic, encodeErr)
		}
		params.TestChanges = nullText(encoded)
	}
	if in.Gate != nil {
		params.Gate = nullText(*in.Gate)
	}
	if in.Evidence != nil {
		encoded, encodeErr := encodeItem(*in.Evidence)
		if encodeErr != nil {
			return fmt.Errorf("encode evidence of task %d of epic %s: %w", seq, epic, encodeErr)
		}
		params.Evidence = nullText(encoded)
	}
	if err = queries.InsertEpicTaskVersion(ctx, params); err != nil {
		return fmt.Errorf("insert version %d of task %d of epic %s: %w", params.Version, seq, epic, err)
	}
	return nil
}

// insertSendBackVersion stores the version a send_back of task seq of epic writes, in the caller's write, applied by
// decision with reason. It changes no field, so the task's spec is what it was, and the version records the send_back
// that reopened the task. It is stored even though no field changes, which appendTaskVersion does not do.
func insertSendBackVersion(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq int64,
	decision, reason string,
	at time.Time,
) error {
	highest, err := queries.MaxEpicTaskVersion(ctx, sqlcgen.MaxEpicTaskVersionParams{Epic: epic, Task: seq})
	if err != nil {
		return fmt.Errorf("read versions of task %d of epic %s: %w", seq, epic, err)
	}
	if err = queries.InsertEpicTaskVersion(ctx, sqlcgen.InsertEpicTaskVersionParams{
		Epic: epic, Task: seq, Version: highest + 1, Source: SourceSendBack, Decision: decision, Reason: reason,
		State: VersionApplied, Created: at.Unix(), Settled: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert version %d of task %d of epic %s: %w", highest+1, seq, epic, err)
	}
	return nil
}

// storedState returns the state in is stored in and the time it is settled at: a version that changes the evidence is
// checking and unsettled, and any other version is applied and settled at at.
func storedState(in TaskVersionInput, at time.Time) (string, int64) {
	if in.Evidence != nil {
		return VersionChecking, 0
	}
	return VersionApplied, at.Unix()
}

// writeTaskFiles stores the file change of in for task seq of epic in the caller's write, and returns the whole file
// list the version stores, encoded. A version applied now changes the task's files, deleting its Remove files and
// adding its Files after the last file. A checking version leaves them until it settles applied. A Remove the task does
// not own, or a list that would be empty, is an error that writes nothing.
func writeTaskFiles(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq int64,
	in TaskVersionInput,
	applied bool,
) (string, error) {
	owned, err := ownedFiles(ctx, queries, epic, seq)
	if err != nil {
		return "", err
	}
	for _, path := range in.Remove {
		if !slices.Contains(owned, path) {
			return "", fmt.Errorf("remove %s from task %d of epic %s: the task does not own it", path, seq, epic)
		}
	}
	whole := append(withoutPaths(owned, in.Remove), in.Files...)
	if len(whole) == 0 {
		return "", fmt.Errorf("version of task %d of epic %s would leave it no files", seq, epic)
	}
	if applied {
		err = applyTaskFiles(ctx, queries, epic, seq, in)
		if err != nil {
			return "", err
		}
	}
	encoded, err := encodeList(whole)
	if err != nil {
		return "", fmt.Errorf("encode files of task %d of epic %s: %w", seq, epic, err)
	}
	return encoded, nil
}

// applyTaskFiles deletes the Remove files of in from task seq of epic and adds its Files after the task's last file, in
// the caller's write.
func applyTaskFiles(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64, in TaskVersionInput) error {
	if err := removeTaskFiles(ctx, queries, epic, seq, in.Remove); err != nil {
		return err
	}
	return appendTaskFiles(ctx, queries, epic, seq, in.Files)
}

// settleTaskFiles makes the files of task seq of epic the whole list that version number of the task stores, in the
// caller's write, when the version stores one. A file the list adds that an open task of the epic other than seq owns
// is a FileOwnedError, as grantFiles refuses it, and writes nothing; one that an accepted task whose integrate batch has
// not ended owns is a RefusalError naming that task, as refuseHeldFiles refuses it, and writes nothing. The build
// reports either and settles again on a later pass, once the holder has integrated. Otherwise the files the task owns
// and the list lacks are deleted, and the files the list adds are appended in its order.
func settleTaskFiles(ctx context.Context, queries *sqlcgen.Queries, epic string, seq, number int64) error {
	rows, err := queries.ListEpicTaskVersions(ctx, epic)
	if err != nil {
		return fmt.Errorf("read versions of epic %s: %w", epic, err)
	}
	byTask, err := taskVersionsOf(rows)
	if err != nil {
		return fmt.Errorf("read versions of epic %s: %w", epic, err)
	}
	index := slices.IndexFunc(byTask[seq], func(version TaskVersion) bool { return version.Version == number })
	if index < 0 {
		return fmt.Errorf("version %d of task %d of epic %s is not stored", number, seq, epic)
	}
	stored := byTask[seq][index].Files
	if stored == nil {
		return nil
	}
	whole := *stored
	owned, err := ownedFiles(ctx, queries, epic, seq)
	if err != nil {
		return err
	}
	added := withoutPaths(whole, owned)
	err = checkFileOwners(ctx, queries, epic, seq, added, nil)
	if err != nil {
		return err
	}
	err = refuseHeldFiles(ctx, queries, epic, seq, added)
	if err != nil {
		return err
	}
	err = removeTaskFiles(ctx, queries, epic, seq, withoutPaths(owned, whole))
	if err != nil {
		return err
	}
	return appendTaskFiles(ctx, queries, epic, seq, added)
}

// ownedFiles returns the files task seq of epic owns, in position order.
func ownedFiles(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64) ([]string, error) {
	rows, err := queries.ListEpicTaskFiles(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("read task files of epic %s: %w", epic, err)
	}
	var owned []string
	for _, row := range rows {
		if row.Task == seq {
			owned = append(owned, row.Path)
		}
	}
	return owned, nil
}

// removeTaskFiles deletes each of paths from the files of task seq of epic, in the caller's write.
func removeTaskFiles(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64, paths []string) error {
	for _, path := range paths {
		if err := queries.DeleteEpicTaskFile(ctx, sqlcgen.DeleteEpicTaskFileParams{
			Epic: epic, Task: seq, Path: path,
		}); err != nil {
			return fmt.Errorf("remove %s from task %d of epic %s: %w", path, seq, epic, err)
		}
	}
	return nil
}

// appendTaskFiles adds files, in order, after the last file of task seq of epic.
func appendTaskFiles(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64, files []string) error {
	rows, err := queries.ListEpicTaskFiles(ctx, epic)
	if err != nil {
		return fmt.Errorf("read task files of epic %s: %w", epic, err)
	}
	var next int64
	for _, row := range rows {
		if row.Task == seq {
			next = max(next, row.Position+1)
		}
	}
	for i, file := range files {
		if err = queries.InsertEpicTaskFile(ctx, sqlcgen.InsertEpicTaskFileParams{
			Epic: epic, Task: seq, Position: next + int64(i), Path: file,
		}); err != nil {
			return fmt.Errorf("grant file %s to task %d of epic %s: %w", file, seq, epic, err)
		}
	}
	return nil
}

// withoutPaths returns the paths of list that are not in drop, in order.
func withoutPaths(list, drop []string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(path string) bool { return slices.Contains(drop, path) })
}

// taskVersionsOf returns the versions stored under each task, from rows in task then version order.
func taskVersionsOf(rows []sqlcgen.EpicTaskVersion) (map[int64][]TaskVersion, error) {
	out := map[int64][]TaskVersion{}
	for _, row := range rows {
		version, err := taskVersionOf(row)
		if err != nil {
			return nil, fmt.Errorf("task %d version %d: %w", row.Task, row.Version, err)
		}
		out[row.Task] = append(out[row.Task], version)
	}
	return out, nil
}

// taskVersionOf returns the version a stored row holds, its fields decoded: a NULL field is nil, and a list or item is
// decoded from its JSON.
func taskVersionOf(stored sqlcgen.EpicTaskVersion) (TaskVersion, error) {
	version := TaskVersion{
		Version: stored.Version, Source: stored.Source, Decision: stored.Decision, Step: stored.Step,
		Reason: stored.Reason, State: stored.State, RedRun: stored.RedRun, Refusal: stored.Refusal,
		Created: time.Unix(stored.Created, 0),
	}
	if stored.Settled != 0 {
		version.Settled = time.Unix(stored.Settled, 0)
	}
	if stored.Deliverable.Valid {
		deliverable := stored.Deliverable.String
		version.Deliverable = &deliverable
	}
	if stored.Gate.Valid {
		gate := stored.Gate.String
		version.Gate = &gate
	}
	if stored.Files.Valid {
		files, err := decodeList[string](stored.Files.String)
		if err != nil {
			return TaskVersion{}, fmt.Errorf("files: %w", err)
		}
		version.Files = &files
	}
	if stored.TestChanges.Valid {
		changes, err := decodeList[string](stored.TestChanges.String)
		if err != nil {
			return TaskVersion{}, fmt.Errorf("test changes: %w", err)
		}
		version.TestChanges = &changes
	}
	if stored.Evidence.Valid {
		item, err := decodeItem(stored.Evidence.String)
		if err != nil {
			return TaskVersion{}, fmt.Errorf("evidence: %w", err)
		}
		version.Evidence = &item
	}
	return version, nil
}

// TaskVersionSettle is how a checking version leaves checking: the Version it settles, the State it settles to, applied
// or refused, the RedRun that judged its evidence, and the Refusal it was refused for, empty when it applies.
type TaskVersionSettle struct {
	Version int64
	State   string
	RedRun  string
	Refusal string
}

// SettleTaskVersion moves the build of task seq of epic from from to to and settles its version settle.Version to
// settle.State, with settle.RedRun and settle.Refusal, at at, in one write, when the stored build is from, as
// SetTaskBuild does. Settled applied, the task's files become the version's stored whole list in the same write: the
// files it deletes are the task's files the list lacks, and the files it adds are the list's files the task lacks,
// appended in the list's order. A file the list adds that another open task of the epic owns is a FileOwnedError, which
// writes nothing and leaves the version checking. Settled refused, the task's files stay as they were. A State other
// than applied or refused is an error. A task with no checking version under settle.Version is ErrVersionNotChecking.
// Both write nothing. A task that is not open, or whose stored build is not from, is ErrTaskMoved and writes nothing.
func (s *Store) SettleTaskVersion(
	ctx context.Context,
	epic string,
	seq int64,
	from, to TaskBuild,
	settle TaskVersionSettle,
	at time.Time,
) error {
	if settle.State != VersionApplied && settle.State != VersionRefused {
		return fmt.Errorf(
			"settle version %d of task %d of epic %s to %q: not applied or refused",
			settle.Version, seq, epic, settle.State,
		)
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := moveTask(ctx, queries, epic, seq, from, to); err != nil {
			return err
		}
		rows, err := queries.SettleEpicTaskVersion(ctx, sqlcgen.SettleEpicTaskVersionParams{
			State: settle.State, RedRun: settle.RedRun, Refusal: settle.Refusal, Settled: at.Unix(),
			Epic: epic, Task: seq, Version: settle.Version,
		})
		if err != nil {
			return fmt.Errorf("settle version %d of task %d of epic %s: %w", settle.Version, seq, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("version %d of task %d of epic %s: %w", settle.Version, seq, epic, ErrVersionNotChecking)
		}
		if settle.State == VersionRefused {
			return nil
		}
		return settleTaskFiles(ctx, queries, epic, seq, settle.Version)
	})
}

// nullText returns text as a stored value that is not NULL.
func nullText(text string) sql.NullString {
	return sql.NullString{String: text, Valid: true}
}
