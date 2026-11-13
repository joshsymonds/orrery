package route

import (
	"cmp"
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
)

const (
	// WatchRecheck is how long a watched notice waits before it is checked again.
	WatchRecheck = 5 * time.Second
	// DeskWindow is how long after the desk's last input the desk still counts as at hand.
	DeskWindow = 2 * time.Minute
	// Escalation is how long a notice at the desk waits unseen before the phone is pushed.
	Escalation = 5 * time.Minute
	// JudgeDelay is how long a judged notice waits unwatched before it is judged.
	JudgeDelay = 10 * time.Second
	// JudgeTimeout is how long a judge may run before its context is canceled. A judge still running then is late.
	JudgeTimeout = 15 * time.Second
)

// Reasons written to the log. Clauses join with "; ", and a verdict's Why follows its word after ": ".
const (
	reasonAtDesk         = "at the desk"
	reasonAway           = "away"
	reasonWatching       = "watching"
	reasonUnseen         = "unseen after 5 minutes"
	reasonChanged        = "changed"
	reasonSeen           = "seen"
	reasonShutdown       = "shutdown"
	reasonUnneeded       = "unneeded"
	reasonNeeded         = "needed"
	reasonJudgeFailed    = "judge failed"
	reasonJudgeLate      = "judge late"
	reasonDeskFailed     = "desk notice failed"
	reasonNoSubscription = "no push subscription"
	reasonNotRestored    = "not restored"
)

// Verdict is Judge's answer on a notice. Needed routes the notice as usual. Why, when not empty, is written to the
// log.
type Verdict struct {
	Needed bool
	Why    string
}

// DeskSink posts notices to the desk and reports whether the desk is present. *deskstream.Hub satisfies it.
type DeskSink interface {
	Post(deskstream.Notice)
	Desk() (input int64, present bool)
}

// PhoneSink sends push notices and clears. *notice.Notices satisfies it.
type PhoneSink interface {
	Notify(ctx context.Context, key notice.Key, build func(context.Context) (push.Notification, error), sent func())
	Seen(ctx context.Context, key notice.Key, seen int64)
}

// Recorder writes one Line to the notice log. *Log satisfies it.
type Recorder interface {
	Write(Line) error
}

// Source is what a notice needs to be rebuilt after a restart. Kind names the source and Data is its bytes. A zero
// Kind means the notice has no source.
type Source struct {
	Kind string
	Data []byte
}

// Notice is one waiting or done notice to route. A non-empty Silent is the reason it goes nowhere. A nil
// Watching is never watched. A nil Judge is never judged. A Source with a Kind keeps the notice's row in the store.
type Notice struct {
	Key      notice.Key
	State    string
	Silent   string
	Source   Source
	Watching func(context.Context) (bool, error)
	Judge    func(context.Context) (Verdict, error)
	Desk     func(context.Context) (deskstream.Notice, error)
	Push     func(context.Context) (push.Notification, error)
}

// Rebuild makes the notice that a stored row of its source kind described, from the row's source data. Its
// Watching, Judge, Desk and Push are the ones the restored notice routes with.
type Rebuild func(ctx context.Context, data []byte) (Notice, error)

// Config holds the Router's collaborators. A nil After uses time.AfterFunc, a nil LocalInput reports no input,
// and a nil Logger discards. Rebuild maps each source kind to its Rebuild; a stored row of a kind it lacks is not
// restored. Attach calls each Rebuild under the Router's lock, so a Rebuild must not call the Router.
type Config struct {
	Host       string
	Now        func() time.Time
	After      func(d time.Duration, f func()) (stop func() bool)
	Desk       DeskSink
	Phone      PhoneSink
	Subscribed func() (bool, error)
	LocalInput func(context.Context) (input int64, ok bool, err error)
	Log        Recorder
	Logger     *slog.Logger
	Rebuild    map[string]Rebuild
}

