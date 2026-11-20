package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// historyAt returns the unix second offset seconds after the hour before the fixed clock: the time of a history fixture.
func historyAt(offset int64) int64 {
	return epicsAgo(time.Hour) + offset
}

// historyFixture returns one orreryd epic's stored history, its labeled steps and its evidence runs. The turn and the
// trigger at time 0 tie with the step that turn runs, and the two steps and the run at 60 tie with each other. The two
// decisions and the first tell at 300 tie, and the turn and the trigger at 1200 tie. The lists are given out of order,
// and a step of another epic and an unlabelled step are left out.
func historyFixture() (*enginev1.StoredEpic, []*enginev1.Step, []*enginev1.EvidenceRun) {
	stored := storedEpicOf("repoa/shared", "accepted", historyAt(3600))
	stored.Turns = []*enginev1.EpicTurn{
		{
			Seq: 1, Step: "turn-1", State: "succeeded", Outline: "synthetic outline", Outcome: "synthetic outcome",
			Decisions: []string{"d1"}, Started: historyAt(0), Ended: historyAt(600),
		},
		{Seq: 2, State: "running", Started: historyAt(1200)},
	}
	stored.Triggers = []*enginev1.EpicTrigger{
		{Id: "trig-1", Cause: "synthetic cause one", State: "consumed", Turn: 1, Created: historyAt(0)},
		{Id: "trig-2", Cause: "synthetic cause two", State: "pending", Created: historyAt(1200)},
	}
	stored.Decisions = []*enginev1.EpicDecision{
		{Seq: 2, Id: "d2", Text: "synthetic decision two", Time: historyAt(300)},
		{Seq: 1, Id: "d1", Text: "synthetic decision one", Time: historyAt(300), Question: "Q1"},
	}
	stored.Tells = []*enginev1.EpicTell{
		{Seq: 2, Text: "synthetic tell two", At: historyAt(900)},
		{Seq: 1, Text: "synthetic tell one", At: historyAt(300), Told: true},
	}
	steps := []*enginev1.Step{
		{Id: "step-b", Epic: "repoa/shared", Task: "one-a", Started: historyAt(60)},
		{Id: "other", Epic: "repoa/other", Started: historyAt(60)},
		{Id: "step-a", Epic: "repoa/shared", ReadOnly: true, Started: historyAt(60)},
		{Id: "turn-1", Epic: "repoa/shared", ReadOnly: true, Started: historyAt(0)},
		{Id: "unlabelled", Started: historyAt(60)},
	}
	runs := []*enginev1.EvidenceRun{{
		Id: "run-1", Epic: "repoa/shared", Item: "R1", Phase: "red", Source: "captured", State: "ended", Exit: 1,
		Verdict: "holds", Passed: true, Revision: "synthetic-rev", LogBytes: 10, Created: historyAt(60),
		Ended: historyAt(120), JudgeStep: "step-a",
		Artifacts: []*enginev1.EvidenceArtifact{{Path: "out/screen.png", State: "kept", Size: 512}},
	}}
	return stored, steps, runs
}

// historyWant is one entry a History answer holds: its time and the message its row carries.
type historyWant struct {
	time int64
	item proto.Message
}

