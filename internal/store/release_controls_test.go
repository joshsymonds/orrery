package store_test

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// resumeEpic is the epic a stopped release resumes, and resumeOther a second epic of its repository, resumeRepo.
const (
	resumeEpic       = "orrery-7e8f9a0/epic-resume"
	resumeOther      = "orrery-7e8f9a0/epic-resume-other"
	resumeRepo       = "/work/synthetic-resume-repo"
	resumeStopReason = "publish rejected"
)

// resumeAt is the second the resume tests resume and name at.
func resumeAt() time.Time {
	return time.Unix(1700003000, 0)
}

// resumeSteps returns the release steps of the resume tests: a step that has an undo, then one that has none.
func resumeSteps() []store.EpicReleaseStep {
	return []store.EpicReleaseStep{
		{Step: "push tag", Target: "origin", Undo: "delete tag"},
		{Step: "publish", Target: "registry", Undo: "none"},
	}
}

// resumeEpicIn returns a synthetic epic with id in phase, in resumeRepo, with resumeSteps as its release steps.
func resumeEpicIn(id, phase string) store.EpicRow {
	row := releaseEpicIn(id, resumeRepo, phase)
	row.ReleaseSteps = resumeSteps()
	return row
}

// releasedStep returns a release run of the step at position, at attempt 1, that passed or failed.
func releasedStep(position int64, passed bool) store.ReleaseRun {
	run := store.ReleaseRun{
		Part: store.ReleasePartStep, Position: position, Attempt: 1, Step: resumeSteps()[position-1].Step,
		Evidence: fmt.Sprintf("run-step-%d", position), Passed: passed, At: releaseRunAt(),
	}
	if !passed {
		run.Reason = "rejected"
	}
	return run
}

// judgedRelease returns a judge run at attempt 1 that failed, as a release whose steps all passed leaves it.
func judgedRelease() store.ReleaseRun {
	return store.ReleaseRun{
		Part: store.ReleasePartJudge, Position: 1, Attempt: 1, Evidence: "run-judge",
		Reason: "the release is not whole",
		At:     releaseRunAt(),
	}
}

// namedPublish is the named release run that naming the publish step at attempt 2 stores.
func namedPublish() store.ReleaseRun {
	return store.ReleaseRun{
		Part:     store.ReleasePartNamed,
		Position: 2,
		Attempt:  2,
		Step:     "publish",
		Reason:   "owner named it",
		At:       resumeAt(),
	}
}

// namedPublishDecision is the owner decision that naming the publish step at attempt 2 records.
func namedPublishDecision() store.EpicDecision {
	return store.EpicDecision{
		Seq: 1, ID: "release-undo-none-2-2", Text: "owner named undo-none release step 2, publish, to run it again",
		Time: resumeAt(),
	}
}

// stoppedRelease returns a store with resumeEpic stopped by its release. resumeEpic is clean, starts its release,
// starts its release node run at attempt 1, records runs under it, and is stopped at that run with resumeStopReason.
func stoppedRelease(t *testing.T, runs ...store.ReleaseRun) (*store.Store, *sql.DB) {
	t.Helper()
	return stoppedReleaseOf(t, resumeEpicIn(resumeEpic, store.PhaseClean), runs...)
}

