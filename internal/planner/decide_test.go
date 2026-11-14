package planner

import (
	"errors"
	"testing"
)

func TestDecide(t *testing.T) {
	roleErr := errors.New(`no role file: "planner" is not defined in roles.toml`)
	cases := []struct {
		name    string
		state   State
		roleErr error
		want    Action
	}{
		{"parked epic gets none", State{Parked: true, Pending: 1}, nil, Action{Kind: ActionNone}},
		{"paused epic gets none", State{Paused: true, Pending: 1}, nil, Action{Kind: ActionNone}},
		{"running turn gets none", State{Running: true, Pending: 1}, nil, Action{Kind: ActionNone}},
		{"no pending trigger gets none", State{}, nil, Action{Kind: ActionNone}},
		{
			"role error with pending triggers parks with its text",
			State{Pending: 2},
			roleErr,
			Action{Kind: ActionPark, Reason: roleErr.Error()},
		},
		{"claimable epic gets claim", State{Pending: 1}, nil, Action{Kind: ActionClaim}},
		{"parked wins over role error", State{Parked: true, Pending: 1}, roleErr, Action{Kind: ActionNone}},
		{"paused wins over role error", State{Paused: true, Pending: 1}, roleErr, Action{Kind: ActionNone}},
		{"running wins over role error", State{Running: true, Pending: 1}, roleErr, Action{Kind: ActionNone}},
		{"no pending wins over role error", State{}, roleErr, Action{Kind: ActionNone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.state, tc.roleErr)
			if got != tc.want {
				t.Errorf("Decide(%+v, %v) = %+v, want %+v", tc.state, tc.roleErr, got, tc.want)
			}
		})
	}
}
