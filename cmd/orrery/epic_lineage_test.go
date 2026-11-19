package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

const (
	lineageAlpha = "demo-1a2b3c4/epic-alpha"
	lineageBeta  = "demo-1a2b3c4/epic-beta"
	lineageGamma = "demo-1a2b3c4/epic-gamma"
)

// lineageEpics is an EpicsClient that lists the epics of its summaries in order and serves each epic by its id. Every
// other call is answered by the recordingEpics it embeds.
type lineageEpics struct {
	*recordingEpics
	summaries []*v1.EpicSummary
	epics     map[string]*v1.StoredEpic
}

// newLineageEpics returns a lineageEpics that lists the epics with ids in order and serves them from epics.
func newLineageEpics(ids []string, epics map[string]*v1.StoredEpic) *lineageEpics {
	summaries := make([]*v1.EpicSummary, 0, len(ids))
	for _, id := range ids {
		summaries = append(summaries, &v1.EpicSummary{Id: id})
	}
	return &lineageEpics{recordingEpics: newRecordingEpics(), summaries: summaries, epics: epics}
}

func (f *lineageEpics) GetEpic(
	_ context.Context,
	req *connect.Request[v1.GetEpicRequest],
) (*connect.Response[v1.GetEpicResponse], error) {
	return respond(f.recordingEpics, req.Msg, &v1.GetEpicResponse{Epic: f.epics[req.Msg.GetId()]})
}

func (f *lineageEpics) ListEpics(
	_ context.Context,
	req *connect.Request[v1.ListEpicsRequest],
) (*connect.Response[v1.ListEpicsResponse], error) {
	return respond(f.recordingEpics, req.Msg, &v1.ListEpicsResponse{Epics: f.summaries})
}

