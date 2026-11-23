package holder

import (
	"context"
	"errors"
	"net"
)

// errNotImplemented is the error a stub in this file returns until its body lands.
var errNotImplemented = errors.New("holder: link re-attach is not implemented")

// Link is a route from a controller to one session's holder that does not go through the holder's socket on this
// machine: a holder on another host, reached through that host's agent. The holder's wire runs over the connections
// the link dials, unchanged.
type Link interface {
	// Dial returns a connection that reaches the holder. ReattachLink dials it once to attach, and again to re-attach
	// after the connection drops. When the holder has ended, or cannot be attached, Dial's error wraps ErrGone,
	// ErrUnattachable or ErrNoAnswer, as Reattach's errors do.
	Dial(ctx context.Context) (net.Conn, error)
	// Done closes once the holder has ended. It is how a Process learns the holder's end over a link, where a session
	// lock reports it on this machine.
	Done() <-chan struct{}
	// Exit returns the holder's exit record, and whether the holder wrote one. It is meaningful once Done has closed.
	Exit() (Exit, bool)
}

// ReattachLink takes over the holder that link reaches, as Reattach takes over the holder on a socket. It attaches at
// replayFrom, speaking version, over connections that link dials, and returns the Process, whose stdout starts at
// replayFrom, and the ids of the writes the holder has already applied to the child's stdin. The Process learns the
// holder's end from link's Done and Exit. Its errors are Reattach's.
//
// ReattachLink is not implemented yet and always returns an error.
func ReattachLink(_ context.Context, _ Link, _ int64, _ int) (*Process, []string, error) {
	return nil, nil, errNotImplemented
}
