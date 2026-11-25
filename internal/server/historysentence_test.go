package server

import (
	"slices"
	"testing"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// sentenceSentences returns the sentence of each entry, in order.
func sentenceSentences(entries []*orreryv1.EpicHistoryEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.GetSentence())
	}
	return out
}

// requireSentences checks that the entries read as want, in order.
func requireSentences(t *testing.T, entries []*orreryv1.EpicHistoryEntry, want []string) {
	t.Helper()
	if got := sentenceSentences(entries); !slices.Equal(got, want) {
		t.Errorf("sentences = %q, want %q", got, want)
	}
}

// runIDs returns the id of each run, in order.
func runIDs(runs []*orreryv1.EvidenceRun) []string {
	out := make([]string, 0, len(runs))
	for _, run := range runs {
		out = append(out, run.GetId())
	}
	return out
}

// sentenceFixture returns an epic that holds one entry of every kind, each with the stored fields its sentence reads,
// with the labeled steps and the run that go with them. Its times are offsets from historyAt.
func sentenceFixture() (*enginev1.StoredEpic, []*enginev1.Step, []*enginev1.EvidenceRun) {
	stored := storedEpicOf("repoa/sentences", "accepted", historyAt(3600))
	stored.Turns = []*enginev1.EpicTurn{
		{Seq: 1, State: "succeeded", Outcome: "work", Started: historyAt(0), Ended: historyAt(10)},
		{Seq: 2, State: "failed", Reason: "tool stuck", Started: historyAt(100), Ended: historyAt(150)},
		{Seq: 3, State: "running", Started: historyAt(200)},
	}
	stored.Triggers = []*enginev1.EpicTrigger{
		{Id: "trig-1", Cause: "owner asked", State: "consumed", Turn: 1, Created: historyAt(0)},
	}
	stored.Decisions = []*enginev1.EpicDecision{
		{Seq: 1, Id: "d1", Text: "use the parser", Time: historyAt(300), By: "owner"},
	}
	stored.Tells = []*enginev1.EpicTell{
		{Seq: 1, Text: "the parser is in", At: historyAt(400), Told: true},
		{Seq: 2, Text: "review pending", At: historyAt(500)},
	}
	stored.Actions = []*enginev1.EpicAction{
		{Seq: 1, Kind: "move", Time: historyAt(600), By: "owner", From: "sess-a", To: "sess-b"},
		{Seq: 2, Kind: "park", Time: historyAt(700), By: "orreryd"},
	}
	steps := []*enginev1.Step{
		{
			Id: "impl", Epic: "repoa/sentences", Purpose: "Write the parser", Doing: "editing lexer.go",
			Started: historyAt(50),
		},
		{
			Id: "one-a-step", Epic: "repoa/sentences", Task: "one-a", End: "done", Summary: "all green",
			Started: historyAt(650), Ended: historyAt(690),
		},
	}
	runs := []*enginev1.EvidenceRun{{
		Id: "run-1", Epic: "repoa/sentences", Item: "R1", Phase: "red", Source: "captured", State: "ended", Exit: 1,
		Verdict: "fails", Passed: true, Created: historyAt(800), Ended: historyAt(810),
	}}
	return stored, steps, runs
}

func TestHistorySentenceEveryKindReadsAsASentence(t *testing.T) {
	t.Parallel()
	stored, steps, runs := sentenceFixture()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := stepDataServe(t, t.TempDir(), &epicsFake{steps: steps}, store, &evidenceRunsFake{runs: runs})

	got := epicsHistory(t, client, "repoa/sentences", "", 0).GetEntries()
	requireSentences(t, got, []string{
		"Planner turn 1 succeeded: work",
		"Trigger trig-1: owner asked",
		"Write the parser running: editing lexer.go",
		"Planner turn 2 failed: tool stuck",
		"Planner turn 3 running",
		"Decision by owner: use the parser",
		"Told the owner: the parser is in",
		"To tell the owner: review pending",
		"Owner moved the epic from session sess-a to session sess-b",
		"One-a ended done: all green",
		"Orreryd did park",
		"Red evidence: R1 failed at base, as expected",
	})
}

