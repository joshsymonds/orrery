package push_test

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
)

func TestWanted(t *testing.T) {
	cases := []struct {
		name string
		to   protocol.State
		want bool
	}{
		{
			name: "done is wanted",
			to:   protocol.StateDone, want: true,
		},
		{
			name: "waiting is wanted",
			to:   protocol.StateWaiting, want: true,
		},
		{
			name: "working is not wanted",
			to:   protocol.StateWorking, want: false,
		},
		{
			name: "unknown state is not wanted",
			to:   protocol.State("bogus"), want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := push.Wanted(tc.to); got != tc.want {
				t.Errorf("Wanted = %v, want %v", got, tc.want)
			}
		})
	}
}
