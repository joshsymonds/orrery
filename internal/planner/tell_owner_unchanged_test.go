package planner

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// ownerTellCase is a tell that routes as an owner notice: the setup that stores it, the epic it is on, and the notice
// Attention routes for it, its id, body, block and whether it is a park notice.
type ownerTellCase struct {
	name     string
	setup    func(t *testing.T, s *store.Store)
	epic     string
	id       string
	body     string
	block    string
	blockRef string
	park     bool
}

// ownerTellCases returns a case for each kind of tell that routes as an owner notice. Each tell is the first tell of
// its epic, so its seq is 1.
func ownerTellCases() []ownerTellCase {
	return []ownerTellCase{
		{
			name: "plain",
			setup: func(t *testing.T, s *store.Store) {
				createTellEpic(t, s, "")
				tellEpic(t, s, "check the gate")
			},
			epic: tellAlpha, id: "orrery-7d8e9f0/tell-alpha:tell:1",
			body: "check the gate",
		},
		{
			name: "met-at-base",
			setup: func(t *testing.T, s *store.Store) {
				createTellEpic(t, s, "")
				tellEpic(t, s, "workflow node red: R1 met at the epic's base main, recorded as met there, and the epic goes on")
			},
			epic: tellAlpha, id: "orrery-7d8e9f0/tell-alpha:tell:1",
			body: "workflow node red: R1 met at the epic's base main, recorded as met there, and the epic goes on",
		},
		{
			name: "release-resumed",
			setup: func(t *testing.T, s *store.Store) {
				createTellEpic(t, s, "")
				tellEpic(t, s, "release resumed at step 2, publish")
			},
			epic: tellAlpha, id: "orrery-7d8e9f0/tell-alpha:tell:1",
			body: "release resumed at step 2, publish",
		},
		{
			name: "park-question",
			setup: func(t *testing.T, s *store.Store) {
				createTellEpic(t, s, "")
				if err := s.ParkEpic(
					t.Context(), tellAlpha, store.ParkClassBranch, "credit ran out", tellAt(),
				); err != nil {
					t.Fatalf("ParkEpic: %v", err)
				}
			},
			epic: tellAlpha, id: "orrery-7d8e9f0/tell-alpha:tell:1",
			body: parkTellBody, block: blockQuestion, blockRef: "Q1", park: true,
		},
		{
			name: "question-unheard",
			setup: func(t *testing.T, s *store.Store) {
				createRunnerEpic(t, s)
				endWorkTurn(t, s, claimedAt())
				askRunnerOwner(t, s)
			},
			epic: runnerEpicID, id: runnerEpicID + ":tell:1",
			body: askedTellBody, block: blockQuestion, blockRef: "Q1",
		},
	}
}

// untoldTellsOfEpic returns the tells of epic that Attention reports as untold. It returns none when Attention has no
// entry for epic, which is the case once an epic with no turn has every tell told.
func untoldTellsOfEpic(t *testing.T, s *store.Store, epic string) []store.EpicTell {
	t.Helper()
	attention, err := s.Attention(t.Context())
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	for _, entry := range attention {
		if entry.Epic == epic {
			return entry.Tells
		}
	}
	return nil
}

// storedNoticeOf returns the epic notice that n stores as its source data, decoded. It fails the test when n is not an
// epic notice.
func storedNoticeOf(t *testing.T, n route.Notice) epicNotice {
	t.Helper()
	if n.Source.Kind != EpicSource {
		t.Fatalf("notice source kind = %q, want %q", n.Source.Kind, EpicSource)
	}
	var stored epicNotice
	if err := json.Unmarshal(n.Source.Data, &stored); err != nil {
		t.Fatalf("decode notice source: %v", err)
	}
	return stored
}

// TestTellOwnerUnchangedRoutesEachKindAsItsOwnerNotice: each kind of tell that routes to the owner routes one notice
// on one pass, with the notice id, body and block the tell has always routed with, and the route marks the tell told.
func TestTellOwnerUnchangedRoutesEachKindAsItsOwnerNotice(t *testing.T) {
	t.Parallel()
	for _, c := range ownerTellCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := openRunnerStore(t)
			c.setup(t, s)
			router := &fakeAttentionRouter{}

			attentionOver(s, router).pass(t.Context())

			if len(router.notices) != 1 {
				t.Fatalf("notices after one pass = %d, want the tell's one", len(router.notices))
			}
			routed := router.notices[0]
			got := shownOf(t, routed)
			if got.key != (notice.Key{Epic: c.epic}) {
				t.Errorf("notice key = %+v, want %+v", got.key, notice.Key{Epic: c.epic})
			}
			if got.desk.Body != c.body || got.desk.Block != c.block || got.desk.BlockRef != c.blockRef {
				t.Errorf("notice body, block, ref = %q, %q, %q, want %q, %q, %q",
					got.desk.Body, got.desk.Block, got.desk.BlockRef, c.body, c.block, c.blockRef)
			}
			stored := storedNoticeOf(t, routed)
			if stored.ID != c.id {
				t.Errorf("notice id = %q, want %q", stored.ID, c.id)
			}
			if stored.Park != c.park {
				t.Errorf("notice park = %v, want %v", stored.Park, c.park)
			}
			want := store.ToldMark{Epic: c.epic, Seq: 1, Tell: true}
			if routed.Told == nil || *routed.Told != want {
				t.Errorf("notice told mark = %+v, want %+v", routed.Told, want)
			}
			if left := untoldTellsOfEpic(t, s, c.epic); len(left) != 0 {
				t.Errorf("untold tells after the route = %+v, want none", left)
			}
		})
	}
}

