package store

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"
)

// actionLogAt is the second every EpicActionLog test makes its calls at.
func actionLogAt() time.Time {
	return time.Unix(1700006000, 0)
}

// actionLogCreate stores the synthetic claimEpic in the accepted phase.
func actionLogCreate(t *testing.T, s *Store) {
	t.Helper()
	createEpic(t, s, fullEpic(claimEpic))
}

// actionLogPaused stores claimEpic and pauses it as the owner.
func actionLogPaused(t *testing.T, s *Store) {
	t.Helper()
	actionLogCreate(t, s)
	if err := s.PauseEpic(context.Background(), claimEpic, Maker{By: MakerOwner}, actionLogAt()); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
}

// actionLogRunning stores claimEpic with a running turn.
func actionLogRunning(t *testing.T, s *Store) {
	t.Helper()
	actionLogCreate(t, s)
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(context.Background(), claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
}

// actionLogParked stores claimEpic parked by a usage limit.
func actionLogParked(t *testing.T, s *Store) {
	t.Helper()
	actionLogCreate(t, s)
	parkEpic(t, s, claimEpic, "credit ran out")
}

// actionLogEndedWithGaps stores claimEpic in the ended_with_gaps phase.
func actionLogEndedWithGaps(t *testing.T, s *Store) {
	t.Helper()
	row := fullEpic(claimEpic)
	row.Phase = phaseEndedWithGaps
	createEpic(t, s, row)
}

// actionLogCase is one control call with the state it runs from. A call that the store accepts writes one action of
// kind; a call that the store refuses, with the text refusal, writes none.
type actionLogCase struct {
	name    string
	epic    string
	setup   func(t *testing.T, s *Store)
	call    func(s *Store) error
	kind    string
	refusal string
}

// actionLogCases returns the seven controls, each accepted from a state it can leave, and refused from one it cannot.
// Every call is made by the owner at actionLogAt.
func actionLogCases() []actionLogCase {
	owner := Maker{By: MakerOwner}
	return []actionLogCase{
		{
			name: "pause accepted", epic: claimEpic, kind: ActionPause, setup: actionLogCreate,
			call: func(s *Store) error {
				return s.PauseEpic(context.Background(), claimEpic, owner, actionLogAt())
			},
		},
		{
			name: "pause refused", epic: claimEpic, refusal: "epic " + claimEpic + " is already paused",
			setup: actionLogPaused,
			call: func(s *Store) error {
				return s.PauseEpic(context.Background(), claimEpic, owner, actionLogAt())
			},
		},
		{
			name: "resume accepted", epic: claimEpic, kind: ActionResume, setup: actionLogPaused,
			call: func(s *Store) error {
				return s.ResumeEpic(context.Background(), claimEpic, owner, actionLogAt())
			},
		},
		{
			name: "resume refused", epic: claimEpic, refusal: "epic " + claimEpic + " is not paused",
			setup: actionLogCreate,
			call: func(s *Store) error {
				return s.ResumeEpic(context.Background(), claimEpic, owner, actionLogAt())
			},
		},
		{
			name: "stop accepted", epic: claimEpic, kind: ActionStop, setup: actionLogRunning,
			call: func(s *Store) error {
				_, err := s.StopEpic(context.Background(), claimEpic, owner, actionLogAt())
				return err
			},
		},
		{
			name: "stop refused", epic: claimEpic, refusal: "epic " + claimEpic + " has no running turn",
			setup: actionLogCreate,
			call: func(s *Store) error {
				_, err := s.StopEpic(context.Background(), claimEpic, owner, actionLogAt())
				return err
			},
		},
		{
			name: "retry accepted", epic: claimEpic, kind: ActionRetry, setup: actionLogParked,
			call: func(s *Store) error {
				return s.RetryEpic(context.Background(), claimEpic, owner, actionLogAt())
			},
		},
		{
			name: "retry refused", epic: claimEpic, refusal: "epic " + claimEpic + " is not parked",
			setup: actionLogCreate,
			call: func(s *Store) error {
				return s.RetryEpic(context.Background(), claimEpic, owner, actionLogAt())
			},
		},
		{
			name: "retry node accepted", epic: claimEpic, kind: ActionRetry, setup: refMoveRunParked,
			call: func(s *Store) error {
				return s.RetryNode(context.Background(), claimEpic, parkRetryRunPath, owner, actionLogAt())
			},
		},
		{
			name: "retry node refused", epic: claimEpic,
			refusal: "node plan/S9/check of epic " + claimEpic + " is not parked",
			setup:   actionLogCreate,
			call: func(s *Store) error {
				return s.RetryNode(context.Background(), claimEpic, "plan/S9/check", owner, actionLogAt())
			},
		},
		{
			name: "close accepted", epic: claimEpic, kind: ActionClose, setup: actionLogCreate,
			call: func(s *Store) error {
				_, err := s.CloseEpic(
					context.Background(), claimEpic, kindShipped, "", CloserOwner, owner, actionLogAt(),
				)
				return err
			},
		},
		{
			name: "close refused", epic: claimEpic,
			refusal: `close epic ` + claimEpic + `: kind "bogus" is not superseded, shipped or abandoned`,
			setup:   actionLogCreate,
			call: func(s *Store) error {
				_, err := s.CloseEpic(context.Background(), claimEpic, "bogus", "", CloserOwner, owner, actionLogAt())
				return err
			},
		},
		{
			name: "amend accepted", epic: amendEpic, kind: ActionAmend, setup: createAmendEpic,
			call: func(s *Store) error {
				_, err := s.AmendEvidence(context.Background(), amendEpic, Amendment{
					Requirement: "R1", Item: newAmendItem(), Reason: "the conclusion is sharpened",
				}, owner, actionLogAt())
				return err
			},
		},
		{
			name: "amend refused", epic: amendEpic,
			refusal: `epic ` + amendEpic + ` has no Requirement "R9"`,
			setup:   createAmendEpic,
			call: func(s *Store) error {
				_, err := s.AmendEvidence(context.Background(), amendEpic, Amendment{
					Requirement: "R9", Item: newAmendItem(), Reason: "no such requirement",
				}, owner, actionLogAt())
				return err
			},
		},
		{
			name: "release accepted", epic: claimEpic, kind: ActionRelease, setup: actionLogEndedWithGaps,
			call: func(s *Store) error {
				return s.ReleaseEpic(context.Background(), claimEpic, nil, owner, actionLogAt())
			},
		},
		{
			name: "release refused", epic: claimEpic, refusal: "epic " + claimEpic + " is not stopped",
			setup: actionLogCreate,
			call: func(s *Store) error {
				return s.ReleaseEpic(context.Background(), claimEpic, nil, owner, actionLogAt())
			},
		},
	}
}

// actionLogOf returns the actions of epic as its history reads them.
func actionLogOf(t *testing.T, s *Store, epic string) []EpicAction {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), epic)
	if err != nil {
		t.Fatalf("EpicHistory of %s: %v", epic, err)
	}
	return history.Actions
}

