package stepmcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// acknowledgementByteCap is the most bytes a write's acknowledgement may have.
const acknowledgementByteCap = 1000

// epicsWithFailingGet is the fake with GetEpic refused with err, so that the phase read of an answer fails while its
// AnswerEpic succeeds.
type epicsWithFailingGet struct {
	*fakeEpics
	err error
}

func (f epicsWithFailingGet) GetEpic(
	_ context.Context, _ *connect.Request[v1.GetEpicRequest],
) (*connect.Response[v1.GetEpicResponse], error) {
	return nil, f.err
}

// assertAcknowledgement checks that a write's outcome is the acknowledgement want, that the text is within the
// byte cap, and that it holds no part of the stored epic: neither the key "contract" nor the first sentence of the
// contract's intent. It logs the text's size under the tool's name.
func assertAcknowledgement(t *testing.T, tool string, outcome toolOutcome, want string, epic *v1.StoredEpic) {
	t.Helper()
	if outcome.isError {
		t.Fatalf("%s returned an error: %s", tool, outcome.text)
	}
	if outcome.text != want {
		t.Fatalf("%s text = %q, want %q", tool, outcome.text, want)
	}
	if n := len(outcome.text); n > acknowledgementByteCap {
		t.Errorf("%s text is %d bytes, want at most %d", tool, n, acknowledgementByteCap)
	}
	t.Logf("%s: %d bytes", tool, len(outcome.text))
	if strings.Contains(outcome.text, `"contract"`) {
		t.Errorf("%s text holds the key \"contract\": %q", tool, outcome.text)
	}
	sentence, _, _ := strings.Cut(epic.GetContract().GetIntent(), ". ")
	if sentence == "" {
		t.Fatal("the epic's intent has no sentence to look for")
	}
	if strings.Contains(outcome.text, sentence) {
		t.Errorf("%s text holds the contract's intent %q", tool, sentence)
	}
}

// assertAnswerCalls checks that epic_answer made one AnswerEpic call, and one GetEpic call for testEpicID with its
// history left out.
func assertAnswerCalls(t *testing.T, fake *fakeEpics) {
	t.Helper()
	calls := fake.calls()
	if len(calls.answers) != 1 {
		t.Errorf("AnswerEpic calls = %d, want 1", len(calls.answers))
	}
	if len(calls.gets) != 1 || !proto.Equal(calls.gets[0], &v1.GetEpicRequest{Id: testEpicID}) {
		t.Errorf("GetEpic requests = %v, want one for %s with history off", calls.gets, testEpicID)
	}
}