func TestHistorySentenceGroupsNodeRunsOfOnePhase(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/grouped", "accepted", historyAt(3600))
	stored.Nodes = []*enginev1.EpicNode{{
		Path: "evidence", Node: "evidence", Kind: "evidence", Runs: []string{"red-2", "red-1", "green-1"},
	}}
	runs := []*enginev1.EvidenceRun{
		{Id: "red-2", Item: "R2", Phase: "red", Verdict: "holds", Created: historyAt(90)},
		{Id: "red-1", Item: "R1", Phase: "red", Verdict: "fails", Created: historyAt(60)},
		{Id: "green-1", Item: "R1", Phase: "green", Verdict: "holds", Passed: true, Created: historyAt(120)},
		{Id: "loose", Item: "R3", Phase: "red", Created: historyAt(200)},
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := stepDataServe(t, t.TempDir(), &epicsFake{}, store, &evidenceRunsFake{runs: runs})

	got := epicsHistory(t, client, "repoa/grouped", "", 0).GetEntries()
	requireSentences(t, got, []string{
		"Red evidence: R1 failed at base, as expected; R2 holds at base",
		"Green evidence: R1 holds",
		"Red evidence: R3 not judged",
	})
	group := got[0]
	if group.GetTime() != historyAt(60) {
		t.Errorf("group time = %d, want the earliest run's %d", group.GetTime(), historyAt(60))
	}
	if ids := runIDs(group.GetRuns()); !slices.Equal(ids, []string{"red-1", "red-2"}) {
		t.Errorf("group runs = %q, want red-1 then red-2", ids)
	}
	if id := group.GetEvidence().GetId(); id != "red-1" {
		t.Errorf("group evidence = %q, want the earliest run red-1", id)
	}

	// The group's line pages like any entry: a page of one holds it, and the next cursor reads the green run's line.
	first := epicsHistory(t, client, "repoa/grouped", "", 1)
	requireSentences(t, first.GetEntries(), []string{"Red evidence: R1 failed at base, as expected; R2 holds at base"})
	if first.GetNext() == "" {
		t.Fatalf("group page next is empty, want the green run's cursor")
	}
	second := epicsHistory(t, client, "repoa/grouped", first.GetNext(), 1)
	requireSentences(t, second.GetEntries(), []string{"Green evidence: R1 holds"})
}

func TestHistorySentenceFoldsJudgeStepsRedAndGreen(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/judged", "accepted", historyAt(3600))
	steps := []*enginev1.Step{
		{Id: "impl", Epic: "repoa/judged", Purpose: "Write it", Doing: "testing", Started: historyAt(30)},
		{
			Id: "judge-red", Epic: "repoa/judged", ReadOnly: true, End: "done",
			Started: historyAt(60), Ended: historyAt(70),
		},
		{
			Id: "judge-green", Epic: "repoa/judged", ReadOnly: true, End: "done",
			Started: historyAt(120), Ended: historyAt(130),
		},
	}
	runs := []*enginev1.EvidenceRun{
		{
			Id: "red-1", Item: "R1", Phase: "red", Verdict: "holds", Created: historyAt(60),
			JudgeStep: "judge-red",
		},
		{
			Id: "green-1", Item: "R2", Phase: "green", Verdict: "holds", Exit: 0, Passed: true,
			Created: historyAt(120), JudgeStep: "judge-green",
		},
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := stepDataServe(t, t.TempDir(), &epicsFake{steps: steps}, store, &evidenceRunsFake{runs: runs})

	got := epicsHistory(t, client, "repoa/judged", "", 0).GetEntries()
	requireSentences(t, got, []string{
		"Write it running: testing",
		"Red evidence: R1 holds at base",
		"Green evidence: R2 holds",
	})
	if id := got[1].GetEvidence().GetJudgeStep(); id != "judge-red" {
		t.Errorf("red run judge step = %q, want judge-red", id)
	}
	if id := got[2].GetEvidence().GetJudgeStep(); id != "judge-green" {
		t.Errorf("green run judge step = %q, want judge-green", id)
	}
}

func TestHistorySentenceRedWordings(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/wordings", "accepted", historyAt(3600))
	steps := []*enginev1.Step{
		{Id: "judge-r2", Epic: "repoa/wordings", ReadOnly: true, End: "done", Started: historyAt(150)},
	}
	runs := []*enginev1.EvidenceRun{
		{Id: "red-1", Item: "R1", Phase: "red", Verdict: "fails", Passed: true, Created: historyAt(60)},
		{
			Id: "red-2", Item: "R2", Phase: "red", State: "judging", Created: historyAt(120),
			JudgeStep: "judge-r2",
		},
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := stepDataServe(t, t.TempDir(), &epicsFake{steps: steps}, store, &evidenceRunsFake{runs: runs})

	got := epicsHistory(t, client, "repoa/wordings", "", 0).GetEntries()
	requireSentences(t, got, []string{
		"Red evidence: R1 failed at base, as expected",
		"Red evidence: R2 not judged",
	})
	if id := got[1].GetEvidence().GetJudgeStep(); id != "judge-r2" {
		t.Errorf("unjudged run judge step = %q, want judge-r2 named on its entry", id)
	}
}

func TestHistorySentenceStepPurposeFallbacks(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/labels", "accepted", historyAt(3600))
	stored.Turns = []*enginev1.EpicTurn{{Seq: 1, Step: "plan-1", State: "running", Started: historyAt(35)}}
	steps := []*enginev1.Step{
		{
			Id: "purposed", Epic: "repoa/labels", Purpose: "Write the parser", Task: "one-a", Doing: "editing lexer.go",
			Started: historyAt(10),
		},
		{
			Id: "tasked", Epic: "repoa/labels", Task: "one-b", End: "done", Summary: "all green",
			Started: historyAt(20), Ended: historyAt(30),
		},
		{Id: "plan-1", Epic: "repoa/labels", ReadOnly: true, Started: historyAt(30)},
		{Id: "orphan", Epic: "repoa/labels", ReadOnly: true, Started: historyAt(40)},
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := stepDataServe(t, t.TempDir(), &epicsFake{steps: steps}, store, &evidenceRunsFake{})

	got := epicsHistory(t, client, "repoa/labels", "", 0).GetEntries()
	requireSentences(t, got, []string{
		"Write the parser running: editing lexer.go",
		"One-b ended done: all green",
		"Planner running",
		"Planner turn 1 running",
		"Step orphan running",
	})
	if purpose := got[0].GetStep().GetPurpose(); purpose != "Write the parser" {
		t.Errorf("purposed step purpose = %q, want the stored purpose", purpose)
	}
	if purpose := got[1].GetStep().GetPurpose(); purpose != "" {
		t.Errorf("tasked step purpose = %q, want empty for a step with none stored", purpose)
	}
}

func TestHistorySentenceGetNewestIsTheNewestSentence(t *testing.T) {
	t.Parallel()
	stored, steps, runs := sentenceFixture()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := stepDataServe(t, t.TempDir(), &epicsFake{steps: steps}, store, &evidenceRunsFake{runs: runs})

	got := epicsGet(t, client, "repoa/sentences")
	const want = "Red evidence: R1 failed at base, as expected"
	if got.GetNewest() != want {
		t.Errorf("stored epic newest = %q, want %q", got.GetNewest(), want)
	}
}
