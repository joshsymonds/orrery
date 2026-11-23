package server

import (
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/conversation"
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
// Every call acts on this machine's panes. The controller has already routed each call here by its host, so the host
// a call names is not checked. The handler has no login guard, no store reader, and no seen, read or pane-send
// reporter, which is what an agent serves behind node.v1's admission. Its streams have no change feed, so they step on
// their transcript's watch and on their keepalive.
func HostPanesHandler(cfg HostPanesConfig) (string, http.Handler) {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	local := newPanesServer(
		cfg.Host, cfg.Tmux, nil, conversation.NewNewest(), conversation.NewGoals(), conversation.NewQueues(),
		&paneLocks{locks: make(map[string]*sync.Mutex)}, &paneLocks{locks: make(map[string]*sync.Mutex)},
		nil, nil, nil, io.Discard, now, newChangeHub(nil, cfg.NewTimer), cfg.NewTimer,
		defaultConversationKeepalive, defaultEventSendTimeout, nil,
	)
	return newPanesHandler(local)
}
