package enginehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// gathererBriefRetry is how long a brief waits before it is delivered again to a gatherer that is busy or running its
// gate.
const gathererBriefRetry = 5 * time.Second

// gathererTask is the Task a gatherer step is labeled with.
const gathererTask = "gatherer"

// gathererEventPrefix heads the PromptID of a brief delivered to a gatherer: the prefix and the brief's run id.
const gathererEventPrefix = "brief:"

// gathererSchema is the schema a gatherer's return follows. The step's done value is read from status, which the schema
// declares as a string; note is the reason the gatherer gives for the briefs it leaves unattached.
const gathererSchema = `{"type":"object","additionalProperties":false,"required":["note"],` +
	`"properties":{"status":{"type":"string"},"note":{"type":"string"}}}`

// gathererIDNamespace starts the parts a gatherer's derived step id hashes, so the ids are this package's.
const gathererIDNamespace = "orrery-evidence-gatherer"

// gathererIDLen is the number of hex digits of a gatherer's step id, the form StepStart.ID requires.
const gathererIDLen = 32

// GathererStore is the store a gatherer records its steps in and takes its briefs with. *store.Store satisfies it.
type GathererStore interface {
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	OpenBriefs(ctx context.Context, epic string) ([]store.EvidenceRun, error)
	EpicsWithWaitingBriefs(ctx context.Context) ([]string, error)
	StartEvidenceGatherer(ctx context.Context, epic, step string, at time.Time) (store.EvidenceGatherer, error)
	TakeEvidenceBrief(ctx context.Context, run, step string) error
	EndEvidenceGatherer(
		ctx context.Context, epic, step, leftReason string, at time.Time,
	) ([]store.EvidenceRun, error)
	RunningEvidenceGatherer(ctx context.Context, epic string) (store.EvidenceGatherer, bool, error)
	RunningEvidenceGatherers(ctx context.Context) ([]store.EvidenceGatherer, error)
	EvidenceGathererOf(ctx context.Context, step string) (store.EvidenceGatherer, bool, error)
	CapturesAt(ctx context.Context, repository, commit string) ([]store.Capture, error)
	EpicHost(ctx context.Context, epic string) (string, error)
}

// GatherersConfig configures Gatherers. Store records the gatherers and takes their briefs, Steps starts the gatherer
// steps and subscribes to their ends, and Deliver sends a step its text. Hosts resolves the host each epic is stored on to
// the Host its project file is read through, and a nil Hosts is hostexec.LocalHosts. Getenv reads the environment a step
// runs under. A nil Now is time.Now, and a nil Logger discards. After returns the channel a wake waits on before it reads an
// epic again, and nil is time.After. NewTimer arms a timer that sends on its channel once, after a duration, and
// returns the timer's stop; a nil NewTimer wraps time.NewTimer. A nil Getenv is os.Getenv.
type GatherersConfig struct {
	Store    GathererStore
	Steps    EvidenceSteps
	Deliver  func(context.Context, DeliverRequest) error
	Hosts    hostexec.Hosts
	Getenv   func(string) string
	Now      func() time.Time
	Logger   *slog.Logger
	After    func(time.Duration) <-chan time.Time
	NewTimer func(time.Duration) (<-chan time.Time, func() bool)
}

// Gatherers starts the gatherer step of an epic whose briefs wait, watches it, and delivers each brief filed while it runs.
// Each epic with a gatherer to start or watch has one loop under way, which reads the epic afresh each round. loops holds
// the loop of each such epic, by epic, and drives waits for the loops to return.
type Gatherers struct {
	cfg GatherersConfig

	mu     sync.Mutex
	closed bool
	loops  map[string]*gathererLoop
	drives sync.WaitGroup
}

// gathererLoop is the loop of one epic under way. kick is signaled when a brief is filed or the epic is to be read
// again, and cancel ends the loop.
type gathererLoop struct {
	kick   chan struct{}
	cancel context.CancelFunc
}

// signal asks the loop to read its epic again. A signal already pending stands for this one.
func (l *gathererLoop) signal() {
	select {
	case l.kick <- struct{}{}:
	default:
	}
}

