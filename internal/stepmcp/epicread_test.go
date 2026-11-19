package stepmcp

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// smallEpic is an invented epic that is paused and parked on a reason of two lines. It has two open questions and one
// answered, tasks of three statuses, a workflow and a report.
func smallEpic() *v1.StoredEpic {
	return &v1.StoredEpic{
		Contract: &v1.EpicContract{
			Id:     testEpicID,
			Title:  "Login with passkeys",
			Intent: "Let members sign in with a passkey.",
		},
		Phase:  "accepted",
		Paused: true,
		Parked: "Waiting for the owner.\nThe planner needs a choice for passkey recovery.",
		Turns: []*v1.EpicTurn{
			{Seq: 1, Step: "step-1", State: "succeeded"},
			{Seq: 2, Step: "step-2", State: "running"},
		},
		Tasks: []*v1.EpicTask{
			{Id: "T1", Turn: 1, Title: "Store the credential", Status: "done"},
			{Id: "T2", Turn: 1, Title: "Add the login form", Status: "done"},
			{Id: "T3", Turn: 2, Title: "Add the recovery flow", Status: "failed"},
			{Id: "T4", Turn: 2, Title: "Add the rollback switch", Status: "open"},
		},
		Decisions: []*v1.EpicDecision{{Seq: 1, Id: "D1", Text: "Build passkey login.", Time: 1700000000}},
		Questions: []*v1.EpicQuestion{
			{Id: "Q1", Turn: 1, Text: "Which recovery path?", State: "open", Label: "recovery-path"},
			{Id: "Q2", Turn: 1, Text: "Keep the device list?", State: "answered", Label: "device-list", Choice: "yes"},
			{Id: "Q3", Turn: 2, Text: "Ship the rollback switch?", State: "open", Label: "rollback-switch"},
		},
		Triggers: []*v1.EpicTrigger{{
			Id: "R1", Cause: "The owner asked.", State: "consumed", Turn: 1, Created: 1700000000,
		}},
		Tells:    []*v1.EpicTell{{Seq: 1, Text: "Recovery is open.", At: 1700000000, Told: true}},
		Workflow: &v1.EpicWorkflow{Name: "epic", Text: "name: epic\n", Sha256: "0f0f"},
		Report:   &v1.EpicReport{Outcome: "shipped", Shipped: []string{"R1"}, Why: "Passkey login shipped."},
	}
}

// largeEpic is an invented epic of 200 tasks, 100 turns, 300 decisions and 50 open questions, with a contract of about
// 40 KB. Its title is longer than the title cap, and its first question's label is longer than the label cap.
func largeEpic() *v1.StoredEpic {
	epic := &v1.StoredEpic{
		Contract: &v1.EpicContract{
			Id:     testEpicID,
			Title:  strings.Repeat("a", 250),
			Intent: strings.Repeat("Members sign in with a passkey and keep the device list. ", 700),
		},
		Phase:    "accepted",
		Workflow: &v1.EpicWorkflow{Name: "epic", Text: strings.Repeat("node: plan\n", 200), Sha256: "0f0f"},
		Report:   &v1.EpicReport{Outcome: "shipped", Shipped: []string{"R1"}, Why: "All of it shipped."},
		Triggers: []*v1.EpicTrigger{{Id: "R1", Cause: "start"}, {Id: "R2", Cause: "wake"}},
		Tells:    []*v1.EpicTell{{Seq: 1, Text: "one"}, {Seq: 2, Text: "two"}, {Seq: 3, Text: "three"}},
	}
	for i := range 200 {
		status := "open"
		switch {
		case i < 150:
			status = "done"
		case i < 160:
			status = "failed"
		}
		epic.Tasks = append(epic.Tasks, &v1.EpicTask{
			Id: fmt.Sprintf("T%d", i+1), Turn: int64(i%100 + 1), Title: "A task.", Status: status,
		})
	}
	for i := range 100 {
		epic.Turns = append(epic.Turns, &v1.EpicTurn{Seq: int64(i + 1), Step: "step", State: "succeeded"})
	}
	for i := range 300 {
		epic.Decisions = append(epic.Decisions, &v1.EpicDecision{
			Seq: int64(i + 1), Id: fmt.Sprintf("D%d", i+1), Text: "A decision.", Time: 1700000000,
		})
	}
	for i := range 50 {
		label := fmt.Sprintf("question-%d", i+1)
		if i == 0 {
			label = strings.Repeat("l", 60)
		}
		epic.Questions = append(epic.Questions, &v1.EpicQuestion{
			Id: fmt.Sprintf("Q%d", i+1), Turn: 1, Text: "A question?", State: "open", Label: label,
		})
	}
	return epic
}

// indentedSize is the byte length of msg's indented protobuf JSON, the size of a single-message part.
func indentedSize(t *testing.T, msg proto.Message) int {
	t.Helper()
	text, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal %s: %v", msg.ProtoReflect().Descriptor().FullName(), err)
	}
	return len(text)
}

