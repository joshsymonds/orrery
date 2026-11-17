package enginehost

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// heldUsageRow is an open step row with the id, limited by usage until limitReset and held under epic. An empty epic
// is a step with no epic.
func heldUsageRow(id, epic string) store.StepRow {
	return store.StepRow{ID: id, Epic: epic, LoopLimited: true, LoopLimitKind: "usage", LoopLimitResets: limitReset()}
}

// TestQuietStepLimitNamesEachHeldEpicOnceWithNoStepID checks that two steps of two epics held by one usage limit route
// exactly one notice, titled as the usage limit, which names each epic by its contract title and the reset, and names
// neither step. The notice's body is checked exactly, so it holds no step id.
func TestQuietStepLimitNamesEachHeldEpicOnceWithNoStepID(t *testing.T) {
	t.Parallel()
	router, _ := runLimitNotices(t, &fakeTold{},
		limitRead{},
		limitRead{rows: []store.StepRow{
			heldUsageRow("step-one", "epic-a"),
			heldUsageRow("step-two", "epic-b"),
		}},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha, Beta")
}

// TestQuietStepLimitRoutesNothingForAFurtherStepOfANamedEpic checks that a further step of an epic already named in a
// limit's notice, joining the same limit, routes no further notice and changes no mark.
func TestQuietStepLimitRoutesNothingForAFurtherStepOfANamedEpic(t *testing.T) {
	t.Parallel()
	told := &fakeTold{}
	router, _ := runLimitNotices(t, told,
		limitRead{},
		limitRead{rows: []store.StepRow{
			heldUsageRow("step-one", "epic-a"),
			heldUsageRow("step-two", "epic-b"),
		}},
		limitRead{rows: []store.StepRow{
			heldUsageRow("step-one", "epic-a"),
			heldUsageRow("step-two", "epic-b"),
			heldUsageRow("step-three", "epic-a"),
		}},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: a further step of Alpha routes none: %+v",
			len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha, Beta")
	if len(router.seens) != 0 {
		t.Fatalf("got seen reports %+v, want none", router.seens)
	}
	checkTells(t, told, tellCall{limit: resetKey().Limit, names: []string{"Alpha", "Beta"}})
}

// TestQuietStepLimitNamesAHeldStepByItsLabelWhenNoEpicHasIt checks that a held step whose epic label names no stored
// epic is named by that label, and the notice still routes. A stored epic whose title is blank is named by its label too.
func TestQuietStepLimitNamesAHeldStepByItsLabelWhenNoEpicHasIt(t *testing.T) {
	t.Parallel()
	told := &fakeTold{}
	router, _ := runLimitNoticesWith(t, told, &fakeEpics{titles: map[string]string{"epic-blank": ""}},
		limitRead{},
		limitRead{rows: []store.StepRow{
			heldUsageRow("step-one", "epic-orphan"),
			heldUsageRow("step-two", "epic-blank"),
		}},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"Held by the usage limit, reset 2026-11-14T17:00:00Z: epic-blank, epic-orphan")
	checkTells(t, told, tellCall{limit: resetKey().Limit, names: []string{"epic-blank", "epic-orphan"}})
}

// TestQuietStepLimitNamesAStepWithNoEpicOnlyAsOutsideAnyEpic checks that a held step with no epic is named only as a
// step outside any epic: two such steps route one notice that says so, and names neither step.
func TestQuietStepLimitNamesAStepWithNoEpicOnlyAsOutsideAnyEpic(t *testing.T) {
	t.Parallel()
	router, _ := runLimitNotices(t, &fakeTold{},
		limitRead{},
		limitRead{rows: []store.StepRow{
			heldUsageRow("step-free", ""),
			heldUsageRow("step-loose", ""),
		}},
	)

	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"Held by the usage limit, reset 2026-11-14T17:00:00Z: a step outside any epic")
}

// TestQuietStepLimitWaitsForAFailedEpicRead checks that a failed read of a held step's epic is logged and routes
// nothing, and that the next check routes the limit once the read succeeds.
func TestQuietStepLimitWaitsForAFailedEpicRead(t *testing.T) {
	t.Parallel()
	router, logs := runLimitNoticesWith(t, &fakeTold{}, &fakeEpics{titles: limitTitles(), failReads: 1},
		limitRead{},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
		limitRead{rows: []store.StepRow{usageRow("a"), usageRow("b")}},
	)

	if !strings.Contains(logs, "store is closed") {
		t.Fatalf("log = %q, want the failed epic read logged", logs)
	}
	if len(router.notices) != 1 {
		t.Fatalf("got %d notices, want 1 once the epic read succeeds: %+v", len(router.notices), router.notices)
	}
	checkLimitNotice(t, router.notices[0], resetKey(),
		"Held by the usage limit, reset 2026-11-14T17:00:00Z: Alpha, Beta")
}
