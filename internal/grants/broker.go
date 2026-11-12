package grants

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/store"
)

// ErrRefused reports that a request names a pool this broker cannot grant: a pool on another host, a pool defined
// differently from the pool of that name in use, or a pool with a member that another pool in use holds.
var ErrRefused = errors.New("request refused")

// ErrBadNeed reports a request with no needs, a need the request's pools cannot meet, or a need for a pool the request
// does not define.
var ErrBadNeed = errors.New("bad need")

// ErrNoGrant reports a request that names a grant to reclaim, which this broker does not hold.
var ErrNoGrant = errors.New("no such grant")

// ErrClosed reports that the broker closed before the call finished.
var ErrClosed = errors.New("broker closed")

// Config is what a Broker runs with. Host is the host a pool must name to be granted here. Store keeps the live grants.
// Environ is the environment each member command runs in, plus ORRERY_POOL and ORRERY_MEMBER. Now stamps grants, and
// nil means time.Now. Logger receives the broker's warnings, and nil discards them.
type Config struct {
	Host    string
	Store   *store.Store
	Environ []string
	Now     func() time.Time
	Logger  *slog.Logger
}

// Request asks for members of pools for Project. Pools holds the pool definitions the client's merged files give,
// and every needed pool must be among them. ClientPID is the process the grant lasts with. GrantID names a grant to
// reclaim.
type Request struct {
	Project   string
	Pools     []pools.Pool
	Needs     []Need
	ClientPID int
	GrantID   string
}

// Grant holds Members until the Acquire that granted it returns.
type Grant struct {
	ID      string
	Members []Member
}

// Event is one message of an Acquire. Position is the request's place in the queue, counted from 1, and it is sent
// when the request queues. Grant is sent once the grant is stored and its members are held.
type Event struct {
	Position int
	Grant    *Grant
}

// Broker grants members of pools to requests. It owns the pools in use, the state of each member, the queue of waiting
// requests, and the grants. A pool is in use while a request queues for it, a grant holds one of its members, or a
// member command runs for it.
type Broker struct {
	host    string
	store   *store.Store
	environ []string
	now     func() time.Time
	logger  *slog.Logger

	// commands counts the member commands in flight, which Close waits for.
	commands sync.WaitGroup
	// done is closed by Close.
	done chan struct{}

	mu sync.Mutex
	// closed is set by Close, after which the broker grants nothing.
	closed bool
	// seq numbers queued requests and member commands.
	seq uint64
	// pools holds the pools in use, by name.
	pools map[string]*poolUse
	// waiting holds the queued requests in arrival order.
	waiting []*waiter
	// cancels holds the cancel of each member command in flight, by command id.
	cancels map[uint64]context.CancelFunc
}

// poolUse is a pool in use: its definition, its members by name, and how many member commands run for it.
type poolUse struct {
	def     pools.Pool
	members map[string]*memberRec
	running int
}

// held reports whether a grant holds a member of the pool.
func (u *poolUse) held() bool {
	for _, mr := range u.members {
		if mr.grant != "" {
			return true
		}
	}
	return false
}

// memberRec is a member as the broker records it: its state, the grant holding it (empty when free), and whether a
// member command runs for it.
type memberRec struct {
	name    string
	state   MemberState
	grant   string
	project string
	busy    bool
}

// waiter is a request in the queue or a grant. decided is closed when the request is granted or fails; then grant or
// err is set, and it is read only after decided closes.
type waiter struct {
	id        string
	req       Request
	poolsJSON string
	decided   chan struct{}
	grant     *Grant
	err       error
}

// storedPool is a pool definition as a grant row records it. Its JSON names are the ones a restore reads back.
type storedPool struct {
	Name        string        `json:"name"`
	Host        string        `json:"host"`
	Cap         int           `json:"cap"`
	Members     []string      `json:"members"`
	Wake        []string      `json:"wake"`
	WakeLimit   time.Duration `json:"wakeLimit"`
	Status      []string      `json:"status"`
	StatusLimit time.Duration `json:"statusLimit"`
	Admit       []string      `json:"admit"`
	AdmitLimit  time.Duration `json:"admitLimit"`
}

// operation is the member command a member runs.
type operation int