// historyFixtureWant returns the twelve entries historyFixture's epic answers, oldest first.
func historyFixtureWant() []historyWant {
	return []historyWant{
		{historyAt(0), &orreryv1.EpicTurn{
			Seq: 1, Step: "turn-1", State: "succeeded", Outline: "synthetic outline", Outcome: "synthetic outcome",
			Decisions: []string{"d1"}, Started: historyAt(0), Ended: historyAt(600),
		}},
		{historyAt(0), &orreryv1.EpicTrigger{
			Id: "trig-1", Cause: "synthetic cause one", State: "consumed", Turn: 1, Created: historyAt(0),
		}},
		{historyAt(0), &orreryv1.EpicStep{Id: "turn-1", ReadOnly: true, Role: "planner", Started: historyAt(0)}},
		{historyAt(60), &orreryv1.EpicStep{Id: "step-a", ReadOnly: true, Role: "evidence", Started: historyAt(60)}},
		{historyAt(60), &orreryv1.EpicStep{
			Id: "step-b", Task: "one-a", Role: "implementer", Started: historyAt(60),
		}},
		{historyAt(60), &orreryv1.EvidenceRun{
			Id: "run-1", Item: "R1", Phase: "red", Source: "captured", State: "ended", Exit: 1, Verdict: "holds",
			Passed: true, Revision: "synthetic-rev", LogBytes: 10, Created: historyAt(60), Ended: historyAt(120),
			Artifacts: []*orreryv1.EvidenceArtifact{{
				Path: "out/screen.png", State: "kept", Size: 512, MediaType: "image/png",
			}},
		}},
		{historyAt(300), &orreryv1.EpicDecision{
			Seq: 1, Id: "d1", Text: "synthetic decision one", Time: historyAt(300), Question: "Q1",
		}},
		{historyAt(300), &orreryv1.EpicDecision{
			Seq: 2, Id: "d2", Text: "synthetic decision two", Time: historyAt(300),
		}},
		{historyAt(300), &orreryv1.EpicTell{Seq: 1, Text: "synthetic tell one", At: historyAt(300), Told: true}},
		{historyAt(900), &orreryv1.EpicTell{Seq: 2, Text: "synthetic tell two", At: historyAt(900)}},
		{historyAt(1200), &orreryv1.EpicTurn{Seq: 2, State: "running", Started: historyAt(1200)}},
		{historyAt(1200), &orreryv1.EpicTrigger{
			Id: "trig-2", Cause: "synthetic cause two", State: "pending", Created: historyAt(1200),
		}},
	}
}

// historyStoreFake is the EpicStoreClient of the History tests. It answers as epicStoreFake does and records each
// GetEpic request.
type historyStoreFake struct {
	*epicStoreFake
	mu    sync.Mutex
	asked []*enginev1.GetEpicRequest
}

func (f *historyStoreFake) GetEpic(
	ctx context.Context,
	req *connect.Request[enginev1.GetEpicRequest],
) (*connect.Response[enginev1.GetEpicResponse], error) {
	f.mu.Lock()
	f.asked = append(f.asked, req.Msg)
	f.mu.Unlock()
	return f.epicStoreFake.GetEpic(ctx, req)
}

// requests returns the GetEpic requests answered so far, in order.
func (f *historyStoreFake) requests() []*enginev1.GetEpicRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*enginev1.GetEpicRequest(nil), f.asked...)
}

// epicsHistory calls History on id, after the cursor after, at limit, and returns the page it answers.
func epicsHistory(
	t *testing.T,
	client orreryv1connect.EpicsClient,
	id, after string,
	limit int32,
) *orreryv1.EpicsHistoryResponse {
	t.Helper()
	resp, err := client.History(context.Background(), connect.NewRequest(&orreryv1.EpicsHistoryRequest{
		Id: id, After: after, Limit: limit,
	}))
	if err != nil {
		t.Fatalf("History %q after %q: %v", id, after, err)
	}
	return resp.Msg
}

// historyItem returns the message the row of entry carries, or nil when the entry carries none.
func historyItem(entry *orreryv1.EpicHistoryEntry) proto.Message {
	switch {
	case entry.GetTurn() != nil:
		return entry.GetTurn()
	case entry.GetTrigger() != nil:
		return entry.GetTrigger()
	case entry.GetStep() != nil:
		return entry.GetStep()
	case entry.GetEvidence() != nil:
		return entry.GetEvidence()
	case entry.GetDecision() != nil:
		return entry.GetDecision()
	case entry.GetTell() != nil:
		return entry.GetTell()
	}
	return nil
}

