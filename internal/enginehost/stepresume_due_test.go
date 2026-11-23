package enginehost

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// probeResetUnix is the reset of the usage limit the fake claude's LIMIT turn names, which a step limited by it stores
// as its due row.
const probeResetUnix = 1900000000

// dueResumesOf returns the due rows of step id that are still due.
func dueResumesOf(t *testing.T, rig stepsRig, id string) []store.DueResume {
	t.Helper()
	rows, err := rig.store.DueResumes(t.Context())
	if err != nil {
		t.Fatalf("DueResumes: %v", err)
	}
	var out []store.DueResume
	for _, row := range rows {
		if row.Target == id {
			out = append(out, row)
		}
	}
	return out
}

// promptEventsOf returns the engine_prompt events of step id, in seq order, as the store holds them now.
func promptEventsOf(t *testing.T, rig stepsRig, id string) []store.EngineEvent {
	t.Helper()
	history, err := store.NewReader(rig.path).EngineHistory(t.Context(), id, 0, 10_000)
	if err != nil {
		t.Fatalf("EngineHistory of step %s: %v", id, err)
	}
	var out []store.EngineEvent
	for _, event := range history.Events {
		if event.Kind == store.EventEnginePrompt {
			out = append(out, event)
		}
	}
	return out
}

// promptFieldsOf decodes the text and the PromptID of the engine_prompt event.
func promptFieldsOf(t *testing.T, event store.EngineEvent) (text, promptID string) {
	t.Helper()
	var payload struct {
		Text     string `json:"text"`
		PromptID string `json:"promptId"`
	}
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
	}
	return payload.Text, payload.PromptID
}

// resumePromptEventsOf returns the engine_prompt events of step id that carry the owner's resume prompt text.
func resumePromptEventsOf(t *testing.T, rig stepsRig, id string) []store.EngineEvent {
	t.Helper()
	var out []store.EngineEvent
	for _, event := range promptEventsOf(t, rig, id) {
		if text, _ := promptFieldsOf(t, event); text == stepResumePrompt {
			out = append(out, event)
		}
	}
	return out
}

// resumePromptsLogged returns how many lines the fake claude in dir has logged from its stdin that carry the resume
// prompt text.
func resumePromptsLogged(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "fakeclaude.stdin"))
	if err != nil {
		t.Fatalf("read fake stdin log: %v", err)
	}
	return strings.Count(string(data), stepResumePrompt)
}

// TestStepsAutoResumeSendsDerivedPromptAndClosesDueRow covers R3 and R1: the automatic resume of a limited step sends one
// resume prompt under the PromptID derived from the step and its reset, respawns the step in its session, and in the
// same write as the cleared limit closes the step's due row.
func TestStepsAutoResumeSendsDerivedPromptAndClosesDueRow(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	dir := filepath.Join(rig.worktrees, id)
	pid := fakePID(t, dir)
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"step limited",
		func() bool { return stepRowOf(t, rig.steps, id).LoopLimited },
	)
	requireGone(t, pid)
	if got := dueResumesOf(t, rig, id); len(got) != 1 || got[0].Resets.Unix() != probeResetUnix {
		t.Fatalf("due rows = %+v, want one for reset %d", got, probeResetUnix)
	}
	writeResumeNext(t, dir)

	if err := rig.steps.AutoResume(t.Context(), id, time.Unix(probeResetUnix, 0)); err != nil {
		t.Fatalf("AutoResume: %v", err)
	}
	requireResumedStart(t, dir)
	if row := stepRowOf(t, rig.steps, id); row.LoopLimited {
		t.Fatal("LoopLimited = true after the automatic resume, want the limit cleared")
	}
	if got := dueResumesOf(t, rig, id); len(got) != 0 {
		t.Fatalf("due rows after the automatic resume = %+v, want none", got)
	}
	resumes := resumePromptEventsOf(t, rig, id)
	if len(resumes) != 1 {
		t.Fatalf("resume prompts stored = %d, want one", len(resumes))
	}
	if _, promptID := promptFieldsOf(t, resumes[0]); promptID != resumePromptID(id, probeResetUnix) {
		t.Fatalf("resume PromptID = %q, want the id derived from the step and its reset", promptID)
	}
	waittest.Until(t, nil, "resume prompt delivered once", func() bool { return resumePromptsLogged(t, dir) == 1 })
	releaseResumedTurn(t, dir)
	var row store.StepRow
	waitUntil(t, engineOf(rig.manager, rig.steps), "resumed step done", func() bool {
		row = stepRowOf(t, rig.steps, id)
		return row.End != ""
	})
	if row.End != stepLoopEndDone {
		t.Fatalf("End = %q reason %q, want %q", row.End, row.Reason, stepLoopEndDone)
	}
}