// stoppedReleaseOf is stoppedRelease for the clean epic row, which must be resumeEpic.
func stoppedReleaseOf(t *testing.T, row store.EpicRow, runs ...store.ReleaseRun) (*store.Store, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	s, raw := openReleaseStore(t, row)
	if err := s.StartRelease(ctx, resumeEpic, resumeAt()); err != nil {
		t.Fatalf("StartRelease of %s: %v", resumeEpic, err)
	}
	run := store.NodeRun{Path: "release", ID: "id-resume", Node: "release", Kind: "release", Attempt: 1}
	if err := s.StartNodes(ctx, resumeEpic, []store.NodeRun{run}, nil, releaseRunAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	for _, release := range runs {
		if err := s.RecordRelease(ctx, resumeEpic, release); err != nil {
			t.Fatalf("RecordRelease %s %d: %v", release.Part, release.Position, err)
		}
	}
	if err := s.StopRelease(ctx, resumeEpic, "release", 1, resumeStopReason, releaseRunAt()); err != nil {
		t.Fatalf("StopRelease of %s: %v", resumeEpic, err)
	}
	return s, raw
}

// stoppedAtPublish is stoppedRelease with the first step passed and the publish step failed, the stop that most refusal
// and resume cases start from.
func stoppedAtPublish(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	return stoppedRelease(t, releasedStep(1, true), releasedStep(2, false))
}

// stoppedAtPublishNone is stoppedAtPublish with the publish step's undo written " None ", which is undo-none as the
// release kind reads it.
func stoppedAtPublishNone(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	row := resumeEpicIn(resumeEpic, store.PhaseClean)
	row.ReleaseSteps[1].Undo = " None "
	return stoppedReleaseOf(t, row, releasedStep(1, true), releasedStep(2, false))
}

// releaseRowsOf returns the rows of each table a release control writes, as text, so a test can check that a refused
// control wrote nothing.
func releaseRowsOf(t *testing.T, raw *sql.DB) string {
	t.Helper()
	var out strings.Builder
	tables := []string{
		"epics", "epic_turns", "epic_triggers", "epic_node_runs", "epic_release_runs", "epic_release_slots",
		"epic_decisions", "epic_tells",
	}
	for _, table := range tables {
		for _, row := range rawRows(t, raw, `SELECT * FROM `+table+` ORDER BY rowid`) {
			fmt.Fprintf(&out, "%s %v\n", table, row)
		}
	}
	return out.String()
}

// inReadOrder returns runs in the order the store reads release runs: by attempt, then part, then position.
func inReadOrder(runs []store.ReleaseRun) []store.ReleaseRun {
	out := slices.Clone(runs)
	slices.SortStableFunc(out, func(a, b store.ReleaseRun) int {
		if c := cmp.Compare(a.Attempt, b.Attempt); c != 0 {
			return c
		}
		if c := strings.Compare(a.Part, b.Part); c != 0 {
			return c
		}
		return cmp.Compare(a.Position, b.Position)
	})
	return out
}

// requireDecisions fails unless the decisions of epic are want.
func requireDecisions(t *testing.T, s *store.Store, epic string, want []store.EpicDecision) {
	t.Helper()
	row, err := s.Epic(context.Background(), epic)
	if err != nil {
		t.Fatalf("Epic %s: %v", epic, err)
	}
	if !reflect.DeepEqual(row.Decisions, want) {
		t.Errorf("decisions of epic %s = %+v, want %+v", epic, row.Decisions, want)
	}
}

// requireRefusedResume fails unless ReleaseEpic of epic naming undo is refused with want and writes nothing.
func requireRefusedResume(t *testing.T, s *store.Store, raw *sql.DB, epic string, undo []int64, want string) {
	t.Helper()
	before := releaseRowsOf(t, raw)
	requireRefusalError(t, s.ReleaseEpic(context.Background(), epic, undo, store.Maker{}, resumeAt()), want)
	if got := releaseRowsOf(t, raw); got != before {
		t.Errorf("rows after refused ReleaseEpic of %s = %s, want %s", epic, got, before)
	}
}

// TestReleaseEpicRefusesWhatItMustNotResume checks that ReleaseEpic refuses each state it must not resume from, naming
// the reason, and writes nothing.
func TestReleaseEpicRefusesWhatItMustNotResume(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T) (*store.Store, *sql.DB)
		epic  string
		undo  []int64
		want  string
	}{
		{
			name: "the epic is not stopped",
			setup: func(t *testing.T) (*store.Store, *sql.DB) {
				t.Helper()
				return openReleaseStore(t, resumeEpicIn(resumeEpic, store.PhaseClean))
			},
			epic: resumeEpic,
			want: "epic " + resumeEpic + " is not stopped",
		},
		{
			name: "no release node run is parked",
			setup: func(t *testing.T) (*store.Store, *sql.DB) {
				t.Helper()
				return openReleaseStore(t, resumeEpicIn(resumeEpic, store.PhaseStopped))
			},
			epic: resumeEpic,
			want: "epic " + resumeEpic + " has no parked release run",
		},
		{
			name: "an ended_with_gaps epic names an undo-none step",
			setup: func(t *testing.T) (*store.Store, *sql.DB) {
				t.Helper()
				return openReleaseStore(t, resumeEpicIn(resumeEpic, "ended_with_gaps"))
			},
			epic: resumeEpic,
			undo: []int64{2},
			want: "epic " + resumeEpic + " has not started a release; name no undo-none step",
		},
		{
			name:  "a named step has an undo",
			setup: stoppedAtPublish,
			epic:  resumeEpic,
			undo:  []int64{1},
			want:  "position 1 of epic " + resumeEpic + " is not an undo-none release step",
		},
		{
			name:  "a named position is past the steps",
			setup: stoppedAtPublish,
			epic:  resumeEpic,
			undo:  []int64{3},
			want:  "position 3 of epic " + resumeEpic + " is not an undo-none release step",
		},
		{
			name:  "a failed undo-none step is not named",
			setup: stoppedAtPublish,
			epic:  resumeEpic,
			want:  "release step 2 of epic " + resumeEpic + " is undo-none and failed; name it to run it again",
		},
		{
			name:  "a failed step whose undo is written None is not named",
			setup: stoppedAtPublishNone,
			epic:  resumeEpic,
			want:  "release step 2 of epic " + resumeEpic + " is undo-none and failed; name it to run it again",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, raw := c.setup(t)
			requireRefusedResume(t, s, raw, c.epic, c.undo, c.want)
		})
	}
}