// NewGatherers returns a Gatherers for cfg.
func NewGatherers(cfg GatherersConfig) *Gatherers {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Getenv == nil {
		cfg.Getenv = os.Getenv
	}
	if cfg.Hosts == nil {
		cfg.Hosts = hostexec.LocalHosts()
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	if cfg.NewTimer == nil {
		cfg.NewTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
			timer := time.NewTimer(d)
			return timer.C, timer.Stop
		}
	}
	return &Gatherers{cfg: cfg, loops: map[string]*gathererLoop{}}
}

// Wake starts a gatherer for epic when the epic holds briefs that wait and no gatherer is running for it, and delivers
// each brief that waits to the gatherer running for it. The work runs in the background of the epic's loop, and Wake
// returns at once. Wakes after Close do nothing.
func (g *Gatherers) Wake(ctx context.Context, epic string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	if loop, ok := g.loops[epic]; ok {
		loop.signal()
		return
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	loop := &gathererLoop{kick: make(chan struct{}, 1), cancel: cancel}
	loop.signal()
	g.loops[epic] = loop
	g.drives.Go(func() { g.gather(loopCtx, epic, loop) })
}

// Resume takes up the gatherers the store holds as running after a restart, each under its step, and wakes the epics
// whose briefs wait, so each starts a gatherer. It returns an error when the store cannot be read; the gatherers it
// wakes report their own failures.
func (g *Gatherers) Resume(ctx context.Context) error {
	running, err := g.cfg.Store.RunningEvidenceGatherers(ctx)
	if err != nil {
		return fmt.Errorf("resume gatherers: %w", err)
	}
	waiting, err := g.cfg.Store.EpicsWithWaitingBriefs(ctx)
	if err != nil {
		return fmt.Errorf("resume gatherers: %w", err)
	}
	for _, gatherer := range running {
		g.Wake(ctx, gatherer.Epic)
	}
	for _, epic := range waiting {
		g.Wake(ctx, epic)
	}
	return nil
}

// Close ends the loops under way and waits for each to return. A gatherer's step keeps running, and a restart's Resume
// takes it up again, so Close records no end. Wakes after Close do nothing.
func (g *Gatherers) Close() {
	g.mu.Lock()
	g.closed = true
	for _, loop := range g.loops {
		loop.cancel()
	}
	g.mu.Unlock()
	g.drives.Wait()
}

// gather runs the loop of epic until it has no gatherer to watch and no brief to start one for, or ctx ends. Each round
// takes up the kicks signaled before it, then serves the epic.
func (g *Gatherers) gather(ctx context.Context, epic string, loop *gathererLoop) {
	for {
		select {
		case <-loop.kick:
		default:
		}
		g.serve(ctx, epic, loop)
		if !g.leave(ctx, epic, loop) {
			return
		}
	}
}

// leave reports whether the loop of epic reads the epic again, since a brief was filed while it served. Otherwise it
// removes the loop and cancels it, so the next Wake starts another.
func (g *Gatherers) leave(ctx context.Context, epic string, loop *gathererLoop) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ctx.Err() == nil && len(loop.kick) > 0 {
		return true
	}
	delete(g.loops, epic)
	loop.cancel()
	return false
}

// serve takes up the running gatherer of epic, or starts one when briefs wait for it, and watches it until it ends or
// ctx ends. A failure is logged, and the epic is left for the next Wake.
func (g *Gatherers) serve(ctx context.Context, epic string, loop *gathererLoop) {
	gatherer, running, err := g.cfg.Store.RunningEvidenceGatherer(ctx, epic)
	if err != nil {
		g.logFailure(ctx, err, "read running gatherer", "epic", epic)
		return
	}
	inPrompt := map[string]bool{}
	if !running {
		gatherer, inPrompt, running, err = g.start(ctx, epic)
		if err != nil {
			g.logFailure(ctx, err, "start evidence gatherer", "epic", epic)
			return
		}
		if !running {
			return
		}
	}
	g.watch(ctx, epic, loop, gatherer, inPrompt)
}

// logFailure logs err, with doing naming what the gatherer was doing, unless ctx has ended: a read or start that a shutdown
// cut short is not a failure.
func (g *Gatherers) logFailure(ctx context.Context, err error, doing string, attrs ...any) {
	if ctx.Err() != nil {
		return
	}
	g.cfg.Logger.ErrorContext(ctx, "evidence gatherer failed", append(attrs, "doing", doing, "err", err)...)
}

