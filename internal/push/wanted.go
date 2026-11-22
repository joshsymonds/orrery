package push

import (
	"github.com/joshsymonds/orrery/internal/protocol"
)

// Wanted reports whether a transition into to sends a Web Push. Waiting and done always do. Other states never do.
func Wanted(to protocol.State) bool {
	switch to {
	case protocol.StateWaiting, protocol.StateDone:
		return true
	default:
		return false
	}
}
