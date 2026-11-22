package server

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The kinds of list row an archive mark names. They are the kinds store.ArchiveRow accepts.
const (
	archiveKindEpic     = "epic"
	archiveKindSession  = "session"
	archiveKindHeadless = "headless"
)

// stateStopped is the headless state of a session the owner stopped. stateFailed is the state of one that failed.
const stateStopped = "stopped"

// epicAge is how long an epic that is not live must go unchanged before it ages out. finalEpicAge is how long a final
// epic must go unchanged before it ages out when no owner has seen it. headlessAge is how long a stopped or failed
// headless session must go unchanged before it ages out.
const (
	epicAge      = 7 * 24 * time.Hour
	finalEpicAge = 24 * time.Hour
	headlessAge  = 24 * time.Hour
)

// archiveKey names one list row by its kind and id.
type archiveKey struct {
	kind, id string
}

// archiveMarks maps each row that has an archive mark to that mark. A row with no mark is absent, and reads as the zero
// mark.
type archiveMarks map[archiveKey]store.ArchiveMark

// readArchiveMarks reads every archive mark through reader into a map keyed by kind and id. A nil reader has no marks,
// so the rows are judged by aging alone. A failed read answers unavailable for store.ErrUnavailable and internal
// otherwise, with fixed messages.
func readArchiveMarks(ctx context.Context, reader *store.Reader) (archiveMarks, error) {
	marks := archiveMarks{}
	if reader == nil {
		return marks, nil
	}
	rows, err := reader.ArchivedRows(ctx)
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read archive marks")
	}
	for _, row := range rows {
		marks[archiveKey{kind: row.Kind, id: row.ID}] = row
	}
	return marks, nil
}

// archivedRow is the archive rule every list applies at its now. changed is the row's change time in unix seconds, mark
// is its mark, and aged reports whether the row ages out at now (see epicAged and headlessAged). A mark with an
// archived_at archives the row while changed is at or before archived_at, so a row that changes after its archive comes
// back. Otherwise an aged row is archived unless the owner unarchived it at or after its last change, so an unarchived
// row stays out of aging until it changes.
func archivedRow(mark store.ArchiveMark, changed int64, aged bool) bool {
	if !mark.ArchivedAt.IsZero() && changed <= mark.ArchivedAt.Unix() {
		return true
	}
	if !aged {
		return false
	}
	return mark.UnarchivedAt.IsZero() || mark.UnarchivedAt.Unix() < changed
}

// The phase and outcome spellings the final-epic rule reads, named once here.
const (
	epicPhaseReleased = "released"
	epicPhaseDone     = "done"
)

// epicFinal reports whether an epic from source with outcome has a final outcome. An orreryd epic is final in released,
// done, closed_superseded, closed_shipped, closed_abandoned or met_at_base. A record epic is final when its outcome's
// leading letters, read as epicLive reads them, are released or retired.
func epicFinal(source, outcome string) bool {
	switch source {
	case epicSourceOrreryd:
		switch outcome {
		case epicPhaseReleased, epicPhaseDone, store.PhaseClosedSuperseded, store.PhaseClosedShipped,
			store.PhaseClosedAbandoned, "met_at_base":
			return true
		}
	case epicSourceRecord:
		switch leadingLetters(outcome) {
		case epicPhaseReleased, "retired":
			return true
		}
	}
	return false
}

// epicAged reports whether an epic that Epics.List answers ages out at now. A live epic never ages. An epic that is not
// live ages once it changed at least epicAge before now. A final epic (see epicFinal) also ages once its seen time is
// set and at or after its change, or once it changed at least finalEpicAge before now.
func epicAged(epic *orreryv1.Epic, now time.Time) bool {
	if epic.GetLive() {
		return false
	}
	changed := epic.GetChanged()
	if changed <= now.Add(-epicAge).Unix() {
		return true
	}
	if !epicFinal(epic.GetSource(), epic.GetOutcome()) {
		return false
	}
	seen := epic.GetSeen()
	return (seen != 0 && seen >= changed) || changed <= now.Add(-finalEpicAge).Unix()
}

// headlessAged reports whether a headless session in state, changed at changed and seen at seen, ages out at now. A
// stopped session ages once it has been unchanged for headlessAge. A failed session ages once it has been unchanged for
// headlessAge and has been seen since it failed. No other state ages.
func headlessAged(state string, changed, seen int64, now time.Time) bool {
	if changed > now.Add(-headlessAge).Unix() {
		return false
	}
	switch state {
	case stateStopped:
		return true
	case stateFailed:
		return seen >= changed
	default:
		return false
	}
}

// markEpics sets the archived field of each epic in epics by the archive rule at now. An epic is matched to its mark
// by its id, so a record-only epic is matched as well.
func markEpics(epics []*orreryv1.Epic, marks archiveMarks, now time.Time) {
	for _, epic := range epics {
		mark := marks[archiveKey{kind: archiveKindEpic, id: epic.GetId()}]
		epic.Archived = archivedRow(mark, epic.GetChanged(), epicAged(epic, now))
	}
}

// sessionChanged reads a session row's changed column as unix seconds. A column that is not unix seconds reads as 0,
// so a mark the row has stands until the row carries a change time after it.
func sessionChanged(value string) int64 {
	changed, err := parseStamp(value)
	if err != nil {
		return 0
	}
	return changed
}
