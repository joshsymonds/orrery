package grants

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
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

// ErrNotHeld reports a Free of a member that no hold keeps.
var ErrNotHeld = errors.New("member not held")

// ErrBadMember reports a member name outside the member name pattern.
var ErrBadMember = errors.New("bad member name")

// memberNamePattern is the form of a member name, the same pattern internal/pools checks in pool files.
var memberNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Config is what a Broker runs with. Host is the host a pool must name to be granted here. Store keeps the live grants.
// Environ is the environment each member command runs in, plus ORRERY_POOL and ORRERY_MEMBER. Now stamps grants, and
// nil means time.Now. Logger receives the broker's warnings, and nil discards them. Alive reports whether the process
// with a pid lives under the start time its grant recorded, for the clients of kept grants, and nil means processAlive.
type Config struct {
	Host    string
	Store   *store.Store
	Environ []string
	Now     func() time.Time
	Logger  *slog.Logger
	Alive   func(pid int, start int64) bool
}

// Request asks for members of pools for Project. Pools holds the pool definitions the client's merged files give,
// and every needed pool must be among them. ClientPID is the process the grant lasts with. GrantID names a grant to
// reclaim. Command is the argv of the command the grant is for, stored with the grant.
type Request struct {
	Project   string
	Pools     []pools.Pool
	Needs     []Need
	ClientPID int
	GrantID   string
	Command   []string
}

// Grant holds Members until the Acquire that granted it returns.
type Grant struct {
	ID      string
	Members []Member
}

// Event is one message of an Acquire. Position is the request's place in the queue, counted from 1, and it is sent
// when the request queues. Grant is sent once the grant is stored and its members are held. Stop names the granted
// members a hold keeps, and is sent once to a grant that a hold stops.
type Event struct {
	Position int
	Grant    *Grant
	Stop     []Member
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
	alive   func(pid int, start int64) bool

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
	// attached holds the grants a call holds, by grant id, so a hold can stop one and a reclaim can find one.
	attached map[string]*waiter
	// detached holds the grants Restore kept, by grant id. Each holds its members while it waits for its client to
	// reclaim it, and is in no queue.
	detached map[string]*waiter
	// holds holds the owner holds by member name, each with the time it was placed. A held member is never reserved or
	// granted, in whichever pool in use it belongs to, and a hold on a member of no pool in use applies once that pool
	// comes into use.
	holds map[string]time.Time
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
// err is set, and it is read only after decided closes. seq is the arrival order, and id is seq as text.
//
// A grant that a hold stops has stopping set, and its call sends the members in stopMembers when stop closes. Once that
// call ends, requeued closes and the request returns to the queue with unattached set and req.GrantID naming the grant it
// was granted under, until its client reclaims it.
type waiter struct {
	id        string
	seq       uint64
	req       Request
	poolsJSON string
	decided   chan struct{}
	grant     *Grant
	err       error
	// queuedAt is when the request queued, and grantedAt is when its grant was stored, which a restored grant keeps from
	// the store. A requeued request keeps queuedAt, since it keeps its place.
	queuedAt  time.Time
	grantedAt time.Time
	// clientStart is the start time the grant stored for its client. A grant reads it to check its client's liveness,
	// and so does a requeued request.
	clientStart int64
	stopping    bool
	stop        chan struct{}
	stopMembers []Member
	requeued    chan struct{}
	unattached  bool
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
		host: cfg.Host, store: cfg.Store, environ: cfg.Environ, now: cfg.Now, logger: cfg.Logger, alive: cfg.Alive,
		done:     make(chan struct{}),
		pools:    make(map[string]*poolUse),
		attached: make(map[string]*waiter),
		detached: make(map[string]*waiter),
		holds:    make(map[string]time.Time),
		cancels:  make(map[uint64]context.CancelFunc),
	}
	if broker.now == nil {
		broker.now = time.Now
	}
	if broker.logger == nil {
		broker.logger = slog.New(slog.DiscardHandler)
	}
	if broker.alive == nil {
		broker.alive = processAlive
	}
	return broker
}

// processAlive reports whether the process with pid lives and started at start, the start time its grant recorded. A
// pid whose /proc entry cannot be read counts as gone, so a pid reused by another process is not the client.
func processAlive(pid int, start int64) bool {
	now, err := readStart(pid)
	return err == nil && now == start
}

// readStart returns the start time of the process with pid, which is field 22 of /proc/<pid>/stat: the clock ticks after
// boot when the process started.
func readStart(pid int) (int64, error) {
	stat, err := fs.ReadFile(os.DirFS("/proc"), strconv.Itoa(pid)+"/stat")
	if err != nil {
		return 0, fmt.Errorf("read stat of process %d: %w", pid, err)
	}
	start, err := parseStartTime(string(stat))
	if err != nil {
		return 0, fmt.Errorf("stat of process %d: %w", pid, err)
	}
	return start, nil
}