// start starts a gatherer for epic when briefs wait for one, once the epic is not paused. It returns the gatherer it
// recorded, the ids of the briefs its prompt holds, and whether it started one. None starts when no brief waits.
func (g *Gatherers) start(ctx context.Context, epic string) (store.EvidenceGatherer, map[string]bool, bool, error) {
	row, err := g.awaitResume(ctx, epic)
	if err != nil {
		return store.EvidenceGatherer{}, nil, false, err
	}
	open, err := g.cfg.Store.OpenBriefs(ctx, epic)
	if err != nil {
		return store.EvidenceGatherer{}, nil, false, fmt.Errorf("open briefs of epic %s: %w", epic, err)
	}
	if !slices.ContainsFunc(open, func(run store.EvidenceRun) bool { return run.State == store.RunWaiting }) {
		return store.EvidenceGatherer{}, nil, false, nil
	}
	id, err := g.nextGathererStep(ctx, epic)
	if err != nil {
		return store.EvidenceGatherer{}, nil, false, err
	}
	agent, err := roles.AgentOr(g.cfg.Getenv, "evidence", "planner")
	if err != nil {
		return store.EvidenceGatherer{}, nil, false, fmt.Errorf("agent of the evidence gatherer: %w", err)
	}
	prompt, err := g.prompt(ctx, row, open)
	if err != nil {
		return store.EvidenceGatherer{}, nil, false, err
	}
	revision := row.StoredBase
	if revision == "" {
		revision = row.Base
	}
	step, err := g.cfg.Steps.Start(ctx, StepStart{
		ID: id, Agent: agent, Prompt: prompt, Schema: gathererSchema, Workspace: row.Repository, Revision: revision,
		ReadOnly: true, Epic: epic, Task: gathererTask,
	})
	if errors.Is(err, ErrStepExists) {
		// A previous start stored this step under its id and did not record it, so it is adopted.
		step, err = id, nil
	}
	if err != nil && step == "" {
		return store.EvidenceGatherer{}, nil, false, fmt.Errorf("start gatherer of epic %s: %w", epic, err)
	}
	if err != nil {
		// The step started and then failed, so it ends, and its end is recorded below as any end is.
		g.cfg.Logger.WarnContext(ctx, "evidence gatherer started with an error", "epic", epic, "step", step, "err", err)
	}
	gatherer, err := g.cfg.Store.StartEvidenceGatherer(ctx, epic, step, g.cfg.Now())
	if err != nil {
		return store.EvidenceGatherer{}, nil, false, fmt.Errorf("record gatherer %s of epic %s: %w", step, epic, err)
	}
	inPrompt := make(map[string]bool, len(open))
	for _, run := range open {
		inPrompt[run.ID] = true
	}
	return gatherer, inPrompt, true, nil
}

// nextGathererStep returns the step id of epic's next gatherer. Gatherers are numbered from 1 in the order they start,
// and each one's step id is derived from its number, so the next number is the first whose derived id no recorded
// gatherer holds. A step a previous start stored under that id and did not record is the one start adopts.
func (g *Gatherers) nextGathererStep(ctx context.Context, epic string) (string, error) {
	for seq := int64(1); ; seq++ {
		id := gathererStepID(epic, seq)
		_, recorded, err := g.cfg.Store.EvidenceGathererOf(ctx, id)
		if err != nil {
			return "", fmt.Errorf("read gatherer %d of epic %s: %w", seq, epic, err)
		}
		if !recorded {
			return id, nil
		}
	}
}

// gathererStepID returns the step id of gatherer number seq of epic: 32 hex digits, derived from the epic and the
// number.
func gathererStepID(epic string, seq int64) string {
	sum := sha256.Sum256([]byte(strings.Join(
		[]string{gathererIDNamespace, epic, strconv.FormatInt(seq, 10)}, "\x00",
	)))
	return hex.EncodeToString(sum[:])[:gathererIDLen]
}

