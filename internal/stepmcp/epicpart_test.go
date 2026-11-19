package stepmcp

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// readPart calls epic_get with arguments and returns its text, failing the test on a tool error.
func readPart(t *testing.T, p *peer, arguments string) string {
	t.Helper()
	outcome := p.tool("epic_get", arguments)
	if outcome.isError {
		t.Fatalf("epic_get %s returned an error: %s", arguments, outcome.text)
	}
	return outcome.text
}

// checkPage fails unless text, split at its newlines, is the lines want, in order.
func checkPage(t *testing.T, text string, want ...string) {
	t.Helper()
	if got := strings.Split(text, "\n"); !slices.Equal(got, want) {
		t.Fatalf("epic_get text =\n%s\nwant\n%s", text, strings.Join(want, "\n"))
	}
}

// checkText fails unless got is want. It names the rune counts, since a text of thousands of runes does not print well.
func checkText(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("epic_get text is %d runes, want %d runes", utf8.RuneCountInString(got), utf8.RuneCountInString(want))
	}
}

// listEpic is an invented epic with 120 tasks, 3 turns, 3 decisions, 3 questions, 2 triggers and 2 tells. Its third
// decision's text is longer than the item text cap, and its first line is cut to it.
func listEpic() *v1.StoredEpic {
	epic := &v1.StoredEpic{
		Contract: &v1.EpicContract{Id: testEpicID, Title: "Login with passkeys"},
		Phase:    "accepted",
		Turns: []*v1.EpicTurn{
			{Seq: 1, State: "succeeded", Outcome: "built", Outline: "Outline 1.\nThe rest of it."},
			{Seq: 2, State: "succeeded", Outcome: "built", Reason: "Reason 2."},
			{Seq: 3, State: "succeeded"},
		},
		Decisions: []*v1.EpicDecision{
			{Seq: 1, Id: "D1", Text: "Build passkey login."},
			{Seq: 2, Id: "D2", Text: "Keep the device list.", Question: "Q1"},
			{Seq: 3, Id: "D3", Text: strings.Repeat("é", 150) + "\nThe second line."},
		},
		Questions: []*v1.EpicQuestion{
			{Id: "Q1", State: "open", Label: "recovery-path", Text: "Which recovery path?"},
			{Id: "Q2", State: "answered", Text: "Keep the device list?"},
			{Id: "Q3", State: "open", Label: "rollback-switch"},
		},
		Triggers: []*v1.EpicTrigger{
			{Id: "R1", State: "consumed", Cause: "The owner asked."},
			{Id: "R2", State: "pending"},
		},
		Tells: []*v1.EpicTell{
			{Seq: 1, Text: "Recovery is open.", Told: true},
			{Seq: 2, Text: "Ask again."},
		},
	}
	for i := range 120 {
		epic.Tasks = append(epic.Tasks, &v1.EpicTask{
			Id: fmt.Sprintf("T%d", i+1), Status: "open", Title: fmt.Sprintf("Task %d.", i+1),
		})
	}
	return epic
}

