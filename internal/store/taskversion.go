package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The sources a task spec version can come from. Only SourceOrchestrator and SourceGrant are written in this epic; the
// other three are reserved for a later epic.
const (
	SourceOrchestrator = "orchestrator"
	SourceGrant        = "grant"
	SourceRescue       = "rescue"
	SourceApproach     = "approach"
	SourceCarry        = "carry"
)

// versionApplied is the state of a version the task's spec takes. The planner's read overlays applied versions only.
const versionApplied = "applied"

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
// version stores the task's whole file list. It writes nothing when in changes no field.
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
	params := sqlcgen.InsertEpicTaskVersionParams{
		Epic: epic, Task: seq, Version: highest + 1, Source: in.Source, Decision: decision, Step: in.Step,
		Reason: in.Reason, Created: at.Unix(), Settled: at.Unix(),
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

// nullText returns text as a stored value that is not NULL.
func nullText(text string) sql.NullString {
	return sql.NullString{String: text, Valid: true}
}