// TestReleaseEpicRefusesAnUnknownEpic checks that ReleaseEpic of an id no epic has is ErrNoEpic and writes nothing.
func TestReleaseEpicRefusesAnUnknownEpic(t *testing.T) {
	t.Parallel()
	s, raw := stoppedAtPublish(t)
	before := releaseRowsOf(t, raw)
	err := s.ReleaseEpic(context.Background(), "orrery-7e8f9a0/no-such-epic", nil, store.Maker{}, resumeAt())
	if !errors.Is(err, store.ErrNoEpic) {
		t.Errorf("ReleaseEpic of an unknown epic: err = %v, want ErrNoEpic", err)
	}
	if got := releaseRowsOf(t, raw); got != before {
		t.Errorf("rows after ReleaseEpic of an unknown epic = %s, want %s", got, before)
	}
}

// TestReleaseEpicResumesAtTheFirstUnfinishedStep checks that a stopped release resumes at its first unfinished step:
// its release node run moves to its next attempt, the epic is unparked, one tell says where the release resumed, and
// an undo-none step runs again only when the call names it, which stores a named run and an owner decision for it.
func TestReleaseEpicResumesAtTheFirstUnfinishedStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		runs          []store.ReleaseRun
		undo          []int64
		stop          []string
		tell          string
		wantNamed     []store.ReleaseRun
		wantDecisions []store.EpicDecision
	}{
		{
			name: "a failed step that has an undo runs without a name",
			runs: []store.ReleaseRun{releasedStep(1, false)},
			tell: "release resumed at step 1, push tag",
		},
		{
			name: "an undo-none step that never ran runs without a name",
			runs: []store.ReleaseRun{releasedStep(1, true)},
			tell: "release resumed at step 2, publish",
		},
		{
			name: "every step passed resumes at the judge",
			runs: []store.ReleaseRun{releasedStep(1, true), releasedStep(2, true), judgedRelease()},
			tell: "release resumed at the judge",
		},
		{
			name:          "a named undo-none step that failed runs again",
			runs:          []store.ReleaseRun{releasedStep(1, true), releasedStep(2, false)},
			undo:          []int64{2},
			stop:          []string{resumeStopReason},
			tell:          "release resumed at step 2, publish",
			wantNamed:     []store.ReleaseRun{namedPublish()},
			wantDecisions: []store.EpicDecision{namedPublishDecision()},
		},
		{
			name:          "a name repeated in the call is one named run and one decision",
			runs:          []store.ReleaseRun{releasedStep(1, true), releasedStep(2, false)},
			undo:          []int64{2, 2},
			stop:          []string{resumeStopReason},
			tell:          "release resumed at step 2, publish",
			wantNamed:     []store.ReleaseRun{namedPublish()},
			wantDecisions: []store.EpicDecision{namedPublishDecision()},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s, raw := stoppedRelease(t, c.runs...)
			if err := s.ReleaseEpic(ctx, resumeEpic, c.undo, store.Maker{}, resumeAt()); err != nil {
				t.Fatalf("ReleaseEpic: %v", err)
			}
			requireNodeRuns(t, s, resumeEpic, []store.NodeRun{{
				Path: "release", ID: "id-resume", Node: "release", Kind: "release", State: store.NodeRunning,
				Attempt: 2, Started: releaseRunAt(),
			}})
			if got := parkedOf(t, raw, resumeEpic); got != "" {
				t.Errorf("parked reason of epic %s after ReleaseEpic = %q, want none", resumeEpic, got)
			}
			if want := append(slices.Clone(c.stop), c.tell); !reflect.DeepEqual(tellsOf(t, raw, resumeEpic), want) {
				t.Errorf("tells of epic %s = %q, want %q", resumeEpic, tellsOf(t, raw, resumeEpic), want)
			}
			requireReleaseRuns(t, s, resumeEpic, inReadOrder(slices.Concat(c.runs, c.wantNamed)))
			requireDecisions(t, s, resumeEpic, c.wantDecisions)
			requirePhase(t, s, resumeEpic, store.PhaseReleasing)
		})
	}
}