// Router decides where each notice goes and writes one Line per ping or silence.
type Router struct {
	cfg Config

	mu sync.Mutex
	// idle is signaled when running drops to zero. It is tied to mu.
	idle *sync.Cond
	// running counts the routing decisions that have started and not finished.
	running int
	seq     uint64
	pending map[notice.Key]*entry
	// putRow and deleteRow write to the store Attach was given, each under storeDeadline; both are nil until Attach.
	// rowID is the highest row id given out.
	putRow    func(store.NoticeRow) error
	deleteRow func(int64) error
	rowID     int64
	// rowPuts are the entries whose last row write failed, and rowDeletes the ids whose delete failed, each with the
	// key it is logged under. A later row write or Close retries them.
	rowPuts    map[int64]*entry
	rowDeletes map[int64]notice.Key
	// beforeAttach holds the keys that saw a Notify before Attach, so a stored row for one of them is older than a
	// notice. It is nil once Attach has run.
	beforeAttach map[notice.Key]bool
}

// entry is a notice waiting for its route. Its fields are guarded by Router.mu, except n, which never changes. phase
// is the wait ent is in, due is when that wait runs out in unix ms, and row is the id of ent's stored row, 0 for none.
// silent is the Silent flag of that row.
type entry struct {
	seq         uint64
	n           Notice
	at          int64
	phase       string
	due         int64
	row         int64
	silent      bool
	ended       bool
	watchLogged bool
	delayed     bool
	stop        func() bool
}

// New returns a Router for cfg.
func New(cfg Config) *Router {
	if cfg.After == nil {
		cfg.After = afterFunc
	}
	if cfg.LocalInput == nil {
		cfg.LocalInput = noLocalInput
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	router := &Router{
		cfg:          cfg,
		pending:      make(map[notice.Key]*entry),
		rowPuts:      make(map[int64]*entry),
		rowDeletes:   make(map[int64]notice.Key),
		beforeAttach: make(map[notice.Key]bool),
	}
	router.idle = sync.NewCond(&router.mu)
	return router
}

// Notify ends any pending notice for incoming's key. It then writes incoming's silence, or records incoming as
// pending and decides its route in the background.
func (r *Router) Notify(ctx context.Context, incoming Notice) {
	ctx = context.WithoutCancel(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.beforeAttach != nil {
		r.beforeAttach[incoming.Key] = true
	}
	if old := r.pending[incoming.Key]; old != nil {
		r.endLocked(old, None, reasonChanged)
	}
	if incoming.Silent != "" {
		r.lineLocked(incoming.Key, incoming.State, None, incoming.Silent)
		return
	}
	r.seq++
	at := r.cfg.Now().Unix()
	ent := &entry{seq: r.seq, n: incoming, at: at, phase: phaseDeciding, due: at * 1000}
	r.pending[incoming.Key] = ent
	r.rowLocked(ent)
	r.startLocked(func() { r.decide(ctx, ent) })
}

// Changed ends the pending notice for key, if there is one, because its pane or session changed.
func (r *Router) Changed(key notice.Key) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent := r.pending[key]; ent != nil {
		r.endLocked(ent, None, reasonChanged)
	}
}

// Seen forwards seen to the phone. It ends the pending notice for key when that notice was recorded at or before
// seen.
func (r *Router) Seen(ctx context.Context, key notice.Key, seen int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg.Phone.Seen(ctx, key, seen)
	if ent := r.pending[key]; ent != nil && seen >= ent.at {
		r.endLocked(ent, None, reasonSeen)
	}
}

// Wait returns once no routing decision is running. Notices that are held or waiting on a timer are not decisions
// running, so Wait does not wait for them.
func (r *Router) Wait() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.running > 0 {
		r.idle.Wait()
	}
}

