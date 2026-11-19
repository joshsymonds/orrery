package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// errKilled is what an action returns once its incarnation is killed. The action is refused and does nothing.
var errKilled = errors.New("run killed: action refused")

// gate is one incarnation's action budget. It performs its first limit actions and refuses every action after them, and
// the refusal cancels the incarnation's context. A negative limit performs every action.
type gate struct {
	limit  int
	count  int
	dead   bool
	cancel context.CancelFunc
}

// crashWorld is one run's store and fakes. Each incarnation builds fresh components over it. Every store write and
// every outside effect of the components and fakes is an action of the world, performed through act in order.
type crashWorld struct {
	t        *testing.T
	store    *store.Store
	dbPath   string
	repo     string
	base     string
	stateDir string
	getenv   func(string) string

	mu      sync.Mutex
	gate    *gate
	clock   time.Time
	labels  []string
	pending []func()
	starts  map[string]int
	sends   map[string]int
	routed  map[string]int
}

// wrapErr wraps err with what, or returns nil when err is nil. A store error that a fake returns is wrapped through it.
func wrapErr(what string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// act performs op as the next action, labeled label, unless the incarnation is killed: the first action past the
// gate's limit is refused, cancels the incarnation's context, and returns errKilled, as does every action after it.
func (w *crashWorld) act(label string, op func() error) error {
	w.mu.Lock()
	g := w.gate
	if g.dead {
		w.mu.Unlock()
		return errKilled
	}
	g.count++
	if g.limit >= 0 && g.count > g.limit {
		g.dead = true
		w.mu.Unlock()
		g.cancel()
		return errKilled
	}
	w.labels = append(w.labels, label)
	w.mu.Unlock()
	return op()
}

// effect performs an action that has no store write and no error: do runs once the action is performed. A refused effect
// does nothing, and the refusal is the kill's, so there is nothing for its caller to report.
func (w *crashWorld) effect(label string, do func()) {
	if err := w.act(label, func() error {
		do()
		return nil
	}); err != nil {
		return
	}
}

// killed reports whether the current incarnation's gate has refused an action.
func (w *crashWorld) killed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.gate.dead
}

// now returns the fake clock's time.
func (w *crashWorld) now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clock
}

// setClock sets the fake clock to the time of round.
func (w *crashWorld) setClock(round int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clock = roundAt(round)
}

// setGate starts an incarnation under g.
func (w *crashWorld) setGate(g *gate) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.gate = g
}

// queue holds a publish report until the router is idle, when drain calls it.
func (w *crashWorld) queue(sent func()) {
	if sent == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, sent)
}

// drain calls the publish reports queued since the last drain, in order.
func (w *crashWorld) drain() {
	w.mu.Lock()
	queued := w.pending
	w.pending = nil
	w.mu.Unlock()
	for _, sent := range queued {
		sent()
	}
}

// count adds one to the outside log's count of key in counts.
func (w *crashWorld) count(counts map[string]int, key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	counts[key]++
}

// implementerRef is the ref the fake implementer moves before it returns. It is a ref the build's check reports as a
// tell, not as a move that parks the epic.
const implementerRef = "refs/heads/scratch-implementer"

