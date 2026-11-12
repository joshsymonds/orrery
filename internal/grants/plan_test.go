package grants

import (
	"reflect"
	"testing"
)

// free is a member no grant holds.
func free(name string, state MemberState) MemberView {
	return MemberView{Name: name, State: state}
}

// heldAdmitted is an admitted member that the grant of project holds.
func heldAdmitted(project string) MemberView {
	return MemberView{Name: "held-" + project, State: Admitted, Grant: "grant-" + project, Project: project}
}

func pool(name string, limit int, members ...MemberView) PoolView {
	return PoolView{Name: name, Cap: limit, Members: members}
}

func req(id, project string, needs ...Need) Waiting {
	return Waiting{ID: id, Project: project, Needs: needs}
}

func need(pool string, count int) Need {
	return Need{Pool: pool, Count: count}
}

type planCase struct {
	name    string
	pools   []PoolView
	waiting []Waiting
	want    Decision
}

func runPlanCases(t *testing.T, cases []planCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Plan(tc.pools, tc.waiting)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Plan() =\n  %+v\nwant\n  %+v", got, tc.want)
			}
		})
	}
}

func TestPlanRequestOrder(t *testing.T) {
	runPlanCases(t, []planCase{
		{
			name:    "earlier request takes the contested member",
			pools:   []PoolView{pool("x", 5, free("x1", Admitted))},
			waiting: []Waiting{req("r1", "p", need("x", 1)), req("r2", "q", need("x", 1))},
			want: Decision{Grants: []Assignment{
				{Request: "r1", Members: []Member{{Pool: "x", Name: "x1"}}},
			}},
		},
		{
			name: "later request takes what the earlier one leaves",
			pools: []PoolView{pool("x", 5,
				free("x1", Admitted), free("x2", Admitted))},
			waiting: []Waiting{req("r1", "p", need("x", 1)), req("r2", "q", need("x", 1))},
			want: Decision{Grants: []Assignment{
				{Request: "r1", Members: []Member{{Pool: "x", Name: "x1"}}},
				{Request: "r2", Members: []Member{{Pool: "x", Name: "x2"}}},
			}},
		},
	})
}

func TestPlanCaps(t *testing.T) {
	runPlanCases(t, []planCase{
		{
			name: "project at its cap reserves nothing",
			pools: []PoolView{pool("x", 2,
				heldAdmitted("p"), free("x2", Admitted), free("x3", Admitted))},
			waiting: []Waiting{req("r1", "p", need("x", 2))},
			want:    Decision{},
		},
		{
			name: "a request that reserves nothing leaves its members free",
			pools: []PoolView{pool("x", 2,
				heldAdmitted("p"), free("x2", Admitted))},
			waiting: []Waiting{req("r1", "p", need("x", 2)), req("r2", "q", need("x", 1))},
			want: Decision{Grants: []Assignment{
				{Request: "r2", Members: []Member{{Pool: "x", Name: "x2"}}},
			}},
		},
		{
			name: "project under its cap is granted",
			pools: []PoolView{pool("x", 2,
				heldAdmitted("p"), free("x2", Admitted))},
			waiting: []Waiting{req("r1", "p", need("x", 1))},
			want: Decision{Grants: []Assignment{
				{Request: "r1", Members: []Member{{Pool: "x", Name: "x2"}}},
			}},
		},
		{
			name: "other projects' holdings do not count against a project",
			pools: []PoolView{pool("x", 1,
				heldAdmitted("q"), free("x2", Admitted))},
			waiting: []Waiting{req("r1", "p", need("x", 1))},
			want: Decision{Grants: []Assignment{
				{Request: "r1", Members: []Member{{Pool: "x", Name: "x2"}}},
			}},
		},
		{
			name: "caps are per pool",
			pools: []PoolView{
				pool("x", 1, heldAdmitted("p")),
				pool("y", 1, free("y1", Admitted)),
			},
			waiting: []Waiting{req("r1", "p", need("y", 1))},
			want: Decision{Grants: []Assignment{
				{Request: "r1", Members: []Member{{Pool: "y", Name: "y1"}}},
			}},
		},
		{
			name: "reservations made earlier in the call count against the cap",
			pools: []PoolView{pool("x", 2,
				free("x1", Admitted), free("x2", Admitted), free("x3", Admitted))},
			waiting: []Waiting{req("r1", "p", need("x", 2)), req("r2", "p", need("x", 1))},
			want: Decision{Grants: []Assignment{
				{Request: "r1", Members: []Member{{Pool: "x", Name: "x1"}, {Pool: "x", Name: "x2"}}},
			}},
		},
		{
			name: "a request's own demand counts against the cap",
			pools: []PoolView{pool("x", 2,
				free("x1", Admitted), free("x2", Admitted), free("x3", Admitted))},
			waiting: []Waiting{req("r1", "p", need("x", 3))},
			want:    Decision{},
		},
		{
			name: "a negative count does not offset the demand against the cap",
			pools: []PoolView{pool("x", 1,
				free("x1", Admitted), free("x2", Admitted))},
			waiting: []Waiting{req("r1", "p", need("x", 2), need("x", -1))},
			want:    Decision{},
		},
		{
			name: "a need for a pool that is not listed reserves nothing",
			pools: []PoolView{pool("x", 5,
				free("x1", Admitted))},
			waiting: []Waiting{req("r1", "p", need("z", 1), need("x", 1))},
			want:    Decision{},
		},
	})
}