// TestStepsAutoResumeWithStoredPromptSendsNothingAndClearsLimit covers R3: when an engine_prompt of the step already
// carries the resume's PromptID, as a kill between the prompt and the cleared limit leaves, the automatic resume sends
// nothing and respawns nothing. It clears the limit and closes the due row.
func TestStepsAutoResumeWithStoredPromptSendsNothingAndClearsLimit(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	pid := fakePID(t, filepath.Join(rig.worktrees, id))
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"step limited",
		func() bool { return stepRowOf(t, rig.steps, id).LoopLimited },
	)
	requireGone(t, pid)
	reset := time.Unix(probeResetUnix, 0)
	writeID, err := newWriteID()
	if err != nil {
		t.Fatalf("newWriteID: %v", err)
	}
	if err := rig.manager.writer.Commit(t.Context(), id, store.EnginePrompt{
		ID: id, Text: stepResumePrompt, PromptID: resumePromptID(id, probeResetUnix), Sender: store.PromptSenderEngine,
		WriteID: writeID,
	}); err != nil {
		t.Fatalf("store the resume prompt: %v", err)
	}
	before := len(promptEventsOf(t, rig, id))

	if err := rig.steps.AutoResume(t.Context(), id, reset); err != nil {
		t.Fatalf("AutoResume with its prompt stored: %v", err)
	}
	if got := len(promptEventsOf(t, rig, id)); got != before {
		t.Fatalf("engine_prompt events = %d after the automatic resume, want %d: nothing sent", got, before)
	}
	rig.manager.mu.Lock()
	_, respawned := rig.manager.sessions[id]
	rig.manager.mu.Unlock()
	if respawned {
		t.Fatal("the step was respawned, want no process started")
	}
	if row := stepRowOf(t, rig.steps, id); row.LoopLimited {
		t.Fatal("LoopLimited = true after the automatic resume, want the limit cleared")
	}
	if got := dueResumesOf(t, rig, id); len(got) != 0 {
		t.Fatalf("due rows after the automatic resume = %+v, want none", got)
	}
	if err := rig.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestStepsAutoResumeOfOtherResetRefusesNothingSent covers R3: an automatic resume for a reset that differs from the
// step's limit returns ErrStepNotLimited, sends nothing, and leaves the step limited with its due row still due.
func TestStepsAutoResumeOfOtherResetRefusesNothingSent(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	pid := fakePID(t, filepath.Join(rig.worktrees, id))
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"step limited",
		func() bool { return stepRowOf(t, rig.steps, id).LoopLimited },
	)
	requireGone(t, pid)
	before := len(promptEventsOf(t, rig, id))

	err := rig.steps.AutoResume(t.Context(), id, time.Unix(probeResetUnix+1, 0))
	if !errors.Is(err, ErrStepNotLimited) {
		t.Fatalf("AutoResume for another reset = %v, want ErrStepNotLimited", err)
	}
	if got := len(promptEventsOf(t, rig, id)); got != before {
		t.Fatalf("engine_prompt events = %d after the refused resume, want %d: nothing sent", got, before)
	}
	if row := stepRowOf(t, rig.steps, id); !row.LoopLimited || row.End != "" {
		t.Fatalf("limited %t, end %q after the refused resume, want limited and open", row.LoopLimited, row.End)
	}
	if got := dueResumesOf(t, rig, id); len(got) != 1 {
		t.Fatalf("due rows after the refused resume = %+v, want the row still due", got)
	}
	if err := rig.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestStepsAutoResumeOfStepNotLimitedRefuses covers R3: an automatic resume of a step whose turn is running and that no
// limit holds returns ErrStepNotLimited.
func TestStepsAutoResumeOfStepNotLimitedRefuses(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "STEPDONE")

	if err := rig.steps.AutoResume(t.Context(), id, time.Unix(probeResetUnix, 0)); !errors.Is(err, ErrStepNotLimited) {
		t.Fatalf("AutoResume of a step not limited = %v, want ErrStepNotLimited", err)
	}
	if row := stepRowOf(t, rig.steps, id); row.End != "" || row.LoopLimited {
		t.Fatalf("end %q, limited %t after the refused resume, want open and not limited", row.End, row.LoopLimited)
	}
	if err := rig.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestStepsResumeClosesDueRowAndSendsUnkeyedPrompt covers R1 and R3: the owner's Resume sends the owner's resume prompt
// without a PromptID, and its clear-limit write closes the step's due row.
func TestStepsResumeClosesDueRowAndSendsUnkeyedPrompt(t *testing.T) {
	t.Parallel()
	rig := newStepsRig(t, nil)
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id := startLimitProbe(t, rig, repo, revision, "LIMIT")
	dir := filepath.Join(rig.worktrees, id)
	pid := fakePID(t, dir)
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"step limited",
		func() bool { return stepRowOf(t, rig.steps, id).LoopLimited },
	)
	requireGone(t, pid)
	if got := dueResumesOf(t, rig, id); len(got) != 1 {
		t.Fatalf("due rows before the owner's resume = %+v, want one", got)
	}
	writeResumeFiles(t, dir)

	if err := rig.steps.Resume(t.Context(), id); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := dueResumesOf(t, rig, id); len(got) != 0 {
		t.Fatalf("due rows after the owner's resume = %+v, want none", got)
	}
	resumes := resumePromptEventsOf(t, rig, id)
	if len(resumes) != 1 {
		t.Fatalf("resume prompts stored = %d, want one", len(resumes))
	}
	if _, promptID := promptFieldsOf(t, resumes[0]); promptID != "" {
		t.Fatalf("owner resume PromptID = %q, want none", promptID)
	}
	waitUntil(
		t,
		engineOf(rig.manager, rig.steps),
		"resumed step done",
		func() bool { return stepRowOf(t, rig.steps, id).End != "" },
	)
}

// TestResumePromptIDDerivesFromStepAndReset covers R3: the automatic resume's PromptID is the same for the same step and
// reset, and differs when the reset or the step differs, so a resume at a later reset is its own prompt.
func TestResumePromptIDDerivesFromStepAndReset(t *testing.T) {
	t.Parallel()
	base := resumePromptID("step-a", probeResetUnix)
	if base == "" {
		t.Fatal("resumePromptID is empty")
	}
	if again := resumePromptID("step-a", probeResetUnix); again != base {
		t.Fatalf("same step and reset gave %q then %q", base, again)
	}
	if later := resumePromptID("step-a", probeResetUnix+1); later == base {
		t.Fatal("a later reset gave the same PromptID")
	}
	if other := resumePromptID("step-b", probeResetUnix); other == base {
		t.Fatal("another step gave the same PromptID")
	}
}