// TestTellOwnerUnchangedSteererDeliveryKeepsOwnerNoticeAndToldMark: after the owner's route, the tell is undelivered to
// the steerer. The steerer's delivery then sets only the tell's steerer delivery: the tell stays told, and the owner
// notice routed for it does not change.
func TestTellOwnerUnchangedSteererDeliveryKeepsOwnerNoticeAndToldMark(t *testing.T) {
	t.Parallel()
	for _, c := range tellSentCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := openRunnerStore(t)
			createTellEpic(t, s, "steerer")
			addTellSession(t, s, "steerer", "done")
			tellEpic(t, s, c.text)
			router := &fakeAttentionRouter{}
			attentionOver(s, router).pass(t.Context())
			if len(router.notices) != 1 {
				t.Fatalf("notices after the owner's pass = %d, want the tell's one", len(router.notices))
			}
			routed := router.notices[0]
			shownBefore := shownOf(t, routed)
			event := store.EpicTellEvent(tellAlpha, 1)
			if left := undeliveredEvents(t, s); !slices.Equal(left, []string{event}) {
				t.Fatalf("undelivered after the owner's route = %q, want the tell still %q", left, event)
			}

			deliver := &fakeCallerDeliver{}
			passTells(t, s, deliver.deliver)

			if got := promptIDsOf(deliver.delivered); !slices.Equal(got, []string{event}) {
				t.Fatalf("prompt ids sent to the steerer = %q, want %q", got, event)
			}
			if left := undeliveredEvents(t, s); len(left) != 0 {
				t.Errorf("undelivered after the steerer's delivery = %q, want none", left)
			}
			if left := untoldTellsOfEpic(t, s, tellAlpha); len(left) != 0 {
				t.Errorf("untold tells after the steerer's delivery = %+v, want the tell still told", left)
			}
			if len(router.notices) != 1 {
				t.Fatalf("notices after the steerer's delivery = %d, want still one", len(router.notices))
			}
			after := router.notices[0]
			if got := shownOf(t, after); !reflect.DeepEqual(got, shownBefore) {
				t.Errorf("owner notice after the steerer's delivery = %+v, want %+v", got, shownBefore)
			}
			if !reflect.DeepEqual(after.Source, routed.Source) || !reflect.DeepEqual(after.Told, routed.Told) {
				t.Errorf("owner notice source and told mark after the steerer's delivery = %+v, %+v, want %+v, %+v",
					after.Source, after.Told, routed.Source, routed.Told)
			}
		})
	}
}

// TestTellOwnerUnchangedSilentMoveRoutesNoOwnerNotice: a silent move tell routes no owner notice, and is marked told.
func TestTellOwnerUnchangedSilentMoveRoutesNoOwnerNotice(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "")
	tellEpic(t, s, "T1 implementer moved to a fresh worktree")
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(t.Context())

	if n := len(router.notices); n != 0 {
		t.Errorf("notices for a silent move tell = %d, want none", n)
	}
	if left := untoldTellsOfEpic(t, s, tellAlpha); len(left) != 0 {
		t.Errorf("untold tells after the pass = %+v, want the silent move marked told", left)
	}
}

// TestTellOwnerUnchangedSteererHeldQuestionTellRoutesNoOwnerNotice: a question tell the steerer holds routes no owner
// notice, and is marked told.
func TestTellOwnerUnchangedSteererHeldQuestionTellRoutesNoOwnerNotice(t *testing.T) {
	t.Parallel()
	s := openRunnerStore(t)
	createTellEpic(t, s, "steerer")
	addTellSession(t, s, "steerer", "done")
	if err := s.ParkEpic(t.Context(), tellAlpha, store.ParkClassBranch, "credit ran out", tellAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(t.Context())

	if n := len(router.notices); n != 0 {
		t.Errorf("notices for a question tell the steerer holds = %d, want none", n)
	}
	if left := untoldTellsOfEpic(t, s, tellAlpha); len(left) != 0 {
		t.Errorf("untold tells after the pass = %+v, want the question tell marked told", left)
	}
}
