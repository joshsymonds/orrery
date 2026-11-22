package push_test

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
)

func TestWanted(t *testing.T) {
	cases := []struct {
		name       string
		to         protocol.State
		background bool
		want       bool
	}{
		{
			name: "done with background work is not wanted",
			to:   protocol.StateDone, background: true, want: false,
		},
		{
			name: "done without background work is wanted",
			to:   protocol.StateDone, background: false, want: true,
		},
		{
			name: "waiting is wanted",
			to:   protocol.StateWaiting, background: false, want: true,
		},
		{
			name: "waiting with background work is wanted",
			to:   protocol.StateWaiting, background: true, want: true,
		},
		{
			name: "working is not wanted",
			to:   protocol.StateWorking, background: false, want: false,
		},
		{
			name: "unknown state is not wanted",
			to:   protocol.State("bogus"), background: false, want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := push.Wanted(tc.to, tc.background); got != tc.want {
				t.Errorf("Wanted = %v, want %v", got, tc.want)
			}
		})
	}
}
