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
)

const (
	// WatchRecheck is how long a watched notice waits before it is checked again.
	WatchRecheck = 5 * time.Second
	// DeskWindow is how long after the desk's last input the desk still counts as at hand.
	DeskWindow = 2 * time.Minute
	// Escalation is how long a notice at the desk waits unseen before the phone is pushed.
	Escalation = 5 * time.Minute
)

// Reasons written to the log. A reason that gains a clause appends it after "; ".
const (
	reasonAtDesk         = "at the desk"
	reasonAway           = "away"
	reasonWatching       = "watching"
	reasonUnseen         = "unseen after 5 minutes"
	reasonChanged        = "changed"
	reasonSeen           = "seen"
	reasonShutdown       = "shutdown"
	reasonDeskFailed     = "desk notice failed"
	reasonNoSubscription = "no push subscription"
)

// DeskSink posts notices to the desk and reports whether the desk is present. *deskstream.Hub satisfies it.
type DeskSink interface {
	Post(deskstream.Notice)
	Desk() (input int64, present bool)
}

// PhoneSink sends push notices and clears. *notice.Notices satisfies it.
type PhoneSink interface {
	Notify(ctx context.Context, key notice.Key, build func(context.Context) (push.Notification, error))
	Seen(ctx context.Context, key notice.Key, seen int64)
}

// Recorder writes one Line to the notice log. *Log satisfies it.
type Recorder interface {
	Write(Line) error
}

// Notice is one waiting or done notice to route. A non-empty Silent is the reason it goes nowhere. A nil
// Watching is never watched.
type Notice struct {
	Key      notice.Key
	State    string
	Silent   string
	Watching func(context.Context) (bool, error)
	Desk     func(context.Context) (deskstream.Notice, error)
	Push     func(context.Context) (push.Notification, error)
}

// Config holds the Router's collaborators. A nil After uses time.AfterFunc, a nil LocalInput reports no input,
// and a nil Logger discards.
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
}

// entry is a notice waiting for its route. Its fields are guarded by Router.mu, except n, which never changes.
type entry struct {
	seq         uint64
	n           Notice
	at          int64
	ended       bool
	watchLogged bool
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
	r := &Router{cfg: cfg, pending: make(map[notice.Key]*entry)}
	r.idle = sync.NewCond(&r.mu)
	return r
}

// Notify ends any pending notice for incoming's key. It then writes incoming's silence, or records incoming as
// pending and decides its route in the background.
func (r *Router) Notify(ctx context.Context, incoming Notice) {
	ctx = context.WithoutCancel(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	if old := r.pending[incoming.Key]; old != nil {
		r.endLocked(old, None, reasonChanged)
	}
	if incoming.Silent != "" {
		r.lineLocked(incoming.Key, incoming.State, None, incoming.Silent)
		return
	}
	r.seq++
	ent := &entry{seq: r.seq, n: incoming, at: r.cfg.Now().Unix()}
	r.pending[incoming.Key] = ent
	r.running++
	go func() {
		defer r.finish()
		r.decide(ctx, ent)
	}()
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

// Close stops every timer, writes a shutdown line for each pending notice in the order they arrived, and waits for
// routing to finish.
func (r *Router) Close() {
	r.mu.Lock()
	pending := slices.SortedFunc(maps.Values(r.pending), func(a, b *entry) int {
		return cmp.Compare(a.seq, b.seq)
	})
	for _, ent := range pending {
		r.endLocked(ent, None, reasonShutdown)
	}
	r.mu.Unlock()
	r.Wait()
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

// finish marks one routing decision as no longer running and wakes Wait when none is left.
func (r *Router) finish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running--
	if r.running == 0 {
		r.idle.Broadcast()
	}
}

// decide routes ent: it holds ent while the pane is watched, and otherwise routes it.
func (r *Router) decide(ctx context.Context, ent *entry) {
	if ent.n.Watching != nil && r.watching(ctx, ent) {
		r.hold(ctx, ent)
		return
	}
	r.route(ctx, ent)
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
}

// route sends ent to the desk when the desk is at hand, and away otherwise.
func (r *Router) route(ctx context.Context, ent *entry) {
	input, present := r.cfg.Desk.Desk()
	if local, ok := r.localInput(ctx); ok && local > input {
		input = local
	}
	atDesk := present && input >= r.cfg.Now().Add(-DeskWindow).Unix()
	dn, built := r.buildDesk(ctx, ent)
	if atDesk && built {
		r.atDesk(ctx, ent, dn)
		return
	}
	r.away(ctx, ent, dn, built)
}

// atDesk posts the desk notice and arms the escalation that pushes the phone if ent stays unseen.
func (r *Router) atDesk(ctx context.Context, ent *entry, dn deskstream.Notice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent.ended {
		return
	}
	r.cfg.Desk.Post(dn)
	r.lineLocked(ent.n.Key, ent.n.State, Desk, reasonAtDesk)
	r.laterLocked(ent, Escalation, func() { r.escalate(ctx, ent) })
}

// away posts the desk notice when one was built and hands the push to the phone when a subscription exists. It
// ends ent.
func (r *Router) away(ctx context.Context, ent *entry, dn deskstream.Notice, built bool) {
	subscribed := r.subscribed()
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent.ended {
		return
	}
	reason := reasonAway
	onDesk := false
	if built {
		r.cfg.Desk.Post(dn)
		onDesk = true
	} else {
		reason += "; " + reasonDeskFailed
	}
	toPhone := false
	if subscribed {
		r.cfg.Phone.Notify(ctx, ent.n.Key, ent.n.Push)
		toPhone = true
	} else {
		reason += "; " + reasonNoSubscription
	}
	r.endLocked(ent, routeOf(onDesk, toPhone), reason)
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
	r.cfg.Phone.Notify(ctx, ent.n.Key, ent.n.Push)
	r.endLocked(ent, Phone, reasonUnseen)
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

// endLocked ends ent. It stops ent's timer, stops treating ent as pending, and writes the one line that ends it.
// The caller holds r.mu and has checked that ent has not ended.
func (r *Router) endLocked(ent *entry, routed, reason string) {
	ent.ended = true
	if ent.stop != nil {
		ent.stop()
	}
	ent.stop = nil
	if r.pending[ent.n.Key] == ent {
		delete(r.pending, ent.n.Key)
	}
	r.lineLocked(ent.n.Key, ent.n.State, routed, reason)
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

func afterFunc(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}

func noLocalInput(context.Context) (int64, bool, error) {
	return 0, false, nil
}