func TestEpicByPartListsOneLinePerItem(t *testing.T) {
	t.Run("tasks", func(t *testing.T) {
		p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: listEpic()}})

		want := make([]string, 0, 52)
		want = append(want, "tasks: 50 of 120 from offset 0")
		for i := range 50 {
			want = append(want, fmt.Sprintf("T%d open: Task %d.", i+1, i+1))
		}
		want = append(want, "offset 50 continues")
		checkPage(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"tasks"}`), want...)

		// A limit of 0 is the default limit, so it gives the same page.
		checkPage(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"tasks","limit":0}`), want...)

		want = []string{"tasks: 20 of 120 from offset 100"}
		for i := 100; i < 120; i++ {
			want = append(want, fmt.Sprintf("T%d open: Task %d.", i+1, i+1))
		}
		checkPage(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"tasks","offset":100,"limit":50}`), want...)

		// The limit counts items: five from offset 10, and the offset that continues.
		want = []string{"tasks: 5 of 120 from offset 10"}
		for i := 10; i < 15; i++ {
			want = append(want, fmt.Sprintf("T%d open: Task %d.", i+1, i+1))
		}
		want = append(want, "offset 15 continues")
		checkPage(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"tasks","offset":10,"limit":5}`), want...)

		// An offset at the end of the list gives the header alone, with no items.
		checkPage(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"tasks","offset":120}`),
			"tasks: 0 of 120 from offset 120")
	})

	t.Run("turns", func(t *testing.T) {
		p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: listEpic()}})

		checkPage(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"turns"}`),
			"turns: 3 of 3 from offset 0",
			"turn 1 succeeded built: Outline 1.",
			"turn 2 succeeded built: Reason 2.",
			"turn 3 succeeded",
		)
	})

	t.Run("decisions", func(t *testing.T) {
		p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: listEpic()}})

		checkPage(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"decisions"}`),
			"decisions: 3 of 3 from offset 0",
			"D1: Build passkey login.",
			"D2 answers Q1: Keep the device list.",
			"D3: "+strings.Repeat("é", 120)+"…",
		)
	})

	t.Run("questions", func(t *testing.T) {
		p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: listEpic()}})

		checkPage(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"questions"}`),
			"questions: 3 of 3 from offset 0",
			"Q1 open recovery-path: Which recovery path?",
			"Q2 answered: Keep the device list?",
			"Q3 open rollback-switch",
		)
	})

	t.Run("history", func(t *testing.T) {
		p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: listEpic()}})

		checkPage(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"history"}`),
			"history: 4 of 4 from offset 0",
			"trigger R1 consumed: The owner asked.",
			"trigger R2 pending",
			"tell 1 told: Recovery is open.",
			"tell 2 untold: Ask again.",
		)
	})
}

func TestEpicByPartReadsOneTaskAndOneQuestionWhole(t *testing.T) {
	task := &v1.EpicTask{
		Id: "T3", Turn: 2, Title: "Add the recovery flow", Deliverable: "The recovery flow sends a code by mail.",
		Files: []string{"auth/recovery.go"}, Requirements: []string{"R2"}, Status: "open", Gate: "unit",
	}
	// The question's text is 5000 runes, and its 30 short choices keep its JSON under the default limit.
	question := &v1.EpicQuestion{
		Id: "Q2", Turn: 1, Text: strings.Repeat("q", 5000), State: "open", Label: "passkey-recovery",
	}
	for i := range 30 {
		question.Choices = append(question.Choices, &v1.EpicQuestionChoice{
			Id: fmt.Sprintf("c%d", i+1), Does: "Does one thing.",
		})
	}
	fake := &fakeEpics{getReply: &v1.GetEpicResponse{Epic: &v1.StoredEpic{
		Tasks:     []*v1.EpicTask{task},
		Questions: []*v1.EpicQuestion{question},
	}}}
	p := newPeer(t, nil, fake)

	var gotTask v1.EpicTask
	unmarshalResult(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"task","task":"T3"}`), &gotTask)
	if !proto.Equal(&gotTask, task) {
		t.Fatalf("part task = %s, want %s", marshalProto(t, &gotTask), marshalProto(t, task))
	}

	// A limit pages the item's JSON as runes, and names the offset that continues.
	taskJSON, err := indentedJSON(task)
	if err != nil {
		t.Fatalf("indentedJSON: %v", err)
	}
	taskRunes := []rune(taskJSON)
	checkText(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"task","task":"T3","limit":10}`),
		string(taskRunes[:10])+fmt.Sprintf("\n… cut at rune 10 of %d; offset 10 continues", len(taskRunes)))

	var gotQuestion v1.EpicQuestion
	unmarshalResult(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"question","question":"Q2"}`), &gotQuestion)
	if n := utf8.RuneCountInString(gotQuestion.GetText()); n != 5000 {
		t.Fatalf("part question text is %d runes, want 5000", n)
	}
	if n := len(gotQuestion.GetChoices()); n != 30 {
		t.Fatalf("part question has %d choices, want 30", n)
	}
	if !proto.Equal(&gotQuestion, question) {
		t.Fatalf("part question = %s, want %s", marshalProto(t, &gotQuestion), marshalProto(t, question))
	}

	gets := fake.calls().gets
	if len(gets) != 3 {
		t.Fatalf("GetEpic calls = %d, want one for each read", len(gets))
	}
	for _, get := range gets {
		if get.GetId() != testEpicID || !get.GetHistory() {
			t.Fatalf("GetEpic request = %s, want the test epic with history", marshalProto(t, get))
		}
	}
}