func TestPlanPreference(t *testing.T) {
	// Declared order: a asleep, b awake, c admitted, d waking, e admitting, f admitted, g unknown, h unavailable.
	members := []MemberView{
		free("a", Asleep),
		free("b", Awake),
		free("c", Admitted),
		free("d", Waking),
		free("e", Admitting),
		free("f", Admitted),
		free("g", Unknown),
		free("h", Unavailable),
	}
	runPlanCases(t, []planCase{
		{
			name:    "admitted members are granted in declared order",
			pools:   []PoolView{pool("x", 10, members...)},
			waiting: []Waiting{req("r1", "p", need("x", 2))},
			want: Decision{Grants: []Assignment{
				{Request: "r1", Members: []Member{{Pool: "x", Name: "c"}, {Pool: "x", Name: "f"}}},
			}},
		},
		{
			name:    "waking and admitting members come before awake ones",
			pools:   []PoolView{pool("x", 10, members...)},
			waiting: []Waiting{req("r1", "p", need("x", 3))},
			want:    Decision{},
		},
		{
			name:    "awake members come before asleep ones",
			pools:   []PoolView{pool("x", 10, members...)},
			waiting: []Waiting{req("r1", "p", need("x", 5))},
			want:    Decision{Prepare: []Member{{Pool: "x", Name: "b"}}},
		},
		{
			name:    "unknown and unavailable members are never reserved",
			pools:   []PoolView{pool("x", 10, members...)},
			waiting: []Waiting{req("r1", "p", need("x", 8))},
			want:    Decision{Prepare: []Member{{Pool: "x", Name: "b"}, {Pool: "x", Name: "a"}}},
		},
		{
			name: "unknown and unavailable members declared first are skipped",
			pools: []PoolView{pool("x", 10,
				free("g", Unknown), free("h", Unavailable), free("a", Asleep))},
			waiting: []Waiting{req("r1", "p", need("x", 1))},
			want:    Decision{Prepare: []Member{{Pool: "x", Name: "a"}}},
		},
		{
			name: "unknown and unavailable members do not count against a cap",
			pools: []PoolView{
				pool("x", 4, free("g", Unknown), free("h", Unavailable), free("a", Asleep), free("b", Asleep)),
				pool("y", 5, free("y1", Asleep)),
			},
			waiting: []Waiting{req("r1", "p", need("x", 4)), req("r2", "p", need("x", 2), need("y", 1))},
			want: Decision{Prepare: []Member{
				{Pool: "x", Name: "a"},
				{Pool: "x", Name: "b"},
				{Pool: "y", Name: "y1"},
			}},
		},
		{
			name: "prepared members keep declared order within a state",
			pools: []PoolView{pool("x", 10,
				free("b2", Awake), free("b1", Awake))},
			waiting: []Waiting{req("r1", "p", need("x", 2))},
			want:    Decision{Prepare: []Member{{Pool: "x", Name: "b2"}, {Pool: "x", Name: "b1"}}},
		},
	})
}

func TestPlanGrant(t *testing.T) {
	runPlanCases(t, []planCase{
		{
			name: "fully admitted request is granted at once, members in need order",
			pools: []PoolView{
				pool("x", 5, free("x1", Admitted)),
				pool("y", 5, free("y1", Admitted), free("y2", Admitted)),
			},
			waiting: []Waiting{req("r1", "p", need("y", 2), need("x", 1))},
			want: Decision{Grants: []Assignment{
				{Request: "r1", Members: []Member{
					{Pool: "y", Name: "y1"},
					{Pool: "y", Name: "y2"},
					{Pool: "x", Name: "x1"},
				}},
			}},
		},
		{
			name: "request short of admitted members is not granted and prepares its awake member",
			pools: []PoolView{pool("x", 5,
				free("x1", Admitted), free("x2", Asleep), free("x3", Awake))},
			waiting: []Waiting{req("r1", "p", need("x", 2))},
			want:    Decision{Prepare: []Member{{Pool: "x", Name: "x3"}}},
		},
		{
			name: "request covered in one pool and short in another is not granted",
			pools: []PoolView{
				pool("x", 5, free("x1", Admitted)),
				pool("y", 5, free("y1", Asleep)),
			},
			waiting: []Waiting{req("r1", "p", need("x", 1), need("y", 1))},
			want:    Decision{Prepare: []Member{{Pool: "y", Name: "y1"}}},
		},
	})
}

func TestPlanReservations(t *testing.T) {
	runPlanCases(t, []planCase{
		{
			name: "a reserved member is unavailable to later requests",
			pools: []PoolView{pool("x", 5,
				free("x1", Asleep), free("x2", Asleep))},
			waiting: []Waiting{req("r1", "p", need("x", 1)), req("r2", "q", need("x", 1))},
			want:    Decision{Prepare: []Member{{Pool: "x", Name: "x1"}, {Pool: "x", Name: "x2"}}},
		},
		{
			name: "members reserved by a request that is not granted are unavailable to later requests",
			pools: []PoolView{
				pool("x", 5, free("x1", Admitted)),
				pool("y", 5, free("y1", Asleep)),
			},
			waiting: []Waiting{req("r1", "p", need("x", 1), need("y", 1)), req("r2", "q", need("x", 1))},
			want:    Decision{Prepare: []Member{{Pool: "y", Name: "y1"}}},
		},
		{
			name: "waking and admitting members are reserved and never prepared",
			pools: []PoolView{pool("x", 5,
				free("x1", Waking), free("x2", Admitting), free("x3", Asleep))},
			waiting: []Waiting{req("r1", "p", need("x", 3)), req("r2", "q", need("x", 1))},
			want:    Decision{Prepare: []Member{{Pool: "x", Name: "x3"}}},
		},
		{
			name: "members held by a grant are unavailable",
			pools: []PoolView{pool("x", 5,
				heldAdmitted("q"))},
			waiting: []Waiting{req("r1", "p", need("x", 1))},
			want:    Decision{},
		},
	})
}