// TestReleaseEpicReleasesAnEndedWithGapsEpic checks that ReleaseEpic of an epic that ended with gaps records the
// owner's decision and moves the epic to clean in one write, with no tell, that the clean epic then starts its release,
// and that a second call is refused as not stopped and writes nothing.
func TestReleaseEpicReleasesAnEndedWithGapsEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openReleaseStore(t, resumeEpicIn(resumeEpic, "ended_with_gaps"))
	tells := tellsOf(t, raw, resumeEpic)
	if err := s.ReleaseEpic(ctx, resumeEpic, nil, store.Maker{By: store.MakerOwner}, resumeAt()); err != nil {
		t.Fatalf("ReleaseEpic of an ended_with_gaps epic: %v", err)
	}
	requirePhase(t, s, resumeEpic, store.PhaseClean)
	requireDecisions(t, s, resumeEpic, []store.EpicDecision{{
		Seq: 1, ID: "release-ended-with-gaps", Text: "owner released epic " + resumeEpic + ", which ended with gaps",
		Time: resumeAt(), Maker: store.Maker{By: store.MakerOwner},
	}})
	if got := tellsOf(t, raw, resumeEpic); !reflect.DeepEqual(got, tells) {
		t.Errorf("tells of epic %s after ReleaseEpic = %q, want %q", resumeEpic, got, tells)
	}
	requireRefusedResume(t, s, raw, resumeEpic, nil, "epic "+resumeEpic+" is not stopped")
	if err := s.StartRelease(ctx, resumeEpic, resumeAt()); err != nil {
		t.Fatalf("StartRelease of %s: %v", resumeEpic, err)
	}
	requirePhase(t, s, resumeEpic, store.PhaseReleasing)
}