func TestEpicByPartCutsTextAtLimit(t *testing.T) {
	// lastLine returns the last line of text, the line that names the offset a cut continues from.
	lastLine := func(text string) string { return text[strings.LastIndex(text, "\n")+1:] }

	t.Run("contract", func(t *testing.T) {
		// Each é is two bytes, so a limit counted in bytes would cut the text at a different rune than a limit of runes.
		contract := &v1.EpicContract{Id: testEpicID, Title: "Login with passkeys", Intent: strings.Repeat("é", 9000)}
		full, err := contractText(contract)
		if err != nil {
			t.Fatalf("contractText: %v", err)
		}
		runes := []rune(full)
		total := len(runes)
		if total <= 8000 {
			t.Fatalf("contract text is %d runes, want more than 8000", total)
		}
		p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: &v1.StoredEpic{Contract: contract}}})

		checkText(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"contract"}`),
			string(runes[:8000])+fmt.Sprintf("\n… cut at rune 8000 of %d; offset 8000 continues", total))
		checkText(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"contract","offset":8000}`), string(runes[8000:]))
		checkText(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"contract","limit":100}`),
			string(runes[:100])+fmt.Sprintf("\n… cut at rune 100 of %d; offset 100 continues", total))
		// A limit cuts a page from an offset within the text, and the page names the offset that continues.
		checkText(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"contract","offset":8000,"limit":100}`),
			string(runes[8000:8100])+fmt.Sprintf("\n… cut at rune 8100 of %d; offset 8100 continues", total))
		// An offset at the end of the text names that the text has nothing from there.
		checkText(t, readPart(t, p, fmt.Sprintf(`{"id":"%s","part":"contract","offset":%d}`, testEpicID, total)),
			fmt.Sprintf("contract: no text from offset %d of %d runes", total, total))
	})

	t.Run("workflow", func(t *testing.T) {
		workflow := &v1.EpicWorkflow{Name: "epic", Text: "name: epic\nnode: plan\n", Sha256: "0f0f"}
		p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: &v1.StoredEpic{Workflow: workflow}}})

		checkText(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"workflow"}`), workflow.GetText())

		// Each é is two bytes, so a limit counted in bytes would cut the text at a different rune than a limit of runes.
		long := &v1.EpicWorkflow{Name: "epic", Text: strings.Repeat("é", 9000), Sha256: "0f0f"}
		runes := []rune(long.GetText())
		total := len(runes)
		cut := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: &v1.StoredEpic{Workflow: long}}})

		got := readPart(t, cut, `{"id":"`+testEpicID+`","part":"workflow"}`)
		checkText(t, got, string(runes[:8000])+fmt.Sprintf("\n… cut at rune 8000 of %d; offset 8000 continues", total))
		t.Logf("workflow: %s", lastLine(got))
		checkText(t, readPart(t, cut, `{"id":"`+testEpicID+`","part":"workflow","offset":8000}`), string(runes[8000:]))
		got = readPart(t, cut, `{"id":"`+testEpicID+`","part":"workflow","limit":100}`)
		checkText(t, got, string(runes[:100])+fmt.Sprintf("\n… cut at rune 100 of %d; offset 100 continues", total))
		t.Logf("workflow: %s", lastLine(got))
		got = readPart(t, cut, `{"id":"`+testEpicID+`","part":"workflow","offset":8000,"limit":100}`)
		checkText(
			t,
			got,
			string(runes[8000:8100])+fmt.Sprintf("\n… cut at rune 8100 of %d; offset 8100 continues", total),
		)
		t.Logf("workflow: %s", lastLine(got))

		unset := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: &v1.StoredEpic{}}})
		checkPage(t, readPart(t, unset, `{"id":"`+testEpicID+`","part":"workflow"}`), "workflow: none")
	})

	t.Run("report", func(t *testing.T) {
		report := &v1.EpicReport{Outcome: "shipped", Shipped: []string{"R1"}, Why: "Passkey login shipped."}
		want, err := reportText(report)
		if err != nil {
			t.Fatalf("reportText: %v", err)
		}
		p := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: &v1.StoredEpic{Report: report}}})

		checkText(t, readPart(t, p, `{"id":"`+testEpicID+`","part":"report"}`), want)

		long := &v1.EpicReport{Outcome: "shipped", Shipped: []string{"R1"}, Why: strings.Repeat("é", 9000)}
		full, err := reportText(long)
		if err != nil {
			t.Fatalf("reportText: %v", err)
		}
		runes := []rune(full)
		total := len(runes)
		if total <= 8000 {
			t.Fatalf("report text is %d runes, want more than 8000", total)
		}
		cut := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: &v1.StoredEpic{Report: long}}})

		got := readPart(t, cut, `{"id":"`+testEpicID+`","part":"report"}`)
		checkText(t, got, string(runes[:8000])+fmt.Sprintf("\n… cut at rune 8000 of %d; offset 8000 continues", total))
		t.Logf("report: %s", lastLine(got))
		checkText(t, readPart(t, cut, `{"id":"`+testEpicID+`","part":"report","offset":8000}`), string(runes[8000:]))
		got = readPart(t, cut, `{"id":"`+testEpicID+`","part":"report","limit":100}`)
		checkText(t, got, string(runes[:100])+fmt.Sprintf("\n… cut at rune 100 of %d; offset 100 continues", total))
		t.Logf("report: %s", lastLine(got))
		got = readPart(t, cut, `{"id":"`+testEpicID+`","part":"report","offset":8000,"limit":100}`)
		checkText(
			t,
			got,
			string(runes[8000:8100])+fmt.Sprintf("\n… cut at rune 8100 of %d; offset 8100 continues", total),
		)
		t.Logf("report: %s", lastLine(got))

		unset := newPeer(t, nil, &fakeEpics{getReply: &v1.GetEpicResponse{Epic: &v1.StoredEpic{}}})
		checkPage(t, readPart(t, unset, `{"id":"`+testEpicID+`","part":"report"}`), "report: none")
	})
}

func TestEpicByPartRefusesBadArguments(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{
			name: "unknown part",
			args: `{"id":"` + testEpicID + `","part":"bogus"}`,
			want: `part: unknown part "bogus", want one of ` + epicPartNames,
		},
		{
			name: "part task without task",
			args: `{"id":"` + testEpicID + `","part":"task"}`,
			want: "part task needs task, such as T3",
		},
		{
			name: "part question without question",
			args: `{"id":"` + testEpicID + `","part":"question"}`,
			want: "part question needs question, such as Q2",
		},
		{
			name: "task with part tasks",
			args: `{"id":"` + testEpicID + `","part":"tasks","task":"T1"}`,
			want: "task needs part task, not part tasks",
		},
		{
			name: "question with part questions",
			args: `{"id":"` + testEpicID + `","part":"questions","question":"Q1"}`,
			want: "question needs part question, not part questions",
		},
		{
			name: "question with part task",
			args: `{"id":"` + testEpicID + `","part":"task","task":"T1","question":"Q1"}`,
			want: "question needs part question, not part task",
		},
		{
			name: "task with part question",
			args: `{"id":"` + testEpicID + `","part":"question","question":"Q1","task":"T1"}`,
			want: "task needs part task, not part question",
		},
		{
			name: "task with no part",
			args: `{"id":"` + testEpicID + `","task":"T1"}`,
			want: "task needs part task",
		},
		{
			name: "question with no part",
			args: `{"id":"` + testEpicID + `","question":"Q1"}`,
			want: "question needs part question",
		},
		{
			name: "offset with no part",
			args: `{"id":"` + testEpicID + `","offset":5}`,
			want: "offset needs a part",
		},
		{
			name: "limit with no part",
			args: `{"id":"` + testEpicID + `","limit":10}`,
			want: "limit needs a part",
		},
		{
			name: "negative offset",
			args: `{"id":"` + testEpicID + `","part":"tasks","offset":-1}`,
			want: "offset must not be negative",
		},
		{
			name: "negative limit",
			args: `{"id":"` + testEpicID + `","part":"tasks","limit":-1}`,
			want: "limit must not be negative",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEpics{}
			p := newPeer(t, nil, fake)

			outcome := p.tool("epic_get", tc.args)

			if !outcome.isError {
				t.Fatalf("epic_get %s returned %q without isError", tc.args, outcome.text)
			}
			if outcome.text != tc.want {
				t.Fatalf("epic_get error text = %q, want %q", outcome.text, tc.want)
			}
			if n := len(fake.calls().gets); n != 0 {
				t.Fatalf("GetEpic calls = %d, want none before the refusal", n)
			}
		})
	}

	t.Run("task the epic lacks", func(t *testing.T) {
		fake := &fakeEpics{getReply: &v1.GetEpicResponse{Epic: smallEpic()}}
		p := newPeer(t, nil, fake)

		outcome := p.tool("epic_get", `{"id":"`+testEpicID+`","part":"task","task":"T9"}`)

		if !outcome.isError {
			t.Fatalf("epic_get returned %q without isError", outcome.text)
		}
		if want := "task: no task T9 in epic " + testEpicID; outcome.text != want {
			t.Fatalf("epic_get error text = %q, want %q", outcome.text, want)
		}
		if n := len(fake.calls().gets); n != 1 {
			t.Fatalf("GetEpic calls = %d, want one", n)
		}
	})

	t.Run("question the epic lacks", func(t *testing.T) {
		fake := &fakeEpics{getReply: &v1.GetEpicResponse{Epic: smallEpic()}}
		p := newPeer(t, nil, fake)

		outcome := p.tool("epic_get", `{"id":"`+testEpicID+`","part":"question","question":"Q9"}`)

		if !outcome.isError {
			t.Fatalf("epic_get returned %q without isError", outcome.text)
		}
		if want := "question: no question Q9 in epic " + testEpicID; outcome.text != want {
			t.Fatalf("epic_get error text = %q, want %q", outcome.text, want)
		}
		if n := len(fake.calls().gets); n != 1 {
			t.Fatalf("GetEpic calls = %d, want one", n)
		}
	})
}

func TestEpicByPartToolsListAdvertisesTheArguments(t *testing.T) {
	p := newPeer(t, nil, &fakeEpics{})

	reply := p.call("tools/list", map[string]any{})
	var listed struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	found := false
	var description string
	var properties, required []string
	for _, tool := range listed.Tools {
		if tool.Name == "epic_get" {
			found = true
			description = tool.Description
			properties = slices.Sorted(maps.Keys(tool.InputSchema.Properties))
			required = tool.InputSchema.Required
		}
	}
	if !found {
		t.Fatal("tools/list does not name epic_get")
	}
	if want := []string{"id", "limit", "offset", "part", "question", "task"}; !slices.Equal(properties, want) {
		t.Fatalf("epic_get properties = %v, want %v", properties, want)
	}
	if !slices.Equal(required, []string{"id"}) {
		t.Fatalf("epic_get required = %v, want [id]", required)
	}
	// The description says what each part returns: the summary without a part, every part by name, one line per item
	// for the list parts, the whole item for task and question, and the runes of text for the rest.
	says := []string{
		"Without part, it returns the one-screen text summary",
		"With part, it returns that part, one of tasks, turns, decisions, questions, history, contract, workflow, " +
			"report, task and question.",
		"tasks, turns, decisions, questions and history give one line per item, 50 from offset by default",
		"return that item whole as protobuf JSON",
		"contract, workflow, report, task and question return limit runes from offset, 8000 by default",
		"name the offset that continues",
	}
	for _, phrase := range says {
		if !strings.Contains(description, phrase) {
			t.Errorf("epic_get description does not say %q: %q", phrase, description)
		}
	}

	// The committed fixture carries the same description, so a change to the description needs the fixture updated.
	raw, err := os.ReadFile(toolsListFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture []listedTool
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	inFixture := false
	for _, tool := range fixture {
		if tool.Name == "epic_get" {
			inFixture = true
			if tool.Description != description {
				t.Errorf("epic_get description differs from the fixture %s", toolsListFixture)
			}
		}
	}
	if !inFixture {
		t.Errorf("the fixture %s does not list epic_get", toolsListFixture)
	}
}

// TestEpicByPartToolsListFixtureAdvertisesTheArguments reads epic_get's arguments and description from the committed
// fixture itself, so the fixture the regression compares against advertises the same arguments as the live tools/list.
func TestEpicByPartToolsListFixtureAdvertisesTheArguments(t *testing.T) {
	raw, err := os.ReadFile(toolsListFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		} `json:"inputSchema"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	found := false
	var description string
	var properties, required []string
	for _, tool := range fixture {
		if tool.Name == "epic_get" {
			found = true
			description = tool.Description
			properties = slices.Sorted(maps.Keys(tool.InputSchema.Properties))
			required = tool.InputSchema.Required
		}
	}
	if !found {
		t.Fatalf("the fixture %s does not list epic_get", toolsListFixture)
	}
	t.Logf("fixture epic_get properties: %v", properties)
	t.Logf("fixture epic_get required: %v", required)
	t.Logf("fixture epic_get description: %s", description)
	if want := []string{"id", "limit", "offset", "part", "question", "task"}; !slices.Equal(properties, want) {
		t.Fatalf("fixture epic_get properties = %v, want %v", properties, want)
	}
	if !slices.Equal(required, []string{"id"}) {
		t.Fatalf("fixture epic_get required = %v, want [id]", required)
	}
	says := []string{
		"Without part, it returns the one-screen text summary",
		"With part, it returns that part, one of tasks, turns, decisions, questions, history, contract, workflow, " +
			"report, task and question.",
		"tasks, turns, decisions, questions and history give one line per item, 50 from offset by default",
		"return that item whole as protobuf JSON",
		"contract, workflow, report, task and question return limit runes from offset, 8000 by default",
		"name the offset that continues",
	}
	for _, phrase := range says {
		if !strings.Contains(description, phrase) {
			t.Errorf("fixture epic_get description does not say %q: %q", phrase, description)
		}
	}
}
