package server

import (
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// inboxTidyFinalPhases returns the stored phases that end an epic for good, which the final-epic rule ages out.
func inboxTidyFinalPhases() []string {
	return []string{"released", "done", "closed_superseded", "closed_shipped", "closed_abandoned", "met_at_base"}
}

// inboxTidyFinalStored returns the stored epic id, in phase, last changed at changed and last seen at seen, unix seconds.
func inboxTidyFinalStored(id, phase string, changed, seen int64) *enginev1.StoredEpic {
	epic := storedEpicOf(id, phase, changed)
	epic.Seen = seen
	return epic
}

// TestInboxTidyFinalEpicSeenHourOldUnarchived lists each stored final epic, seen at the time it last changed and changed
// an hour ago, as unarchived: being seen does not age a final epic, so it waits for its day like an unseen one.
func TestInboxTidyFinalEpicSeenHourOldUnarchived(t *testing.T) {
	t.Parallel()
	phases := inboxTidyFinalPhases()
	stored := make([]*enginev1.StoredEpic, 0, len(phases))
	want := make(map[string]bool)
	for _, phase := range phases {
		id := "repoa/seen-" + phase
		stored = append(stored, inboxTidyFinalStored(id, phase, epicsAgo(time.Hour), epicsAgo(time.Hour)))
		want[id] = false
	}
	client := archiveEpicsClient(t, archiveStorePath(t), &epicStoreFake{stored: stored})
	expectArchived(t, "Epics.List", archivedByID(epicsList(t, client)), want)
}

// TestInboxTidyFinalEpicUnseenDayUnchangedArchives lists each stored final epic, never seen and unchanged 24 hours, as
// archived.
func TestInboxTidyFinalEpicUnseenDayUnchangedArchives(t *testing.T) {
	t.Parallel()
	phases := inboxTidyFinalPhases()
	stored := make([]*enginev1.StoredEpic, 0, len(phases))
	want := make(map[string]bool)
	for _, phase := range phases {
		id := "repoa/day-" + phase
		stored = append(stored, inboxTidyFinalStored(id, phase, epicsAgo(24*time.Hour), 0))
		want[id] = true
	}
	client := archiveEpicsClient(t, archiveStorePath(t), &epicStoreFake{stored: stored})
	expectArchived(t, "Epics.List", archivedByID(epicsList(t, client)), want)
}

// TestInboxTidyFinalEpicUnseenShortOfDayUnarchived lists each stored final epic, never seen and unchanged 23 hours, as
// unarchived.
func TestInboxTidyFinalEpicUnseenShortOfDayUnarchived(t *testing.T) {
	t.Parallel()
	phases := inboxTidyFinalPhases()
	stored := make([]*enginev1.StoredEpic, 0, len(phases))
	want := make(map[string]bool)
	for _, phase := range phases {
		id := "repoa/short-" + phase
		stored = append(stored, inboxTidyFinalStored(id, phase, epicsAgo(23*time.Hour), 0))
		want[id] = false
	}
	client := archiveEpicsClient(t, archiveStorePath(t), &epicStoreFake{stored: stored})
	expectArchived(t, "Epics.List", archivedByID(epicsList(t, client)), want)
}

// TestInboxTidyFinalEpicLiveStoredUnarchived lists stored ended_with_gaps and stopped epics, seen at the time they last
// changed and unchanged 24 hours, as unarchived: the rule never ages a live epic.
func TestInboxTidyFinalEpicLiveStoredUnarchived(t *testing.T) {
	t.Parallel()
	stored := []*enginev1.StoredEpic{
		inboxTidyFinalStored("repoa/gaps", "ended_with_gaps", epicsAgo(24*time.Hour), epicsAgo(24*time.Hour)),
		inboxTidyFinalStored("repoa/stopped", "stopped", epicsAgo(24*time.Hour), epicsAgo(24*time.Hour)),
	}
	client := archiveEpicsClient(t, archiveStorePath(t), &epicStoreFake{stored: stored})
	expectArchived(t, "Epics.List", archivedByID(epicsList(t, client)), map[string]bool{
		"repoa/gaps":    false,
		"repoa/stopped": false,
	})
}