// Member commands: status reports a member's state, wake brings an asleep member up, and admit readies an awake member
// for grants.
const (
	opStatus operation = iota
	opWake
	opAdmit
)

// command is one member command in flight: its id, the pool and member it runs for, and its operation.
type command struct {
	id     uint64
	use    *poolUse
	member *memberRec
	op     operation
}

// NewBroker returns a Broker for cfg. It runs no command until a request arrives.
func NewBroker(cfg Config) *Broker {
	broker := &Broker{
		host: cfg.Host, store: cfg.Store, environ: cfg.Environ, now: cfg.Now, logger: cfg.Logger,
		done:    make(chan struct{}),
		pools:   make(map[string]*poolUse),
		cancels: make(map[uint64]context.CancelFunc),
	}
	if broker.now == nil {
		broker.now = time.Now
	}
	if broker.logger == nil {
		broker.logger = slog.New(slog.DiscardHandler)
	}
	return broker
}

// Run runs the status of each member of each pool in use on each tick of tick, and returns when ctx ends, when the
// broker closes, or when tick closes. A member whose command still runs gets no status on a tick, so no member has two
// commands at once. Each status applies its outcome when it ends, which reconsiders the waiting requests.
func (b *Broker) Run(ctx context.Context, tick <-chan time.Time) {
	// Member commands run on a context that never ends, as Acquire's do: Close, not the caller leaving, ends them.
	cmdCtx := context.WithoutCancel(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-b.done:
			return
		case _, ok := <-tick:
			if !ok {
				return
			}
			b.statusTick(cmdCtx)
		}
	}
}

// statusTick starts the status of each member of each pool in use that no command runs for. It does nothing once the
// broker is closed.
func (b *Broker) statusTick(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, use := range b.pools {
		for _, name := range use.def.Members {
			member := use.members[name]
			if !member.busy {
				b.startLocked(ctx, use, member, opStatus, use.def.Status, use.def.StatusLimit)
			}
		}
	}
}

// Acquire queues req and blocks until its grant ends. A request that cannot be granted returns ErrBadNeed, ErrRefused
// or ErrNoGrant before it queues, without calling send. Otherwise send receives the queue position, counted from 1, and
// then the grant once the store holds it. The grant lasts until ctx ends: Acquire then ends the grant and returns ctx's
// error. Close returns ErrClosed to a call still blocked, and leaves its grant stored.
func (b *Broker) Acquire(ctx context.Context, req Request, send func(Event) error) error {
	// The store writes and the member commands run on storeCtx, which never ends: a grant is ended after ctx has ended,
	// and the broker's own work must not be cut short by the caller leaving.
	storeCtx := context.WithoutCancel(ctx)
	queued, position, err := b.enqueue(storeCtx, req)
	if err != nil {
		return err
	}
	if err = send(Event{Position: position}); err != nil {
		return b.leave(storeCtx, queued, err)
	}
	select {
	case <-queued.decided:
	case <-ctx.Done():
		return b.leave(storeCtx, queued, ctx.Err())
	case <-b.done:
		return ErrClosed
	}
	if queued.err != nil {
		return queued.err
	}
	if err = send(Event{Grant: queued.grant}); err != nil {
		return b.leave(storeCtx, queued, err)
	}
	select {
	case <-ctx.Done():
		return b.leave(storeCtx, queued, ctx.Err())
	case <-b.done:
		return ErrClosed
	}
}

// Close stops the broker: calls still blocked return ErrClosed, stored grants stay for the next broker, and Close waits
// for the member commands in flight to end.
func (b *Broker) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	close(b.done)
	for _, cancel := range b.cancels {
		cancel()
	}
	b.mu.Unlock()
	b.commands.Wait()
}

