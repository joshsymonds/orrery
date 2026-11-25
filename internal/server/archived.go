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

// epicAge is the seven-day floor: how long any list row must go unchanged before it ages out, whatever its state and
// whether or not it is live (see floorAged).
const epicAge = 7 * 24 * time.Hour

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
// is its mark, and aged reports whether the row ages out at now (see epicAged and endedAged). A mark with
// an archived_at archives the row while changed is at or before archived_at, so a row that changes after its archive
// comes back. Otherwise an aged row is archived unless the owner unarchived it at or after its last change, so an
// unarchived row stays out of aging until it changes.
func archivedRow(mark store.ArchiveMark, changed int64, aged bool) bool {
	if !mark.ArchivedAt.IsZero() && changed <= mark.ArchivedAt.Unix() {
		return true
	}
	if !aged {
		return false
	}
	return !unarchivedAtChange(mark, changed)
}

// unarchivedAtChange reports whether the owner unarchived the row mark marks at or after its change time changed, unix
// seconds. Such a row stays out of aging, and out of ended, until it changes again.
func unarchivedAtChange(mark store.ArchiveMark, changed int64) bool {
	return !mark.UnarchivedAt.IsZero() && mark.UnarchivedAt.Unix() >= changed
}

// The phase and outcome spellings the final-epic rule reads, named once here.
const (
	epicPhaseReleased = "released"
	epicPhaseDone     = "done"
)

// epicFinal reports whether an epic from source with outcome has a final outcome. An orreryd epic is final in released,
// done, closed_superseded, closed_shipped, closed_abandoned or met_at_base.
func epicFinal(source, outcome string) bool {
	if source != epicSourceOrreryd {
		return false
	}
	switch outcome {
	case epicPhaseReleased, epicPhaseDone, store.PhaseClosedSuperseded, store.PhaseClosedShipped,
		store.PhaseClosedAbandoned, "met_at_base":
		return true
	}
	return false
}

// floorAged reports whether a row changed at changed, unix seconds, ages out at now by the seven-day floor: it changed
// at least epicAge before now. The floor applies to every list row, whatever its state and whether or not it is live,
// and is added to each row's other rules.
func floorAged(changed int64, now time.Time) bool {
	return changed <= now.Add(-epicAge).Unix()
}

// epicAged reports whether an epic that Epics.List answers ages out at now. An epic ages once it changed at least
// epicAge before now, live or not (see floorAged). An epic that is not live also ages by the final-epic rule: a final
// epic (see epicFinal) ends at its change time, so it ages once it changed at least endedAge before now, whether or
// not an owner has seen it (see endedAged). A live epic ages by the floor alone.
func epicAged(epic *orreryv1.Epic, now time.Time) bool {
	changed := epic.GetChanged()
	if floorAged(changed, now) {
		return true
	}
	if epic.GetLive() || !epicFinal(epic.GetSource(), epic.GetOutcome()) {
		return false
	}
	return endedAged(true, changed, now)
}

// markEpics sets the archived field of each epic in epics by the archive rule at now. An epic is matched to its mark
// by its id.
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
