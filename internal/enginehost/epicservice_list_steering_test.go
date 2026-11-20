package enginehost

import (
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// inboxWant is the inbox fields an epic carries: its steerer, its repository, whether it is paused and by whom, and why
// it is parked.
type inboxWant struct {
	steerer     string
	repository  string
	paused      bool
	pauseSource string
	parked      string
}

// wantEpicInbox asserts through client that the epic with epicServiceID lists with the inbox fields want and the
// openQuestions count, and that it gets with the same steerer, repository, pause and park.
func wantEpicInbox(t *testing.T, client enginev1connect.EpicsClient, want inboxWant, openQuestions int32) {
	t.Helper()
	listed, err := client.ListEpics(t.Context(), connect.NewRequest(&v1.ListEpicsRequest{}))
	if err != nil {
		t.Fatalf("ListEpics: %v", err)
	}
	var summary *v1.EpicSummary
	for _, epic := range listed.Msg.GetEpics() {
		if epic.GetId() == epicServiceID {
			summary = epic
		}
	}
	if summary == nil {
		t.Fatalf("ListEpics has no epic %s", epicServiceID)
	}
	got := inboxWant{
		steerer:     summary.GetSteerer(),
		repository:  summary.GetRepository(),
		paused:      summary.GetPaused(),
		pauseSource: summary.GetPauseSource(),
		parked:      summary.GetParked(),
	}
	if got != want {
		t.Errorf("ListEpics inbox = %+v, want %+v", got, want)
	}
	if n := summary.GetOpenQuestions(); n != openQuestions {
		t.Errorf("ListEpics open questions = %d, want %d", n, openQuestions)
	}

	stored := getEpicOK(t, client)
	gotStored := inboxWant{
		steerer:     stored.GetSteerer(),
		repository:  stored.GetContract().GetRepository(),
		paused:      stored.GetPaused(),
		pauseSource: stored.GetPauseSource(),
		parked:      stored.GetParked(),
	}
	if gotStored != want {
		t.Errorf("GetEpic inbox = %+v, want %+v", gotStored, want)
	}
}

func TestEpicListSteeringUnpausedEpicCarriesItsSteererAndRepository(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	createEpicOK(t, client, fullEpicRequest(repo, epicServiceID))

	wantEpicInbox(t, client, inboxWant{steerer: fixedSteerID, repository: repo}, 0)
}

func TestEpicListSteeringOwnerPauseIsOwner(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	createEpicOK(t, client, fullEpicRequest(repo, epicServiceID))
	pause := connect.NewRequest(&v1.PauseEpicRequest{Id: epicServiceID})
	if _, err := client.PauseEpic(t.Context(), pause); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}

	wantEpicInbox(t, client, inboxWant{
		steerer: fixedSteerID, repository: repo, paused: true, pauseSource: "owner",
	}, 0)
}

func TestEpicListSteeringUsageLimitPauseIsLimit(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	createEpicOK(t, client, fullEpicRequest(repo, epicServiceID))
	turn, err := st.ClaimTurn(t.Context(), epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	free := store.TurnFree{Reason: "usage limit", Pause: true, Limit: true}
	if err = st.FreeTurn(t.Context(), epicServiceID, turn.Seq, free, time.Unix(turnFreed, 0)); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}

	wantEpicInbox(t, client, inboxWant{
		steerer: fixedSteerID, repository: repo, paused: true, pauseSource: "limit",
	}, 0)
}

func TestEpicListSteeringParkIsListedWithItsReason(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	createEpicOK(t, client, fullEpicRequest(repo, epicServiceID))
	const reason = "Synthetic park reason."
	if err := st.ParkEpic(
		t.Context(), epicServiceID, store.ParkClassOrchestrator, reason, time.Unix(turnFreed, 0),
	); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}

	// An owner-class park opens its own question, so that one is the only open question here.
	wantEpicInbox(t, client, inboxWant{steerer: fixedSteerID, repository: repo, parked: reason}, 1)
}

func TestEpicListSteeringOpenQuestionsAreCounted(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	createEpicOK(t, client, fullEpicRequest(repo, epicServiceID))
	askQuestions(t, st, "Synthetic question one?", "Synthetic question two?")

	wantEpicInbox(t, client, inboxWant{steerer: fixedSteerID, repository: repo}, 2)
}

func TestEpicListSteeringEpicWithNoSteererListsEmpty(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	const repo = "/work/synthetic-repo"
	if err := st.CreateEpic(ctx, store.EpicRow{
		ID: epicServiceID, Repository: repo, Title: "Steering", Phase: "accepted",
		Approved: "Synthetic approval.", Intent: "Synthetic intent.", Care: "serious", Approach: "Synthetic approach.",
		Checks:  "just check",
		Created: time.Unix(turnStarted, 0), Changed: time.Unix(turnStarted, 0),
		Workflow: store.EpicWorkflow{Name: "steering", Text: "steering", SHA256: "steering"},
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	client := newEpicsClient(t, startEpicServer(t, st))

	wantEpicInbox(t, client, inboxWant{repository: repo}, 0)
}
