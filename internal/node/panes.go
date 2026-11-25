package node

import (
	"context"
	"net/http"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// Load is the load of a host. LoadPercent is the one-minute load average over the online CPUs, times 100, and it is
// uncapped, so a host busier than its CPUs reads above 100. MemoryPercent is (MemTotal - MemAvailable) / MemTotal,
// times 100.
type Load struct {
	LoadPercent   float64 `json:"loadPercent"`
	MemoryPercent float64 `json:"memoryPercent"`
}

// PaneSnapshot is one pane of a host's tmux servers, as a controller lists it. Server is the tmux socket, as
// tmuxctl.Server.Column gives it. Pane is the tmux pane id, and ID is the pane's @orrery_id. Session and Window
// locate the pane in tmux, and Fields holds the ten options that orrery mirrors onto the pane.
type PaneSnapshot struct {
	Server  string       `json:"server"`
	Pane    string       `json:"pane"`
	ID      string       `json:"id"`
	Session string       `json:"session"`
	Window  int          `json:"window"`
	Fields  store.Fields `json:"fields"`
}

// PaneTransition is a change of one pane's state, with the facts of the update that caused it. From and To are the
// states either side of the change. Cwd, BaseURL, Transcript and LastMessage are the facts that update carried, and
// Failed and APIError say whether the turn ended in an API error and what it was.
type PaneTransition struct {
	From        protocol.State `json:"from"`
	To          protocol.State `json:"to"`
	Cwd         string         `json:"cwd"`
	BaseURL     string         `json:"baseUrl"`
	Transcript  string         `json:"transcript"`
	LastMessage string         `json:"lastMessage"`
	Failed      bool           `json:"failed"`
	APIError    string         `json:"apiError"`
}

// PaneEvent is one event of a host's pane watch. Full is set on a full listing, which Listing holds: every pane of
// the host's tmux servers. Otherwise Pane is one pane after a change, and Transition is set with it when that change
// moved the pane's state.
type PaneEvent struct {
	Full       bool            `json:"full,omitempty"`
	Listing    []PaneSnapshot  `json:"listing,omitempty"`
	Pane       *PaneSnapshot   `json:"pane,omitempty"`
	Transition *PaneTransition `json:"transition,omitempty"`
}

// HostPanes serves the panes of one host to a controller: the panes of this machine's own tmux servers. An AgentConfig
// names one, and the agent's pane ops answer with it.
type HostPanes interface {
	// Handler serves orrery.v1.Panes for this host's own panes. It is what an OpPanes connection carries.
	Handler() http.Handler
	// Watch sends a full listing of the host's panes first, then each change, until ctx ends or send fails. It returns the
	// error that ended the watch.
	Watch(ctx context.Context, send func(PaneEvent) error) error
	// Load returns the host's Load.
	Load(ctx context.Context) (Load, error)
}
