package server

import (
	"net/http"
	"time"

	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// HostPanesConfig configures HostPanesHandler: the tmux client of this machine, the orrery name that each listed pane
// names as its host, the clock, and the timer that its streams run on.
type HostPanesConfig struct {
	// Tmux runs tmux against this machine's own servers.
	Tmux *tmuxctl.Client
	// Host is the orrery name written into each listed Pane's host.
	Host string
	// Now is the clock the handler reads. Nil takes time.Now.
	Now func() time.Time
	// NewTimer arms the keepalive timers of the handler's streams, as Config.NewTimer does. Nil takes time.NewTimer.
	NewTimer func(time.Duration) (<-chan time.Time, func() bool)
}

// HostPanesHandler returns the path and handler that serve orrery.v1.Panes over one machine's own tmux and transcripts.
// It has no login guard, no store reader, and no seen, read or pane-send reporter, which is what an agent serves behind
// node.v1's admission. Until the pane tasks fill in its methods, every RPC of the handler answers CodeUnimplemented.
func HostPanesHandler(_ HostPanesConfig) (string, http.Handler) {
	return orreryv1connect.NewPanesHandler(orreryv1connect.UnimplementedPanesHandler{})
}
