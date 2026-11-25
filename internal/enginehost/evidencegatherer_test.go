package enginehost

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// gathererTestEpic is the epic the gatherer tests gather for.
const gathererTestEpic = "gatherer-epic"

// gathererTestRoles is a role file whose evidence key names the evidence role's agent, and planner the planner's.
const gathererTestRoles = "evidence = \"evidence-agent\"\nplanner = \"planner-agent\"\n"

// gathererTestPlannerRoles is a role file with no evidence key, so the evidence role falls back to the planner's agent.
const gathererTestPlannerRoles = "planner = \"planner-agent\"\n"

// gathererTestIDPattern is the form a gatherer's step id takes.
var gathererTestIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// gathererTestSteps is the EvidenceSteps of the gatherer tests. Start records each start and returns its id, and
// Subscribe records each subscription and returns one no end reaches, so a gatherer runs until its loop is closed.
type gathererTestSteps struct {
	mu     sync.Mutex
	starts []StepStart
	subs   int
}

// Start records start and returns its id.
func (s *gathererTestSteps) Start(_ context.Context, start StepStart) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starts = append(s.starts, start)
	return start.ID, nil
}

// Subscribe records the subscription and returns one that no end reaches.
func (s *gathererTestSteps) Subscribe(_ context.Context, ids []string) (*StepSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs++
	return NewStepSubscription(ids), nil
}

// startedSteps returns the starts recorded so far, in order.
func (s *gathererTestSteps) startedSteps() []StepStart {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.starts)
}

// subscriptions returns how many subscriptions were recorded so far.
func (s *gathererTestSteps) subscriptions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subs
}

// gathererTestGetenv writes roles as the role file of a fresh XDG_CONFIG_HOME, and returns the getenv that names it.
func gathererTestGetenv(t *testing.T, roles string) func(string) string {
	t.Helper()
	home := t.TempDir()
	stepGateWriteFile(t, filepath.Join(home, "orrery", "roles.toml"), roles)
	return func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return home
		}
		return ""
	}
}

// gathererTestRunner returns a Gatherers of a test over st and steps, delivering through deliveries and reading the
// agents of roles. Close ends it at cleanup, so it is registered after st's own cleanup.
func gathererTestRunner(
	t *testing.T, st GathererStore, steps EvidenceSteps, deliveries *captureTestDeliveries, roles string,
) *Gatherers {
	t.Helper()
	g := NewGatherers(GatherersConfig{
		Store: st, Steps: steps, Deliver: deliveries.deliver, Getenv: gathererTestGetenv(t, roles),
	})
	t.Cleanup(g.Close)
	return g
}

// idle reports whether g has no loop under way, which holds once every loop has returned.
func (g *Gatherers) idle() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.loops) == 0
}

// gathererTestEpicIn stores the epic gathererTestEpic in st, over the repository repo at base.
func gathererTestEpicIn(t *testing.T, st *store.Store, repo, base string) {
	t.Helper()
	created := time.Unix(1700000000, 0)
	if err := st.CreateEpic(t.Context(), store.EpicRow{
		ID: gathererTestEpic, Repository: repo, Base: base, Title: "Gatherer epic", Intent: "Synthetic intent.",
		Care: "serious", Phase: "accepted", Created: created, Changed: created,
	}); err != nil {
		t.Fatalf("store CreateEpic: %v", err)
	}
}

// gathererTestAddBrief stores a waiting brief id of the epic: a captured run of R1 in phase red at commit, whose
// capture runs argv and whose conclusion is fixed.
func gathererTestAddBrief(t *testing.T, st *store.Store, id, commit string, argv []string) {
	t.Helper()
	if err := st.AddEvidenceRun(t.Context(), store.EvidenceRun{
		ID: id, Epic: gathererTestEpic, Item: "R1", Revision: commit, Phase: evidence.PhaseRed,
		Source: evidence.SourceCaptured, State: store.RunWaiting,
		Spec: evidence.Item{
			Capture: argv, Limit: 60, Artifacts: []string{"marker"}, Conclusion: "the marker says head",
		},
		Created: time.Unix(1700000000, 0),
	}); err != nil {
		t.Fatalf("AddEvidenceRun %s: %v", id, err)
	}
}