// compactSize is the sum of the byte lengths of each item's compact protobuf JSON, the size of a list part.
func compactSize[T proto.Message](t *testing.T, items []T) int {
	t.Helper()
	total := 0
	for _, item := range items {
		total += len(marshalProto(t, item))
	}
	return total
}

// partLineWant is the summary line a part with items items and bytes bytes is expected to have.
func partLineWant(name string, items, bytes int) string {
	return fmt.Sprintf("  %s: %d items, %d bytes", name, items, bytes)
}

func TestEpicByPartSummaryNamesEachLineOfASmallEpic(t *testing.T) {
	epic := smallEpic()
	p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: epic}})

	outcome := p.tool("epic_get", `{"id":"`+testEpicID+`"}`)

	if outcome.isError {
		t.Fatalf("epic_get returned an error: %s", outcome.text)
	}
	want := strings.Join([]string{
		"epic demo-0a1b2c3/epic-login: Login with passkeys",
		"phase: accepted",
		"paused: yes",
		"parked: Waiting for the owner.",
		"open questions: 2",
		"  Q1 recovery-path",
		"  Q3 rollback-switch",
		"tasks: 4 (done 2, failed 1, open 1)",
		"parts:",
		partLineWant("contract", 1, indentedSize(t, epic.GetContract())),
		partLineWant("workflow", 1, len(epic.GetWorkflow().GetText())),
		partLineWant("tasks", 4, compactSize(t, epic.GetTasks())),
		partLineWant("turns", 2, compactSize(t, epic.GetTurns())),
		partLineWant("decisions", 1, compactSize(t, epic.GetDecisions())),
		partLineWant("questions", 3, compactSize(t, epic.GetQuestions())),
		partLineWant("history", 2, compactSize(t, epic.GetTriggers())+compactSize(t, epic.GetTells())),
		partLineWant("report", 1, indentedSize(t, epic.GetReport())),
	}, "\n")
	if outcome.text != want {
		t.Fatalf("epic_get text =\n%s\nwant\n%s", outcome.text, want)
	}
}

func TestEpicByPartSummaryOfALargeEpicStaysWithinFourKilobytes(t *testing.T) {
	p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: largeEpic()}})

	outcome := p.tool("epic_get", `{"id":"`+testEpicID+`"}`)

	if outcome.isError {
		t.Fatalf("epic_get returned an error: %s", outcome.text)
	}
	if n := len(outcome.text); n > 4000 {
		t.Fatalf("epic_get text is %d bytes, want at most 4000", n)
	}
	lines := strings.Split(outcome.text, "\n")
	for _, want := range []string{
		"epic demo-0a1b2c3/epic-login: " + strings.Repeat("a", 200) + "…",
		"open questions: 50",
		"  Q1 " + strings.Repeat("l", 40) + "…",
		"  and 10 more",
		"tasks: 200 (done 150, failed 10, open 40)",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("epic_get text has no line %q", want)
		}
	}
	questionLines := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "  Q") {
			questionLines++
		}
	}
	if questionLines != 40 {
		t.Errorf("epic_get text has %d question lines, want 40", questionLines)
	}

	parts := []struct {
		name  string
		items int
	}{
		{"contract", 1}, {"workflow", 1}, {"tasks", 200}, {"turns", 100},
		{"decisions", 300}, {"questions", 50}, {"history", 5}, {"report", 1},
	}
	at := slices.Index(lines, "parts:")
	if at < 0 || len(lines) != at+1+len(parts) {
		t.Fatalf("epic_get text has %d lines after its header, want %d part lines", len(lines)-at-1, len(parts))
	}
	for i, part := range parts {
		prefix := fmt.Sprintf("  %s: %d items, ", part.name, part.items)
		if !strings.HasPrefix(lines[at+1+i], prefix) {
			t.Errorf("part line %q, want it to start %q", lines[at+1+i], prefix)
		}
	}
}

func TestEpicByPartSummaryCutsAHugeIDOnARuneBoundary(t *testing.T) {
	// Each é is two bytes, so a cut that falls inside one is a cut on a byte that is not a rune boundary.
	epic := &v1.StoredEpic{
		Contract: &v1.EpicContract{Id: "demo-0a1b2c3/" + strings.Repeat("é", 3000), Title: "Login"},
		Phase:    "accepted",
	}
	p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: epic}})

	outcome := p.tool("epic_get", `{"id":"`+testEpicID+`"}`)

	if outcome.isError {
		t.Fatalf("epic_get returned an error: %s", outcome.text)
	}
	if n := len(outcome.text); n > 4000 || n < 3990 {
		t.Fatalf("epic_get text is %d bytes, want a cut of 3990 to 4000 bytes", n)
	}
	if !utf8.ValidString(outcome.text) {
		t.Fatalf("epic_get text is not valid UTF-8: %q", outcome.text)
	}
	if !strings.HasPrefix(outcome.text, "epic demo-0a1b2c3/é") || !strings.HasSuffix(outcome.text, "…") {
		t.Fatalf("epic_get text starts %q and ends %q, want the id's start and an ellipsis",
			outcome.text[:min(len(outcome.text), 40)], outcome.text[max(0, len(outcome.text)-10):])
	}
}
