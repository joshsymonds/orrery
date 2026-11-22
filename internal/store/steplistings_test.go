package store

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// listingResultCase is one stored result and the summary a listing reads of it.
type listingResultCase struct {
	name    string
	result  string
	summary string
}

// listingResultCases are the results the listing tests store: well-formed objects, values that are not strings, keys
// held twice, and results that are not JSON objects at all.
func listingResultCases() []listingResultCase {
	filler := strings.Repeat("synthetic filler ", 300)
	return []listingResultCase{
		{
			name:    "summary among other keys",
			result:  `{"summary":"synthetic summary","status":"done"}`,
			summary: "synthetic summary",
		},
		{name: "no key", result: `{"status":"done"}`},
		{name: "empty string", result: `{"summary":""}`},
		{name: "value that is not a string", result: `{"summary":3}`},
		{name: "null value", result: `{"summary":null}`},
		{name: "key held twice", result: `{"summary":"first","summary":"second"}`, summary: "second"},
		{name: "key held twice, last not a string", result: `{"summary":"first","summary":3}`},
		{name: "nested key", result: `{"result":{"summary":"inner"}}`},
		{name: "escaped key", result: `{"summary":"escaped summary"}`, summary: "escaped summary"},
		{name: "escaped value", result: `{"summary":"line one\nline \"two\""}`, summary: "line one\nline \"two\""},
		{name: "malformed", result: `{"summary":"cut`},
		{name: "trailing garbage", result: `{"summary":"synthetic"} trailing`},
		{name: "json5", result: `{summary:'synthetic',}`},
		{name: "raw control character", result: "{\"summary\":\"a\tb\"}"},
		{name: "array", result: `["summary","synthetic"]`},
		{name: "string", result: `"summary"`},
		{name: "number", result: `42`},
		{name: "null", result: `null`},
		{name: "empty", result: ``},
		{
			name:    "large object",
			result:  `{"notes":"` + filler + `","summary":"after the notes","more":"` + filler + `"}`,
			summary: "after the notes",
		},
	}
}

// TestStepListingsReadTheResultAsStepRowListingDoes stores a step for each listing result case and reads it back both
// ways: through the step_listings view, and as StepRow.Listing of the full row. Each must give the case's summary, so
// the view and the Go reading are one rule, and no result fails the view's read.
func TestStepListingsReadTheResultAsStepRowListingDoes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	cases := listingResultCases()
	ops := make([]EngineOp, 0, 2*len(cases))
	for i, tc := range cases {
		id := fmt.Sprintf("s%02d", i)
		ops = append(ops, labelledStepOf(id, "E", "T"), StepReturn{ID: id, Result: tc.result})
	}
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	listings, err := s.StepListings(ctx, "E", "")
	if err != nil {
		t.Fatalf("StepListings: %v", err)
	}
	if len(listings) != len(cases) {
		t.Fatalf("StepListings = %d listings, want %d", len(listings), len(cases))
	}
	for i, tc := range cases {
		listing := listings[i]
		row, err := s.Step(ctx, listing.ID)
		if err != nil {
			t.Fatalf("Step %s: %v", listing.ID, err)
		}
		for _, got := range []struct {
			by      string
			listing StepListing
		}{{"step_listings", listing}, {"StepRow.Listing", row.Listing()}} {
			if got.listing.Summary != tc.summary {
				t.Errorf("%s: %s = summary %q, want %q", tc.name, got.by, got.listing.Summary, tc.summary)
			}
		}
	}
}