// TestEpicByPartWritesReturnAnAcknowledgement checks that each write tool answers with its acknowledgement in place of
// the stored epic. The acknowledgement fits in acknowledgementByteCap bytes and holds no part of the contract.
func TestEpicByPartWritesReturnAnAcknowledgement(t *testing.T) {
	t.Parallel()
	const head = "epic " + testEpicID + ": phase accepted\n"
	t.Run("epic_create", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		p := newPeer(t, nil, &fakeEpics{createReply: &v1.CreateEpicResponse{Epic: epic}})

		outcome := p.tool("epic_create", `{"id":"`+testEpicID+`"}`)

		assertAcknowledgement(t, "epic_create", outcome, head+"created with 0 requirements and 300 decisions", epic)
	})
	t.Run("epic_answer", func(t *testing.T) {
		t.Parallel()
		t.Run("question", func(t *testing.T) {
			t.Parallel()
			epic := largeEpic()
			fake := &fakeEpics{
				getReply:    &v1.GetEpicResponse{Epic: epic},
				answerReply: &v1.AnswerEpicResponse{Decision: &v1.EpicDecision{Id: "D301", Question: "Q2"}},
			}
			p := newPeer(t, nil, fake)

			outcome := p.tool("epic_answer", `{"epicId":"`+testEpicID+`","id":"D301","question":"Q2","choice":"retry"}`)

			assertAnswerCalls(t, fake)
			assertAcknowledgement(t, "epic_answer", outcome,
				head+"recorded decision D301 answering Q2 with retry", epic)
		})
		t.Run("note with warning", func(t *testing.T) {
			t.Parallel()
			epic := largeEpic()
			fake := &fakeEpics{
				getReply: &v1.GetEpicResponse{Epic: epic},
				answerReply: &v1.AnswerEpicResponse{
					Decision: &v1.EpicDecision{Id: "D302"},
					Warning:  "recorded as a note: it answers no question, and Q1, Q2 are open",
				},
			}
			p := newPeer(t, nil, fake)

			outcome := p.tool("epic_answer", `{"epicId":"`+testEpicID+`","id":"D302","text":"A note."}`)

			assertAnswerCalls(t, fake)
			assertAcknowledgement(t, "epic_answer", outcome,
				head+"recorded note D302\n"+
					"warning: recorded as a note: it answers no question, and Q1, Q2 are open", epic)
		})
	})
	t.Run("epic_amend", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		fake := &fakeEpics{amendReply: &v1.AmendEvidenceResponse{
			Amendment: &v1.EpicAmendment{Seq: 1, Requirement: "R2", State: "checking"},
			Epic:      epic,
		}}
		p := newPeer(t, nil, fake)

		outcome := p.tool("epic_amend", `{"id":"`+testEpicID+`","requirement":"R2",`+
			`"item":{"capture":["/bin/true"],"limit":1,"conclusion":"it holds","judge":"j","kind":"capture"}}`)

		assertAcknowledgement(t, "epic_amend", outcome, head+"amendment A1 of R2 is checking", epic)
	})
	t.Run("epic_kick", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		p := newPeer(t, nil, &fakeEpics{kickReply: &v1.KickEpicResponse{Epic: epic}})

		outcome := p.tool("epic_kick", `{"id":"`+testEpicID+`","text":"Try the sqlite plan first."}`)

		assertAcknowledgement(t, "epic_kick", outcome,
			head+"kicked; the planner reads the words at its next plan run", epic)
	})
	t.Run("epic_pause", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		p := newPeer(t, nil, &fakeEpics{pauseReply: &v1.PauseEpicResponse{Epic: epic}})

		outcome := p.tool("epic_pause", `{"id":"`+testEpicID+`"}`)

		assertAcknowledgement(t, "epic_pause", outcome, head+"paused", epic)
	})
	t.Run("epic_resume", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		fake := &fakeEpics{resumeReply: &v1.ResumeEpicResponse{
			Epic:    epic,
			Resumed: []string{"step-a", "step-b"},
		}}
		p := newPeer(t, nil, fake)

		outcome := p.tool("epic_resume", `{"id":"`+testEpicID+`"}`)

		assertAcknowledgement(t, "epic_resume", outcome,
			head+"resumed 2 limited steps: step-a, step-b", epic)
	})
	t.Run("epic_retry", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		p := newPeer(t, nil, &fakeEpics{retryReply: &v1.RetryEpicResponse{Epic: epic}})

		outcome := p.tool("epic_retry", `{"id":"`+testEpicID+`"}`)

		assertAcknowledgement(t, "epic_retry", outcome, head+"ended the park", epic)
	})
	t.Run("epic_release", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		p := newPeer(t, nil, &fakeEpics{releaseReply: &v1.ReleaseEpicResponse{Epic: epic}})

		outcome := p.tool("epic_release", `{"id":"`+testEpicID+`","undoNone":[2,3]}`)

		assertAcknowledgement(t, "epic_release", outcome,
			head+"release started; undo-none steps 2, 3 run again", epic)
	})
	t.Run("epic_stop", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		p := newPeer(t, nil, &fakeEpics{stopReply: &v1.StopEpicResponse{Epic: epic}})

		outcome := p.tool("epic_stop", `{"id":"`+testEpicID+`"}`)

		assertAcknowledgement(t, "epic_stop", outcome,
			head+"stopped its release and freed the release slot", epic)
	})
	t.Run("epic_close", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		p := newPeer(t, nil, &fakeEpics{closeReply: &v1.CloseEpicResponse{Epic: epic}})

		outcome := p.tool("epic_close",
			`{"id":"`+testEpicID+`","kind":"superseded","by":"demo-0a1b2c3/epic-next"}`)

		assertAcknowledgement(t, "epic_close", outcome,
			head+"closed as superseded, superseded by demo-0a1b2c3/epic-next", epic)
	})
}

