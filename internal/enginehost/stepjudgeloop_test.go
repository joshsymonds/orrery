package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// judgeLoopPrompt is the step's first prompt, which the judge is given with each return.
const judgeLoopPrompt = "Write the report on the parser."

// judgeLoopReply is one canned reply of a judgeLoopHaiku: a structured output, or an error when err is set.
type judgeLoopReply struct {
	raw json.RawMessage
	err error
}

// judgeLoopHaiku answers each call with the next canned reply and records each call's schema and input. A call past
// its replies fails.
type judgeLoopHaiku struct {
	mu      sync.Mutex
	replies []judgeLoopReply
	schemas []string
	inputs  []string
}

func (h *judgeLoopHaiku) Call(_ context.Context, schema, input string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.schemas = append(h.schemas, schema)
	h.inputs = append(h.inputs, input)
	if len(h.replies) == 0 {
		return nil, errors.New("judgeLoopHaiku: no canned reply left")
	}
	reply := h.replies[0]
	h.replies = h.replies[1:]
	return reply.raw, reply.err
}

// callInputs returns a copy of the inputs the calls were made with.
func (h *judgeLoopHaiku) callInputs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.inputs...)
}

// judgeLoopVerdict is a canned judge reply with verdict and reason.
func judgeLoopVerdict(verdict, reason string) judgeLoopReply {
	return judgeLoopReply{raw: json.RawMessage(fmt.Sprintf(`{"verdict":%q,"reason":%q}`, verdict, reason))}
}

// judgeLoopFailure is a canned reply of a failed call.
func judgeLoopFailure() judgeLoopReply {
	return judgeLoopReply{err: errors.New("haiku: claude run failed")}
}

// judgeLoopDone is a done return whose note tells two returns apart.
func judgeLoopDone(note string) session.TurnEnded {
	raw := fmt.Sprintf(`{"status":%q,"note":%q}`, stepLoopDone, note)
	return session.TurnEnded{StructuredOutput: json.RawMessage(raw)}
}

// newJudgeLoopFixture returns a step loop fixture whose step has prompt judgeLoopPrompt and calls haiku as its judge,
// configured further by configure.
func newJudgeLoopFixture(t *testing.T, haiku haikuCaller, configure func(*stepRunConfig)) *stepLoopFixture {
	t.Helper()
	return newStepDoneFixture(t, func(cfg *stepRunConfig) {
		cfg.prompt = judgeLoopPrompt
		cfg.haiku = haiku
		configure(cfg)
	})
}

// judgeLoopNoConfig leaves a read-only step as the fixture builds it.
func judgeLoopNoConfig(*stepRunConfig) {}

// judgeLoopCheckVerdict fails the test unless got is the judge verdict with the given round, verdict, detail and action,
// and no error. Its write time is not compared.
func judgeLoopCheckVerdict(t *testing.T, got store.StepVerdict, round int, verdict, detail, action string) {
	t.Helper()
	got.At = 0
	want := store.StepVerdict{Kind: "judge", Round: round, Verdict: verdict, Detail: detail, Action: action}
	if got != want {
		t.Fatalf("verdict = %+v, want %+v", got, want)
	}
}

func TestJudgeLoopMetEndsDoneWithVerdictStored(t *testing.T) {
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{judgeLoopVerdict("met", "the report is complete")}}
	f := newJudgeLoopFixture(t, haiku, judgeLoopNoConfig)
	_, done := f.start(t)

	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)})
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndDone || row.Status != stepLoopDone {
		t.Fatalf("end = %q status %q, want done DONE", row.End, row.Status)
	}
	if len(row.Verdicts) != 1 {
		t.Fatalf("stored %d verdicts, want 1: %+v", len(row.Verdicts), row.Verdicts)
	}
	judgeLoopCheckVerdict(t, row.Verdicts[0], 1, "met", "the report is complete", "end")
	if got := f.sessions.continuedTexts(); len(got) != 0 {
		t.Errorf("Continue called %d times for a met return, want 0", len(got))
	}
}