// enqueue refuses req, or queues it and grants whatever the queue now allows. It returns the queued request and its
// position, counted from 1.
func (b *Broker) enqueue(ctx context.Context, req Request) (*waiter, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, 0, ErrClosed
	}
	if req.GrantID != "" {
		return nil, 0, fmt.Errorf("grant %s: %w", req.GrantID, ErrNoGrant)
	}
	needed, err := b.checkLocked(req)
	if err != nil {
		return nil, 0, err
	}
	poolsJSON, err := encodePools(needed)
	if err != nil {
		return nil, 0, err
	}
	for _, def := range needed {
		b.useLocked(ctx, def)
	}
	b.seq++
	queued := &waiter{
		id: strconv.FormatUint(b.seq, 10), req: req, poolsJSON: poolsJSON, decided: make(chan struct{}),
	}
	b.waiting = append(b.waiting, queued)
	position := len(b.waiting)
	b.settleLocked(ctx)
	return queued, position, nil
}

// encodePools returns the JSON that a grant row records for the pool definitions defs.
func encodePools(defs []pools.Pool) (string, error) {
	stored := make([]storedPool, 0, len(defs))
	for _, def := range defs {
		stored = append(stored, storedPool{
			Name: def.Name, Host: def.Host, Cap: def.Cap, Members: def.Members, Wake: def.Wake,
			WakeLimit: def.WakeLimit, Status: def.Status, StatusLimit: def.StatusLimit, Admit: def.Admit,
			AdmitLimit: def.AdmitLimit,
		})
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return "", fmt.Errorf("encode pools of request: %w", err)
	}
	return string(encoded), nil
}

// checkLocked refuses a request that cannot be queued: one with no needs, a need its pool cannot meet, or a pool this
// broker cannot grant. It returns the definitions of the pools req needs, in need order.
func (b *Broker) checkLocked(req Request) ([]pools.Pool, error) {
	if len(req.Needs) == 0 {
		return nil, fmt.Errorf("request has no needs: %w", ErrBadNeed)
	}
	defs := make(map[string]pools.Pool, len(req.Pools))
	for _, def := range req.Pools {
		defs[def.Name] = def
	}
	needed := make([]pools.Pool, 0, len(req.Needs))
	for _, need := range req.Needs {
		def, ok := defs[need.Pool]
		if !ok {
			return nil, fmt.Errorf("need of pool %q, which the request does not define: %w", need.Pool, ErrBadNeed)
		}
		if need.Count < 1 || need.Count > def.Cap || need.Count > len(def.Members) {
			return nil, fmt.Errorf("need of %d from pool %q is outside 1 to %d: %w",
				need.Count, need.Pool, min(def.Cap, len(def.Members)), ErrBadNeed)
		}
		if !slices.ContainsFunc(needed, func(p pools.Pool) bool { return p.Name == def.Name }) {
			needed = append(needed, def)
		}
	}
	for _, def := range needed {
		if err := b.refuseLocked(def); err != nil {
			return nil, err
		}
	}
	return needed, nil
}

// refuseLocked refuses a pool this broker cannot grant: one on another host, one defined differently from the pool of
// its name in use, or one with a member that another pool in use holds.
func (b *Broker) refuseLocked(def pools.Pool) error {
	if def.Host != b.host {
		return fmt.Errorf("pool %q is on host %q, not %q: %w", def.Name, def.Host, b.host, ErrRefused)
	}
	if use, ok := b.pools[def.Name]; ok && !use.def.Equal(def) {
		return fmt.Errorf("pool %q is defined differently from the one in use: %w", def.Name, ErrRefused)
	}
	for name, use := range b.pools {
		if name == def.Name {
			continue
		}
		for _, member := range def.Members {
			if slices.Contains(use.def.Members, member) {
				return fmt.Errorf("member %q of pool %q is in pool %q, which is in use: %w",
					member, def.Name, name, ErrRefused)
			}
		}
	}
	return nil
}

// useLocked brings def's pool into use if it is not already, and starts the status of each member, which is how a
// member's state is first learned.
func (b *Broker) useLocked(ctx context.Context, def pools.Pool) {
	if _, ok := b.pools[def.Name]; ok {
		return
	}
	use := &poolUse{def: def, members: make(map[string]*memberRec, len(def.Members))}
	b.pools[def.Name] = use
	for _, name := range def.Members {
		member := &memberRec{name: name}
		use.members[name] = member
		b.startLocked(ctx, use, member, opStatus, def.Status, def.StatusLimit)
	}
}