// TestEpicByPartWriteAcknowledgementStaysWithinOneKilobyte checks that a long epic id and a long answer warning are cut
// before the acknowledgement passes acknowledgementByteCap bytes.
func TestEpicByPartWriteAcknowledgementStaysWithinOneKilobyte(t *testing.T) {
	t.Parallel()
	t.Run("epic id of 5000 runes", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		epic.Contract.Id = strings.Repeat("é", 5000)
		p := newPeer(t, nil, &fakeEpics{pauseReply: &v1.PauseEpicResponse{Epic: epic}})

		outcome := p.tool("epic_pause", `{"id":"`+testEpicID+`"}`)

		assertAcknowledgement(t, "epic_pause", outcome,
			"epic "+strings.Repeat("é", 200)+"…: phase accepted\npaused", epic)
	})
	t.Run("warning of 5000 runes", func(t *testing.T) {
		t.Parallel()
		epic := largeEpic()
		fake := &fakeEpics{
			getReply: &v1.GetEpicResponse{Epic: epic},
			answerReply: &v1.AnswerEpicResponse{
				Decision: &v1.EpicDecision{Id: "D302"},
				Warning:  strings.Repeat("é", 5000),
			},
		}
		p := newPeer(t, nil, fake)

		outcome := p.tool("epic_answer", `{"epicId":"`+testEpicID+`","id":"D302","text":"A note."}`)

		assertAcknowledgement(t, "epic_answer", outcome,
			"epic "+testEpicID+": phase accepted\nrecorded note D302\nwarning: "+strings.Repeat("é", 300)+"…", epic)
	})
	t.Run("long id and long warning together", func(t *testing.T) {
		t.Parallel()
		// An answer's acknowledgement names the request's epic id, so the long id is the request's.
		longID := strings.Repeat("é", 5000)
		fake := &fakeEpics{
			getReply: &v1.GetEpicResponse{Epic: largeEpic()},
			answerReply: &v1.AnswerEpicResponse{
				Decision: &v1.EpicDecision{Id: "D303", Question: "Q2"},
				Warning:  strings.Repeat("é", 5000),
			},
		}
		p := newPeer(t, nil, fake)

		outcome := p.tool("epic_answer",
			`{"epicId":"`+longID+`","id":"D303","question":"Q2","choice":"other","text":"Use OIDC."}`)

		if outcome.isError {
			t.Fatalf("epic_answer returned an error: %s", outcome.text)
		}
		// The three lines together pass the cap, so the text is cut on a rune boundary to just under it.
		n := len(outcome.text)
		if n > acknowledgementByteCap || n < acknowledgementByteCap-10 {
			t.Errorf("epic_answer text is %d bytes, want a cut of %d to %d bytes",
				n, acknowledgementByteCap-10, acknowledgementByteCap)
		}
		if !utf8.ValidString(outcome.text) {
			t.Errorf("epic_answer text is not valid UTF-8: %q", outcome.text)
		}
		if !strings.HasSuffix(outcome.text, "…") {
			t.Errorf("epic_answer text ends %q, want the cut's ellipsis", outcome.text[max(0, n-10):])
		}
		t.Logf("epic_answer: %d bytes", n)
	})
}

// TestEpicByPartAnswerAcknowledgesWhenThePhaseReadFails checks that an answer whose phase read fails is still recorded
// and answered with an acknowledgement that is not a tool error. The line names the phase as not read, with the
// error's first line, cut to 200 runes.
func TestEpicByPartAnswerAcknowledgesWhenThePhaseReadFails(t *testing.T) {
	t.Parallel()
	// The error's first line is 311 runes, so its cut shows; the second line is left out.
	refusal := connect.NewError(connect.CodeNotFound, errors.New(strings.Repeat("x", 300)+"\nsecond line"))
	fake := &fakeEpics{answerReply: &v1.AnswerEpicResponse{Decision: &v1.EpicDecision{Id: "D3", Question: "Q2"}}}
	p := newPeer(t, nil, epicsWithFailingGet{fakeEpics: fake, err: refusal})

	outcome := p.tool("epic_answer", `{"epicId":"`+testEpicID+`","id":"D3","question":"Q2","choice":"retry"}`)

	if outcome.isError {
		t.Fatalf("epic_answer returned an error: %s", outcome.text)
	}
	want := "epic " + testEpicID + ": phase not read (not_found: " + strings.Repeat("x", 189) + "…)\n" +
		"recorded decision D3 answering Q2 with retry"
	if outcome.text != want {
		t.Fatalf("epic_answer text = %q, want %q", outcome.text, want)
	}
	if n := len(fake.calls().answers); n != 1 {
		t.Fatalf("AnswerEpic calls = %d, want 1: the answer is recorded", n)
	}
}

