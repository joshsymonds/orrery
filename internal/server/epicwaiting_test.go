package server

import (
	"path/filepath"
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// epicByID returns the epic of epics whose id is id, failing the test when none is.
func epicByID(t *testing.T, epics []*orreryv1.Epic, id string) *orreryv1.Epic {
	t.Helper()
	for _, epic := range epics {
		if epic.GetId() == id {
			return epic
		}
	}
	t.Fatalf("epics %v hold no %s", epicIDs(epics), id)
	return nil
}

// endedStep returns a step of epic, id, that ended done at the start of the hour before epicsNow, read-only or not.
func endedStep(id, epic string, readOnly bool) *enginev1.Step {
	return &enginev1.Step{
		Id: id, Epic: epic, ReadOnly: readOnly, End: "done",
		Started: epicsAgo(2 * time.Hour), Ended: epicsAgo(time.Hour),
	}
}

func TestEpicsListWaitingRule(t *testing.T) {
	t.Parallel()
	const id = "repoa/epic"
	cases := []struct {
		name   string
		head   string
		mtime  time.Time
		effort string
		steps  []*enginev1.Step
		want   bool
	}{
		{
			name:  "only ended read-only steps",
			head:  `{}`,
			mtime: epicsNow(),
			steps: []*enginev1.Step{endedStep("ro-a", id, true)},
			want:  true,
		},
		{
			name:  "only an ended writing step",
			head:  `{}`,
			mtime: epicsNow(),
			steps: []*enginev1.Step{endedStep("write-a", id, false)},
			want:  true,
		},
		{
			name:   "an effort task and no matching step",
			head:   `{}`,
			mtime:  epicsNow(),
			effort: `{"tasks":[{"slug":"one-a","subject":"One A","status":"pending"}]}`,
			want:   false,
		},
		{
			name:  "a running read-only step",
			head:  `{}`,
			mtime: epicsNow(),
			steps: []*enginev1.Step{{Id: "ro-run", Epic: id, ReadOnly: true, Started: epicsAgo(time.Hour)}},
			want:  false,
		},
		{
			name:  "an outcome",
			head:  `{"outcome":"shipped"}`,
			mtime: epicsNow(),
			want:  false,
		},
		{
			name:  "a folded record",
			head:  `{}`,
			mtime: epicsNow().Add(-48 * time.Hour),
			steps: []*enginev1.Step{endedStep("ro-a", id, true)},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeHead(t, root, "repoa", "epic", tc.head, tc.mtime)
			if tc.effort != "" {
				writeEffortState(t, filepath.Join(root, "repoa", "epic"), "1", tc.effort)
			}
			client := epicsServe(t, root, &epicsFake{steps: tc.steps})

			got := epicByID(t, epicsList(t, client), id)
			if got.GetWaiting() != tc.want {
				t.Errorf("waiting = %v, want %v", got.GetWaiting(), tc.want)
			}
		})
	}
}

func TestEpicsListCarriesHeadOutcomeDetail(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "shipped",
		`{"outcome":"shipped","outcome_detail":"synthetic shipped detail"}`, epicsNow())
	writeHead(t, root, "repoa", "waiting", `{"outcome_detail":"synthetic waiting detail"}`, epicsNow())
	client := epicsServe(t, root, &epicsFake{})

	epics := epicsList(t, client)
	if got := epicByID(t, epics, "repoa/shipped").GetOutcomeDetail(); got != "synthetic shipped detail" {
		t.Errorf("shipped outcome_detail = %q, want synthetic shipped detail", got)
	}
	waiting := epicByID(t, epics, "repoa/waiting")
	if waiting.GetOutcomeDetail() != "synthetic waiting detail" || !waiting.GetWaiting() {
		t.Errorf("waiting = %v outcome_detail %q, want waiting with synthetic waiting detail",
			waiting.GetWaiting(), waiting.GetOutcomeDetail())
	}
}

func TestEpicsGetWaitingEpicCarriesOutcomeDetail(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "epic", `{"outcome_detail":"synthetic detail"}`, epicsNow())
	client := epicsServe(t, root, &epicsFake{steps: []*enginev1.Step{endedStep("ro-a", "repoa/epic", true)}})

	got := epicsGet(t, client, "repoa/epic").GetEpic()
	if !got.GetWaiting() || got.GetOutcomeDetail() != "synthetic detail" {
		t.Errorf("Get epic waiting = %v outcome_detail %q, want waiting with synthetic detail",
			got.GetWaiting(), got.GetOutcomeDetail())
	}
}

func TestEpicsStoredEpicIsNeverWaiting(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "shared", `{"outcome_detail":"synthetic detail"}`, epicsNow())
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/shared", "", epicsAgo(time.Hour)),
	}}
	steps := &epicsFake{steps: []*enginev1.Step{endedStep("ro-a", "repoa/shared", true)}}
	client := epicStoreServe(t, root, steps, store)

	got := epicByID(t, epicsList(t, client), "repoa/shared")
	if got.GetSource() != epicSourceOrreryd {
		t.Fatalf("source = %q, want %q", got.GetSource(), epicSourceOrreryd)
	}
	if got.GetWaiting() || got.GetOutcomeDetail() != "" {
		t.Errorf("stored epic waiting = %v outcome_detail %q, want not waiting with empty outcome_detail",
			got.GetWaiting(), got.GetOutcomeDetail())
	}
}
