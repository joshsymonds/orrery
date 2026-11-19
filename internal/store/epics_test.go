package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// epicCreated returns the unix second every test epic is created at.
func epicCreated() time.Time {
	return time.Unix(1700000000, 0)
}

// fullEpic returns a synthetic contract with every section at two or more rows. Its seed decisions carry a seq and
// a time that CreateEpic must ignore: the seeds are numbered from 1 and dated at the epic's creation. Its phase time is
// its creation, since CreateEpic stores the created time as the time the phase was set.
func fullEpic(id string) EpicRow {
	return EpicRow{
		ID:          id,
		Repository:  "/work/synthetic-repo",
		Title:       "Synthetic epic",
		Approved:    "Approved sentence",
		Intent:      "Store the contract",
		Care:        "serious",
		Approach:    "Ordered rows",
		ReleaseThen: "Then line",
		Checks:      "just check",
		Phase:       "accepted",
		Created:     epicCreated(),
		Changed:     time.Unix(1700000060, 0),
		PhaseAt:     epicCreated(),
		Failures: []EpicFailure{
			{ID: "F1", Text: "Rows come back reordered", Rating: "serious", Response: "Order by position"},
			{ID: "F2", Text: "A decision changes later", Rating: "severe", Response: "No update query"},
		},
		Additions: []EpicAddition{
			{Location: "R1", What: "Round-trip test", Why: "Proves order", Cost: "small"},
			{Location: "F2", What: "Trigger guard", Why: "Append only", Cost: "small"},
		},
		Requirements: []EpicRequirement{
			{ID: "R1", Text: "Stored as ordered rows", Evidence: "round-trip test", Kind: "check"},
			{ID: "R3", Text: "Append once per client id", Evidence: "store tests", Kind: "review"},
		},
		Premises: []EpicPremise{
			{ID: "P1", Text: "Position keeps insertion order", IfWrong: "Sort by rowid"},
			{ID: "P2", Text: "Seeds arrive in order", IfWrong: "Refuse unordered seeds"},
		},
		Exclusions: []string{"No event ledger", "No engine behavior"},
		Alternatives: []EpicAlternative{
			{Alternative: "One JSON column", WhyNot: "Not queryable by section", Reconsider: "Never"},
			{Alternative: "Event log", WhyNot: "Too much machinery", Reconsider: "If history is needed"},
		},
		ReleaseSteps: []EpicReleaseStep{
			{Step: "Merge", Target: "main", Undo: "Revert the merge"},
			{Step: "Tag", Target: "v1", Undo: "Delete the tag"},
		},
		Decisions: []EpicDecision{
			{Seq: 9, ID: "D1", Text: "Use the store", Time: time.Unix(5, 0)},
			{Seq: 9, ID: "D2", Text: "Rows, not JSON", Time: time.Unix(5, 0)},
		},
		Reviewers: []reviewers.Reviewer{
			{
				Name: "floor", Enabled: true, Stage: []string{"task", "epic"},
				Must: []string{"Names the failing check"}, MustNot: []string{"Edits a file"},
				Prompt: "Review the diff against the floor", Model: "synthetic-model",
				Tools: []string{"Read", "Grep"}, Effort: "high", Limit: 900, Verify: true,
			},
			{
				Name: "security", Enabled: false, Stage: []string{"epic"},
				Must: []string{"Lists each trust boundary"}, Agent: "security-agent", Limit: 1800, Verify: false,
			},
		},
	}
}

// seededDecisions is what an epic created from fullEpic reads back as its decisions.
func seededDecisions() []EpicDecision {
	return []EpicDecision{
		{Seq: 1, ID: "D1", Text: "Use the store", Time: epicCreated()},
		{Seq: 2, ID: "D2", Text: "Rows, not JSON", Time: epicCreated()},
	}
}

// headOnly returns row with every section and its decisions cleared, the shape Epics returns.
func headOnly(row EpicRow) EpicRow {
	row.Failures, row.Additions, row.Requirements, row.Premises = nil, nil, nil, nil
	row.Exclusions, row.Alternatives, row.ReleaseSteps, row.Decisions = nil, nil, nil, nil
	row.Reviewers = nil
	return row
}

func openEpicStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(context.Background(), path, noSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func createEpic(t *testing.T, s *Store, row EpicRow) {
	t.Helper()
	if err := s.CreateEpic(context.Background(), row); err != nil {
		t.Fatalf("CreateEpic %s: %v", row.ID, err)
	}
}

func TestCreatedEpicReadsBackEveryFieldInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := fullEpic(id)
	want.Decisions = seededDecisions()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Epic round trip differs:\n got %+v\nwant %+v", got, want)
	}
}

func TestStoredReviewerKeepsNoToolsApartFromUnsetTools(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	row := fullEpic(id)
	row.Reviewers = []reviewers.Reviewer{
		{
			Name:    "no_tools",
			Enabled: true,
			Stage:   []string{"task"},
			Must:    []string{"Reads the diff"},
			Prompt:  "Review the diff",
			Model:   "synthetic-model",
			Tools:   []string{},
			Effort:  "low",
			Limit:   60,
			Verify:  true,
		},
		{
			Name: "all_tools", Enabled: true, Stage: []string{"task"}, Must: []string{"Reads the diff"},
			Prompt: "Review the diff", Model: "synthetic-model", Limit: 60, Verify: true,
		},
	}
	createEpic(t, s, row)

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !reflect.DeepEqual(got.Reviewers, row.Reviewers) {
		t.Errorf("Reviewers round trip differs:\n got %+v\nwant %+v", got.Reviewers, row.Reviewers)
	}
}

func TestEpicWithoutReviewersReadsBackNil(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	row := fullEpic(id)
	row.Reviewers = nil
	createEpic(t, s, row)

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if got.Reviewers != nil {
		t.Errorf("Reviewers = %+v, want nil", got.Reviewers)
	}
}

func TestCreateEpicOfStoredIDIsErrEpicExistsAndChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	again := fullEpic(id)
	again.Title = "Rewritten title"
	again.Failures = again.Failures[:1]
	again.Decisions = append(again.Decisions, EpicDecision{ID: "D3", Text: "Late decision"})
	if err := s.CreateEpic(ctx, again); !errors.Is(err, ErrEpicExists) {
		t.Fatalf("second CreateEpic err = %v, want ErrEpicExists", err)
	}

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := fullEpic(id)
	want.Decisions = seededDecisions()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("refused CreateEpic changed the stored epic:\n got %+v\nwant %+v", got, want)
	}
	epics, err := s.Epics(ctx)
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	if len(epics) != 1 {
		t.Errorf("Epics lists %d epics, want 1", len(epics))
	}
}

func TestEpicsListsEveryEpicByIDWithHeadFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const storeID = "orrery-1a2b3c4/epic-store"
	const alphaID = "orrery-1a2b3c4/epic-alpha"
	createEpic(t, s, fullEpic(storeID))
	alpha := fullEpic(alphaID)
	alpha.Title = "Alpha"
	alpha.Phase = "released"
	alpha.Changed = time.Unix(1700000900, 0)
	createEpic(t, s, alpha)

	got, err := s.Epics(ctx)
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	want := []EpicRow{headOnly(alpha), headOnly(fullEpic(storeID))}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Epics = %+v, want %+v", got, want)
	}
}