// awaitResume returns epic's row once the epic is not paused. While it is paused, it reads the epic again each
// evidencePauseRecheck, as a judge's awaitResume does, so a gatherer starts once the epic is resumed. It returns an
// error when the epic cannot be read, or when ctx ends first.
func (g *Gatherers) awaitResume(ctx context.Context, epic string) (store.EpicRow, error) {
	for {
		row, err := g.cfg.Store.Epic(ctx, epic)
		if err != nil {
			return store.EpicRow{}, fmt.Errorf("epic %s: %w", epic, err)
		}
		if !row.Paused {
			return row, nil
		}
		select {
		case <-ctx.Done():
			return store.EpicRow{}, fmt.Errorf("wait for epic %s to resume: %w", epic, ctx.Err())
		case <-g.cfg.After(evidencePauseRecheck):
		}
	}
}

// watch follows gatherer until it ends or ctx ends. The briefs inPrompt holds are the ones its prompt gave it, so they
// are not delivered again. Every other brief the gatherer holds, and every brief that waits, is delivered to it as an
// event, and a delivery refused as busy is tried again each gathererBriefRetry. The end of the gatherer is recorded
// when its step ends, and the loop then reads the epic again, so a gatherer starts for the briefs the end left waiting.
func (g *Gatherers) watch(
	ctx context.Context, epic string, loop *gathererLoop, gatherer store.EvidenceGatherer, inPrompt map[string]bool,
) {
	sub, err := g.subscribe(ctx, gatherer.Step)
	if err != nil {
		g.logFailure(ctx, err, "watch evidence gatherer", "epic", epic, "step", gatherer.Step)
		return
	}
	defer sub.Close()
	var (
		retry <-chan time.Time
		stop  func() bool
	)
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	delivered := inPrompt
	busy := g.deliver(ctx, epic, gatherer.Step, delivered)
	if busy {
		retry, stop = g.cfg.NewTimer(gathererBriefRetry)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-sub.Ready():
			if row, ok := judgeEndRow(sub.Take()); ok {
				g.recordEnd(ctx, epic, gatherer.Step, row)
				loop.signal()
				return
			}
		case <-loop.kick:
		case <-retry:
			retry, stop = nil, nil
		}
		if g.deliver(ctx, epic, gatherer.Step, delivered) && retry == nil {
			retry, stop = g.cfg.NewTimer(gathererBriefRetry)
		}
	}
}

// subscribe subscribes to the end of step. A Subscribe that fails is logged and tried again each
// evidenceSubscribeRetry, until ctx ends. A step with no row is returned as the error it is, since nothing will end it.
func (g *Gatherers) subscribe(ctx context.Context, step string) (*StepSubscription, error) {
	for {
		sub, err := g.cfg.Steps.Subscribe(ctx, []string{step})
		if err == nil {
			return sub, nil
		}
		if errors.Is(err, store.ErrNoStep) {
			return nil, fmt.Errorf("subscribe to gatherer step %s: %w", step, err)
		}
		g.logFailure(ctx, err, "subscribe to gatherer step", "step", step)
		ticks, stopTimer := g.cfg.NewTimer(evidenceSubscribeRetry)
		select {
		case <-ctx.Done():
			stopTimer()
			return nil, fmt.Errorf("subscribe to gatherer step %s: %w", step, ctx.Err())
		case <-ticks:
		}
	}
}

