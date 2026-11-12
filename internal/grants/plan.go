// Package grants decides which waiting requests are granted members of pools, and which members to wake for the
// requests that are not yet covered. Plan is a pure function: it reads the pools and the waiting requests and returns
// a Decision, with no I/O, goroutines, or clock.
package grants

import "slices"

// MemberState is the state of a pool member as the broker last recorded it.
type MemberState int

// Member states. Unknown and Unavailable members are never reserved.
const (
	Unknown MemberState = iota
	Asleep
	Awake
	Waking
	Admitting
	Admitted
	Unavailable
)

// Member names one member of one pool.
type Member struct {
	Pool string
	Name string
}

// MemberView is a member as the broker records it. Grant and Project are empty when the member is free; otherwise
// Grant names the grant holding it and Project is the project that grant belongs to.
type MemberView struct {
	Name    string
	State   MemberState
	Grant   string
	Project string
}

// PoolView is a pool with its cap on the members one project may hold from it at once, and its members in declared
// order.
type PoolView struct {
	Name    string
	Cap     int
	Members []MemberView
}

// Need asks for Count members of the pool named Pool.
type Need struct {
	Pool  string
	Count int
}

// Waiting is a request for members from Project, made of Needs in order.
type Waiting struct {
	ID      string
	Project string
	Needs   []Need
}

// Assignment grants Members to the request named Request.
type Assignment struct {
	Request string
	Members []Member
}

// Decision is the outcome of Plan: the requests granted members, and the members to wake for the rest.
type Decision struct {
	Grants  []Assignment
	Prepare []Member
}

// poolProject keys the members one project has reserved from one pool during a Plan call.
type poolProject struct {
	pool    string
	project string
}

// Plan decides which waiting requests are granted members and which members to prepare, given the pools and the
// waiting requests in arrival order. A request is taken in turn. If its project, counting the members it holds and
// the members reserved for it earlier in the call, would pass a pool's cap with the request's needs, the request
// reserves nothing. Otherwise each need reserves free members not reserved by an earlier request, in the order
// admitted, then waking or admitting, then awake, then asleep, declared order within each. A request whose every need
// is covered by admitted members is granted all its members at once, in need order. Any other request prepares its
// reserved awake and asleep members. Unknown and unavailable members are never reserved, and a pool absent from pools
// has no members and a cap of zero.
func Plan(pools []PoolView, waiting []Waiting) Decision {
	byName := make(map[string]PoolView, len(pools))
	for _, p := range pools {
		byName[p.Name] = p
	}
	reserved := make(map[Member]bool)
	reservedFor := make(map[poolProject]int)
	var decision Decision
	for _, request := range waiting {
		if overCap(byName, request, reservedFor) {
			continue
		}
		var members, prepare []Member
		granted := true
		for _, needed := range request.Needs {
			picked := pick(byName[needed.Pool], size(needed), reserved)
			admitted := 0
			for _, mv := range picked {
				member := Member{Pool: needed.Pool, Name: mv.Name}
				members = append(members, member)
				switch mv.State {
				case Admitted:
					admitted++
				case Awake, Asleep:
					prepare = append(prepare, member)
				case Unknown, Waking, Admitting, Unavailable:
					// Waking and admitting members are already on their way, so they are reserved but not prepared.
					// Unknown and unavailable members are never reserved.
				}
			}
			if admitted < size(needed) {
				granted = false
			}
			key := poolProject{pool: needed.Pool, project: request.Project}
			reservedFor[key] += len(picked)
		}
		if granted {
			decision.Grants = append(decision.Grants, Assignment{Request: request.ID, Members: members})
			continue
		}
		decision.Prepare = append(decision.Prepare, prepare...)
	}
	return decision
}

// overCap reports whether the project of request, counting the members it holds from each pool and the members
// reserved for it earlier in the call, would pass a pool's cap with the counts of request's needs.
func overCap(pools map[string]PoolView, request Waiting, reservedFor map[poolProject]int) bool {
	demand := make(map[string]int, len(request.Needs))
	for _, needed := range request.Needs {
		demand[needed.Pool] += size(needed)
	}
	for name, want := range demand {
		view := pools[name]
		key := poolProject{pool: name, project: request.Project}
		total := heldBy(view, request.Project) + reservedFor[key] + want
		if total > view.Cap {
			return true
		}
	}
	return false
}

// heldBy counts the members of p that a grant holds for project.
func heldBy(p PoolView, project string) int {
	count := 0
	for _, mv := range p.Members {
		if mv.Grant != "" && mv.Project == project {
			count++
		}
	}
	return count
}

// size is the number of members a need asks for. A negative count asks for none, so it cannot offset another need.
func size(n Need) int {
	return max(n.Count, 0)
}

// pick reserves up to n members of view that are free and not already reserved, in the order admitted, then waking or
// admitting, then awake, then asleep. Members keep their declared order within each state. It marks each member it
// picks in reserved and returns the picks in order.
func pick(view PoolView, n int, reserved map[Member]bool) []MemberView {
	var picked []MemberView
	for _, states := range [][]MemberState{{Admitted}, {Waking, Admitting}, {Awake}, {Asleep}} {
		for _, mv := range view.Members {
			if len(picked) == n {
				return picked
			}
			member := Member{Pool: view.Name, Name: mv.Name}
			if !slices.Contains(states, mv.State) || mv.Grant != "" || reserved[member] {
				continue
			}
			reserved[member] = true
			picked = append(picked, mv)
		}
	}
	return picked
}