// TestReleaseEpicResumesWhileAnotherEpicOfItsRepositoryReleases checks that a stopped release resumes at once although
// another epic of its repository is releasing.
func TestReleaseEpicResumesWhileAnotherEpicOfItsRepositoryReleases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := stoppedRelease(t, releasedStep(1, true))
	if err := s.CreateEpic(ctx, resumeEpicIn(resumeOther, store.PhaseClean)); err != nil {
		t.Fatalf("CreateEpic %s: %v", resumeOther, err)
	}
	if err := s.StartRelease(ctx, resumeOther, resumeAt()); err != nil {
		t.Fatalf("StartRelease of %s: %v", resumeOther, err)
	}
	if err := s.ReleaseEpic(ctx, resumeEpic, nil, store.Maker{}, resumeAt()); err != nil {
		t.Fatalf("ReleaseEpic of %s: %v", resumeEpic, err)
	}
	requirePhase(t, s, resumeEpic, store.PhaseReleasing)
	requirePhase(t, s, resumeOther, store.PhaseReleasing)
}

// TestRetryNodeRefusesTheReleaseNode checks that RetryNode of a stopped release's node is refused, pointing to orrery
// epic release, and writes nothing, so the release stays one ReleaseEpic can resume.
func TestRetryNodeRefusesTheReleaseNode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := stoppedRelease(t, releasedStep(1, true))
	before := releaseRowsOf(t, raw)
	want := "node release of epic " + resumeEpic + " is the release; resume it with orrery epic release " + resumeEpic
	requireRefusalError(
		t,
		s.RetryNode(ctx, resumeEpic, "release", store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0)),
		want,
	)
	if got := releaseRowsOf(t, raw); got != before {
		t.Errorf("rows after refused RetryNode of %s = %s, want %s", resumeEpic, got, before)
	}
	if err := s.ReleaseEpic(ctx, resumeEpic, nil, store.Maker{}, resumeAt()); err != nil {
		t.Errorf("ReleaseEpic after refused RetryNode: %v", err)
	}
}

// TestReleaseEpicNamesAnUndoNoneStepWrittenNone checks that a failed step whose undo is written " None " can be named,
// as the release kind reads that undo as none: the call stores the named run for the next attempt.
func TestReleaseEpicNamesAnUndoNoneStepWrittenNone(t *testing.T) {
	t.Parallel()
	s, _ := stoppedAtPublishNone(t)
	if err := s.ReleaseEpic(context.Background(), resumeEpic, []int64{2}, store.Maker{}, resumeAt()); err != nil {
		t.Fatalf("ReleaseEpic naming step 2: %v", err)
	}
	requireReleaseRuns(t, s, resumeEpic, inReadOrder([]store.ReleaseRun{
		releasedStep(1, true), releasedStep(2, false), namedPublish(),
	}))
}