// Close retries the failed row writes, stops every timer, writes a shutdown line for each pending notice in the order
// they arrived, and waits for routing to finish. A pending notice whose current row is stored is the exception: it
// stops with no line, and its row keeps it for the next start. One whose row could not be stored ends `shutdown`.
func (r *Router) Close() {
	r.mu.Lock()
	r.retryLocked()
	for _, ent := range r.sortedPendingLocked() {
		if r.rowStoredLocked(ent) {
			r.stopLocked(ent)
			continue
		}
		r.endLocked(ent, None, reasonShutdown)
	}
	r.mu.Unlock()
	r.Wait()
}

// sortedPendingLocked returns the pending notices in the order they arrived. The caller holds r.mu.
func (r *Router) sortedPendingLocked() []*entry {
	return slices.SortedFunc(maps.Values(r.pending), func(a, b *entry) int {
		return cmp.Compare(a.seq, b.seq)
	})
}

// enter counts a decision on ent as running, unless ent has ended, and reports whether the decision should proceed.
// A timer's callback calls enter before it routes, so Wait sees the decision.
func (r *Router) enter(ent *entry) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent.ended {
		return false
	}
	r.running++
	return true
}

// startLocked runs f as a routing decision on its own goroutine, counted as running until f returns. The caller holds
// r.mu.
func (r *Router) startLocked(f func()) {
	r.running++
	go func() {
		defer r.finish()
		f()
	}()
}

// finish marks one routing decision as no longer running and wakes Wait when none is left.
func (r *Router) finish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running--
	if r.running == 0 {
		r.idle.Broadcast()
	}
}

// decide routes ent: it holds ent while the pane is watched. A judged notice that is not watched waits JudgeDelay
// and is judged. Any other notice routes at once.
func (r *Router) decide(ctx context.Context, ent *entry) {
	if ent.n.Watching != nil && r.watching(ctx, ent) {
		r.hold(ctx, ent)
		return
	}
	if ent.n.Judge == nil {
		r.route(ctx, ent, "")
		return
	}
	if r.delayServed(ctx, ent) {
		r.judge(ctx, ent)
	}
}

// delayServed reports whether ent has served its JudgeDelay. The first call arms the wait and reports false, and
// the wait's end decides ent again. Later calls report true, so the delay is never served twice. It reports false
// once ent has ended.
func (r *Router) delayServed(ctx context.Context, ent *entry) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent.ended {
		return false
	}
	if ent.delayed {
		return true
	}
	ent.delayed = true
	r.laterLocked(ent, JudgeDelay, func() { r.decide(ctx, ent) })
	ent.phase, ent.due = phaseDelayed, r.dueIn(JudgeDelay)
	r.rowLocked(ent)
	return false
}

// judge asks ent's Judge under a context that JudgeTimeout cancels, and routes ent by the answer. A judge that
// fails or runs late routes ent as needed, and the reason says so.
func (r *Router) judge(ctx context.Context, ent *entry) {
	jctx, cancel := context.WithCancel(ctx)
	stop := r.cfg.After(JudgeTimeout, cancel)
	verdict, err := ent.n.Judge(jctx)
	late := !stop()
	cancel()
	switch {
	case late:
		r.route(ctx, ent, "; "+reasonJudgeLate)
	case err != nil:
		r.route(ctx, ent, "; "+reasonJudgeFailed+": "+err.Error())
	case !verdict.Needed:
		r.unneeded(ctx, ent, verdict.Why)
	default:
		r.route(ctx, ent, "; "+reasonNeeded+whyClause(verdict.Why))
	}
}

// unneeded sends ent to the desk quietly and to the phone silently, at once. Nothing escalates it.
func (r *Router) unneeded(ctx context.Context, ent *entry, why string) {
	dn, built := r.buildDesk(ctx, ent)
	dn.Quiet = true
	r.deliver(ctx, ent, dn, built, silent(ent.n.Push), true, reasonUnneeded+whyClause(why))
}