// requireActionLog fails unless the actions of epic are want, in order.
func requireActionLog(t *testing.T, s *Store, epic string, want []EpicAction) {
	t.Helper()
	got := actionLogOf(t, s, epic)
	if len(got) != len(want) {
		t.Fatalf("actions of %s = %+v, want %+v", epic, got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("action %d of %s = %+v, want %+v", i, epic, got[i], want[i])
		}
	}
}

// TestEpicActionLogAcceptedControlWritesOneActionOfItsKind checks that each control the store accepts appends exactly one
// action of its kind, made by the owner at the fixed time, and that it adds nothing else to the action log.
func TestEpicActionLogAcceptedControlWritesOneActionOfItsKind(t *testing.T) {
	t.Parallel()
	for _, tc := range actionLogCases() {
		if tc.refusal != "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			tc.setup(t, s)
			before := actionLogOf(t, s, tc.epic)

			if err := tc.call(s); err != nil {
				t.Fatalf("control: %v", err)
			}
			want := slices.Clone(before)
			want = append(want, EpicAction{
				Seq: int64(len(before) + 1), Kind: tc.kind, Maker: Maker{By: MakerOwner}, At: actionLogAt(),
			})
			requireActionLog(t, s, tc.epic, want)
		})
	}
}

// TestEpicActionLogRefusedControlWritesNoAction checks that each control the store refuses is a RefusalError and leaves
// the action log as it was.
func TestEpicActionLogRefusedControlWritesNoAction(t *testing.T) {
	t.Parallel()
	for _, tc := range actionLogCases() {
		if tc.refusal == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := openEpicStore(t)
			tc.setup(t, s)
			before := actionLogOf(t, s, tc.epic)

			requireRefusal(t, tc.call(s), tc.refusal)
			requireActionLog(t, s, tc.epic, before)
		})
	}
}

