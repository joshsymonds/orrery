package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The sources a task spec version can come from. SourcePlanner is the source of version 1, the planner's epic_tasks row,
// which is never stored as a version row. SourceOrchestrator and SourceGrant are the sources this epic writes; the
// other three are reserved for a later epic.
const (
	SourcePlanner      = "planner"
	SourceOrchestrator = "orchestrator"
	SourceGrant        = "grant"
	SourceRescue       = "rescue"
	SourceApproach     = "approach"
	SourceCarry        = "carry"
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
// Files are the files the version adds to the task, after its last file.
type TaskVersionInput struct {
	Source      string
	Step        string
	Reason      string
	Deliverable *string
	Files       []string
	TestChanges *[]string
	Gate        *string
	Evidence    *evidence.Item
}

// changesAny reports whether in changes a field. A version that changes none is not stored.
func (in TaskVersionInput) changesAny() bool {
	return in.Deliverable != nil || len(in.Files) > 0 || in.TestChanges != nil || in.Gate != nil || in.Evidence != nil
}

// appendTaskVersion stores in as the next version of task seq of epic, in the caller's write, applied by the epic
// decision decision, or by none when decision is empty. Its files are added after the task's last file, and the
// version stores the task's whole file list. A version that changes the evidence is stored checking, unsettled, and
// every other version applied and settled at at. It writes nothing when in changes no field, and an input that sets
// both evidence and files is an error that writes nothing.
func appendTaskVersion(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq int64,
	decision string,
	in TaskVersionInput,
	at time.Time,
) error {
	if err := checkVersionChanges(epic, seq, in); err != nil {
		return err
	}
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
	if len(in.Files) > 0 {
		whole, filesErr := appendTaskFiles(ctx, queries, epic, seq, in.Files)
		if filesErr != nil {
			return filesErr
		}
		encoded, encodeErr := encodeList(whole)
		if encodeErr != nil {
			return fmt.Errorf("encode files of task %d of epic %s: %w", seq, epic, encodeErr)
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

// checkVersionChanges returns an error when in sets both evidence and files, which one version may not do.
func checkVersionChanges(epic string, seq int64, in TaskVersionInput) error {
	if in.Evidence != nil && len(in.Files) > 0 {
		return fmt.Errorf("version of task %d of epic %s sets both evidence and files", seq, epic)
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

// appendTaskFiles adds files, in order, after the last file of task seq of epic, and returns the task's whole file
// list.
func appendTaskFiles(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq int64,
	files []string,
) ([]string, error) {
	rows, err := queries.ListEpicTaskFiles(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("read task files of epic %s: %w", epic, err)
	}
	var whole []string
	var next int64
	for _, row := range rows {
		if row.Task == seq {
			whole = append(whole, row.Path)
			next = max(next, row.Position+1)
		}
	}
	for i, file := range files {
		if err = queries.InsertEpicTaskFile(ctx, sqlcgen.InsertEpicTaskFileParams{
			Epic: epic, Task: seq, Position: next + int64(i), Path: file,
		}); err != nil {
			return nil, fmt.Errorf("grant file %s to task %d of epic %s: %w", file, seq, epic, err)
		}
		whole = append(whole, file)
	}
	return whole, nil
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
// SetTaskBuild does. A State other than applied or refused is an error. A task with no checking version under
// settle.Version is ErrVersionNotChecking. Both write nothing. A task that is not open, or whose stored build is not
// from, is ErrTaskMoved and writes nothing.
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
		return nil
	})
}

// nullText returns text as a stored value that is not NULL.
func nullText(text string) sql.NullString {
	return sql.NullString{String: text, Valid: true}
}