// TestStepListingsCarryEveryListedFieldOfTheRow stores steps with every field a listing carries set, through every
// step write, and checks that each listing step_listings reads equals StepRow.Listing of its full row.
func TestStepListingsCarryEveryListedFieldOfTheRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	running := labelledStepOf("running", "E", "T1")
	running.GateName = "synthetic-gate"
	running.ReadOnly = false
	ended := labelledStepOf("ended", "E", "T2")
	if err := s.ApplyEngine(ctx, []EngineOp{running, ended}, t0); err != nil {
		t.Fatalf("ApplyEngine add: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		StepReturn{ID: "running", Result: `{"summary":"synthetic"}`, Checks: []StepCheck{
			{Round: 1, Name: "gate", OK: false, Detail: "exit 1", Stdout: "synthetic out", Stderr: "synthetic err"},
		}},
		StepWatch{ID: "running", Verdict: StepVerdict{Kind: "judge", Round: 1, Verdict: "unmet"}, Doing: "testing"},
		StepLoop{
			ID: "running", Round: 2, Limited: true, LimitKind: "synthetic-kind", LimitResets: t0.Add(time.Hour),
			Stage: StepStageLimited, StageFrom: t0.Add(time.Minute),
		},
		StepReturn{ID: "ended", Result: `{"summary":"synthetic ended"}`},
		StepEnd{ID: "ended", End: "done", Status: "DONE", Reason: "returned"},
	}, t0.Add(2*time.Minute)); err != nil {
		t.Fatalf("ApplyEngine writes: %v", err)
	}
	listings, err := s.StepListings(ctx, "E", "")
	if err != nil {
		t.Fatalf("StepListings: %v", err)
	}
	if got := len(listings); got != 2 {
		t.Fatalf("StepListings = %d listings, want 2", got)
	}
	for _, listing := range listings {
		row, err := s.Step(ctx, listing.ID)
		if err != nil {
			t.Fatalf("Step %s: %v", listing.ID, err)
		}
		if want := row.Listing(); !reflect.DeepEqual(listing, want) {
			t.Errorf("listing of %s = %+v, want %+v", listing.ID, listing, want)
		}
	}
	// Both started at t0, so the listings are in id order: ended, then running.
	endedListing, runningListing := listings[0], listings[1]
	if runningListing.LoopLimitResets.IsZero() || runningListing.LoopStageFrom.IsZero() ||
		len(runningListing.Checks) != 1 || len(runningListing.Verdicts) != 1 || runningListing.Summary != "synthetic" ||
		endedListing.Ended.IsZero() || endedListing.End != "done" || endedListing.Summary != "synthetic ended" {
		t.Errorf("listings = %+v, want the running step's limit, stage, check, verdict and summary and the ended "+
			"step's end and summary", listings)
	}
}

// listingMarkers are the texts the large fixture's prompts and results hold that no listing may carry.
const (
	listingPromptMarker = "synthetic-prompt-marker"
	listingResultMarker = "synthetic-result-marker"
)

// seedLargeListingSteps stores, in epic E, ended steps and one running step, each with a 64 KiB prompt and a result
// that holds a 32 KiB note besides its summary, and one running step in epic F.
func seedLargeListingSteps(t *testing.T, s *Store, ended int) {
	t.Helper()
	ctx := context.Background()
	prompt := listingPromptMarker + strings.Repeat("p", 64<<10)
	result := `{"notes":"` + listingResultMarker + strings.Repeat("r", 32<<10) +
		`","summary":"synthetic summary"}`
	var ops []EngineOp
	add := func(id, epic, task string) {
		step := labelledStepOf(id, epic, task)
		step.Prompt = prompt
		ops = append(ops, step, StepReturn{ID: id, Result: result})
	}
	for i := range ended {
		id := fmt.Sprintf("ended-%03d", i)
		add(id, "E", fmt.Sprintf("T%d", i%3))
		ops = append(ops, StepEnd{ID: id, End: "done", Status: "DONE", Reason: "returned"})
	}
	add("running-e", "E", "T0")
	add("running-f", "F", "T0")
	if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// requireLeanListings fails unless every listing carries the fixture's summary and none of its prompt or result text.
func requireLeanListings(t *testing.T, listings []StepListing) {
	t.Helper()
	for _, listing := range listings {
		text := fmt.Sprintf("%+v", listing)
		if strings.Contains(text, listingPromptMarker) || strings.Contains(text, listingResultMarker) {
			t.Errorf("listing %s carries prompt or result text", listing.ID)
		}
		if listing.Summary != "synthetic summary" {
			t.Errorf("listing %s = summary %q, want the fixture's summary", listing.ID, listing.Summary)
		}
	}
}

// TestStepListingsCarryNoPromptOrResultText stores steps with large prompts and results and reads them as listings,
// by epic and as the running steps: each listing has the result's summary and none of the prompt's or
// result's text.
func TestStepListingsCarryNoPromptOrResultText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	seedLargeListingSteps(t, s, 6)
	byEpic, err := s.StepListings(ctx, "E", "")
	if err != nil {
		t.Fatalf("StepListings: %v", err)
	}
	if len(byEpic) != 7 {
		t.Fatalf("StepListings of E = %d listings, want 7", len(byEpic))
	}
	requireLeanListings(t, byEpic)
	running, ended, err := s.RunningStepListings(ctx, "", "")
	if err != nil {
		t.Fatalf("RunningStepListings: %v", err)
	}
	requireLeanListings(t, running)
	if got := stepListingIDs(running); !reflect.DeepEqual(got, []string{"running-e", "running-f"}) {
		t.Errorf("running listings = %v, want running-e and running-f", got)
	}
	if want := map[string]int{"E": 6}; !reflect.DeepEqual(ended, want) {
		t.Errorf("ended = %v, want %v", ended, want)
	}
}