// requireHistoryEntries checks that entries are want, in order, and that each entry carries a cursor.
func requireHistoryEntries(t *testing.T, entries []*orreryv1.EpicHistoryEntry, want []historyWant) {
	t.Helper()
	if len(entries) != len(want) {
		t.Fatalf("entries = %d, want %d", len(entries), len(want))
	}
	for i, w := range want {
		entry := entries[i]
		if entry.GetTime() != w.time || !proto.Equal(historyItem(entry), w.item) {
			t.Errorf("entry %d = time %d item %v, want time %d item %v",
				i, entry.GetTime(), historyItem(entry), w.time, w.item)
		}
		if entry.GetCursor() == "" {
			t.Errorf("entry %d has no cursor", i)
		}
	}
}

// rawCursor returns the cursor that text encodes as, for the cursors a History call is given that do not parse.
func rawCursor(text string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(text))
}

func TestEpicsHistoryAnswersEveryKindOldestFirst(t *testing.T) {
	t.Parallel()
	stored, steps, runs := historyFixture()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := stepDataServe(t, t.TempDir(), &epicsFake{steps: steps}, store, &evidenceRunsFake{runs: runs})

	got := epicsHistory(t, client, "repoa/shared", "", 0)
	requireHistoryEntries(t, got.GetEntries(), historyFixtureWant())
	if got.GetNext() != "" {
		t.Errorf("next = %q, want empty: all twelve entries fit one page", got.GetNext())
	}
}

func TestEpicsHistoryPagesWalkEveryEntryOnce(t *testing.T) {
	t.Parallel()
	stored, steps, runs := historyFixture()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := stepDataServe(t, t.TempDir(), &epicsFake{steps: steps}, store, &evidenceRunsFake{runs: runs})
	want := historyFixtureWant()

	var entries []*orreryv1.EpicHistoryEntry
	after := ""
	pages := 0
	for {
		pages++
		if pages > len(want) {
			t.Fatalf("history did not end after %d pages and %d entries", pages-1, len(entries))
		}
		page := epicsHistory(t, client, "repoa/shared", after, 2)
		if n := len(page.GetEntries()); n > 2 {
			t.Fatalf("page %d holds %d entries, want at most 2", pages, n)
		}
		entries = append(entries, page.GetEntries()...)
		if page.GetNext() == "" {
			break
		}
		after = page.GetNext()
	}
	requireHistoryEntries(t, entries, want)
	if pages != 6 {
		t.Errorf("pages = %d, want 6 of two entries for twelve entries", pages)
	}
}

func TestEpicsHistoryLimitDefaultsAndCaps(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "shared", `{"outcome":"shipped"}`, epicsNow())
	steps := make([]*enginev1.Step, 0, 260)
	for i := range 260 {
		steps = append(steps, &enginev1.Step{
			Id: fmt.Sprintf("step-%03d", i), Epic: "repoa/shared", ReadOnly: true, Started: historyAt(int64(i)),
		})
	}
	client := stepDataServe(t, root, &epicsFake{steps: steps}, nil, nil)

	first := epicsHistory(t, client, "repoa/shared", "", 0)
	if n := len(first.GetEntries()); n != 50 {
		t.Fatalf("limit 0 entries = %d, want 50", n)
	}
	if last := first.GetEntries()[49].GetStep().GetId(); last != "step-049" {
		t.Errorf("limit 0 last entry = %q, want step-049", last)
	}
	if first.GetNext() == "" {
		t.Fatalf("limit 0 next is empty, want a cursor: 210 entries follow")
	}

	second := epicsHistory(t, client, "repoa/shared", first.GetNext(), 500)
	if n := len(second.GetEntries()); n != 200 {
		t.Fatalf("limit 500 entries = %d, want the cap of 200", n)
	}
	if id := second.GetEntries()[0].GetStep().GetId(); id != "step-050" {
		t.Errorf("second page first entry = %q, want step-050", id)
	}
	if second.GetNext() == "" {
		t.Errorf("limit 500 next is empty, want a cursor: 10 entries follow the cap")
	}
}

