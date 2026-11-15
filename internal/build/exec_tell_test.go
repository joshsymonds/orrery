package build

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// tellStore is the store a ref-check test runs its runner over: the fixture's store, with the tells each
// SetTaskBuildTelling and each ParkTask is handed recorded in telling and parks.
type tellStore struct {
	*store.Store
	telling [][]string
	parks   [][]string
}

func (s *tellStore) SetTaskBuildTelling(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, tells []string, at time.Time,
) error {
	s.telling = append(s.telling, tells)
	return s.Store.SetTaskBuildTelling(ctx, epic, seq, from, to, tells, at)
}

func (s *tellStore) ParkTask(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, reason string, at time.Time, tells ...string,
) error {
	s.parks = append(s.parks, tells)
	return s.Store.ParkTask(ctx, epic, seq, from, to, reason, at, tells...)
}

// errRefused is what a refusing store returns for the write it refuses.
var errRefused = errors.New("store refused the write")

// refusingStore is a tellStore whose SetTaskBuildTelling refuses, as a store does for a stale build.
type refusingStore struct {
	tellStore
}

func (s *refusingStore) SetTaskBuildTelling(
	_ context.Context, _ string, _ int64, _, _ store.TaskBuild, tells []string, _ time.Time,
) error {
	s.telling = append(s.telling, tells)
	return errRefused
}

// parkRefusingStore is a tellStore whose ParkTask refuses, as a store does for a stale build.
type parkRefusingStore struct {
	tellStore
}

func (s *parkRefusingStore) ParkTask(
	_ context.Context, _ string, _ int64, _, _ store.TaskBuild, _ string, _ time.Time, tells ...string,
) error {
	s.parks = append(s.parks, tells)
	return errRefused
}

// runOver makes the fixture's runner pass over s rather than the fixture's store.
func (f *passFixture) runOver(s Store) {
	f.runner = New(Config{
		Store: s, Steps: f.steps, Evidence: f.evidence, Getenv: f.getenv, StateDir: f.stateDir, Now: passClock,
	})
}

// isFeatureMove reports whether text is the tell of the implementer's move of refs/heads/feature to commit.
func isFeatureMove(text, commit string) bool {
	return strings.HasPrefix(text, "T1 implementer moved refs/heads/feature (") && strings.Contains(text, commit)
}

// TestPassRefMoveIsToldInTheMoveWrite covers R3 and R4: a ref move that does not park reaches the store in the one
// write that moves the build, with its tell.
func TestPassRefMoveIsToldInTheMoveWrite(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	commit := buildObject(t, f.repo, f.base)
	buildRun(t, f.repo, "update-ref", "refs/heads/feature", commit)
	f.setStep(passStep(StageImplement, 1, 0), "done", `{"summary":"done","commit":"`+commit+`"}`)
	tells := &tellStore{Store: f.store}
	f.runOver(tells)

	f.pass()

	if len(tells.telling) != 1 || len(tells.telling[0]) != 1 ||
		!isFeatureMove(tells.telling[0][0], commit) {
		t.Errorf("SetTaskBuildTelling tells = %q, want the move of refs/heads/feature to %s in one write",
			tells.telling, commit)
	}
	stored := f.tells()
	if !slices.ContainsFunc(stored, func(text string) bool { return isFeatureMove(text, commit) }) {
		t.Errorf("stored tells = %v, want the move of refs/heads/feature to %s", stored, commit)
	}
	f.expectGreenAt(commit)
}

// TestPassParkingRefCheckWritesItsOtherTellsWithThePark covers R3 and R4: a move that parks the epic and a move that
// does not both reach the store in the park's write, the non-parking move's tell with the park's reason.
func TestPassParkingRefCheckWritesItsOtherTellsWithThePark(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	commit := buildObject(t, f.repo, f.base)
	buildRun(t, f.repo, "update-ref", "refs/heads/feature", commit)
	moved := buildObject(t, f.repo, f.base)
	buildRun(t, f.repo, "update-ref", "refs/heads/main", moved)
	f.setStep(passStep(StageImplement, 1, 0), "done", `{"summary":"done"}`)
	tells := &tellStore{Store: f.store}
	f.runOver(tells)

	f.pass()

	reason := fmt.Sprintf("T1 implementer moved refs/heads/main (%s to %s)", f.base, moved)
	if got := f.epic().Parked; got != reason {
		t.Errorf("epic Parked = %q, want %q", got, reason)
	}
	if len(tells.parks) != 1 || len(tells.parks[0]) != 1 ||
		!isFeatureMove(tells.parks[0][0], commit) {
		t.Errorf("ParkTask tells = %q, want the move of refs/heads/feature to %s in the park's write",
			tells.parks, commit)
	}
	stored := f.tells()
	if !slices.Contains(stored, reason) ||
		!slices.ContainsFunc(stored, func(text string) bool { return isFeatureMove(text, commit) }) {
		t.Errorf("stored tells = %v, want the park reason and the move of refs/heads/feature to %s", stored, commit)
	}
}

// TestPassRefCheckRefusedByStoreRecordsNoTell covers R4: a ref check whose store refuses its write stores no tell and
// leaves the move unstored.
func TestPassRefCheckRefusedByStoreRecordsNoTell(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	commit := buildObject(t, f.repo, f.base)
	buildRun(t, f.repo, "update-ref", "refs/heads/feature", commit)
	f.setStep(passStep(StageImplement, 1, 0), "done", `{"summary":"done","commit":"`+commit+`"}`)
	refusing := &refusingStore{tellStore: tellStore{Store: f.store}}
	f.runOver(refusing)

	f.pass()

	if len(refusing.telling) != 1 || len(refusing.telling[0]) != 1 ||
		!isFeatureMove(refusing.telling[0][0], commit) {
		t.Fatalf("SetTaskBuildTelling tells = %q, want the refused move of refs/heads/feature to %s",
			refusing.telling, commit)
	}
	stored := f.tells()
	if slices.ContainsFunc(stored, func(text string) bool { return isFeatureMove(text, commit) }) {
		t.Errorf("stored tells = %v, want no tell of the refused move", stored)
	}
	if got := f.task().Build.Refs; got == "" {
		t.Errorf("build Refs is empty, want the refused check still open")
	}
}

// TestPassParkingRefCheckRefusedByStoreRecordsNoTell covers R4: a parking ref check whose store refuses its park
// stores neither the park's reason nor the other tells, and leaves the epic unparked.
func TestPassParkingRefCheckRefusedByStoreRecordsNoTell(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	commit := buildObject(t, f.repo, f.base)
	buildRun(t, f.repo, "update-ref", "refs/heads/feature", commit)
	moved := buildObject(t, f.repo, f.base)
	buildRun(t, f.repo, "update-ref", "refs/heads/main", moved)
	f.setStep(passStep(StageImplement, 1, 0), "done", `{"summary":"done"}`)
	refusing := &parkRefusingStore{tellStore: tellStore{Store: f.store}}
	f.runOver(refusing)

	f.pass()

	if len(refusing.parks) != 1 || len(refusing.parks[0]) != 1 ||
		!isFeatureMove(refusing.parks[0][0], commit) {
		t.Fatalf("ParkTask tells = %q, want the move of refs/heads/feature to %s in the refused park",
			refusing.parks, commit)
	}
	if got := f.tells(); len(got) != 0 {
		t.Errorf("stored tells = %v, want none when the park is refused", got)
	}
	if got := f.epic().Parked; got != "" {
		t.Errorf("epic Parked = %q, want the epic unparked when the park is refused", got)
	}
}