func TestJudgeLoopUnmetThenMetSendsOneContinueAndEndsDone(t *testing.T) {
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{
		judgeLoopVerdict("unmet", "the report is missing its summary"),
		judgeLoopVerdict("met", "the summary is there now"),
	}}
	f := newJudgeLoopFixture(t, haiku, judgeLoopNoConfig)
	_, done := f.start(t)

	f.run.Return(judgeLoopDone("first"))
	f.run.Return(judgeLoopDone("second"))
	waitRunEnd(t, done)

	continued := f.sessions.continuedTexts()
	if len(continued) != 1 {
		t.Fatalf("Continue called %d times, want 1", len(continued))
	}
	if !strings.Contains(continued[0], "the report is missing its summary") {
		t.Errorf("Continue text %q does not carry the judge's reason", continued[0])
	}
	row := f.row(t)
	if row.End != stepLoopEndDone || row.Status != stepLoopDone {
		t.Fatalf("end = %q status %q, want done DONE", row.End, row.Status)
	}
	if len(row.Verdicts) != 2 {
		t.Fatalf("stored %d verdicts, want 2: %+v", len(row.Verdicts), row.Verdicts)
	}
	judgeLoopCheckVerdict(t, row.Verdicts[0], 1, "unmet", "the report is missing its summary", "continue")
	judgeLoopCheckVerdict(t, row.Verdicts[1], 2, "met", "the summary is there now", "end")
}

func TestJudgeLoopUnmetTwiceEndsGoalUnmetKeepingReturn(t *testing.T) {
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{
		judgeLoopVerdict("unmet", "first gap"),
		judgeLoopVerdict("unmet", "second gap"),
	}}
	f := newJudgeLoopFixture(t, haiku, judgeLoopNoConfig)
	_, done := f.start(t)

	f.run.Return(judgeLoopDone("first"))
	f.run.Return(judgeLoopDone("second"))
	waitRunEnd(t, done)

	if got := f.sessions.continuedTexts(); len(got) != 1 {
		t.Fatalf("Continue called %d times, want 1", len(got))
	}
	row := f.row(t)
	if row.End != "goal_unmet" || row.Status != "" {
		t.Fatalf("end = %q status %q, want goal_unmet with no status", row.End, row.Status)
	}
	if row.Reason != "second gap" {
		t.Errorf("reason = %q, want the second judge reason", row.Reason)
	}
	if !strings.Contains(row.Result, `"note":"second"`) {
		t.Errorf("result %q is not the second return, want it kept", row.Result)
	}
	if got := len(stepDoneRoundChecks(row.Checks, 2)); got != 1 {
		t.Errorf("round 2 stored %d checks, want the schema check kept", got)
	}
	if len(row.Verdicts) != 2 {
		t.Fatalf("stored %d verdicts, want 2: %+v", len(row.Verdicts), row.Verdicts)
	}
	judgeLoopCheckVerdict(t, row.Verdicts[0], 1, "unmet", "first gap", "continue")
	judgeLoopCheckVerdict(t, row.Verdicts[1], 2, "unmet", "second gap", "end")
}

func TestJudgeLoopWritingStepInputCarriesDiffStatAndGateTail(t *testing.T) {
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{stepDoneOwned: "owned\n"})
	gate := stepDoneScript(t, "echo gate-tail-marker")
	lock := filepath.Join(t.TempDir(), "slot")
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{judgeLoopVerdict("met", "complete")}}
	f := newJudgeLoopFixture(t, haiku, stepDoneWriting(dir, start, []string{gate}, time.Minute, lock))
	_, done := f.start(t)

	f.run.Return(stepDoneReturn(commit))
	waitRunEnd(t, done)

	inputs := haiku.callInputs()
	if len(inputs) != 1 {
		t.Fatalf("judge called %d times, want 1", len(inputs))
	}
	input := inputs[0]
	for _, want := range []string{
		"## Task\n" + judgeLoopPrompt,
		"## Return\n" + fmt.Sprintf(`{"status":%q,"commit":%q}`, stepLoopDone, commit),
		"## Diff stat\n",
		stepDoneOwned,
		"## Gate output tail\n",
		"gate exited 0; output tail: gate-tail-marker",
	} {
		if !strings.Contains(input, want) {
			t.Errorf("judge input does not contain %q:\n%s", want, input)
		}
	}
}