// gathererTestStarted wakes the epic over a store with one waiting brief, under a role file roles, and returns the
// first gatherer step it starts, once that step is recorded and watched.
func gathererTestStarted(t *testing.T, roles string) StepStart {
	t.Helper()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	gathererTestEpicIn(t, st, repo, base)
	gathererTestAddBrief(t, st, "brief-1", base, []string{"/bin/sh", "-c", "cat marker"})
	steps := &gathererTestSteps{}
	g := gathererTestRunner(t, st, steps, &captureTestDeliveries{}, roles)
	g.Wake(t.Context(), gathererTestEpic)
	waittest.UntilIdle(t, g.idle, "the gatherer's step starts", func() bool { return len(steps.startedSteps()) == 1 })
	waittest.UntilIdle(t, g.idle, "the gatherer watches its step", func() bool { return steps.subscriptions() == 1 })
	return steps.startedSteps()[0]
}

// TestGathererStartsOneReadOnlyStepUnderTheEvidenceRole wakes an epic with a waiting brief, and checks one read-only
// step starts for it under the evidence role's agent, at the epic's base, with the gatherer's label, and that the brief
// is taken by it.
func TestGathererStartsOneReadOnlyStepUnderTheEvidenceRole(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	gathererTestEpicIn(t, st, repo, base)
	gathererTestAddBrief(t, st, "brief-1", base, []string{"/bin/sh", "-c", "cat marker"})
	steps := &gathererTestSteps{}
	g := gathererTestRunner(t, st, steps, &captureTestDeliveries{}, gathererTestRoles)

	g.Wake(t.Context(), gathererTestEpic)
	waittest.UntilIdle(t, g.idle, "the gatherer's step starts", func() bool { return len(steps.startedSteps()) == 1 })
	waittest.UntilIdle(t, g.idle, "the gatherer watches its step", func() bool { return steps.subscriptions() == 1 })

	start := steps.startedSteps()[0]
	if !gathererTestIDPattern.MatchString(start.ID) {
		t.Errorf("step id = %q, want 32 lowercase hex digits", start.ID)
	}
	if !start.ReadOnly || start.Agent != "evidence-agent" || start.Workspace != repo || start.Revision != base ||
		start.Epic != gathererTestEpic || start.Task != gathererTask {
		t.Fatalf("start = %+v, want a read-only gatherer of %s under evidence-agent at %s in %s",
			start, gathererTestEpic, base, repo)
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(
		[]byte(start.Schema),
		&schema,
	); err != nil ||
		!slices.Equal(schema.Required, []string{"note"}) {
		t.Fatalf("schema = %s, want one required string note", start.Schema)
	}
	if err := checkStepSchema(start.Schema); err != nil {
		t.Fatalf("schema is refused: %v", err)
	}
	got, err := st.EvidenceRun(t.Context(), "brief-1")
	if err != nil {
		t.Fatalf("EvidenceRun: %v", err)
	}
	if got.State != store.RunGathering || got.Gatherer != start.ID {
		t.Fatalf("brief run is %s held by %q, want gathering by %s", got.State, got.Gatherer, start.ID)
	}
}

// TestGathererFallsBackToThePlannersAgent gives the role file no evidence key, and checks the gatherer's step runs
// under the planner's agent.
func TestGathererFallsBackToThePlannersAgent(t *testing.T) {
	t.Parallel()
	start := gathererTestStarted(t, gathererTestPlannerRoles)
	if start.Agent != "planner-agent" {
		t.Fatalf("agent = %q, want the planner's planner-agent", start.Agent)
	}
}

// TestGathererWakesAndResumeNeverStartASecondStep starts an epic's gatherer, wakes the epic again, then restarts over
// the same store. The restarted runner's Resume and Wake watch the running gatherer, and no second step starts for the
// epic.
func TestGathererWakesAndResumeNeverStartASecondStep(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	gathererTestEpicIn(t, st, repo, base)
	gathererTestAddBrief(t, st, "brief-1", base, []string{"/bin/sh", "-c", "cat marker"})
	steps := &gathererTestSteps{}
	first := gathererTestRunner(t, st, steps, &captureTestDeliveries{}, gathererTestRoles)

	first.Wake(t.Context(), gathererTestEpic)
	waittest.UntilIdle(
		t,
		first.idle,
		"the gatherer's step starts",
		func() bool { return len(steps.startedSteps()) == 1 },
	)
	waittest.UntilIdle(
		t,
		first.idle,
		"the gatherer watches its step",
		func() bool { return steps.subscriptions() == 1 },
	)
	first.Wake(t.Context(), gathererTestEpic)
	first.Close()

	second := gathererTestRunner(t, st, steps, &captureTestDeliveries{}, gathererTestRoles)
	if err := second.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waittest.UntilIdle(t, second.idle, "the restarted runner watches the gatherer", func() bool {
		return steps.subscriptions() == 2
	})
	second.Wake(t.Context(), gathererTestEpic)
	second.Close()

	if got := len(steps.startedSteps()); got != 1 {
		t.Fatalf("started %d steps for epic %s, want 1", got, gathererTestEpic)
	}
	running, err := st.RunningEvidenceGatherers(t.Context())
	if err != nil {
		t.Fatalf("RunningEvidenceGatherers: %v", err)
	}
	if len(running) != 1 || running[0].Step != steps.startedSteps()[0].ID {
		t.Fatalf("running gatherers = %+v, want the one step started", running)
	}
}

// TestGathererDeliversABriefFiledWhileItRuns files a second brief while the gatherer runs, and wakes the epic. The
// brief reaches the gatherer's step once, as an event whose PromptID is the brief's run id, and its text is the brief's
// block.
func TestGathererDeliversABriefFiledWhileItRuns(t *testing.T) {
	t.Parallel()
	repo, base := captureTestRepo(t)
	st := epicTestStore(t)
	gathererTestEpicIn(t, st, repo, base)
	gathererTestAddBrief(t, st, "brief-1", base, []string{"/bin/sh", "-c", "cat marker"})
	steps := &gathererTestSteps{}
	deliveries := &captureTestDeliveries{}
	g := gathererTestRunner(t, st, steps, deliveries, gathererTestRoles)

	g.Wake(t.Context(), gathererTestEpic)
	waittest.UntilIdle(t, g.idle, "the gatherer watches its step", func() bool { return steps.subscriptions() == 1 })
	gathererTestAddBrief(t, st, "brief-2", base, []string{"/bin/sh", "-c", "cat marker"})
	g.Wake(t.Context(), gathererTestEpic)
	waittest.UntilIdle(
		t,
		g.idle,
		"the brief reaches the gatherer",
		func() bool { return len(deliveries.requests()) == 1 },
	)
	g.Close()

	reqs := deliveries.requests()
	if len(reqs) != 1 {
		t.Fatalf("delivered %d events, want 1", len(reqs))
	}
	step := steps.startedSteps()[0].ID
	if reqs[0].ID != step || reqs[0].PromptID != "brief:brief-2" ||
		!strings.HasPrefix(reqs[0].Text, "Brief brief-2:\n") {
		t.Fatalf("delivered %+v, want brief-2's block as an event to %s under brief:brief-2", reqs[0], step)
	}
}

// TestGathererPromptHoldsBriefsEarlierCapturesRunnersAndRules starts a gatherer for two briefs, with an ended capture
// at the first brief's commit and a running capture there too, and a project file committed at the epic's base with a
// runner. It checks the prompt holds each brief, the ended capture with its command, the runner with its note and
// local, the orrery commands the gatherer captures and attaches with from its shell, and the rules. It names no MCP
// tool: a step has none.
func TestGathererPromptHoldsBriefsEarlierCapturesRunnersAndRules(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	base := stepGitTestCommit(t, repo, map[string]string{
		".orrery/project.toml": captureTestProject, "runner.sh": captureTestRunnerScript,
	})
	head := stepGitTestCommit(t, repo, map[string]string{"marker": "head\n"})
	st := epicTestStore(t)
	gathererTestEpicIn(t, st, repo, base)
	gathererTestAddBrief(t, st, "brief-1", head, []string{"/bin/sh", "-c", "cat marker"})
	gathererTestAddBrief(t, st, "brief-2", base, []string{"/bin/sh", "-c", "cat marker"})
	ctx := t.Context()
	for _, capture := range []store.Capture{
		{ID: "ended-capture", Argv: []string{"/bin/sh", "-c", "echo earlier"}, Runner: localRunnerName, Commit: head},
		{ID: "running-capture", Argv: []string{"/bin/sh", "-c", "echo running"}, Runner: localRunnerName, Commit: head},
	} {
		capture.Epic, capture.Repository, capture.Step, capture.Role = gathererTestEpic, repo, "step", captureRoleStep
		capture.Created = time.Unix(1700000001, 0)
		if err := st.AddCapture(ctx, capture); err != nil {
			t.Fatalf("AddCapture %s: %v", capture.ID, err)
		}
	}
	if err := st.RecordCaptureEnd(
		ctx,
		"ended-capture",
		store.EvidenceCapture{At: time.Unix(1700000002, 0)},
	); err != nil {
		t.Fatalf("RecordCaptureEnd: %v", err)
	}
	steps := &gathererTestSteps{}
	g := gathererTestRunner(t, st, steps, &captureTestDeliveries{}, gathererTestRoles)

	g.Wake(ctx, gathererTestEpic)
	waittest.UntilIdle(t, g.idle, "the gatherer's step starts", func() bool { return len(steps.startedSteps()) == 1 })
	prompt := steps.startedSteps()[0].Prompt
	for _, want := range []string{
		"Brief brief-1:\n  item: R1\n  phase: red\n  commit: " + head + "\n",
		"  capture argv: [\"/bin/sh\",\"-c\",\"cat marker\"]\n",
		"  limit: 60 seconds\n",
		"  artifacts: marker\n",
		"  conclusion: the marker says head\n",
		"  ended captures at " + head + ":\n    ended-capture: argv " +
			"[\"/bin/sh\",\"-c\",\"echo earlier\"], runner local, exit 0,",
		"Brief brief-2:\n  item: R1\n  phase: red\n  commit: " + base + "\n",
		"  ended captures at " + base + ":\n    none\n  earlier judged runs of R1, oldest first:\n    none\n",
		"local: always available, with no prefix.",
		"fake: argv prefix [\"/bin/sh\",\"runner.sh\"]; note: Echoes a line, then runs the argv it is given.",
		"Run one capture per distinct command at a commit, with a --brief for every brief it shows.",
		"When the capture ends, orrery attaches it to each of those briefs still open, so do not wait for it",
		"Finish as soon as every brief has a capture started or attached,",
		"Run the narrowest command that shows each conclusion.",
		"Run a heavy command on the runner whose note favors it.",
		"An earlier capture at a brief's commit may be attached to that brief when it shows the conclusion",
		"Commands, run from your shell:",
		"  orrery evidence capture --commit SHA --brief RUN [--brief RUN]... [--runner NAME] [--file PATH]... -- " +
			"ARGV...: runs ARGV at SHA under the runner",
		"  orrery evidence attach CAPTURE RUN...: attaches an ended capture, such as an earlier one at a brief's " +
			"commit, to the briefs it answers",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	for _, tool := range []string{"evidence_capture", "evidence_attach", "mcp__"} {
		if strings.Contains(prompt, tool) {
			t.Errorf("prompt names the MCP tool %q, which a step does not have:\n%s", tool, prompt)
		}
	}
	if strings.Contains(prompt, "running-capture") {
		t.Errorf("prompt lists the capture that has not ended:\n%s", prompt)
	}
}
