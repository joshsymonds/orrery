package store

import (
	"context"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// triggerWriter is one store write that adds a trigger, for TestNoWriterAddsATriggerToAnEpicInAFinalPhase. prepare
// opens a store holding the epic the write acts on and everything the write needs, and returns the store, the epic and
// the write; want is the id of the trigger the write adds to an epic that is not in a final phase.
type triggerWriter struct {
	name    string
	prepare func(t *testing.T) (*Store, string, func() error)
	want    string
}

// claimEpicStore opens a store holding claimEpic, created and not yet planned.
func claimEpicStore(t *testing.T) *Store {
	t.Helper()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	return s
}

// triggerWriters returns one case for each write that can add a trigger to an epic already in a final phase. The writes
// that refuse such an epic before they add one, a kick, an amendment's start and a release's replan, and those whose
// own question or phase rules keep them from it, an answer to an overtaken question and a correction of an ended
// epic's gaps, are not among them; CreateEpic never creates an epic in a final phase. An answer to the final park
// question of an epic in met_at_base is TestAnAnswerOnAMetAtBaseEpicWritesNoTrigger.
func triggerWriters() []triggerWriter {
	at := callsAt()
	onClaimEpic := func(write func(s *Store) error) func(t *testing.T) (*Store, string, func() error) {
		return func(t *testing.T) (*Store, string, func() error) {
			t.Helper()
			s := claimEpicStore(t)
			return s, claimEpic, func() error { return write(s) }
		}
	}
	return []triggerWriter{
		{"a plan node's start", onClaimEpic(func(s *Store) error {
			return s.StartNodes(context.Background(), claimEpic, nil,
				&NodeTrigger{ID: "workflow:start", Cause: "workflow node plan started"}, at)
		}), "workflow:start"},
		{"a failed check", onClaimEpic(func(s *Store) error {
			return s.EndNodesWaking(context.Background(), claimEpic, nil,
				NodeTrigger{ID: "workflow:check", Cause: "workflow node check failed: boom"}, at)
		}), "workflow:check"},
		{"an unbuilt plan", onClaimEpic(func(s *Store) error {
			return s.ParkEpicWaking(context.Background(), claimEpic, "epic not built: R1",
				NodeTrigger{ID: "workflow:unbuilt", Cause: "epic not built: R1"}, at)
		}), "workflow:unbuilt"},
		{"a gap round", onClaimEpic(func(s *Store) error {
			run := NodeRun{Path: "conclude", ID: "id-conclude", Node: "conclude", Kind: "conclude", Attempt: 1}
			return s.StartGapRound(context.Background(), claimEpic, run,
				[]NodeTrigger{{ID: "gap:review", Cause: "gap review: the review has gaps"}}, nil, at)
		}), "gap:review"},
		{"a stall", onClaimEpic(func(s *Store) error {
			return s.WakeStall(context.Background(), claimEpic, "the epic is idle", StallMark{}, at)
		}), "stall:1"},
		{"a stall park's retry", func(t *testing.T) (*Store, string, func() error) {
			t.Helper()
			s := claimEpicStore(t)
			if err := s.ParkStall(context.Background(), claimEpic, "the epic is idle", StallMark{}, at); err != nil {
				t.Fatalf("ParkStall: %v", err)
			}
			return s, claimEpic, func() error { return s.RetryEpic(context.Background(), claimEpic, Maker{By: MakerOwner}, at) }
		}, "stall-retry:1"},
		{"a planner park", func(t *testing.T) (*Store, string, func() error) {
			t.Helper()
			s := claimEpicStore(t)
			runs := []NodeRun{{Path: "check", ID: "id-check", Node: "check", Kind: "gate", Attempt: 1}}
			if err := s.StartNodes(context.Background(), claimEpic, runs, nil, claimedAt()); err != nil {
				t.Fatalf("StartNodes: %v", err)
			}
			return s, claimEpic, func() error {
				return s.ParkNode(context.Background(), claimEpic, "check", 1, "failed", "boom", ParkClassNode,
					"workflow node check failed: boom", at)
			}
		}, "park:2"},
		{"a batch's end", func(t *testing.T) (*Store, string, func() error) {
			t.Helper()
			s := openIntegrateStore(t)
			startIntegrateRuns(t, s,
				NodeRun{Path: "plan/integrate", ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 1})
			return s, integrateEpic, func() error {
				return s.EndBatch(context.Background(), integrateEpic, BatchEnd{
					Path: "plan/integrate", Attempt: 1, Outcome: "failed", Output: "lint failed",
				}, &NodeTrigger{ID: "workflow:S1", Cause: "workflow node plan/integrate failed: lint failed"}, at)
			}
		}, "workflow:S1"},
		{"a climb onto the approach rung", func(t *testing.T) (*Store, string, func() error) {
			t.Helper()
			s := sendBackStore(t)
			from := TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: OrchestratorRung}
			if err := s.SetTaskBuild(context.Background(), sendBackEpic, 1, TaskBuild{}, from); err != nil {
				t.Fatalf("SetTaskBuild: %v", err)
			}
			const cause = "the orchestrator's attempt failed"
			return s, sendBackEpic, func() error {
				return s.ClimbTask(context.Background(), sendBackEpic, 1, from, ClimbedBuild(from, ApproachRung, cause),
					RungMove{MovedBy: MovedByUnit, Cause: cause}, nil, nil, at)
			}
		}, "T1-approach"},
		{"a refused action", func(t *testing.T) (*Store, string, func() error) {
			t.Helper()
			s := sendBackStore(t)
			action := sendBackAction(t, s, 1, EpicActionInput{Kind: ActionSendBack, Task: 9, Reason: sendBackReason})
			return s, sendBackEpic, func() error {
				_, err := s.CarryOutAction(context.Background(), action, at)
				return err
			}
		}, "action-1-1-refused"},
		{"an applied amendment", func(t *testing.T) (*Store, string, func() error) {
			t.Helper()
			s := openEpicStore(t)
			createAmendEpic(t, s)
			moveStoredBase(t, s)
			amended := amendOwner(t, s, newAmendItem())
			judgedRun(t, s, amended.RedRun, evidence.PhaseRed, amendBase, 1, evidence.VerdictFails)
			settle(t, s)
			return s, amendEpic, func() error { return s.StopAmendment(context.Background(), amendEpic, 1, "no such revision", at) }
		}, "amend-1"},
	}
}