func TestEpicsHistoryAsksGetEpicWithHistory(t *testing.T) {
	t.Parallel()
	stored, steps, runs := historyFixture()
	store := &historyStoreFake{epicStoreFake: &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}}
	client := stepDataServe(t, t.TempDir(), &epicsFake{steps: steps}, store, &evidenceRunsFake{runs: runs})

	epicsHistory(t, client, "repoa/shared", "", 0)
	asked := store.requests()
	if len(asked) != 1 || asked[0].GetId() != "repoa/shared" || !asked[0].GetHistory() {
		t.Errorf("GetEpic requests = %v, want one for repoa/shared with history set", asked)
	}
}

func TestEpicsHistoryRecordOnlyEpicAnswersItsSteps(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow())
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/elsewhere", "accepted", epicsAgo(time.Hour)),
	}}
	steps := &epicsFake{steps: []*enginev1.Step{
		{Id: "rec-b", Epic: "repoa/record", End: "done", Started: historyAt(120), Ended: historyAt(180)},
		{Id: "rec-a", Epic: "repoa/record", Started: historyAt(60)},
		{Id: "elsewhere", Epic: "repoa/elsewhere", Started: historyAt(90)},
		{Id: "unlabelled", Started: historyAt(90)},
	}}
	client := stepDataServe(t, root, steps, store, nil)

	got := epicsHistory(t, client, "repoa/record", "", 0)
	requireHistoryEntries(t, got.GetEntries(), []historyWant{
		{historyAt(60), &orreryv1.EpicStep{Id: "rec-a", Role: "implementer", Started: historyAt(60)}},
		{historyAt(120), &orreryv1.EpicStep{
			Id: "rec-b", Role: "implementer", End: "done", Started: historyAt(120), Ended: historyAt(180),
		}},
	})
	if got.GetNext() != "" {
		t.Errorf("next = %q, want empty for two steps", got.GetNext())
	}
}

func TestEpicsHistoryUnknownIDAnswersNotFound(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root := filepath.Join(parent, "records")
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, epicsNow())
	// A head outside the record root that "../x" would reach if the id were joined into a path.
	writeHead(t, root, "..", "x", `{"outcome":"shipped"}`, epicsNow())
	steps := &epicsFake{steps: []*enginev1.Step{{Id: "missing", Epic: "repo/missing", Started: historyAt(0)}}}
	store := &epicStoreFake{
		stored: []*enginev1.StoredEpic{storedEpicOf("repoa/shared", "accepted", epicsAgo(time.Hour))},
	}
	client := stepDataServe(t, root, steps, store, nil)

	for _, id := range []string{"repo/missing", "../x", ""} {
		_, err := client.History(context.Background(), connect.NewRequest(&orreryv1.EpicsHistoryRequest{Id: id}))
		requireConnectError(t, err, connect.CodeNotFound, "epic not found")
	}
}

func TestEpicsHistoryBadCursorAnswersInvalidArgument(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, epicsNow())
	client := stepDataServe(t, root, &epicsFake{}, nil, nil)
	cases := []struct {
		name   string
		cursor string
	}{
		{name: "not base64", cursor: "!!!"},
		{name: "no separators", cursor: rawCursor("garbage")},
		{name: "time not a number", cursor: rawCursor("x|0|0|")},
		{name: "kind out of range", cursor: rawCursor(fmt.Sprintf("0|%d|0|", historyKinds))},
	}
	for _, tc := range cases {
		_, err := client.History(context.Background(), connect.NewRequest(&orreryv1.EpicsHistoryRequest{
			Id: "repo/epic", After: tc.cursor,
		}))
		if err == nil {
			t.Errorf("%s: History answered, want invalid_argument", tc.name)
			continue
		}
		requireConnectError(t, err, connect.CodeInvalidArgument, "epic history cursor does not parse")
	}
}