func TestJudgeLoopFailedCallEndsDoneWithErrorStored(t *testing.T) {
	cases := []struct {
		name      string
		reply     judgeLoopReply
		wantError string
	}{
		{name: "call fails", reply: judgeLoopFailure(), wantError: "claude run failed"},
		{
			name:      "reply has a verdict that is neither met nor unmet",
			reply:     judgeLoopVerdict("maybe", "unsure"),
			wantError: "maybe",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			haiku := &judgeLoopHaiku{replies: []judgeLoopReply{tc.reply}}
			f := newJudgeLoopFixture(t, haiku, judgeLoopNoConfig)
			_, done := f.start(t)

			f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)})
			waitRunEnd(t, done)

			row := f.row(t)
			if row.End != stepLoopEndDone || row.Status != stepLoopDone {
				t.Fatalf("end = %q status %q, want done DONE", row.End, row.Status)
			}
			if len(row.Verdicts) != 1 {
				t.Fatalf("stored %d verdicts, want 1: %+v", len(row.Verdicts), row.Verdicts)
			}
			v := row.Verdicts[0]
			if v.Verdict != "" || v.Action != "none" || !strings.Contains(v.Error, tc.wantError) {
				t.Errorf("verdict = %+v, want an empty verdict, action none and an error naming %q", v, tc.wantError)
			}
			if got := f.sessions.continuedTexts(); len(got) != 0 {
				t.Errorf("Continue called %d times after a failed judge, want 0", len(got))
			}
		})
	}
}

func TestJudgeLoopFailingCheckIsNeverJudged(t *testing.T) {
	dir, start := stepDoneRepo(t)
	commit := stepGitTestCommit(t, dir, map[string]string{"other/outside.txt": "stray\n"})
	lock := filepath.Join(t.TempDir(), "slot")
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{judgeLoopVerdict("met", "complete")}}
	f := newJudgeLoopFixture(t, haiku, stepDoneWriting(dir, start, nil, time.Minute, lock))
	_, done := f.start(t)

	for range stepLoopMaxContinues + 1 {
		f.run.Return(stepDoneReturn(commit))
	}
	waitRunEnd(t, done)

	if row := f.row(t); row.End != stepLoopEndChecksFailed {
		t.Fatalf("end = %q, want checks_failed", row.End)
	}
	if got := haiku.callInputs(); len(got) != 0 {
		t.Errorf("judge called %d times for returns with failing checks, want 0", len(got))
	}
}

func TestJudgeLoopNonDoneStatusIsNotJudged(t *testing.T) {
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{judgeLoopVerdict("met", "complete")}}
	f := newJudgeLoopFixture(t, haiku, judgeLoopNoConfig)
	_, done := f.start(t)

	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"NOPE"}`)})
	waitRunEnd(t, done)

	if row := f.row(t); row.End != stepLoopEndReturned {
		t.Fatalf("end = %q, want returned", row.End)
	}
	if got := haiku.callInputs(); len(got) != 0 {
		t.Errorf("judge called %d times for a non-done return, want 0", len(got))
	}
}

func TestJudgeLoopNilHaikuStoresNoVerdictAndEndsDone(t *testing.T) {
	f := newJudgeLoopFixture(t, nil, judgeLoopNoConfig)
	_, done := f.start(t)

	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)})
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndDone || row.Status != stepLoopDone {
		t.Fatalf("end = %q status %q, want done DONE", row.End, row.Status)
	}
	if len(row.Verdicts) != 0 {
		t.Errorf("stored %d verdicts with no judge, want 0: %+v", len(row.Verdicts), row.Verdicts)
	}
}