// settleLocked runs after every change to the broker: it grants the requests Plan grants, prepares the members Plan
// reserves, and forgets the pools nothing uses. It does nothing once the broker is closed.
func (b *Broker) settleLocked(ctx context.Context) {
	if b.closed {
		return
	}
	decision := Plan(b.poolViewsLocked(), b.waitingViewsLocked())
	for _, assignment := range decision.Grants {
		b.grantLocked(ctx, assignment)
	}
	for _, reserved := range decision.Prepare {
		b.prepareLocked(ctx, reserved)
	}
	b.forgetLocked()
}

// poolViewsLocked returns the pools in use as Plan reads them, each with its members in declared order.
func (b *Broker) poolViewsLocked() []PoolView {
	views := make([]PoolView, 0, len(b.pools))
	for _, use := range b.pools {
		view := PoolView{Name: use.def.Name, Cap: use.def.Cap, Members: make([]MemberView, 0, len(use.def.Members))}
		for _, name := range use.def.Members {
			mr := use.members[name]
			view.Members = append(view.Members, MemberView{
				Name: name, State: mr.state, Grant: mr.grant, Project: mr.project,
			})
		}
		views = append(views, view)
	}
	return views
}

// waitingViewsLocked returns the queued requests as Plan reads them, in arrival order.
func (b *Broker) waitingViewsLocked() []Waiting {
	views := make([]Waiting, 0, len(b.waiting))
	for _, entry := range b.waiting {
		views = append(views, Waiting{ID: entry.id, Project: entry.req.Project, Needs: entry.req.Needs})
	}
	return views
}

// prepareLocked starts the command that moves a member Plan reserved toward admitted. An asleep member is woken, and
// the settle that follows the wake admits it if a request still needs it. An awake member is admitted now. A member
// whose status runs is left alone, since Plan may reserve it while its state is still awake or asleep: that status ends
// in a settle, which prepares the member then.
func (b *Broker) prepareLocked(ctx context.Context, reserved Member) {
	use := b.pools[reserved.Pool]
	member := use.members[reserved.Name]
	if member.busy {
		return
	}
	if member.state == Asleep {
		member.state = Waking
		b.startLocked(ctx, use, member, opWake, use.def.Wake, use.def.WakeLimit)
		return
	}
	member.state = Admitting
	b.startLocked(ctx, use, member, opAdmit, use.def.Admit, use.def.AdmitLimit)
}

// grantLocked grants the members of assignment to the queued request it names. The grant is stored before the request
// is granted, and a store error fails the request and leaves its members free.
func (b *Broker) grantLocked(ctx context.Context, assignment Assignment) {
	queued := b.queuedLocked(assignment.Request)
	id := rand.Text()
	stored := make([]store.GrantMember, 0, len(assignment.Members))
	for _, m := range assignment.Members {
		stored = append(stored, store.GrantMember{Pool: m.Pool, Member: m.Name})
	}
	row := store.GrantRow{
		ID: id, Project: queued.req.Project, ClientPID: queued.req.ClientPID, Pools: queued.poolsJSON,
		Members: stored, Granted: b.now(),
	}
	if err := b.store.AddGrant(ctx, row); err != nil {
		b.failLocked(queued, fmt.Errorf("store grant %s: %w", id, err))
		return
	}
	for _, m := range assignment.Members {
		member := b.pools[m.Pool].members[m.Name]
		member.grant = id
		member.project = queued.req.Project
	}
	queued.grant = &Grant{ID: id, Members: assignment.Members}
	b.dequeueLocked(queued)
	close(queued.decided)
}

// queuedLocked returns the queued request with id, which Plan named.
func (b *Broker) queuedLocked(id string) *waiter {
	return b.waiting[slices.IndexFunc(b.waiting, func(entry *waiter) bool { return entry.id == id })]
}

// failLocked ends the queued request queued with err, which its Acquire returns.
func (b *Broker) failLocked(queued *waiter, err error) {
	queued.err = err
	b.dequeueLocked(queued)
	close(queued.decided)
}

// dequeueLocked removes queued from the queue, if it is queued.
func (b *Broker) dequeueLocked(queued *waiter) {
	b.waiting = slices.DeleteFunc(b.waiting, func(entry *waiter) bool { return entry == queued })
}

