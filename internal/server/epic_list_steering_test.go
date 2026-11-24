package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// steeringStoreFake is orreryd's Epics client for the steering tests. ListEpics answers summaries, and GetEpic answers
// the stored epic of its id, or not_found when none holds it.
type steeringStoreFake struct {
	summaries []*enginev1.EpicSummary
	stored    []*enginev1.StoredEpic
}

func (f *steeringStoreFake) ListEpics(
	context.Context,
	*connect.Request[enginev1.ListEpicsRequest],
) (*connect.Response[enginev1.ListEpicsResponse], error) {
	return connect.NewResponse(&enginev1.ListEpicsResponse{Epics: f.summaries}), nil
}

func (f *steeringStoreFake) GetEpic(
	_ context.Context,
	req *connect.Request[enginev1.GetEpicRequest],
) (*connect.Response[enginev1.GetEpicResponse], error) {
	for _, epic := range f.stored {
		if epic.GetContract().GetId() == req.Msg.GetId() {
			return connect.NewResponse(&enginev1.GetEpicResponse{Epic: epic}), nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("synthetic not found"))
}

// steeringFields is what an Epic carries of its steering: the steering session, the repository path, the pause, the
// park reason and the open question count.
type steeringFields struct {
	steering, path, pauseSource, parked string
	paused                              bool
	openQuestions                       int32
}

// steeringOf returns the steering fields of epic.
func steeringOf(epic *orreryv1.Epic) steeringFields {
	return steeringFields{
		steering:      epic.GetSteering(),
		path:          epic.GetPath(),
		paused:        epic.GetPaused(),
		pauseSource:   epic.GetPauseSource(),
		parked:        epic.GetParked(),
		openQuestions: epic.GetOpenQuestions(),
	}
}

// TestEpicListSteeringListCarriesSteering answers the steering fields of each epic through List: an orreryd epic
// carries its summary's steering session, repository path, pause, park reason and open question count, and an unsteered
// orreryd epic carries no steering.
func TestEpicListSteeringListCarriesSteering(t *testing.T) {
	t.Parallel()
	store := &steeringStoreFake{summaries: []*enginev1.EpicSummary{
		{
			Id: "repoa/owned", Repository: "/work/repoa", Phase: "accepted", PhaseAt: epicsAgo(time.Hour),
			Changed: epicsAgo(time.Hour), Steerer: "session-owned", Paused: true, PauseSource: "owner",
			Parked: "waiting on review", OpenQuestions: 2,
		},
		{
			Id: "repoa/limited", Repository: "/work/repoa", Phase: "accepted", PhaseAt: epicsAgo(time.Hour),
			Changed: epicsAgo(time.Hour), Steerer: "session-limited", Paused: true, PauseSource: "limit",
		},
		{
			Id: "repoa/unsteered", Repository: "/work/repoa", Phase: "accepted", PhaseAt: epicsAgo(time.Hour),
			Changed: epicsAgo(time.Hour),
		},
	}}
	client := epicStoreServe(t, &epicsFake{}, store)

	want := map[string]steeringFields{
		"repoa/owned": {
			steering: "session-owned", path: "/work/repoa", paused: true, pauseSource: "owner",
			parked: "waiting on review", openQuestions: 2,
		},
		"repoa/limited":   {steering: "session-limited", path: "/work/repoa", paused: true, pauseSource: "limit"},
		"repoa/unsteered": {path: "/work/repoa"},
	}
	listed := epicsList(t, client)
	if len(listed) != len(want) {
		t.Fatalf("List answered %d epics, want %d", len(listed), len(want))
	}
	for _, epic := range listed {
		w, ok := want[epic.GetId()]
		if !ok {
			t.Errorf("List answered unexpected epic %q", epic.GetId())
			continue
		}
		if got := steeringOf(epic); got != w {
			t.Errorf("List %s steering = %+v, want %+v", epic.GetId(), got, w)
		}
	}
}

// TestEpicListSteeringGetCarriesSteering answers the steering fields of each epic through Get: an orreryd epic carries
// its stored steerer, pause, park reason and repository path, and its open question count counts only open questions;
// an unsteered orreryd epic carries no steering.
func TestEpicListSteeringGetCarriesSteering(t *testing.T) {
	t.Parallel()
	owned := storedEpicOf("repoa/owned", "accepted", epicsAgo(time.Hour))
	owned.Contract.Repository = "/work/repoa"
	owned.Steerer = "session-owned"
	owned.Paused = true
	owned.PauseSource = "owner"
	owned.Parked = "waiting on review"
	owned.Questions = []*enginev1.EpicQuestion{
		{Id: "Q1", State: "open"},
		{Id: "Q2", State: "answered"},
		{Id: "Q3", State: "open"},
	}
	limited := storedEpicOf("repoa/limited", "accepted", epicsAgo(time.Hour))
	limited.Contract.Repository = "/work/repoa"
	limited.Steerer = "session-limited"
	limited.Paused = true
	limited.PauseSource = "limit"
	unsteered := storedEpicOf("repoa/unsteered", "accepted", epicsAgo(time.Hour))
	unsteered.Contract.Repository = "/work/repoa"
	store := &steeringStoreFake{stored: []*enginev1.StoredEpic{owned, limited, unsteered}}
	client := epicStoreServe(t, &epicsFake{}, store)

	want := map[string]steeringFields{
		"repoa/owned": {
			steering: "session-owned", path: "/work/repoa", paused: true, pauseSource: "owner",
			parked: "waiting on review", openQuestions: 2,
		},
		"repoa/limited":   {steering: "session-limited", path: "/work/repoa", paused: true, pauseSource: "limit"},
		"repoa/unsteered": {path: "/work/repoa"},
	}
	for id, w := range want {
		if got := steeringOf(epicsGet(t, client, id).GetEpic()); got != w {
			t.Errorf("Get %s steering = %+v, want %+v", id, got, w)
		}
	}
}