// TestEpicActionLogOwnerActionIsOwedToTheSteerer checks that an action the owner makes names the epic's steerer as its
// recipient, and that an action a conversation makes is owed to no session.
func TestEpicActionLogOwnerActionIsOwedToTheSteerer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openPendingStore(t)
	addPendingEngines(t, s, "engine-1")
	row := fullEpic(claimEpic)
	row.Steerer = "engine-1"
	createEpic(t, s, row)

	if err := s.PauseEpic(ctx, claimEpic, Maker{By: MakerOwner}, actionLogAt()); err != nil {
		t.Fatalf("PauseEpic by owner: %v", err)
	}
	conversation := Maker{By: MakerConversation, Session: "engine-1"}
	if err := s.ResumeEpic(ctx, claimEpic, conversation, actionLogAt()); err != nil {
		t.Fatalf("ResumeEpic by conversation: %v", err)
	}
	requireActionLog(t, s, claimEpic, []EpicAction{
		{Seq: 1, Kind: ActionPause, Maker: Maker{By: MakerOwner}, Recipient: "engine-1", At: actionLogAt()},
		{Seq: 2, Kind: ActionResume, Maker: conversation, At: actionLogAt()},
	})
}

// TestEpicActionLogOwnerAmendKeepsItsDecision checks that an owner amendment that applies in its write keeps the decision
// it writes and adds exactly one amend action beside it. The item keeps the conclusion and kind, so it applies at once.
func TestEpicActionLogOwnerAmendKeepsItsDecision(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createAmendEpic(t, s)

	if _, err := s.AmendEvidence(context.Background(), amendEpic, Amendment{
		Requirement: "R1", Item: captureAmendItem(), Reason: "the test moved to cmd/orreryd",
	}, Maker{By: MakerOwner}, actionLogAt()); err != nil {
		t.Fatalf("AmendEvidence: %v", err)
	}
	seeds := len(fullEpic(amendEpic).Decisions)
	if got := closeRow(t, s, amendEpic).Decisions; len(got) != seeds+1 {
		t.Fatalf("decisions of %s after an owner amendment = %+v, want the %d seeds and the amendment's decision",
			amendEpic, got, seeds)
	}
	requireActionLog(t, s, amendEpic, []EpicAction{
		{Seq: 1, Kind: ActionAmend, Maker: Maker{By: MakerOwner}, At: actionLogAt()},
	})
}

// TestEpicActionLogEndedWithGapsReleaseKeepsItsDecision checks that releasing an epic that ended with gaps keeps the owner
// decision that releases it and adds exactly one release action.
func TestEpicActionLogEndedWithGapsReleaseKeepsItsDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	actionLogEndedWithGaps(t, s)

	if err := s.ReleaseEpic(ctx, claimEpic, nil, Maker{By: MakerOwner}, actionLogAt()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	decisions := closeRow(t, s, claimEpic).Decisions
	seeds := len(fullEpic(claimEpic).Decisions)
	if len(decisions) != seeds+1 || decisions[seeds].ID != releaseEndedWithGapsID {
		t.Fatalf("decisions after ReleaseEpic = %+v, want the %d seeds and release decision %s",
			decisions, seeds, releaseEndedWithGapsID)
	}
	requireActionLog(t, s, claimEpic, []EpicAction{
		{Seq: 1, Kind: ActionRelease, Maker: Maker{By: MakerOwner}, At: actionLogAt()},
	})
}