// TestTaskLadderLineageListsAcrossEpics checks that lineage prints a block for each task with rungs, epic by epic in
// list order, with the rungs, versions and confirmed findings of each. It prints no block for a task without rungs, and
// no line for a finding that is not confirmed.
func TestTaskLadderLineageListsAcrossEpics(t *testing.T) {
	t.Parallel()
	fake := newLineageEpics([]string{lineageBeta, lineageAlpha}, map[string]*v1.StoredEpic{
		lineageAlpha: {Tasks: []*v1.EpicTask{
			{
				Id: "t1", Title: "Parse config", Status: "held",
				Rungs: []*v1.EpicTaskRung{
					{Rung: 1, Kind: "attempt", Outcome: "failed", Reason: "build is red"},
					{
						Rung: 2, Kind: "rescue", MovedBy: "unit", Cause: "parser drops the last field",
						Outcome: "accepted",
					},
					{
						Rung: 3, Kind: "approach", MovedBy: "finding", Cause: "config is read twice",
						Gate: "review", Assertion: "reads the file once",
					},
				},
				Versions: []*v1.EpicTaskVersion{
					{
						Version: 2, Source: "orchestrator", State: "applied", Reason: "narrow the deliverable",
						Changed: []string{"deliverable", "files"},
					},
					{Version: 3, Source: "approach", State: "refused", Reason: "off the target"},
				},
				Reviews: []*v1.EpicTaskReview{
					{Attempt: 1, Gate: "review", Verdict: "fails", Findings: []*v1.EpicTaskFinding{
						{Id: "f1", Claim: "parser drops the last field", Result: "confirmed"},
						{Id: "f2", Claim: "unused import", Result: "dropped"},
					}},
					{Attempt: 2, Gate: "review", Verdict: "holds", Findings: []*v1.EpicTaskFinding{
						{Id: "f3", Claim: "config is read twice", Result: "confirmed"},
						{Id: "f4", Claim: "naming", Result: "undecided"},
					}},
				},
			},
			{Id: "t2", Title: "Write docs", Status: "held"},
		}},
		lineageBeta: {Tasks: []*v1.EpicTask{
			{
				Id: "t5", Title: "Retry the build", Status: "parked",
				Rungs: []*v1.EpicTaskRung{
					{Rung: 1, Kind: "attempt", Outcome: "failed", Reason: "lint failed"},
					{
						Rung: 2, Kind: "orchestrator", MovedBy: "fail_decision",
						Cause: "the owner asked for one more try",
					},
				},
				Reviews: []*v1.EpicTaskReview{
					{Attempt: 1, Gate: "lint", Verdict: "fails", Findings: []*v1.EpicTaskFinding{
						{Id: "f5", Claim: "unused variable", Result: "confirmed"},
					}},
				},
			},
		}},
	})

	stdout, _, err := runEpicCommand(t, fake, nil, "lineage")
	if err != nil {
		t.Fatalf("epicCommand(lineage) = %v, want nil", err)
	}
	want := "demo-1a2b3c4/epic-beta t5 Retry the build (parked, rung 2)\n" +
		"  rung 1 attempt -> failed: lint failed\n" +
		"  rung 2 orchestrator moved by fail_decision: the owner asked for one more try\n" +
		"  finding attempt 1 lint f5: unused variable\n" +
		"demo-1a2b3c4/epic-alpha t1 Parse config (held, rung 3)\n" +
		"  rung 1 attempt -> failed: build is red\n" +
		"  rung 2 rescue moved by unit: parser drops the last field -> accepted\n" +
		"  rung 3 approach moved by finding: config is read twice (gate review: reads the file once)\n" +
		"  version 2 orchestrator applied: narrow the deliverable [changed deliverable, files]\n" +
		"  version 3 approach refused: off the target\n" +
		"  finding attempt 1 review f1: parser drops the last field\n" +
		"  finding attempt 2 review f3: config is read twice\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// TestTaskLadderLineageEscalated checks that --escalated keeps a task whose highest rung is 3, and drops one whose
// highest rung is 2.
func TestTaskLadderLineageEscalated(t *testing.T) {
	t.Parallel()
	fake := newLineageEpics([]string{lineageGamma}, map[string]*v1.StoredEpic{
		lineageGamma: {Tasks: []*v1.EpicTask{
			{
				Id: "t6", Title: "Split the parser", Status: "held",
				Rungs: []*v1.EpicTaskRung{
					{Rung: 1, Kind: "attempt"}, {Rung: 2, Kind: "rescue"}, {Rung: 3, Kind: "approach"},
				},
			},
			{
				Id: "t7", Title: "Rename the flag", Status: "held",
				Rungs: []*v1.EpicTaskRung{{Rung: 1, Kind: "attempt"}, {Rung: 2, Kind: "rescue"}},
			},
		}},
	})

	stdout, _, err := runEpicCommand(t, fake, nil, "lineage", "--escalated")
	if err != nil {
		t.Fatalf("epicCommand(lineage --escalated) = %v, want nil", err)
	}
	want := "demo-1a2b3c4/epic-gamma t6 Split the parser (held, rung 3)\n" +
		"  rung 1 attempt\n" +
		"  rung 2 rescue\n" +
		"  rung 3 approach\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// TestTaskLadderLineageOneEpic checks that --epic ID reads that epic with GetEpic and lists no epics.
func TestTaskLadderLineageOneEpic(t *testing.T) {
	t.Parallel()
	fake := newLineageEpics([]string{lineageAlpha}, map[string]*v1.StoredEpic{lineageBeta: {}})

	if _, _, err := runEpicCommand(t, fake, nil, "lineage", "--epic", lineageBeta); err != nil {
		t.Fatalf("epicCommand(lineage --epic) = %v, want nil", err)
	}
	requests := fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1: a GetEpic for the one epic", len(requests))
	}
	req, ok := requests[0].(*v1.GetEpicRequest)
	if !ok || req.GetId() != lineageBeta {
		t.Fatalf("request = %v, want a GetEpic for %s", requests[0], lineageBeta)
	}
}

// TestTaskLadderLineageForm checks that each wrong form returns a usage error naming the lineage form, and calls
// nothing.
func TestTaskLadderLineageForm(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"lineage", "extra"},
		{"lineage", "--epic"},
		{"lineage", "--epic", ""},
		{"lineage", "--bogus"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			fake := newLineageEpics([]string{lineageAlpha}, nil)
			stdout, _, err := runEpicCommand(t, fake, nil, args...)
			if _, ok := errors.AsType[usageError](err); !ok {
				t.Fatalf("epicCommand(%v) = %v, want a usage error", args, err)
			}
			if !strings.Contains(err.Error(), epicLineageForm) {
				t.Errorf("epicCommand(%v) error %q does not name the form %q", args, err, epicLineageForm)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
			if n := len(fake.recorded()); n != 0 {
				t.Errorf("epicCommand(%v) made %d calls, want none", args, n)
			}
		})
	}
}
