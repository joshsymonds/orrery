package server

import (
	"testing"

	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// actionLogFixture returns one orreryd epic's stored history: a decision and a tell, and two actions at interleaved
// times. The first action ties with the decision at 300, and the second falls between the decision and the tell.
func actionLogFixture() *enginev1.StoredEpic {
	stored := storedEpicOf("repoa/shared", "accepted", historyAt(3600))
	stored.Decisions = []*enginev1.EpicDecision{{Seq: 1, Id: "d1", Text: "synthetic decision", Time: historyAt(300)}}
	stored.Tells = []*enginev1.EpicTell{{Seq: 1, Text: "synthetic tell", At: historyAt(900), Told: true}}
	stored.Actions = []*enginev1.EpicAction{
		{
			Seq: 1, Kind: "move", Time: historyAt(300), By: "synthetic maker one", Session: "sess-a",
			From: "sess-x", To: "sess-y",
		},
		{
			Seq: 2, Kind: "move", Time: historyAt(600), By: "synthetic maker two", Session: "sess-b",
			From: "sess-z", To: "sess-w",
		},
	}
	return stored
}

// actionLogWant returns the four entries actionLogFixture's epic answers, oldest first, with the action entries
// carrying their EpicAction.
func actionLogWant() []historyWant {
	return []historyWant{
		{historyAt(300), &orreryv1.EpicDecision{Seq: 1, Id: "d1", Text: "synthetic decision", Time: historyAt(300)}},
		{historyAt(300), &orreryv1.EpicAction{
			Seq: 1, Kind: "move", Time: historyAt(300), By: "synthetic maker one", Session: "sess-a",
			From: "sess-x", To: "sess-y",
		}},
		{historyAt(600), &orreryv1.EpicAction{
			Seq: 2, Kind: "move", Time: historyAt(600), By: "synthetic maker two", Session: "sess-b",
			From: "sess-z", To: "sess-w",
		}},
		{historyAt(900), &orreryv1.EpicTell{Seq: 1, Text: "synthetic tell", At: historyAt(900), Told: true}},
	}
}

// actionLogItem returns the message the row of entry carries, including an action row.
func actionLogItem(entry *orreryv1.EpicHistoryEntry) proto.Message {
	if action := entry.GetAction(); action != nil {
		return action
	}
	return historyItem(entry)
}

// requireActionLogEntries checks that entries are want, in order, and that each entry carries a cursor.
func requireActionLogEntries(t *testing.T, entries []*orreryv1.EpicHistoryEntry, want []historyWant) {
	t.Helper()
	if len(entries) != len(want) {
		t.Fatalf("entries = %d, want %d", len(entries), len(want))
	}
	for i, w := range want {
		entry := entries[i]
		if entry.GetTime() != w.time || !proto.Equal(actionLogItem(entry), w.item) {
			t.Errorf("entry %d = time %d item %v, want time %d item %v",
				i, entry.GetTime(), actionLogItem(entry), w.time, w.item)
		}
		if entry.GetCursor() == "" {
			t.Errorf("entry %d has no cursor", i)
		}
	}
}

func TestEpicActionLogHistoryMergesActionsInTimeOrder(t *testing.T) {
	t.Parallel()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{actionLogFixture()}}
	client := stepDataServe(t, &epicsFake{}, store, &evidenceRunsFake{})

	got := epicsHistory(t, client, "repoa/shared", "", 0)
	requireActionLogEntries(t, got.GetEntries(), actionLogWant())
	if got.GetNext() != "" {
		t.Errorf("next = %q, want empty: all four entries fit one page", got.GetNext())
	}
}

func TestEpicActionLogHistoryPageContinuesAfterAction(t *testing.T) {
	t.Parallel()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{actionLogFixture()}}
	client := stepDataServe(t, &epicsFake{}, store, &evidenceRunsFake{})
	want := actionLogWant()

	first := epicsHistory(t, client, "repoa/shared", "", 2)
	requireActionLogEntries(t, first.GetEntries(), want[:2])
	if first.GetNext() == "" {
		t.Fatalf("next is empty after the first action, want a cursor: two entries follow")
	}

	second := epicsHistory(t, client, "repoa/shared", first.GetNext(), 2)
	requireActionLogEntries(t, second.GetEntries(), want[2:])
	if second.GetNext() != "" {
		t.Errorf("next = %q, want empty: no entry follows the tell", second.GetNext())
	}
}