// leave ends a call that is returning cause. A queued request leaves the queue; a granted one ends its grant. The
// queue is then reconsidered. After Close it changes nothing and returns ErrClosed, so stored grants stay for the next
// broker.
func (b *Broker) leave(ctx context.Context, queued *waiter, cause error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	var endErr error
	if queued.grant != nil {
		endErr = b.releaseLocked(ctx, queued.grant)
	}
	b.dequeueLocked(queued)
	b.settleLocked(ctx)
	if endErr != nil {
		return errors.Join(cause, endErr)
	}
	return cause
}

// releaseLocked ends grant: it removes the grant from the store, then frees its members. When the store refuses the
// removal the members are freed all the same, so a request for one of them then fails at the store rather than waiting
// on a grant that has ended.
func (b *Broker) releaseLocked(ctx context.Context, grant *Grant) error {
	err := b.store.EndGrant(ctx, grant.ID)
	for _, m := range grant.Members {
		member := b.pools[m.Pool].members[m.Name]
		member.grant = ""
		member.project = ""
	}
	if err != nil {
		return fmt.Errorf("end grant %s: %w", grant.ID, err)
	}
	return nil
}

// forgetLocked drops each pool that no queued request needs, no grant holds, and no member command runs for.
func (b *Broker) forgetLocked() {
	for name, use := range b.pools {
		if use.running == 0 && !use.held() && !b.queuedNeedLocked(name) {
			delete(b.pools, name)
		}
	}
}

// queuedNeedLocked reports whether a queued request needs the pool named name.
func (b *Broker) queuedNeedLocked(name string) bool {
	return slices.ContainsFunc(b.waiting, func(entry *waiter) bool {
		return slices.ContainsFunc(entry.req.Needs, func(n Need) bool { return n.Pool == name })
	})
}

// startLocked runs argv under limit as op's command for member of use, in its own goroutine. The caller has already set
// member to the state the command is in progress from, if it has one; a status keeps the state it finds. finish applies
// the outcome when the command ends.
func (b *Broker) startLocked(
	ctx context.Context, use *poolUse, member *memberRec, op operation, argv []string, limit time.Duration,
) {
	cmdCtx, cancel := context.WithCancel(ctx)
	b.seq++
	cmd := command{id: b.seq, use: use, member: member, op: op}
	b.cancels[cmd.id] = cancel
	use.running++
	member.busy = true
	env := append(slices.Clone(b.environ), "ORRERY_POOL="+use.def.Name, "ORRERY_MEMBER="+member.name)
	b.commands.Go(func() {
		outcome, err := RunCommand(cmdCtx, argv, limit, env)
		b.finish(ctx, cmd, outcome, err)
	})
}

// finish ends member command cmd with its outcome: it releases the command's context, sets the member to the state the
// outcome leaves it in, and settles the broker.
func (b *Broker) finish(ctx context.Context, cmd command, outcome Outcome, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancels[cmd.id]()
	delete(b.cancels, cmd.id)
	cmd.use.running--
	cmd.member.busy = false
	cmd.member.state = settledState(cmd.op, cmd.member.state, outcome)
	if cmd.member.state == Unavailable {
		b.logger.WarnContext(ctx, "member unavailable",
			"pool", cmd.use.def.Name, "member", cmd.member.name, "err", err)
	}
	b.settleLocked(ctx)
}

// settledState is the state a member command leaves its member in. prior is the member's state before the command
// ended. A status that exits 0 leaves an admitted member admitted and any other member awake. A status that exits
// nonzero leaves its member asleep, unless the member was unavailable, which only a status that exits 0 changes. A wake
// that exits 0 leaves its member awake, and an admit that exits 0 leaves it admitted. Any command that fails to start,
// passes its limit, or exits nonzero when it should not leaves its member unavailable.
func settledState(op operation, prior MemberState, outcome Outcome) MemberState {
	switch {
	case outcome == Succeeded && op == opAdmit:
		return Admitted
	case op == opStatus && outcome == Succeeded && prior == Admitted:
		return Admitted
	case outcome == Succeeded:
		return Awake
	case op == opStatus && outcome == Exited && prior != Unavailable:
		return Asleep
	default:
		return Unavailable
	}
}