// hold keeps ent pending. It writes the watching line the first time and checks ent again after WatchRecheck.
func (r *Router) hold(ctx context.Context, ent *entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent.ended {
		return
	}
	if !ent.watchLogged {
		ent.watchLogged = true
		r.lineLocked(ent.n.Key, ent.n.State, None, reasonWatching)
	}
	r.laterLocked(ent, WatchRecheck, func() { r.decide(ctx, ent) })
	ent.phase, ent.due = phaseHeld, r.dueIn(WatchRecheck)
	r.rowLocked(ent)
}

// route sends ent to the desk when the desk is at hand, and away otherwise. clause is added to the reason of the
// line route writes.
func (r *Router) route(ctx context.Context, ent *entry, clause string) {
	input, present := r.cfg.Desk.Desk()
	if local, ok := r.localInput(ctx); ok && local > input {
		input = local
	}
	atDesk := present && input >= r.cfg.Now().Add(-DeskWindow).Unix()
	dn, built := r.buildDesk(ctx, ent)
	if atDesk && built {
		r.atDesk(ctx, ent, dn, clause)
		return
	}
	r.deliver(ctx, ent, dn, built, ent.n.Push, false, reasonAway+clause)
}

// atDesk posts the desk notice and arms the escalation that pushes the phone if ent stays unseen.
func (r *Router) atDesk(ctx context.Context, ent *entry, dn deskstream.Notice, clause string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent.ended {
		return
	}
	ent.phase, ent.due = phaseDesk, r.dueIn(Escalation)
	r.rowLocked(ent)
	r.cfg.Desk.Post(dn)
	r.lineLocked(ent.n.Key, ent.n.State, Desk, reasonAtDesk+clause)
	r.laterLocked(ent, Escalation, func() { r.escalate(ctx, ent) })
}

// deliver posts the desk notice when one was built and hands build to the phone when a subscription exists. quiet is
// whether build is the silent push. It ends ent with reason, plus a clause for each of those that does not happen.
func (r *Router) deliver(ctx context.Context, ent *entry, dn deskstream.Notice, built bool,
	build func(context.Context) (push.Notification, error), quiet bool, reason string) {
	subscribed := r.subscribed()
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent.ended {
		return
	}
	onDesk := false
	if built {
		r.cfg.Desk.Post(dn)
		onDesk = true
	} else {
		reason += "; " + reasonDeskFailed
	}
	if !subscribed {
		r.endLocked(ent, routeOf(onDesk, false), reason+"; "+reasonNoSubscription)
		return
	}
	r.handOffLocked(ctx, ent, build, quiet, routeOf(onDesk, true), reason)
}

// escalate runs once ent has waited Escalation at the desk. If ent is still pending it goes to the phone and ends.
func (r *Router) escalate(ctx context.Context, ent *entry) {
	subscribed := r.subscribed()
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent.ended {
		return
	}
	if !subscribed {
		r.endLocked(ent, None, reasonUnseen+"; "+reasonNoSubscription)
		return
	}
	r.handOffLocked(ctx, ent, ent.n.Push, false, Phone, reasonUnseen)
}

// handOffLocked hands build to the phone, then ends ent with routed and reason. When ent has a row, the row is first
// written as sending, with quiet as its Silent flag, and it stays until the phone's publish report deletes it by id.
// An entry without a row has nothing to keep. The caller holds r.mu.
func (r *Router) handOffLocked(ctx context.Context, ent *entry, build func(context.Context) (push.Notification, error),
	quiet bool, routed, reason string) {
	var sent func()
	if ent.row != 0 {
		ent.phase, ent.due, ent.silent = phaseSending, 0, quiet
		r.rowLocked(ent)
		sent = func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.deleteRowLocked(ent)
		}
	}
	r.cfg.Phone.Notify(ctx, ent.n.Key, build, sent)
	r.stopLocked(ent)
	r.lineLocked(ent.n.Key, ent.n.State, routed, reason)
}

