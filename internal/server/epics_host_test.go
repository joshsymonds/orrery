package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// publicHostsStore is orreryd's Epics store over stored epics, whose engine messages carry each epic's host: ListEpics
// summarizes each stored epic with its host, and GetEpic answers the stored epic of its id, or not_found.
type publicHostsStore struct {
	stored []*enginev1.StoredEpic
}

func (f *publicHostsStore) ListEpics(
	context.Context,
	*connect.Request[enginev1.ListEpicsRequest],
) (*connect.Response[enginev1.ListEpicsResponse], error) {
	summaries := make([]*enginev1.EpicSummary, 0, len(f.stored))
	for _, epic := range f.stored {
		summaries = append(summaries, &enginev1.EpicSummary{
			Id:      epic.GetContract().GetId(),
			Phase:   epic.GetPhase(),
			PhaseAt: epic.GetPhaseAt(),
			Created: epic.GetCreated(),
			Changed: epic.GetChanged(),
			Host:    epic.GetHost(),
		})
	}
	return connect.NewResponse(&enginev1.ListEpicsResponse{Epics: summaries}), nil
}

func (f *publicHostsStore) GetEpic(
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

// publicHostsEpicHosts returns the host each epic the public host tests serve carries in its engine message, and
// publicHostsStepHosts returns the host each step carries. An epic and its step are on the same host.
func publicHostsEpicHosts() map[string]string {
	return map[string]string{"repoa/on-b": "b", "repoa/on-ctl": "ctl"}
}

func publicHostsStepHosts() map[string]string {
	return map[string]string{"step-b": "b", "step-ctl": "ctl"}
}

// publicHostsFixture returns the store and the steps of the public host tests: an epic stored on host b with a running
// step on b, and an epic stored on host ctl with a running step on ctl.
func publicHostsFixture() (*publicHostsStore, *epicsFake) {
	onB := storedEpicOf("repoa/on-b", "accepted", epicsAgo(time.Hour))
	onB.Host = "b"
	onCtl := storedEpicOf("repoa/on-ctl", "accepted", epicsAgo(time.Hour))
	onCtl.Host = "ctl"
	steps := &epicsFake{steps: []*enginev1.Step{
		{Id: "step-b", Epic: "repoa/on-b", Task: "t1", Started: epicsAgo(time.Minute), Host: "b"},
		{Id: "step-ctl", Epic: "repoa/on-ctl", Task: "t1", Started: epicsAgo(time.Minute), Host: "ctl"},
	}}
	return &publicHostsStore{stored: []*enginev1.StoredEpic{onB, onCtl}}, steps
}

// TestPublicHostsEpicsGet covers the host each epic and each of its steps carry through Get: the epic answers the host
// its engine message carries, and its step answers the host of its engine step.
func TestPublicHostsEpicsGet(t *testing.T) {
	t.Parallel()
	epicStore, steps := publicHostsFixture()
	client := epicStoreServe(t, steps, epicStore)
	for id, wantHost := range publicHostsEpicHosts() {
		got := epicsGet(t, client, id)
		if host := got.GetEpic().GetHost(); host != wantHost {
			t.Errorf("Get %s epic host = %q, want %q", id, host, wantHost)
		}
		if n := len(got.GetSteps()); n != 1 {
			t.Fatalf("Get %s answered %d steps, want 1", id, n)
		}
		step := got.GetSteps()[0]
		if host, want := step.GetHost(), publicHostsStepHosts()[step.GetId()]; host != want {
			t.Errorf("Get %s step %s host = %q, want %q", id, step.GetId(), host, want)
		}
	}
}

// TestPublicHostsEpicsList covers the host each epic and each of its running steps carry through List: the epic
// answers the host its engine message carries, and its running step answers the host of its engine step.
func TestPublicHostsEpicsList(t *testing.T) {
	t.Parallel()
	epicStore, steps := publicHostsFixture()
	client := epicStoreServe(t, steps, epicStore)
	epics := epicsList(t, client)
	if n := len(epics); n != len(publicHostsEpicHosts()) {
		t.Fatalf("List answered %d epics, want %d", n, len(publicHostsEpicHosts()))
	}
	for _, epic := range epics {
		id := epic.GetId()
		wantHost, ok := publicHostsEpicHosts()[id]
		if !ok {
			t.Errorf("List answered epic %s, want none", id)
			continue
		}
		if host := epic.GetHost(); host != wantHost {
			t.Errorf("List epic %s host = %q, want %q", id, host, wantHost)
		}
		if n := len(epic.GetRunningSteps()); n != 1 {
			t.Fatalf("List epic %s answered %d running steps, want 1", id, n)
		}
		step := epic.GetRunningSteps()[0]
		if host, want := step.GetHost(), publicHostsStepHosts()[step.GetId()]; host != want {
			t.Errorf("List epic %s step %s host = %q, want %q", id, step.GetId(), host, want)
		}
	}
}