// TestStopEpicReleaseStopsTheReleaseAndReturnsTheOpenStep checks that StopEpic of a releasing epic with no running turn
// records the owner's stop of its release at the attempt of its running release run, returns the release step of that
// attempt that has no end, or "" when none does, and neither pauses the epic nor changes its phase.
func TestStopEpicReleaseStopsTheReleaseAndReturnsTheOpenStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		ended []string
		want  string
	}{
		{
			name:  "the first step with no end is returned",
			ended: []string{"release 1"},
			want:  store.NodeWorkID("id-release", 1, "release 2"),
		},
		{
			name:  "no step with no end is returned",
			ended: []string{"release 1", "release 2", "judge"},
			want:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s, raw := openReleaseStore(t, resumeEpicIn(resumeEpic, store.PhaseClean))
			if err := s.StartRelease(ctx, resumeEpic, resumeAt()); err != nil {
				t.Fatalf("StartRelease of %s: %v", resumeEpic, err)
			}
			addReleaseSteps(t, s, resumeEpic)
			for _, part := range c.ended {
				end := store.StepEnd{
					ID:     store.NodeWorkID("id-release", 1, part),
					End:    "done",
					Status: "passed",
					Reason: "green",
				}
				if err := s.ApplyEngine(ctx, []store.EngineOp{end}, checkTime(0)); err != nil {
					t.Fatalf("ApplyEngine ending %s: %v", part, err)
				}
			}

			step, err := s.StopEpic(ctx, resumeEpic, store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0))
			if err != nil {
				t.Fatalf("StopEpic: %v", err)
			}
			if step != c.want {
				t.Errorf("StopEpic step = %q, want %q", step, c.want)
			}
			requirePhase(t, s, resumeEpic, store.PhaseReleasing)
			requireReleaseRuns(t, s, resumeEpic, []store.ReleaseRun{{
				Part: store.ReleasePartStop, Attempt: 1, Reason: "owner stopped the release",
				At: time.Unix(1700000000, 0),
			}})
			var paused int64
			if err = raw.QueryRow(`SELECT paused FROM epics WHERE id = ?`, resumeEpic).Scan(&paused); err != nil {
				t.Fatalf("read paused of epic %s: %v", resumeEpic, err)
			}
			if paused != 0 {
				t.Errorf("paused after StopEpic = %d, want 0", paused)
			}
			if got := parkedOf(t, raw, resumeEpic); got != "" {
				t.Errorf("parked after StopEpic = %q, want none", got)
			}
		})
	}
}

// TestStopEpicStoppedReleaseTakesTheOwnerStopClass checks that StopEpic of an epic in phase stopped, whose release node
// run is parked, records the owner's stop at the parked run's attempt, returns no step, and moves the park class to
// release_owner_stop while the park keeps its reason.
func TestStopEpicStoppedReleaseTakesTheOwnerStopClass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := stoppedAtPublish(t)
	at := time.Unix(1700000000, 0)
	step, err := s.StopEpic(ctx, resumeEpic, store.Maker{By: store.MakerOwner}, at)
	if err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if step != "" {
		t.Errorf("StopEpic step = %q, want none: the stopped release has no open step", step)
	}
	requireReleaseRuns(t, s, resumeEpic, inReadOrder([]store.ReleaseRun{
		releasedStep(1, true), releasedStep(2, false),
		{Part: store.ReleasePartStop, Attempt: 1, Reason: "owner stopped the release", At: at},
	}))
	row, err := s.Epic(ctx, resumeEpic)
	if err != nil {
		t.Fatalf("Epic %s: %v", resumeEpic, err)
	}
	if row.Phase != store.PhaseStopped || row.ParkClass != store.ParkClassReleaseOwnerStop ||
		row.Parked != resumeStopReason {
		t.Errorf("epic phase = %q, park class = %q and parked = %q, want stopped, %q and %q",
			row.Phase, row.ParkClass, row.Parked, store.ParkClassReleaseOwnerStop, resumeStopReason)
	}
}

// TestStopEpicReleaseRefusesAnEpicWithNoRunningTurn checks that StopEpic of an epic with no running turn is refused as
// before and writes nothing, while another epic of its repository is releasing.
func TestStopEpicReleaseRefusesAnEpicWithNoRunningTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openReleaseStore(t,
		resumeEpicIn(resumeEpic, store.PhaseClean),
		resumeEpicIn(resumeOther, store.PhaseClean),
	)
	if err := s.StartRelease(ctx, resumeEpic, resumeAt()); err != nil {
		t.Fatalf("StartRelease of %s: %v", resumeEpic, err)
	}
	before := releaseRowsOf(t, raw)

	_, err := s.StopEpic(ctx, resumeOther, store.Maker{By: store.MakerOwner}, time.Unix(1700000000, 0))
	requireRefusalError(t, err, "epic "+resumeOther+" has no running turn")
	if got := releaseRowsOf(t, raw); got != before {
		t.Errorf("rows after refused StopEpic = %s, want %s", got, before)
	}
	requirePhase(t, s, resumeEpic, store.PhaseReleasing)
}