// TestEpicByPartAcknowledgementNamesEachBranch checks the line each write names in the cases the main test leaves out:
// a stop of a paused epic, a retry that names a node, a resume that resumes nothing, a close as shipped, and a note
// with no warning.
func TestEpicByPartAcknowledgementNamesEachBranch(t *testing.T) {
	t.Parallel()
	const head = "epic " + testEpicID + ": phase accepted\n"
	cases := []struct {
		name      string
		tool      string
		arguments string
		want      string
		fake      func(epic *v1.StoredEpic) *fakeEpics
	}{
		{
			name: "stop of a paused epic", tool: "epic_stop", arguments: `{"id":"` + testEpicID + `"}`,
			want: head + "stopped its running turn and paused it",
			fake: func(epic *v1.StoredEpic) *fakeEpics {
				epic.Paused = true
				return &fakeEpics{stopReply: &v1.StopEpicResponse{Epic: epic}}
			},
		},
		{
			name: "retry naming a node", tool: "epic_retry", arguments: `{"id":"` + testEpicID + `","node":"check"}`,
			want: head + "reran node check",
			fake: func(epic *v1.StoredEpic) *fakeEpics {
				return &fakeEpics{retryReply: &v1.RetryEpicResponse{Epic: epic}}
			},
		},
		{
			name: "resume with no step limited", tool: "epic_resume", arguments: `{"id":"` + testEpicID + `"}`,
			want: head + "resumed 0 limited steps",
			fake: func(epic *v1.StoredEpic) *fakeEpics {
				return &fakeEpics{resumeReply: &v1.ResumeEpicResponse{Epic: epic}}
			},
		},
		{
			name: "close as shipped", tool: "epic_close", arguments: `{"id":"` + testEpicID + `","kind":"shipped"}`,
			want: head + "closed as shipped",
			fake: func(epic *v1.StoredEpic) *fakeEpics {
				return &fakeEpics{closeReply: &v1.CloseEpicResponse{Epic: epic}}
			},
		},
		{
			name: "note with no warning", tool: "epic_answer",
			arguments: `{"epicId":"` + testEpicID + `","id":"D4","text":"A note."}`,
			want:      head + "recorded note D4",
			fake: func(_ *v1.StoredEpic) *fakeEpics {
				return &fakeEpics{answerReply: &v1.AnswerEpicResponse{Decision: &v1.EpicDecision{Id: "D4"}}}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			epic := largeEpic()
			fake := tc.fake(epic)
			fake.getReply = &v1.GetEpicResponse{Epic: epic}
			p := newPeer(t, nil, fake)

			outcome := p.tool(tc.tool, tc.arguments)

			assertAcknowledgement(t, tc.tool, outcome, tc.want, epic)
		})
	}
}

// TestEpicByPartWriteToolsListSaysTheAcknowledgement checks that the description of each write tool in tools/list says
// it returns the acknowledgement, and that the committed fixture describes the tool the same way.
func TestEpicByPartWriteToolsListSaysTheAcknowledgement(t *testing.T) {
	t.Parallel()
	const sentence = "Returns a short acknowledgement, not the stored epic: " +
		"the epic id, its phase and one line naming what changed."
	writeTools := []string{
		"epic_create", "epic_answer", "epic_amend", "epic_kick", "epic_pause", "epic_resume", "epic_retry",
		"epic_release", "epic_stop", "epic_close",
	}
	raw, err := os.ReadFile(toolsListFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture []listedTool
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	fixtureTools := make(map[string]listedTool, len(fixture))
	for _, tool := range fixture {
		fixtureTools[tool.Name] = tool
	}

	p := newPeer(t, nil, &fakeEpics{})
	reply := p.call("tools/list", map[string]any{})
	var listed struct {
		Tools []listedTool `json:"tools"`
	}
	if err := json.Unmarshal(reply.Result, &listed); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	live := make(map[string]listedTool, len(listed.Tools))
	for _, tool := range listed.Tools {
		live[tool.Name] = tool
	}

	for _, name := range writeTools {
		got, ok := live[name]
		if !ok {
			t.Errorf("tools/list is missing tool %s", name)
			continue
		}
		if !strings.Contains(got.Description, sentence) {
			t.Errorf("%s description does not say it returns the acknowledgement: %q", name, got.Description)
		}
		want, ok := fixtureTools[name]
		if !ok {
			t.Errorf("the fixture has no tool %s", name)
			continue
		}
		if got.Description != want.Description {
			t.Errorf("%s description differs from the fixture: %q, want %q", name, got.Description, want.Description)
		}
		t.Logf("%s: %s", name, got.Description)
	}
}