// startTimeIndex is the index of field 22, the start time, among the stat fields after comm, which begin at field 3.
const startTimeIndex = 22 - 3

// parseStartTime returns the start time in a /proc/<pid>/stat line. Comm, the second field, is in parentheses and may
// hold spaces and parentheses itself, so the fields are counted from its last closing parenthesis.
func parseStartTime(stat string) (int64, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, errors.New("no comm field")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) <= startTimeIndex {
		return 0, fmt.Errorf("%d fields after comm, want at least %d", len(fields), startTimeIndex+1)
	}
	start, err := strconv.ParseInt(fields[startTimeIndex], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse start time: %w", err)
	}
	return start, nil
}

// Run runs the status of each member of each pool in use on each tick of tick, and returns when ctx ends, when the
// broker closes, or when tick closes. On each tick a kept grant, or a requeued request, whose client is no longer alive
// ends. A member whose
// command still runs gets no status on a tick, so no member has two commands at once. Each status applies its outcome
// when it ends, which reconsiders the waiting requests.
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

// statusTick ends the kept grants whose client is gone, then starts the status of each member of each pool in use that
// no command runs for. It does nothing once the broker is closed.
func (b *Broker) statusTick(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.endDeadLocked(ctx)
	for _, use := range b.pools {
		for _, name := range use.def.Members {
			member := use.members[name]
			if !member.busy {
				b.startLocked(ctx, use, member, opStatus, use.def.Status, use.def.StatusLimit)
			}
		}
	}
}

// endDeadLocked ends each detached grant whose client is no longer alive, and each requeued request whose client is no
// longer alive, then reconsiders the queue. A grant whose row the store cannot remove still frees its members, as
// releaseLocked does, and the failure is logged.
func (b *Broker) endDeadLocked(ctx context.Context) {
	ended := false
	for id, held := range b.detached {
		if b.alive(held.req.ClientPID, held.clientStart) {
			continue
		}
		delete(b.detached, id)
		if err := b.releaseLocked(ctx, held.grant); err != nil {
			b.logger.WarnContext(ctx, "end grant of dead client", "grant", id, "err", err)
		}
		ended = true
	}
	for _, entry := range slices.Clone(b.waiting) {
		if entry.unattached && !b.alive(entry.req.ClientPID, entry.clientStart) {
			b.dequeueLocked(entry)
			ended = true
		}
	}
	if ended {
		b.settleLocked(ctx)
	}
}

// Restore brings back the holds and the grants an earlier broker stored, before this broker serves. The holds come back
// first, before any grant. A stored grant whose client is not alive ends in the store at once. Each other grant's pools
// come back into use and its members are held again, and the grant waits detached until its client reclaims it by id
// and pid. A grant with a held member is stopping, so its reclaim is sent the stop. A detached grant whose client dies
// ends on a tick.
func (b *Broker) Restore(ctx context.Context) error {
	holds, err := b.store.Holds(ctx)
	if err != nil {
		return fmt.Errorf("read stored holds: %w", err)
	}
	rows, err := b.store.Grants(ctx)
	if err != nil {
		return fmt.Errorf("read stored grants: %w", err)
	}
	// Member commands run on a context that never ends, as Acquire's do.
	storeCtx := context.WithoutCancel(ctx)
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, hold := range holds {
		b.holds[hold.Member] = hold.Since
	}
	for _, row := range rows {
		if !b.alive(row.ClientPID, row.ClientStart) {
			err = b.store.EndGrant(storeCtx, row.ID)
			if err != nil {
				return fmt.Errorf("end grant %s of dead client %d: %w", row.ID, row.ClientPID, err)
			}
			continue
		}
		err = b.restoreLocked(storeCtx, row)
		if err != nil {
			return err
		}
	}
	return nil
}