// TestEpicsReadsInThreeQueries checks that Epics reads the heads, workflow nodes and seen marks of every epic in three
// queries however many epics are stored, and gives each epic its own nodes and mark. The interpreter lists the epics
// on every pass, and the store has one connection, so a query per epic would queue every other store call behind it.
func TestEpicsReadsInThreeQueries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const epics = 8
	for n := range epics {
		row := fullEpic(fmt.Sprintf("orrery-1a2b3c4/epic-count-%d", n))
		row.Workflow = EpicWorkflow{
			Name: "count", Text: "count", SHA256: "sum",
			Nodes: []EpicWorkflowNode{{ID: fmt.Sprintf("node-%d", n), Kind: "plan"}},
		}
		createEpic(t, s, row)
		if n%2 == 0 {
			if err := s.SeeEpic(ctx, row.ID, time.Unix(int64(1700000000+n), 0)); err != nil {
				t.Fatalf("SeeEpic: %v", err)
			}
		}
	}
	recorder := &planRecorder{db: s.db.DB}
	got, err := readEpics(ctx, sqlcgen.New(recorder))
	if err != nil {
		t.Fatalf("readEpics: %v", err)
	}
	if len(recorder.texts) != 3 {
		t.Errorf("readEpics of %d epics issued %d queries, want 3", epics, len(recorder.texts))
	}
	listed, err := s.Epics(ctx)
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	if !reflect.DeepEqual(listed, got) {
		t.Errorf("Epics = %+v, want the epics its read returns %+v", listed, got)
	}
	if len(got) != epics {
		t.Fatalf("Epics lists %d epics, want %d", len(got), epics)
	}
	for n, row := range got {
		wantNodes := []EpicWorkflowNode{{ID: fmt.Sprintf("node-%d", n), Kind: "plan"}}
		if !reflect.DeepEqual(row.Workflow.Nodes, wantNodes) {
			t.Errorf("epic %s nodes = %+v, want %+v", row.ID, row.Workflow.Nodes, wantNodes)
		}
		var wantSeen time.Time
		if n%2 == 0 {
			wantSeen = time.Unix(int64(1700000000+n), 0)
		}
		if !row.Seen.Equal(wantSeen) {
			t.Errorf("epic %s seen = %v, want %v", row.ID, row.Seen, wantSeen)
		}
	}
}

func TestEpicOfUnknownIDIsErrNoEpic(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	if _, err := s.Epic(context.Background(), "orrery-1a2b3c4/missing"); !errors.Is(err, ErrNoEpic) {
		t.Fatalf("Epic of unknown id err = %v, want ErrNoEpic", err)
	}
}

func TestAnswerEpicAppendsSeqThreeAndMovesChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	at := time.Unix(1700000500, 0)
	got, err := s.AnswerEpic(ctx, id, "D3", "Answer", at)
	if err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	answer := EpicDecision{Seq: 3, ID: "D3", Text: "Answer", Time: at}
	if !reflect.DeepEqual(got, answer) {
		t.Errorf("AnswerEpic = %+v, want %+v", got, answer)
	}

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if want := append(seededDecisions(), answer); !reflect.DeepEqual(epic.Decisions, want) {
		t.Errorf("decisions = %+v, want %+v", epic.Decisions, want)
	}
	if !epic.Changed.Equal(at) {
		t.Errorf("Changed = %v, want %v", epic.Changed, at)
	}
}

func TestAnswerEpicWithStoredIDAndTextReturnsStoredRowAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	first := time.Unix(1700000500, 0)
	stored, err := s.AnswerEpic(ctx, id, "D3", "Answer", first)
	if err != nil {
		t.Fatalf("first AnswerEpic: %v", err)
	}
	got, err := s.AnswerEpic(ctx, id, "D3", "Answer", first.Add(time.Hour))
	if err != nil {
		t.Fatalf("repeated AnswerEpic: %v", err)
	}
	if !reflect.DeepEqual(got, stored) {
		t.Errorf("repeated AnswerEpic = %+v, want stored %+v", got, stored)
	}

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(epic.Decisions) != 3 {
		t.Errorf("epic has %d decisions, want 3", len(epic.Decisions))
	}
	if !epic.Changed.Equal(first) {
		t.Errorf("Changed = %v, want %v: a repeated answer moved it", epic.Changed, first)
	}
}

