// Package holderhost holds one host's session holders, by session id. A host is this machine, which Local runs
// sessions on, or another of the owner's machines, which a node Client reaches through that machine's agent. The
// session manager reaches every host through Host, so it never needs to know which one a session runs on.
//
// A session is never relaunched on another host: two copies of a session would write one tree.
package holderhost

import (
	"context"
	"errors"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holder"
)

// ErrUnreachable means the host could not be reached: a remote host's agent did not answer, or the connection to it
// dropped before the answer came.
var ErrUnreachable = errors.New("holderhost: host unreachable")

// Host is one host's session holders, by session id. Each method acts on the holder of the session that id names, as
// the holder package's function of the same name acts on a session's holder directory.
type Host interface {
	// Launch starts the session that spec describes under a holder on the host, and returns it as a session, as
	// session.Start does. The session's id is spec.Session.
	Launch(ctx context.Context, spec Spec) (*session.Session, error)
	// Reattach takes over the holder of session id, at replayFrom, speaking version, as holder.Reattach does. Its errors
	// match holder.ErrGone, holder.ErrUnattachable and holder.ErrNoAnswer exactly as holder.Reattach's do, or match
	// ErrUnreachable.
	Reattach(ctx context.Context, id string, replayFrom int64, version int) (*holder.Process, []string, error)
	// Stop ends the holder of session id, and leaves its directory in place, as holder.Stop does.
	Stop(ctx context.Context, id string) error
	// Kill ends the holder of session id without asking it, as holder.Kill does.
	Kill(ctx context.Context, id string) error
	// Clear empties the holder directory of session id so a new holder can start there, as holder.Clear does.
	Clear(ctx context.Context, id string) error
	// StopOrphan stops the child that the gone holder of session id recorded, as holder.StopOrphan does.
	StopOrphan(ctx context.Context, id string) error
	// OpenGone returns the child of session id as its gone holder left it, with its stdout from offset from, as
	// holder.OpenGone does.
	OpenGone(ctx context.Context, id string, from int64) (Gone, error)
	// Sessions returns the ids that have a holder directory on the host.
	Sessions(ctx context.Context) ([]string, error)
	// Reach reports whether the host is reachable now, and returns a channel that closes at the next change in that.
	Reach() (bool, <-chan struct{})
}

// Gone is a session's child as its gone holder leaves it: a session.Process whose exit record Exit returns. Its stdin
// takes no writes and its Stop does nothing, since the child has ended. *holder.Gone satisfies Gone.
type Gone interface {
	session.Process
	// Exit returns the exit record, and whether the holder wrote one.
	Exit() (holder.Exit, bool)
}

var _ Gone = (*holder.Gone)(nil)

// Spec describes one session's launch on a host. Session names the session, and the holder of the session is found by
// that id. Dir is the session's working directory. Resume, when set, is the claude session the launch resumes. Model,
// Agent, Agents and Schema are the claude options the launch takes, as session.Config has them, each empty when unset.
// Step marks a step session.
type Spec struct {
	Session string
	Dir     string
	Resume  string
	Model   string
	Agent   string
	Agents  string
	Schema  string
	Step    bool
}
