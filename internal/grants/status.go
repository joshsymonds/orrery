package grants

import (
	"fmt"
	"maps"
	"slices"
	"time"
)

// Status is a snapshot of the broker: the pools in use, and every owner hold.
type Status struct {
	Pools []PoolStatus
	Holds []HoldStatus
}

// PoolStatus is a pool in use: its cap, its members in declared order, and the requests waiting for it in queue order.
type PoolStatus struct {
	Name    string
	Cap     int
	Members []MemberStatus
	Waiters []WaiterStatus
}

// MemberStatus is a member of a pool in use as the broker records it. Holder is the grant holding the member, nil when
// no grant holds it, and HeldSince is when an owner hold was placed on it, zero when it is not held.
type MemberStatus struct {
	Name      string
	State     MemberState
	Holder    *Holder
	HeldSince time.Time
}

// Holder is the grant holding a member: its id, the project and client it belongs to, the command it runs, when it was
// stored, and whether a hold is stopping it.
type Holder struct {
	GrantID   string
	Project   string
	ClientPID int
	Command   []string
	Since     time.Time
	Stopping  bool
}

// WaiterStatus is a request waiting for a pool. Position counts from 1 over the whole queue, Count is the members the
// request needs from the pool, and Since is when the request queued.
type WaiterStatus struct {
	Position int
	Project  string
	Command  []string
	Count    int
	Since    time.Time
}

// HoldStatus is an owner hold on a member, and when it was placed.
type HoldStatus struct {
	Member string
	Since  time.Time
}

// Status returns a snapshot of the pools in use and of every owner hold. It reads under the broker's lock and changes
// nothing.
func (b *Broker) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	var status Status
	for _, name := range slices.Sorted(maps.Keys(b.pools)) {
		status.Pools = append(status.Pools, b.poolStatusLocked(b.pools[name]))
	}
	for _, member := range slices.Sorted(maps.Keys(b.holds)) {
		status.Holds = append(status.Holds, HoldStatus{Member: member, Since: b.holds[member]})
	}
	return status
}

// poolStatusLocked returns the status of a pool in use: its members in declared order, each with the state the broker
// records and its holder, and the queued requests that need the pool, in queue order.
func (b *Broker) poolStatusLocked(use *poolUse) PoolStatus {
	ps := PoolStatus{Name: use.def.Name, Cap: use.def.Cap, Members: make([]MemberStatus, 0, len(use.def.Members))}
	for _, name := range use.def.Members {
		mr := use.members[name]
		member := MemberStatus{Name: name, State: mr.state}
		if mr.grant != "" {
			member.Holder = b.holderStatusLocked(mr.grant)
		}
		member.HeldSince = b.holds[name]
		ps.Members = append(ps.Members, member)
	}
	for i, entry := range b.waiting {
		count := countFor(entry.req.Needs, use.def.Name)
		if count == 0 {
			continue
		}
		ps.Waiters = append(ps.Waiters, WaiterStatus{
			Position: i + 1, Project: entry.req.Project, Command: entry.req.Command,
			Count: count, Since: entry.queuedAt,
		})
	}
	return ps
}

// holderStatusLocked returns the holder of the grant with id. A member names only a grant the broker holds, so a lookup
// that finds none is a failure of the broker's bookkeeping: it is reported by id alone rather than dereferenced.
func (b *Broker) holderStatusLocked(id string) *Holder {
	owner := b.holderLocked(id)
	if owner == nil {
		return &Holder{GrantID: id}
	}
	return &Holder{
		GrantID: id, Project: owner.req.Project, ClientPID: owner.req.ClientPID, Command: owner.req.Command,
		Since: owner.grantedAt, Stopping: owner.stopping,
	}
}

// countFor returns how many members the needs ask from the pool named name, which is zero when they ask for none.
func countFor(needs []Need, name string) int {
	count := 0
	for _, need := range needs {
		if need.Pool == name {
			count += need.Count
		}
	}
	return count
}

// String returns the name of the state as the API reports it.
func (s MemberState) String() string {
	switch s {
	case Unknown:
		return "unknown"
	case Asleep:
		return "asleep"
	case Awake:
		return "awake"
	case Waking:
		return "waking"
	case Admitting:
		return "admitting"
	case Admitted:
		return "admitted"
	case Unavailable:
		return "unavailable"
	default:
		return fmt.Sprintf("MemberState(%d)", int(s))
	}
}