func TestAnswerEpicWithStoredIDAndOtherTextIsErrDecisionConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	first := time.Unix(1700000500, 0)
	if _, err := s.AnswerEpic(ctx, id, "D3", "Answer", first); err != nil {
		t.Fatalf("first AnswerEpic: %v", err)
	}
	_, err := s.AnswerEpic(ctx, id, "D3", "Other answer", first.Add(time.Hour))
	if !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("AnswerEpic with other text err = %v, want ErrDecisionConflict", err)
	}

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := append(seededDecisions(), EpicDecision{Seq: 3, ID: "D3", Text: "Answer", Time: first})
	if !reflect.DeepEqual(epic.Decisions, want) {
		t.Errorf("decisions = %+v, want %+v", epic.Decisions, want)
	}
	if !epic.Changed.Equal(first) {
		t.Errorf("Changed = %v, want %v: a refused answer moved it", epic.Changed, first)
	}
}

func TestAnswerEpicOfUnknownEpicIsErrNoEpic(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	_, err := s.AnswerEpic(context.Background(), "orrery-1a2b3c4/missing", "D1", "text", epicCreated())
	if !errors.Is(err, ErrNoEpic) {
		t.Fatalf("AnswerEpic of unknown epic err = %v, want ErrNoEpic", err)
	}
}

// requirePhaseAt fails unless integrateEpic is in phase and its phase time is the second at.
func requirePhaseAt(t *testing.T, s *Store, phase string, at int64) {
	t.Helper()
	row := integrateEpicRow(t, s)
	if row.Phase != phase {
		t.Fatalf("phase of epic %s = %q, want %q", integrateEpic, row.Phase, phase)
	}
	if want := time.Unix(at, 0); !row.PhaseAt.Equal(want) {
		t.Errorf("phase time of epic %s in %s = %v, want %v", integrateEpic, phase, row.PhaseAt, want)
	}
}

// TestPhaseAtFollowsEachPhaseWrite checks that the phase time of an epic is the second its phase was last set: its
// created time at creation, the time of each write that changes the phase, and the stored time through a write that
// leaves the phase as it is. A release stop and a release resume each set it.
func TestPhaseAtFollowsEachPhaseWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	requirePhaseAt(t, s, "accepted", 1700000000)

	startIntegrateRuns(t, s, NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review", Attempt: 1})
	if err := s.FreezeReview(ctx, integrateEpic, "review", 1, "rev-7", time.Unix(1700002400, 0)); err != nil {
		t.Fatalf("FreezeReview: %v", err)
	}
	requirePhaseAt(t, s, "reviewing", 1700002400)

	// A second run's freeze sets the phase reviewing again, which leaves the phase time where it was.
	startIntegrateRuns(t, s, NodeRun{
		Path: "review/again", ID: "id-review-again", Node: "review", Kind: "review", Attempt: 1,
	})
	if err := s.FreezeReview(ctx, integrateEpic, "review/again", 1, "rev-7", time.Unix(1700002500, 0)); err != nil {
		t.Fatalf("FreezeReview of a second run: %v", err)
	}
	requirePhaseAt(t, s, "reviewing", 1700002400)

	if err := s.EndEpic(ctx, integrateEpic, "clean", "", nil, time.Unix(1700002600, 0)); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}
	requirePhaseAt(t, s, "clean", 1700002600)

	holder, err := s.TakeReleaseSlot(ctx, integrateEpic, time.Unix(1700002700, 0))
	if err != nil {
		t.Fatalf("TakeReleaseSlot: %v", err)
	}
	if holder != integrateEpic {
		t.Errorf("slot holder = %q, want %q", holder, integrateEpic)
	}
	requirePhaseAt(t, s, "releasing", 1700002700)

	startIntegrateRuns(t, s, NodeRun{Path: "release", ID: "id-release", Node: "release", Kind: "release", Attempt: 1})
	if err := s.StopRelease(
		ctx,
		integrateEpic,
		"release",
		1,
		"publish rejected",
		time.Unix(1700002800, 0),
	); err != nil {
		t.Fatalf("StopRelease: %v", err)
	}
	requirePhaseAt(t, s, "stopped", 1700002800)

	if err := s.ReleaseEpic(ctx, integrateEpic, nil, time.Unix(1700002900, 0)); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	requirePhaseAt(t, s, "releasing", 1700002900)
}