// TestNoWriterAddsATriggerToAnEpicInAFinalPhase runs each write of triggerWriters twice: on its epic as prepare leaves
// it, where the write adds its trigger, and on the same epic set to released first, where it adds none, since no
// planner turn would claim it (insertTrigger). The live run shows the write reaches its trigger, so the released run
// shows the final phase alone is what keeps it out.
func TestNoWriterAddsATriggerToAnEpicInAFinalPhase(t *testing.T) {
	t.Parallel()
	for _, writer := range triggerWriters() {
		for _, final := range []bool{false, true} {
			name := writer.name + "/live"
			if final {
				name = writer.name + "/released"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				s, epic, write := writer.prepare(t)
				if final {
					if _, err := s.fixtureExec(context.Background(), `UPDATE epics SET phase = ? WHERE id = ?`,
						phaseReleased, epic); err != nil {
						t.Fatalf("release epic %s: %v", epic, err)
					}
				}
				before := triggerIDsOf(t, s, epic)

				if err := write(); err != nil {
					t.Fatalf("write: %v", err)
				}

				var added []string
				for _, id := range triggerIDsOf(t, s, epic) {
					if !slices.Contains(before, id) {
						added = append(added, id)
					}
				}
				want := []string{writer.want}
				if final {
					want = nil
				}
				if !slices.Equal(added, want) {
					t.Errorf("triggers the write added = %q, want %q", added, want)
				}
			})
		}
	}
}

// triggerIDsOf returns the ids of the triggers of epic, in the order ListEpicTriggers reads them.
func triggerIDsOf(t *testing.T, s *Store, epic string) []string {
	t.Helper()
	triggers, err := s.fixtureQueries().ListEpicTriggers(context.Background(), epic)
	if err != nil {
		t.Fatalf("ListEpicTriggers of epic %s: %v", epic, err)
	}
	ids := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		ids = append(ids, trigger.ID)
	}
	return ids
}

// TestEveryTriggerIsWrittenThroughInsertTrigger reads the store's source and checks that the two queries that insert a
// trigger, InsertEpicTrigger and InsertEpicTriggerOnce, are called only in triggers.go, so every trigger the store writes
// passes the final-phase rule of insertTrigger and insertTriggerOnce.
func TestEveryTriggerIsWrittenThroughInsertTrigger(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("list the store's source: %v", err)
	}
	call := regexp.MustCompile(`\.InsertEpicTrigger(Once)?\(`)
	seen := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, readErr := os.ReadFile(name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		calls := len(call.FindAllIndex(data, -1))
		if name == "triggers.go" {
			seen = calls
			continue
		}
		if calls != 0 {
			t.Errorf("%s calls a trigger insert query %d times; add the trigger with insertTrigger or "+
				"insertTriggerOnce", name, calls)
		}
	}
	if seen != 2 {
		t.Errorf("triggers.go calls the trigger insert queries %d times, want 2, one in each helper", seen)
	}
}