// restoreLocked keeps a stored grant whose client is alive: it brings each pool the grant names into use, marks each of
// its members granted, and records the grant as detached.
func (b *Broker) restoreLocked(ctx context.Context, row store.GrantRow) error {
	defs, err := decodePools(row.Pools)
	if err != nil {
		return fmt.Errorf("grant %s: %w", row.ID, err)
	}
	for _, def := range defs {
		b.useLocked(ctx, def)
	}
	kept := &Grant{ID: row.ID}
	for _, stored := range row.Members {
		use, ok := b.pools[stored.Pool]
		if !ok {
			return fmt.Errorf("grant %s holds a member of pool %q, which the grant does not name", row.ID, stored.Pool)
		}
		member, ok := use.members[stored.Member]
		if !ok {
			return fmt.Errorf("grant %s holds member %q, which pool %q does not define",
				row.ID, stored.Member, stored.Pool)
		}
		member.grant = row.ID
		member.project = row.Project
		kept.Members = append(kept.Members, Member{Pool: stored.Pool, Name: stored.Member})
	}
	b.seq++
	restored := &waiter{
		id: strconv.FormatUint(b.seq, 10), seq: b.seq,
		req: Request{
			Project: row.Project, Pools: defs, Needs: needsOf(kept.Members), ClientPID: row.ClientPID,
			Command: row.Command,
		},
		poolsJSON:   row.Pools,
		grant:       kept,
		queuedAt:    row.Granted,
		grantedAt:   row.Granted,
		clientStart: row.ClientStart,
		stop:        make(chan struct{}),
		requeued:    make(chan struct{}),
	}
	b.detached[row.ID] = restored
	if len(b.heldLocked(kept.Members)) > 0 {
		b.stopLocked(restored)
	}
	return nil
}

// needsOf returns the needs that members meet: for each pool in the order members first name it, the count of its
// members. A restored grant's request asks for these when a hold stops it and it waits again.
func needsOf(members []Member) []Need {
	var needs []Need
	for _, m := range members {
		i := slices.IndexFunc(needs, func(n Need) bool { return n.Pool == m.Pool })
		if i < 0 {
			needs = append(needs, Need{Pool: m.Pool})
			i = len(needs) - 1
		}
		needs[i].Count++
	}
	return needs
}

// decodePools returns the pool definitions a grant row records, the inverse of encodePools.
func decodePools(encoded string) ([]pools.Pool, error) {
	var stored []storedPool
	if err := json.Unmarshal([]byte(encoded), &stored); err != nil {
		return nil, fmt.Errorf("decode pools of grant: %w", err)
	}
	defs := make([]pools.Pool, 0, len(stored))
	for _, s := range stored {
		defs = append(defs, pools.Pool{
			Name: s.Name, Host: s.Host, Cap: s.Cap, Members: s.Members, Wake: s.Wake, WakeLimit: s.WakeLimit,
			Status: s.Status, StatusLimit: s.StatusLimit, Admit: s.Admit, AdmitLimit: s.AdmitLimit,
		})
	}
	return defs, nil
}

// Acquire queues req and blocks until its grant ends. A request with a GrantID reclaims a grant: one Restore kept is
// granted at once, and a grant a hold stopped is reclaimed once its stream has ended, at the place its request was
// requeued to. Any other request queues. Send receives the request's queue position, counted from 1, and then the grant
// once the store holds it. A stopped grant's call receives the members a hold keeps, once, after the grant. A request
// that cannot be granted returns ErrBadNeed, ErrRefused or ErrNoGrant before it queues, without calling send. The grant
// lasts until ctx ends: Acquire then ends the grant and returns ctx's error, and a stopped grant is requeued. Close returns
// ErrClosed to a call still blocked, and leaves its grant stored.
func (b *Broker) Acquire(ctx context.Context, req Request, send func(Event) error) error {
	// The store writes and the member commands run on storeCtx, which never ends: a grant is ended after ctx has ended,
	// and the broker's own work must not be cut short by the caller leaving.
	storeCtx := context.WithoutCancel(ctx)
	if req.GrantID != "" {
		return b.reclaim(ctx, storeCtx, req, send)
	}
	queued, position, err := b.enqueue(storeCtx, req)
	if err != nil {
		return err
	}
	return b.await(ctx, storeCtx, queued, position, send)
}

// reclaim serves a call that reclaims the grant req names. A grant Restore kept is held by the call at once. A grant
// that is stopping is waited for, since its stream is still open, and once it is requeued the call waits in the queue
// for its request. A request that is queued for the grant is attached to the call. Any other grant, or a client that
// is not the grant's own, returns ErrNoGrant before the call queues.
func (b *Broker) reclaim(ctx, storeCtx context.Context, req Request, send func(Event) error) error {
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return ErrClosed
		}
		if kept, ok := b.detached[req.GrantID]; ok && kept.req.ClientPID == req.ClientPID {
			delete(b.detached, req.GrantID)
			b.attached[req.GrantID] = kept
			b.mu.Unlock()
			return b.hold(ctx, storeCtx, kept, send)
		}
		if old, ok := b.attached[req.GrantID]; ok && old.stopping && old.req.ClientPID == req.ClientPID {
			requeued := old.requeued
			b.mu.Unlock()
			select {
			case <-requeued:
				continue
			case <-ctx.Done():
				return fmt.Errorf("wait for grant %s to end: %w", req.GrantID, ctx.Err())
			case <-b.done:
				return ErrClosed
			}
		}
		if queued, position := b.requeuedLocked(req); queued != nil {
			queued.unattached = false
			b.settleLocked(storeCtx)
			b.mu.Unlock()
			return b.await(ctx, storeCtx, queued, position, send)
		}
		b.mu.Unlock()
		return fmt.Errorf("grant %s: %w", req.GrantID, ErrNoGrant)
	}
}

