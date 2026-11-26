package node

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// RemoteHost is the hostexec.Host of one remote machine: its git, its file system and the session holders on the
// machine, which its Client's agent serves. Each method is a stub until the op that serves it is added, and it returns
// an error that says so.
type RemoteHost struct {
	client *Client
}

// NewRemoteHost returns the RemoteHost of the machine that client reaches.
func NewRemoteHost(client *Client) *RemoteHost {
	return &RemoteHost{client: client}
}

// Compile-time check that *RemoteHost is a hostexec.Host.
var _ hostexec.Host = (*RemoteHost)(nil)

// Git is not yet served.
func (*RemoteHost) Git(_ context.Context, _ hostexec.GitCall) ([]byte, error) {
	return nil, notServed("RemoteHost.Git")
}

// ReadFile is not yet served.
func (*RemoteHost) ReadFile(_ context.Context, _ string) ([]byte, error) {
	return nil, notServed("RemoteHost.ReadFile")
}

// ReadRooted is not yet served.
func (*RemoteHost) ReadRooted(_ context.Context, _, _ string) ([]byte, error) {
	return nil, notServed("RemoteHost.ReadRooted")
}

// Stat is not yet served.
func (*RemoteHost) Stat(_ context.Context, _ string) (fs.FileInfo, error) {
	return nil, notServed("RemoteHost.Stat")
}

// Lstat is not yet served.
func (*RemoteHost) Lstat(_ context.Context, _ string) (fs.FileInfo, error) {
	return nil, notServed("RemoteHost.Lstat")
}

// ReadDir is not yet served.
func (*RemoteHost) ReadDir(_ context.Context, _ string) ([]fs.DirEntry, error) {
	return nil, notServed("RemoteHost.ReadDir")
}

// WriteFile is not yet served.
func (*RemoteHost) WriteFile(_ context.Context, _ string, _ []byte, _ fs.FileMode) error {
	return notServed("RemoteHost.WriteFile")
}

// Symlink is not yet served.
func (*RemoteHost) Symlink(_ context.Context, _, _ string) error {
	return notServed("RemoteHost.Symlink")
}

// MkdirTemp is not yet served.
func (*RemoteHost) MkdirTemp(_ context.Context, _, _ string) (string, error) {
	return "", notServed("RemoteHost.MkdirTemp")
}

// MkdirAll is not yet served.
func (*RemoteHost) MkdirAll(_ context.Context, _ string, _ fs.FileMode) error {
	return notServed("RemoteHost.MkdirAll")
}

// Remove is not yet served.
func (*RemoteHost) Remove(_ context.Context, _ string) error {
	return notServed("RemoteHost.Remove")
}

// RemoveAll is not yet served.
func (*RemoteHost) RemoveAll(_ context.Context, _ string) error {
	return notServed("RemoteHost.RemoveAll")
}

// EvalSymlinks is not yet served.
func (*RemoteHost) EvalSymlinks(_ context.Context, _ string) (string, error) {
	return "", notServed("RemoteHost.EvalSymlinks")
}

// SessionDir is not yet served.
func (*RemoteHost) SessionDir(_, _ string) (string, error) {
	return "", notServed("RemoteHost.SessionDir")
}

// Launch is not yet served.
func (*RemoteHost) Launch(_ context.Context, _ hostexec.Launch) (hostexec.Held, error) {
	return nil, notServed("RemoteHost.Launch")
}

// Reattach is not yet served.
func (*RemoteHost) Reattach(_ context.Context, _, _ string) (hostexec.Held, error) {
	return nil, notServed("RemoteHost.Reattach")
}

// Stop is not yet served.
func (*RemoteHost) Stop(_ context.Context, _ string) error {
	return notServed("RemoteHost.Stop")
}

// Clear is not yet served.
func (*RemoteHost) Clear(_ context.Context, _ string) error {
	return notServed("RemoteHost.Clear")
}

// ExitRecord is not yet served. Its signature has no error to return, so the stub reports that no record is readable.
func (*RemoteHost) ExitRecord(_ string) (hostexec.Exit, bool) {
	return hostexec.Exit{}, false
}

// KillLeftovers is not yet served.
func (*RemoteHost) KillLeftovers(_ string) error {
	return notServed("RemoteHost.KillLeftovers")
}

// notServed is the error a stub returns: the op it names is not yet served by the agent.
func notServed(op string) error {
	return fmt.Errorf("node: %s is not yet served", op)
}
