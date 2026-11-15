package closer

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// TestNewestOtherSessionPicksTheMostRecentlyActiveKeptSession checks the session a closing client moves to, on
// synthetic activity times: the most recently active session that is not closing and not a shell session, the name
// that sorts first among sessions equally recent, and none when no such session remains.
func TestNewestOtherSessionPicksTheMostRecentlyActiveKeptSession(t *testing.T) {
	shellSession := protocol.ShellSessionPrefix + "popup"
	cases := []struct {
		name    string
		panes   []protocol.Pane
		closing map[string]bool
		want    string
	}{
		{
			name: "newest kept session",
			panes: []protocol.Pane{
				{Session: "alpha", Activity: 400}, {Session: "beta", Activity: 100},
				{Session: "delta", Activity: 200}, {Session: "gamma", Activity: 300},
				{Session: shellSession, Activity: 500},
			},
			closing: map[string]bool{"alpha": true},
			want:    "gamma",
		},
		{
			name:    "equally recent sessions",
			panes:   []protocol.Pane{{Session: "gamma", Activity: 100}, {Session: "beta", Activity: 100}},
			closing: map[string]bool{},
			want:    "beta",
		},
		{
			name:    "only closing and shell sessions",
			panes:   []protocol.Pane{{Session: "alpha", Activity: 100}, {Session: shellSession, Activity: 200}},
			closing: map[string]bool{"alpha": true},
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := newestOtherSession(tc.panes, tc.closing); got != tc.want {
				t.Errorf("newestOtherSession = %q, want %q", got, tc.want)
			}
		})
	}
}
