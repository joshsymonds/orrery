package holderhost

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holder"
)

// LocalConfig configures a Local host: the programs and environment its sessions run with, where their holders live,
// and how they are started and attached.
type LocalConfig struct {
	// Shell is the owner's login shell, an absolute path, that each session's claude runs through.
	Shell string
	// Claude is the claude binary, an absolute path.
	Claude string
	// Environ is the environment each session inherits, as session.Config takes it.
	Environ []string
	// StateDir is the state directory. Each session's holder directory is under StateDir/holders, as holder.Launch and
	// the engine host's holders subdirectory have it.
	StateDir string
	// Scope is the absolute path of systemd-run, which runs each holder in its own user scope. Empty starts the holders
	// plainly.
	Scope string
	// CronHook makes a session that is not a step take the settings that carry its cron tools to the orrery hook. A step
	// never takes them. Another epic removes the hook, and with it this field.
	CronHook bool
	// AttachTimeout bounds the hello and the attach of each Reattach. Zero takes holder.AttachTimeout.
	AttachTimeout time.Duration
}

// Local is the host this machine runs its sessions on. Each session's holder is the one holder.Launch starts under
// StateDir/holders, and the host attaches to it with holder.Reattach.
type Local struct {
	cfg LocalConfig
}

// NewLocal returns the Local host that cfg describes.
func NewLocal(cfg LocalConfig) *Local {
	return &Local{cfg: cfg}
}

var _ Host = (*Local)(nil)

// Launch starts the session on this machine. It is not implemented yet and returns a not-implemented error.
func (*Local) Launch(_ context.Context, _ Spec) (*session.Session, error) {
	return nil, notImplemented("Launch")
}

// Reattach takes over the holder of session id on this machine. It is not implemented yet and returns a not-implemented
// error.
func (*Local) Reattach(_ context.Context, _ string, _ int64, _ int) (*holder.Process, []string, error) {
	return nil, nil, notImplemented("Reattach")
}

// Stop ends the holder of session id on this machine. It is not implemented yet and returns a not-implemented error.
func (*Local) Stop(_ context.Context, _ string) error {
	return notImplemented("Stop")
}

// Kill ends the holder of session id on this machine without asking it. It is not implemented yet and returns a
// not-implemented error.
func (*Local) Kill(_ context.Context, _ string) error {
	return notImplemented("Kill")
}

// Clear empties the holder directory of session id on this machine. It is not implemented yet and returns a
// not-implemented error.
func (*Local) Clear(_ context.Context, _ string) error {
	return notImplemented("Clear")
}

// StopOrphan stops the child the gone holder of session id recorded on this machine. It is not implemented yet and
// returns a not-implemented error.
func (*Local) StopOrphan(_ context.Context, _ string) error {
	return notImplemented("StopOrphan")
}

// OpenGone returns the child of session id on this machine as its gone holder left it. It is not implemented yet and
// returns a not-implemented error.
func (*Local) OpenGone(_ context.Context, _ string, _ int64) (Gone, error) {
	return nil, notImplemented("OpenGone")
}

// Sessions returns the ids that have a holder directory on this machine. It is not implemented yet and returns a
// not-implemented error.
func (*Local) Sessions(_ context.Context) ([]string, error) {
	return nil, notImplemented("Sessions")
}

// Reach reports that this machine is not reachable and returns a nil channel. It is a stub until Reach is implemented.
func (*Local) Reach() (bool, <-chan struct{}) {
	return false, nil
}

// notImplemented is the error a stub method of Local returns, naming the method.
func notImplemented(method string) error {
	return fmt.Errorf("holderhost: %s is not implemented", method)
}