// finishOpen ends each open step that is not limited. It is the work a resumed step does after its resume: the
// implementer moves its scratch ref, which is a git ref write, and then returns. Each is one action.
func (w *crashWorld) finishOpen(ctx context.Context) error {
	rows, err := w.store.OpenSteps(ctx)
	if err != nil {
		return wrapErr("list open steps", err)
	}
	for _, row := range rows {
		if row.LoopLimited {
			continue
		}
		err = w.act("git ref write", func() error {
			_, gitErr := runGit(ctx, w.repo, "update-ref", implementerRef, w.base)
			return wrapErr("move implementer ref", gitErr)
		})
		if err != nil {
			return err
		}
		ops := []store.EngineOp{
			store.StepReturn{ID: row.ID, Result: implementerReturnJSON(w.base)},
			store.StepEnd{ID: row.ID, End: endDone},
		}
		err = w.act("step return", func() error {
			return wrapErr("end step "+row.ID, w.store.ApplyEngine(ctx, ops, w.now()))
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// implementerReturnJSON is the kept return of an implementer step that finished at base.
func implementerReturnJSON(base string) string {
	return fmt.Sprintf(`{"summary":"done","commit":%q}`, base)
}

// crashPlanReturn is the planner's return for a turn that plans one task, with no questions.
const crashPlanReturn = `{"status":"done","decisions":["use the store"],` +
	`"tasks":[{"title":"Build the thing","deliverable":"the thing holds","files":["thing.go"],` +
	`"requirements":["R1"],"gate":"check","evidence":{"capture":["scripts/check"],"limit":60,"artifacts":[],` +
	`"supplied":[],"conclusion":"the thing holds","judge":"the judge"}}],"outline":"one task","questions":[],` +
	`"outcome":"work"}`

// crashPlannerAgent is the agent the planner's steps run under.
const crashPlannerAgent = "crash-planner"

// crashLimitKind is the usage limit a limited step hits, and crashLimitFor how long after its start it resets.
const (
	crashLimitKind = "five_hour"
	crashLimitFor  = time.Hour
)

// crashResumeText is the prompt an automatic resume sends a limited step.
const crashResumeText = "the usage limit has reset; continue"

// startStep is a step start: it writes the step's row, with the limit or the return the step has from its start, in one
// action named label. The planner's turn 1 step is limited at its start and stops there, and a turn after it returns
// the plan at once. An implementer step is limited once at its start and finishes after its automatic resume. The start
// is one action, so no kill lands between its row and its effect.
func (w *crashWorld) startStep(ctx context.Context, label string, start enginehost.StepStart) (string, error) {
	ops := []store.EngineOp{store.StepAdd{
		ID: start.ID, Agent: start.Agent, Prompt: start.Prompt, Schema: start.Schema, Workspace: start.Workspace,
		Revision: start.Revision, ReadOnly: start.ReadOnly, Owned: start.Owned, DoneValue: start.DoneValue,
		Epic: start.Epic, Task: start.Task,
	}}
	if start.Agent != crashPlannerAgent {
		ops = append(ops, w.limitOps(start.ID)...)
	} else {
		turn, err := w.runningTurn(ctx, start.Epic)
		if err != nil {
			return "", err
		}
		if turn == 1 {
			ops = append(ops, w.limitOps(start.ID)...)
		} else {
			ops = append(ops,
				store.StepReturn{ID: start.ID, Result: crashPlanReturn},
				store.StepEnd{ID: start.ID, End: endDone},
			)
		}
	}
	err := w.act(label, func() error {
		return wrapErr("write step "+start.ID, w.store.ApplyEngine(ctx, ops, w.now()))
	})
	if err != nil {
		return "", fmt.Errorf("start step %s: %w", start.ID, err)
	}
	w.count(w.starts, start.ID)
	return start.ID, nil
}

// limitOps are the ops that limit step id at a usage limit that resets crashLimitFor from now. The limit stores the
// step's due row in the same write.
func (w *crashWorld) limitOps(id string) []store.EngineOp {
	resets := w.now().Add(crashLimitFor)
	return []store.EngineOp{
		store.EngineRateLimited{ID: id, Status: "rejected", Kind: crashLimitKind, ResetsAt: resets},
		store.StepLoop{ID: id, Limited: true, LimitKind: crashLimitKind, LimitResets: resets},
	}
}

// runningTurn returns the seq of the running turn of epic.
func (w *crashWorld) runningTurn(ctx context.Context, epic string) (int64, error) {
	row, err := w.store.Epic(ctx, epic)
	if err != nil {
		return 0, wrapErr("read epic "+epic, err)
	}
	for _, turn := range row.Turns {
		if turn.State == "running" {
			return turn.Seq, nil
		}
	}
	return 0, fmt.Errorf("epic %s has no running turn", epic)
}

// stepRow reads the row of step id.
func (w *crashWorld) stepRow(ctx context.Context, id string) (store.StepRow, error) {
	row, err := w.store.Step(ctx, id)
	if err != nil {
		return store.StepRow{}, wrapErr("read step "+id, err)
	}
	return row, nil
}

// crashBuildSteps is the Steps the build runner starts its units on.
type crashBuildSteps struct{ w *crashWorld }

func (s crashBuildSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	return s.w.startStep(ctx, "build step start", start)
}

func (s crashBuildSteps) Status(ctx context.Context, id string) (store.StepRow, error) {
	return s.w.stepRow(ctx, id)
}

// crashPlanSteps is the Steps the planner runner starts its turns' steps on.
type crashPlanSteps struct{ w *crashWorld }

func (s crashPlanSteps) Start(ctx context.Context, start enginehost.StepStart) (string, error) {
	return s.w.startStep(ctx, "planner step start", start)
}

func (s crashPlanSteps) Status(ctx context.Context, id string) (store.StepRow, error) {
	return s.w.stepRow(ctx, id)
}

// Wait returns the step's row once it has ended. A limited step's row comes back as open, so the runner stops it. A
// step that is open and not limited never occurs in these runs, and Wait says so rather than spin.
func (s crashPlanSteps) Wait(
	ctx context.Context, ids []string, _ time.Duration,
) (*store.StepRow, []store.StepRow, error) {
	row, err := s.w.stepRow(ctx, ids[0])
	if err != nil {
		return nil, nil, err
	}
	switch {
	case row.End != "":
		return &row, nil, nil
	case row.LoopLimited:
		return nil, []store.StepRow{row}, nil
	}
	return nil, nil, fmt.Errorf("step %s is open and not limited", ids[0])
}

// Stop ends step id as stopped, one action.
func (s crashPlanSteps) Stop(ctx context.Context, id string) error {
	return s.w.act("planner step stop", func() error {
		return wrapErr("stop step "+id, s.w.store.ApplyEngine(ctx, []store.EngineOp{
			store.StepEnd{ID: id, End: endStopped},
		}, s.w.now()))
	})
}

// crashResume is the DueSteps a due pass resumes limited steps through. It follows the engine's order: the resume
// prompt, which is skipped when a prompt of that id is stored, then one write that clears the limit and closes the
// step's due rows. The prompt's record and its send are one action, so no kill lands between them: the window the
// engine has there is not one of the harness's kill points.
type crashResume struct{ w *crashWorld }

func (r crashResume) AutoResume(ctx context.Context, id string, resets time.Time) error {
	row, err := r.w.stepRow(ctx, id)
	if err != nil {
		return err
	}
	if row.End != "" {
		return enginehost.ErrStepNotRunning
	}
	if !row.LoopLimited || !row.LoopLimitResets.Equal(resets) {
		return enginehost.ErrStepNotLimited
	}
	promptID := fmt.Sprintf("%s:resume:%d", id, resets.Unix())
	sent, err := r.w.store.EnginePromptSent(ctx, id, promptID)
	if err != nil {
		return wrapErr("check resume prompt of step "+id, err)
	}
	if !sent {
		err = r.w.act("resume prompt", func() error {
			prompt := store.EnginePrompt{
				ID: id, Text: crashResumeText, PromptID: promptID, Sender: store.PromptSenderEngine,
			}
			if err := r.w.store.ApplyEngine(ctx, []store.EngineOp{prompt}, r.w.now()); err != nil {
				return wrapErr("record resume prompt of step "+id, err)
			}
			r.w.count(r.w.sends, promptID)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return r.w.act("resume clear", func() error {
		return wrapErr("clear limit of step "+id, r.w.store.ApplyEngine(ctx, []store.EngineOp{
			store.EngineRateLimitCleared{ID: id},
			store.StepLoop{ID: id},
			store.StepDueResumesClosed{ID: id},
		}, r.w.now()))
	})
}

// crashEvidence is the Evidence the build runner starts runs on. A run starts and finishes in one action: its capture
// and verdict are stored with it, red failing at the base and green holding.
type crashEvidence struct{ w *crashWorld }

func (e crashEvidence) RunTask(
	ctx context.Context, id, epic string, seq int64, revision, phase string,
) (store.EvidenceRun, error) {
	exit, verdict := 0, evidence.VerdictHolds
	if phase == evidence.PhaseRed {
		exit, verdict = 1, evidence.VerdictFails
	}
	err := e.w.act("evidence run start", func() error {
		if err := e.w.runEvidence(ctx, id, epic, seq, revision, phase, exit, verdict); err != nil {
			return err
		}
		e.w.count(e.w.starts, id)
		return nil
	})
	if err != nil {
		return store.EvidenceRun{}, fmt.Errorf("start evidence run %s: %w", id, err)
	}
	run, err := e.w.store.EvidenceRun(ctx, id)
	if err != nil {
		return store.EvidenceRun{}, wrapErr("read evidence run "+id, err)
	}
	return run, nil
}

// runEvidence stores a run of phase with its capture and verdict.
func (w *crashWorld) runEvidence(
	ctx context.Context, id, epic string, seq int64, revision, phase string, exit int, verdict string,
) error {
	spec, err := taskItem(ctx, w.store, epic, seq)
	if err != nil {
		return err
	}
	at := w.now()
	if err := w.store.AddEvidenceRun(ctx, store.EvidenceRun{
		ID: id, Epic: epic, Item: taskLabel(seq), Revision: revision, Phase: phase, Source: evidence.SourceCaptured,
		Spec: spec, Created: at,
	}); err != nil {
		return wrapErr("add evidence run "+id, err)
	}
	if err := w.store.RecordEvidenceCapture(ctx, id, store.EvidenceCapture{
		Exit: exit, LogHash: "loghash", LogBytes: 3, At: at,
	}); err != nil {
		return wrapErr("record capture of "+id, err)
	}
	return wrapErr("record verdict of "+id, w.store.RecordEvidenceVerdict(ctx, id, store.EvidenceVerdict{
		Verdict: verdict, Reason: "the judge read the log", Passed: evidence.Passed(phase, "", exit, verdict), At: at,
	}))
}

// Artifact returns no log: the crash runs read no red log.
func (e crashEvidence) Artifact(_ context.Context, _, _ string) ([]byte, string, error) {
	return nil, "", nil
}

// StderrLog returns no log: the crash runs store no stderr log, so the build reads none.
func (e crashEvidence) StderrLog(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", nil
}

// crashGit is the Git the build runner writes repository refs and worktrees through, one action per write.
type crashGit struct{ w *crashWorld }

func (g crashGit) CreateBranch(ctx context.Context, repo, epic, base string) error {
	return g.w.act("git branch create", func() error {
		return wrapErr("create branch", CreateBranch(ctx, repo, epic, base))
	})
}

func (g crashGit) Pin(ctx context.Context, repo, ref, commit string) error {
	return g.w.act("git ref pin", func() error {
		return wrapErr("pin "+ref, Pin(ctx, repo, ref, commit))
	})
}

func (g crashGit) AddWorktree(ctx context.Context, repo, path, base string) error {
	return g.w.act("git worktree add", func() error {
		return wrapErr("add worktree", AddWorktree(ctx, repo, path, base))
	})
}

func (g crashGit) MoveWorktree(ctx context.Context, path, wip, to string) error {
	return g.w.act("git worktree move", func() error {
		return wrapErr("move worktree", MoveWorktree(ctx, path, wip, to))
	})
}

func (g crashGit) RemoveWorktrees(ctx context.Context, repo, dir string) error {
	return g.w.act("git worktree remove", func() error {
		return wrapErr("remove worktrees", RemoveWorktrees(ctx, repo, dir))
	})
}

// crashPhone is the push sink. A push handed to it is an effect, and its publish report runs once the router is idle.
type crashPhone struct{ w *crashWorld }

func (p crashPhone) Notify(
	_ context.Context, _ notice.Key, _ func(context.Context) (push.Notification, error), sent func(),
) {
	p.w.effect("phone notify", func() { p.w.queue(sent) })
}

func (p crashPhone) Seen(_ context.Context, _ notice.Key, _ int64) {
	p.w.effect("phone seen", func() {})
}

// crashDesk is the desk sink. The desk is never present in these runs, so a notice is posted quietly and goes to the
// phone.
type crashDesk struct{ w *crashWorld }

func (d crashDesk) Post(deskstream.Notice) {
	d.w.effect("desk post", func() {})
}

func (d crashDesk) Desk() int64 {
	return 0
}

// crashLog is the notice log. Each line is one action.
type crashLog struct{ w *crashWorld }

func (l crashLog) Write(route.Line) error {
	return l.w.act("notice line", func() error { return nil })
}

// crashRouter is the router the attention pass routes through. Each Notify is one notice route. Once the router is idle
// the route is counted for its id if its notice row is stored, and then the publish reports it queued run. A publish
// report deletes its row, so the count is taken first.
type crashRouter struct {
	w      *crashWorld
	router *route.Router
}

func (r crashRouter) Notify(ctx context.Context, n route.Notice) {
	id := noticeID(n.Source.Data)
	err := r.w.act("notice route", func() error {
		r.router.Notify(ctx, n)
		return nil
	})
	if err != nil {
		return
	}
	r.router.Wait()
	defer r.w.drain()
	stored, err := r.w.noticeStored(context.WithoutCancel(ctx), id)
	if err != nil {
		r.w.t.Errorf("read notice rows: %v", err)
		return
	}
	if stored {
		r.w.count(r.w.routed, id)
	}
}

func (r crashRouter) Seen(ctx context.Context, key notice.Key, seen int64) {
	r.router.Seen(ctx, key, seen)
}

// noticeID returns the id of the epic notice whose source data is data, or "" when data does not carry one.
func noticeID(data []byte) string {
	var stored struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return ""
	}
	return stored.ID
}

// noticeStored reports whether a stored notice row carries the notice id.
func (w *crashWorld) noticeStored(ctx context.Context, id string) (bool, error) {
	rows, err := w.store.Notices(ctx)
	if err != nil {
		return false, wrapErr("list notice rows", err)
	}
	for _, row := range rows {
		if noticeID(row.Source) == id {
			return true, nil
		}
	}
	return false, nil
}

// crashStore is the store the components write through. Its writes are actions of the world, and its reads go to the
// store as they are.
type crashStore struct {
	*store.Store
	w *crashWorld
}

func (s crashStore) ClaimTurn(ctx context.Context, epic string, at time.Time) (store.EpicTurn, error) {
	var turn store.EpicTurn
	err := s.w.act("ClaimTurn", func() error {
		var err error
		turn, err = s.Store.ClaimTurn(ctx, epic, at)
		return wrapErr("claim turn of "+epic, err)
	})
	return turn, err
}

func (s crashStore) SetTurnStep(ctx context.Context, epic string, turn int64, step string) error {
	return s.w.act("SetTurnStep", func() error {
		return wrapErr("record step of turn", s.Store.SetTurnStep(ctx, epic, turn, step))
	})
}

func (s crashStore) CompleteTurn(
	ctx context.Context,
	epic string,
	turn int64,
	plan store.EpicPlan,
	at time.Time,
) error {
	return s.w.act("CompleteTurn", func() error {
		return wrapErr("complete turn", s.Store.CompleteTurn(ctx, epic, turn, plan, at))
	})
}

func (s crashStore) FailTurn(ctx context.Context, epic string, turn int64, reason string, at time.Time) error {
	return s.w.act("FailTurn", func() error {
		return wrapErr("fail turn", s.Store.FailTurn(ctx, epic, turn, reason, at))
	})
}

func (s crashStore) FreeTurn(ctx context.Context, epic string, turn int64, free store.TurnFree, at time.Time) error {
	return s.w.act("FreeTurn", func() error {
		return wrapErr("free turn", s.Store.FreeTurn(ctx, epic, turn, free, at))
	})
}

func (s crashStore) SetTaskBuild(ctx context.Context, epic string, seq int64, from, to store.TaskBuild) error {
	return s.w.act("SetTaskBuild", func() error {
		return wrapErr("set build", s.Store.SetTaskBuild(ctx, epic, seq, from, to))
	})
}

func (s crashStore) ClimbTask(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, move store.RungMove,
	version *store.TaskVersionInput, decision *store.TaskDecision, at time.Time,
) error {
	return s.w.act("ClimbTask", func() error {
		return wrapErr("climb task", s.Store.ClimbTask(ctx, epic, seq, from, to, move, version, decision, at))
	})
}

func (s crashStore) GrantTaskFiles(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, grant store.TaskVersionInput,
	at time.Time,
) error {
	return s.w.act("GrantTaskFiles", func() error {
		return wrapErr("grant files", s.Store.GrantTaskFiles(ctx, epic, seq, from, to, grant, at))
	})
}

func (s crashStore) RerunTask(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, version store.TaskVersionInput,
	behind []int64, decision store.TaskDecision, at time.Time,
) error {
	return s.w.act("RerunTask", func() error {
		return wrapErr("rerun task", s.Store.RerunTask(ctx, epic, seq, from, to, version, behind, decision, at))
	})
}

func (s crashStore) AskTask(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, question store.EpicQuestionInput,
	tell string, decision store.TaskDecision, at time.Time,
) error {
	return s.w.act("AskTask", func() error {
		return wrapErr("ask task", s.Store.AskTask(ctx, epic, seq, from, to, question, tell, decision, at))
	})
}

func (s crashStore) SettleTaskVersion(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, settle store.TaskVersionSettle,
	at time.Time,
) error {
	return s.w.act("SettleTaskVersion", func() error {
		return wrapErr("settle version", s.Store.SettleTaskVersion(ctx, epic, seq, from, to, settle, at))
	})
}

func (s crashStore) AcceptTask(ctx context.Context, epic string, seq int64, from store.TaskBuild, review string) error {
	return s.w.act("AcceptTask", func() error {
		return wrapErr("accept task", s.Store.AcceptTask(ctx, epic, seq, from, review))
	})
}

func (s crashStore) StopTask(ctx context.Context, epic string, seq int64, from, to store.TaskBuild) error {
	return s.w.act("StopTask", func() error {
		return wrapErr("stop task", s.Store.StopTask(ctx, epic, seq, from, to))
	})
}

func (s crashStore) SetTaskBuildTelling(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, tells []string, at time.Time,
) error {
	return s.w.act("SetTaskBuildTelling", func() error {
		return wrapErr("set build and tells", s.Store.SetTaskBuildTelling(ctx, epic, seq, from, to, tells, at))
	})
}

func (s crashStore) ParkTask(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, class store.ParkClass, reason string,
	at time.Time, tells ...string,
) error {
	return s.w.act("ParkTask", func() error {
		return wrapErr("park task", s.Store.ParkTask(ctx, epic, seq, from, to, class, reason, at, tells...))
	})
}

func (s crashStore) ParkEpic(
	ctx context.Context,
	epic string,
	class store.ParkClass,
	reason string,
	at time.Time,
) error {
	return s.w.act("ParkEpic", func() error {
		return wrapErr("park epic", s.Store.ParkEpic(ctx, epic, class, reason, at))
	})
}

func (s crashStore) SetEpicBranch(ctx context.Context, epic, state string) error {
	return s.w.act("SetEpicBranch", func() error {
		return wrapErr("set branch", s.Store.SetEpicBranch(ctx, epic, state))
	})
}

func (s crashStore) AddTaskReview(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, review store.TaskReview,
) error {
	return s.w.act("AddTaskReview", func() error {
		return wrapErr("add review", s.Store.AddTaskReview(ctx, epic, seq, from, to, review))
	})
}

func (s crashStore) SetFindingResult(
	ctx context.Context, epic string, seq int64, from, to store.TaskBuild, result store.TaskFindingResult,
) error {
	return s.w.act("SetFindingResult", func() error {
		return wrapErr("set finding", s.Store.SetFindingResult(ctx, epic, seq, from, to, result))
	})
}

func (s crashStore) MarkTold(ctx context.Context, epic string, seq int64) error {
	return s.w.act("MarkTold", func() error {
		return wrapErr("mark turn told", s.Store.MarkTold(ctx, epic, seq))
	})
}

func (s crashStore) MarkTellTold(ctx context.Context, epic string, seq int64) error {
	return s.w.act("MarkTellTold", func() error {
		return wrapErr("mark tell told", s.Store.MarkTellTold(ctx, epic, seq))
	})
}

func (s crashStore) PutNotice(ctx context.Context, row store.NoticeRow) error {
	return s.w.act("PutNotice", func() error {
		return wrapErr("put notice row", s.Store.PutNotice(ctx, row))
	})
}

func (s crashStore) DeleteNotice(ctx context.Context, id int64) error {
	return s.w.act("DeleteNotice", func() error {
		return wrapErr("delete notice row", s.Store.DeleteNotice(ctx, id))
	})
}

func (s crashStore) CloseDueResume(ctx context.Context, id string) error {
	return s.w.act("CloseDueResume", func() error {
		return wrapErr("close due resume", s.Store.CloseDueResume(ctx, id))
	})
}

func (s crashStore) ResumeDueEpic(ctx context.Context, id string) (bool, error) {
	var resumed bool
	err := s.w.act("ResumeDueEpic", func() error {
		var err error
		resumed, err = s.Store.ResumeDueEpic(ctx, id)
		return wrapErr("resume due epic", err)
	})
	return resumed, err
}

// crashResolver answers that every gate and judge resolves. The fake steps never run a return check.
type crashResolver struct{}

func (crashResolver) Gate(context.Context, store.EpicRow, string) error { return nil }

func (crashResolver) Judge(store.EpicRow, string) error { return nil }

// crashAgent is the planner's agent, read once per pass.
func crashAgent() (string, error) {
	return crashPlannerAgent, nil
}