// deliver takes the waiting briefs of epic for step, and delivers to step each brief it holds that delivered does not
// hold, as an event whose PromptID is the brief's. A brief delivered is added to delivered. It reports whether a
// delivery was refused as busy, so the caller tries again. It returns false when step has ended, since its end is
// recorded by the watch.
func (g *Gatherers) deliver(ctx context.Context, epic, step string, delivered map[string]bool) bool {
	open, err := g.cfg.Store.OpenBriefs(ctx, epic)
	if err != nil {
		g.logFailure(ctx, err, "read briefs of gatherer", "epic", epic, "step", step)
		return false
	}
	row, err := g.cfg.Store.Epic(ctx, epic)
	if err != nil {
		g.logFailure(ctx, err, "read epic of gatherer", "epic", epic, "step", step)
		return false
	}
	busy := false
	for _, run := range open {
		if delivered[run.ID] {
			continue
		}
		if run.State == store.RunWaiting {
			err = g.cfg.Store.TakeEvidenceBrief(ctx, run.ID, step)
			if errors.Is(err, store.ErrEvidenceState) {
				continue
			}
			if err != nil {
				g.logFailure(ctx, err, "take brief for gatherer", "run", run.ID, "step", step)
				continue
			}
			run.Gatherer = step
		}
		if run.Gatherer != step {
			continue
		}
		var text string
		text, err = g.briefText(ctx, row, run)
		if err != nil {
			g.logFailure(ctx, err, "read brief for gatherer", "run", run.ID, "step", step)
			continue
		}
		err = g.cfg.Deliver(ctx, DeliverRequest{ID: step, Text: text, PromptID: gathererEventPrefix + run.ID})
		switch {
		case err == nil:
			delivered[run.ID] = true
		case errors.Is(err, ErrSessionBusy), errors.Is(err, ErrStepGating):
			busy = true
		case errors.Is(err, ErrStepNotRunning), errors.Is(err, store.ErrNoStep):
			return false
		default:
			g.logFailure(ctx, err, "deliver brief to gatherer", "run", run.ID, "step", step)
		}
	}
	return busy
}

// recordEnd records that step, the gatherer of epic, ended, releasing the briefs it held back to waiting with the
// reason its end gives (see gathererLeftReason). A write the store refuses is tried again until it lands or ctx ends,
// and a gatherer left unrecorded at close is watched again by a restart's Resume.
func (g *Gatherers) recordEnd(ctx context.Context, epic, step string, row store.StepRow) {
	reason := gathererLeftReason(row)
	err := retryStore(ctx, g.cfg.After, g.cfg.Logger, "evidence gatherer end", []any{"epic", epic, "step", step},
		func(storeCtx context.Context) error {
			_, err := g.cfg.Store.EndEvidenceGatherer(storeCtx, epic, step, reason, g.cfg.Now())
			if err != nil {
				return fmt.Errorf("end gatherer %s of epic %s: %w", step, epic, err)
			}
			return nil
		})
	if err != nil {
		g.cfg.Logger.ErrorContext(ctx, "evidence gatherer end unstored at close, left for a restart",
			"epic", epic, "step", step, "err", err)
	}
}

// gathererLeftReason returns the reason a gatherer's end gives for the briefs it leaves unattached: the note its return
// gives when it ends done with one, and otherwise its end, with the reason the row gives.
func gathererLeftReason(row store.StepRow) string {
	if row.End == stepLoopEndDone {
		var ret struct {
			Note string `json:"note"`
		}
		if json.Unmarshal([]byte(row.Result), &ret) == nil && ret.Note != "" {
			return ret.Note
		}
	}
	if row.Reason == "" {
		return row.End
	}
	return row.End + ": " + row.Reason
}

// briefText returns the block a gatherer is told of run, with the ended captures at run's commit in the repository of
// row.
func (g *Gatherers) briefText(ctx context.Context, row store.EpicRow, run store.EvidenceRun) (string, error) {
	captures, err := g.endedCapturesAt(ctx, row.Repository, run.Revision)
	if err != nil {
		return "", err
	}
	return strings.Join(briefBlock(run, captures), "\n"), nil
}

// endedCapturesAt returns the ended captures of repository at commit, in the order CapturesAt gives them.
func (g *Gatherers) endedCapturesAt(ctx context.Context, repository, commit string) ([]store.Capture, error) {
	captures, err := g.cfg.Store.CapturesAt(ctx, repository, commit)
	if err != nil {
		return nil, fmt.Errorf("captures at %s: %w", commit, err)
	}
	return slices.DeleteFunc(captures, func(capture store.Capture) bool { return capture.Ended.IsZero() }), nil
}