// laterLocked runs f as a decision on ent after d, unless ent ends first. The caller holds r.mu.
func (r *Router) laterLocked(ent *entry, d time.Duration, f func()) {
	ent.stop = r.cfg.After(d, func() {
		if r.enter(ent) {
			defer r.finish()
			f()
		}
	})
}

// endLocked ends ent. It stops ent's timer, stops treating ent as pending, writes the one line that ends it, and
// deletes ent's row. The caller holds r.mu and has checked that ent has not ended.
func (r *Router) endLocked(ent *entry, routed, reason string) {
	r.stopLocked(ent)
	r.lineLocked(ent.n.Key, ent.n.State, routed, reason)
	if ent.row != 0 {
		r.deleteRowLocked(ent)
	}
}

// stopLocked stops ent's timer and stops treating ent as pending, without a line. The caller holds r.mu.
func (r *Router) stopLocked(ent *entry) {
	ent.ended = true
	if ent.stop != nil {
		ent.stop()
	}
	ent.stop = nil
	if r.pending[ent.n.Key] == ent {
		delete(r.pending, ent.n.Key)
	}
}

// lineLocked writes one line for the notice at key and state. The caller holds r.mu.
func (r *Router) lineLocked(key notice.Key, state, routed, reason string) {
	line := Line{
		Time:    r.cfg.Now().Unix(),
		Host:    r.cfg.Host,
		Pane:    key.Pane,
		Session: key.Session,
		State:   state,
		Route:   routed,
		Reason:  reason,
	}
	if err := r.cfg.Log.Write(line); err != nil {
		r.cfg.Logger.Error("write notice line", "error", err)
	}
}

// watching reports whether ent's pane is watched. An error reads as not watched.
func (r *Router) watching(ctx context.Context, ent *entry) bool {
	watching, err := ent.n.Watching(ctx)
	if err != nil {
		r.cfg.Logger.ErrorContext(ctx, "check watching", "error", err)
		return false
	}
	return watching
}

// buildDesk builds ent's desk notice. It reports false when the build fails.
func (r *Router) buildDesk(ctx context.Context, ent *entry) (deskstream.Notice, bool) {
	dn, err := ent.n.Desk(ctx)
	if err != nil {
		r.cfg.Logger.ErrorContext(ctx, "build desk notice", "error", err)
		return deskstream.Notice{}, false
	}
	return dn, true
}

// localInput reads the input made on this machine. An error reads as no input.
func (r *Router) localInput(ctx context.Context) (int64, bool) {
	input, ok, err := r.cfg.LocalInput(ctx)
	if err != nil {
		r.cfg.Logger.ErrorContext(ctx, "read local input", "error", err)
		return 0, false
	}
	return input, ok
}

// subscribed reports whether a push subscription exists. An error reads as subscribed, so the push is attempted.
func (r *Router) subscribed() bool {
	ok, err := r.cfg.Subscribed()
	if err != nil {
		r.cfg.Logger.Error("check push subscription", "error", err)
		return true
	}
	return ok
}

// routeOf names the sinks a notice reached.
func routeOf(onDesk, toPhone bool) string {
	switch {
	case onDesk && toPhone:
		return DeskPhone
	case onDesk:
		return Desk
	case toPhone:
		return Phone
	default:
		return None
	}
}

// whyClause is the ": why" that follows a verdict's word in a reason, or nothing when why is empty.
func whyClause(why string) string {
	if why == "" {
		return ""
	}
	return ": " + why
}

// silent wraps build so the notification it makes is silent.
func silent(build func(context.Context) (push.Notification, error)) func(context.Context) (push.Notification, error) {
	return func(ctx context.Context) (push.Notification, error) {
		note, err := build(ctx)
		note.Silent = true
		return note, err
	}
}

func afterFunc(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}

func noLocalInput(context.Context) (int64, bool, error) {
	return 0, false, nil
}