// requeuedLocked returns the queued request that req reclaims, a stopped grant's request that waits for its client, and
// its position counted from 1. It returns nil when no such request is queued for req's client.
func (b *Broker) requeuedLocked(req Request) (*waiter, int) {
	i := slices.IndexFunc(b.waiting, func(entry *waiter) bool {
		return entry.unattached && entry.req.GrantID == req.GrantID && entry.req.ClientPID == req.ClientPID
	})
	if i < 0 {
		return nil, 0
	}
	return b.waiting[i], i + 1
}

// await sends queued's position in the queue, then waits until queued is granted or fails, then holds the grant.
func (b *Broker) await(ctx, storeCtx context.Context, queued *waiter, position int, send func(Event) error) error {
	if err := send(Event{Position: position}); err != nil {
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
	return b.hold(ctx, storeCtx, queued, send)
}

// hold sends the grant that granted holds, then holds it until ctx ends. Once the grant is stopped, hold sends its stop
// once, after the grant, since the client reads a stop only once it has its grant. Acquire ends the grant when ctx ends,
// or returns ErrClosed when the broker closes first.
func (b *Broker) hold(ctx, storeCtx context.Context, granted *waiter, send func(Event) error) error {
	if err := send(Event{Grant: granted.grant}); err != nil {
		return b.leave(storeCtx, granted, err)
	}
	stop := granted.stop
	for {
		select {
		case <-ctx.Done():
			return b.leave(storeCtx, granted, ctx.Err())
		case <-b.done:
			return ErrClosed
		case <-stop:
			stop = nil
			if err := send(Event{Stop: granted.stopMembers}); err != nil {
				return b.leave(storeCtx, granted, err)
			}
		}
	}
}

// Hold places an owner hold on member, so the member is never reserved or granted until Free. The hold is stored, so it
// survives a restart. A hold on a granted member stops its grant. Hold of a member already held succeeds and keeps the
// first hold's time. It returns ErrBadMember for a name outside the member name pattern, and ErrClosed after Close.
func (b *Broker) Hold(ctx context.Context, member string) error {
	if !memberNamePattern.MatchString(member) {
		return fmt.Errorf("hold member %q: %w", member, ErrBadMember)
	}
	storeCtx := context.WithoutCancel(ctx)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	if _, held := b.holds[member]; held {
		return nil
	}
	since := b.now()
	if err := b.store.AddHold(storeCtx, store.HoldRow{Member: member, Since: since}); err != nil {
		return fmt.Errorf("hold member %q: %w", member, err)
	}
	b.holds[member] = since
	b.stopHolderLocked(member)
	return nil
}

// stopHolderLocked stops the grant that holds member, if one does.
func (b *Broker) stopHolderLocked(member string) {
	for _, use := range b.pools {
		mr, ok := use.members[member]
		if !ok {
			continue
		}
		if mr.grant != "" {
			if w := b.holderLocked(mr.grant); w != nil {
				b.stopLocked(w)
			}
		}
		return
	}
}

// holderLocked returns the grant with id, whether a call holds it or Restore kept it, or nil.
func (b *Broker) holderLocked(id string) *waiter {
	if w, ok := b.attached[id]; ok {
		return w
	}
	return b.detached[id]
}

// stopLocked stops granted, the grant of a member an owner hold keeps. It records those members for the stop its call
// sends, and closes stop, which the call waits on. A grant that is stopping already is left as it is.
func (b *Broker) stopLocked(granted *waiter) {
	if granted.stopping {
		return
	}
	granted.stopping = true
	granted.stopMembers = b.heldLocked(granted.grant.Members)
	close(granted.stop)
}

// heldLocked returns the members, of those given, that an owner hold keeps.
func (b *Broker) heldLocked(members []Member) []Member {
	var held []Member
	for _, m := range members {
		if _, ok := b.holds[m.Name]; ok {
			held = append(held, m)
		}
	}
	return held
}

// Free ends the owner hold on member, so the member can be reserved and granted again, and the requests waiting for it
// are reconsidered. It returns ErrNotHeld for a member no hold keeps, ErrBadMember for a name outside the member name
// pattern, and ErrClosed after Close.
func (b *Broker) Free(ctx context.Context, member string) error {
	if !memberNamePattern.MatchString(member) {
		return fmt.Errorf("free member %q: %w", member, ErrBadMember)
	}
	storeCtx := context.WithoutCancel(ctx)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	if _, held := b.holds[member]; !held {
		return fmt.Errorf("free member %q: %w", member, ErrNotHeld)
	}
	if err := b.store.EndHold(storeCtx, member); err != nil {
		return fmt.Errorf("free member %q: %w", member, err)
	}
	delete(b.holds, member)
	b.settleLocked(storeCtx)
	return nil
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
		id: strconv.FormatUint(b.seq, 10), seq: b.seq, req: req, poolsJSON: poolsJSON, decided: make(chan struct{}),
		queuedAt: b.now(),
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

// poolViewsLocked returns the pools in use as Plan reads them, each with its members in declared order. A held member
// is read as unavailable, so Plan never reserves it.
func (b *Broker) poolViewsLocked() []PoolView {
	views := make([]PoolView, 0, len(b.pools))
	for _, use := range b.pools {
		view := PoolView{Name: use.def.Name, Cap: use.def.Cap, Members: make([]MemberView, 0, len(use.def.Members))}
		for _, name := range use.def.Members {
			mr := use.members[name]
			state := mr.state
			if _, held := b.holds[name]; held {
				state = Unavailable
			}
			view.Members = append(view.Members, MemberView{
				Name: name, State: state, Grant: mr.grant, Project: mr.project,
			})
		}
		views = append(views, view)
	}
	return views
}

// waitingViewsLocked returns the queued requests as Plan reads them, in arrival order. A requeued request waits for its
// client to reclaim it, and Plan sees no request behind it until then, so none of them is granted ahead of it.
func (b *Broker) waitingViewsLocked() []Waiting {
	views := make([]Waiting, 0, len(b.waiting))
	for _, entry := range b.waiting {
		if entry.unattached {
			break
		}
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
	clientStart, startErr := readStart(queued.req.ClientPID)
	if startErr != nil {
		// A client whose /proc entry is unreadable is already gone. It is stored with start time 0, which no process
		// has, so the next restore ends its grant.
		b.logger.WarnContext(ctx, "client start time unreadable", "client", queued.req.ClientPID, "err", startErr)
		clientStart = 0
	}
	granted := b.now()
	row := store.GrantRow{
		ID: id, Project: queued.req.Project, ClientPID: queued.req.ClientPID, ClientStart: clientStart,
		Pools: queued.poolsJSON, Command: queued.req.Command, Members: stored, Granted: granted,
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
	queued.grantedAt = granted
	queued.clientStart = clientStart
	queued.stop = make(chan struct{})
	queued.requeued = make(chan struct{})
	b.attached[id] = queued
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

// leave ends a call that is returning cause. A granted request ends its grant, and a stopping one is requeued at its
// place for its client to reclaim. A request still queued leaves the queue, unless it is a requeued one, which stays
// queued unattached for its client's next reclaim. The queue is then reconsidered. After Close it changes nothing and
// returns ErrClosed, so stored grants stay for the next broker.
func (b *Broker) leave(ctx context.Context, queued *waiter, cause error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	var endErr error
	switch {
	case queued.grant != nil:
		endErr = b.releaseLocked(ctx, queued.grant)
		if queued.stopping {
			b.requeueLocked(queued)
		}
	case queued.req.GrantID != "":
		queued.unattached = true
	default:
		b.dequeueLocked(queued)
	}
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
	delete(b.attached, grant.ID)
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

// requeueLocked puts the request of the stopped grant queued back in the queue at its arrival place, unattached and
// keyed by the grant's id, for its client's reclaim. The reclaims waiting on the grant's stream are woken.
func (b *Broker) requeueLocked(queued *waiter) {
	close(queued.requeued)
	queued.req.GrantID = queued.grant.ID
	queued.grant = nil
	queued.stopping = false
	queued.stopMembers = nil
	queued.unattached = true
	queued.decided = make(chan struct{})
	b.insertWaitingLocked(queued)
}

// insertWaitingLocked queues waiting in arrival order, before the first queued request that arrived after it.
func (b *Broker) insertWaitingLocked(waiting *waiter) {
	i := slices.IndexFunc(b.waiting, func(entry *waiter) bool { return entry.seq > waiting.seq })
	if i < 0 {
		b.waiting = append(b.waiting, waiting)
		return
	}
	b.waiting = slices.Insert(b.waiting, i, waiting)
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