// prompt returns the prompt of the gatherer of the epic row describes, which holds each brief of open, the ended
// captures at each brief's commit, and the runners the project names at the epic's base, read on the host the epic
// lives on.
func (g *Gatherers) prompt(ctx context.Context, row store.EpicRow, open []store.EvidenceRun) (string, error) {
	host, err := hostOfEpic(ctx, g.cfg.Store, g.cfg.Hosts, row.ID)
	if err != nil {
		return "", err
	}
	runners, err := ReadProjectRunners(ctx, host, row.Repository, row.Base)
	if err != nil {
		return "", fmt.Errorf("runners of epic %s: %w", row.ID, err)
	}
	blocks := make([]string, 0, len(open))
	for _, run := range open {
		var captures []store.Capture
		captures, err = g.endedCapturesAt(ctx, row.Repository, run.Revision)
		if err != nil {
			return "", err
		}
		blocks = append(blocks, strings.Join(briefBlock(run, captures), "\n"))
	}
	return gathererPrompt(row.ID, runners, blocks), nil
}

// briefBlock returns the lines that tell a gatherer of run: its run id, item, phase, commit, capture argv, limit,
// artifacts and conclusion, then each capture of its commit that has ended, with its id, argv, runner, exit, whether it
// timed out and its end, or a line that there is none.
func briefBlock(run store.EvidenceRun, captures []store.Capture) []string {
	lines := []string{
		"Brief " + run.ID + ":",
		"  item: " + run.Item,
		"  phase: " + run.Phase,
		"  commit: " + run.Revision,
		"  capture argv: " + argvText(run.Spec.Capture),
		fmt.Sprintf("  limit: %d seconds", run.Spec.Limit),
		"  artifacts: " + listOrNone(run.Spec.Artifacts),
		"  conclusion: " + run.Spec.Conclusion,
		"  ended captures at " + run.Revision + ":",
	}
	if len(captures) == 0 {
		lines = append(lines, "    none")
	}
	for _, capture := range captures {
		lines = append(lines, fmt.Sprintf(
			"    %s: argv %s, runner %s, exit %d, timed out %t, ended %s",
			capture.ID, argvText(capture.Argv), capture.Runner, capture.Exit, capture.TimedOut,
			capture.Ended.UTC().Format(time.RFC3339),
		))
	}
	return lines
}

// gathererPrompt returns the prompt of the gatherer of epic. It says what the gatherer is for, lists the runners a
// capture can run under, with local first, names the tools that capture and attach evidence, and states the rules it
// gathers by. blocks are the briefs, each as briefBlock gives it.
func gathererPrompt(epic string, runners []ProjectRunner, blocks []string) string {
	lines := make([]string, 0, 3+2*len(blocks)+len(runners)+12)
	lines = append(lines,
		"You are the evidence gatherer of epic "+epic+". Each brief below asks for evidence that one conclusion holds "+
			"at one commit, in one phase. Evidence reaches a brief only as a capture: a command orrery runs at a commit, "+
			"which you start with evidence_capture and attach with evidence_attach. This step is read-only: change no file.",
		"",
		"Briefs:",
	)
	for _, block := range blocks {
		lines = append(lines, block, "")
	}
	lines = append(lines,
		"Runners. A capture runs under one of these, and its argv prefix runs ahead of the capture's argv.",
		"  local: always available, with no prefix.",
	)
	for _, runner := range runners {
		lines = append(
			lines,
			fmt.Sprintf("  %s: argv prefix %s; note: %s", runner.Name, argvText(runner.Argv), runner.Note),
		)
	}
	lines = append(
		lines,
		"",
		"Tools:",
		"  evidence_capture (commit, argv, runner, briefs, files): runs argv at commit under runner, as a capture of the "+
			"briefs it answers, and keeps the files listed. Its end arrives as an event; do not poll for it.",
		"  evidence_attach (capture, briefs): attaches a capture to the briefs it answers.",
		"",
		"Rules:",
		"  Run one capture per distinct command at a commit, and attach it to every brief it shows.",
		"  Run the narrowest command that shows each conclusion.",
		"  Run a heavy command on the runner whose note favors it.",
		"  An earlier capture at a brief's commit may be attached to that brief when it shows the conclusion; "+
			"judge that yourself.",
		"",
		"Finish with status done and a note: what you attached, and the reason any brief is left without a capture.",
	)
	return strings.Join(lines, "\n")
}

// argvText returns argv as a list of quoted strings, the form a brief and a capture name a command in.
func argvText(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = strconv.Quote(arg)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

var _ EvidenceGatherers = (*Gatherers)(nil)