// stepListingIDs returns the ids of listings, in order.
func stepListingIDs(listings []StepListing) []string {
	ids := make([]string, 0, len(listings))
	for _, listing := range listings {
		ids = append(ids, listing.ID)
	}
	return ids
}

// recordedQueryNames returns the sqlc name of each query recorder ran, in order.
func recordedQueryNames(recorder *planRecorder) []string {
	names := make([]string, 0, len(recorder.texts))
	for _, text := range recorder.texts {
		name, ok := queryName(text)
		if !ok {
			name = text
		}
		names = append(names, name)
	}
	return names
}

// TestRunningStepListingsReadOnlyTheOpenStepsAndTheLabelCounts reads the running steps of a store whose epic E holds
// forty ended steps, narrowed in each way. Each read issues the same two queries, the open steps' listings and the
// count of the labeled steps by label, however many steps have ended, and the counts of the ended steps follow the
// narrowing. An unlabelled open step is never listed.
func TestRunningStepListingsReadOnlyTheOpenStepsAndTheLabelCounts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	seedLargeListingSteps(t, s, 40)
	if err := s.ApplyEngine(ctx, []EngineOp{stepAddOf("unlabelled")}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	cases := []struct {
		name, epic, task string
		running          []string
		ended            map[string]int
	}{
		{name: "every epic", running: []string{"running-e", "running-f"}, ended: map[string]int{"E": 40}},
		{name: "one epic", epic: "E", running: []string{"running-e"}, ended: map[string]int{"E": 40}},
		{name: "one task", task: "T0", running: []string{"running-e", "running-f"}, ended: map[string]int{"E": 14}},
		{name: "epic and task", epic: "E", task: "T1", running: nil, ended: map[string]int{"E": 13}},
		{name: "no ended step", epic: "F", running: []string{"running-f"}, ended: map[string]int{}},
	}
	for _, tc := range cases {
		recorder := &planRecorder{db: s.db.DB}
		listings, ended, err := readRunningStepListings(ctx, sqlcgen.New(recorder), tc.epic, tc.task)
		if err != nil {
			t.Fatalf("%s: readRunningStepListings: %v", tc.name, err)
		}
		want := []string{"ListOpenStepListings", "CountLabelledSteps"}
		if got := recordedQueryNames(recorder); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: queries = %v, want %v", tc.name, got, want)
		}
		if got := stepListingIDs(listings); !slices.Equal(got, tc.running) {
			t.Errorf("%s: running = %v, want %v", tc.name, got, tc.running)
		}
		if !reflect.DeepEqual(ended, tc.ended) {
			t.Errorf("%s: ended = %v, want %v", tc.name, ended, tc.ended)
		}
	}
}

// TestStepListingsIssueOneQueryPerRead checks that a read of an epic's listings, narrowed to a task or not, issues one
// query, the view's search by the epic's labels, however many steps the epic holds.
func TestStepListingsIssueOneQueryPerRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openWriterAt(t, filepath.Join(t.TempDir(), "orrery.db"))
	seedLargeListingSteps(t, s, 20)
	for _, tc := range []struct {
		epic, task, query string
		count             int
	}{
		{epic: "E", query: "ListEpicStepListings", count: 21},
		{epic: "E", task: "T2", query: "ListEpicTaskStepListings", count: 6},
		{task: "T0", query: "ListLabelledStepListings", count: 9},
	} {
		recorder := &planRecorder{db: s.db.DB}
		listings, err := readStepListings(ctx, sqlcgen.New(recorder), tc.epic, tc.task)
		if err != nil {
			t.Fatalf("readStepListings(%q, %q): %v", tc.epic, tc.task, err)
		}
		if got := recordedQueryNames(recorder); !reflect.DeepEqual(got, []string{tc.query}) {
			t.Errorf("readStepListings(%q, %q) queries = %v, want [%s]", tc.epic, tc.task, got, tc.query)
		}
		if len(listings) != tc.count {
			t.Errorf("readStepListings(%q, %q) = %d listings, want %d", tc.epic, tc.task, len(listings), tc.count)
		}
	}
}
