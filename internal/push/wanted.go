package push

import (
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Wanted reports whether a transition into to sends a Web Push. Waiting always does. Done does unless background, which
// is whether the turn ended with a subagent or workflow task still running. Other states never do.
func Wanted(to protocol.State, background bool) bool {
	switch to {
	case protocol.StateWaiting:
		return true
	case protocol.StateDone:
		return !background
	default:
		return false
	}
}
