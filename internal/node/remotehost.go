package node

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/hostexec"
)

// RemoteHost is the hostexec.Host of one remote machine: its git, its file system and the session holders on the machine,
// which its Client's agent serves. Each call is answered by the agent, and returns what hostexec.Local returns for the same
// call on that machine.
type RemoteHost struct {
	client *Client
}

// NewRemoteHost returns the RemoteHost of the machine that client reaches.
func NewRemoteHost(client *Client) *RemoteHost {
	return &RemoteHost{client: client}
}

// Compile-time check that *RemoteHost is a hostexec.Host.
var _ hostexec.Host = (*RemoteHost)(nil)

// Git runs the git run call describes on the remote machine, and returns its stdout untrimmed.
func (r *RemoteHost) Git(ctx context.Context, call hostexec.GitCall) ([]byte, error) {
	return r.client.hostGit(ctx, call)
}

// ReadFile reads the file at path on the remote machine.
func (r *RemoteHost) ReadFile(ctx context.Context, path string) ([]byte, error) {
	res, err := r.client.hostFile(ctx, "read", FileSpec{Op: FileRead, Path: path}, path)
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

// ReadRooted reads the regular file name inside root on the remote machine. A file that is not regular fails with an error
// matching hostexec.ErrNotRegular.
func (r *RemoteHost) ReadRooted(ctx context.Context, root, name string) ([]byte, error) {
	where := filepath.Join(root, name)
	res, err := r.client.hostFile(ctx, "read", FileSpec{Op: FileReadRooted, Root: root, Name: name}, where)
	if err != nil {
		return nil, err
	}
	return res.Data, nil
}

// Stat describes the file at path on the remote machine, following symlinks.
func (r *RemoteHost) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	return r.client.hostInfo(ctx, "stat", FileSpec{Op: FileStat, Path: path}, path)
}

// Lstat describes the file at path on the remote machine without following a final symlink.
func (r *RemoteHost) Lstat(ctx context.Context, path string) (fs.FileInfo, error) {
	return r.client.hostInfo(ctx, "lstat", FileSpec{Op: FileLstat, Path: path}, path)
}

// ReadDir lists the directory at path on the remote machine.
func (r *RemoteHost) ReadDir(ctx context.Context, path string) ([]fs.DirEntry, error) {
	return r.client.hostEntries(ctx, path)
}

// WriteFile writes data to the file at path on the remote machine, creating it with perm when it is missing.
func (r *RemoteHost) WriteFile(ctx context.Context, path string, data []byte, perm fs.FileMode) error {
	_, err := r.client.hostFile(ctx, "write", FileSpec{Op: FileWrite, Path: path, Data: data, Perm: perm}, path)
	return err
}

// Symlink makes link a symbolic link to target on the remote machine.
func (r *RemoteHost) Symlink(ctx context.Context, target, link string) error {
	_, err := r.client.hostFile(ctx, "symlink", FileSpec{Op: FileSymlink, Path: link, Target: target}, link)
	return err
}

// MkdirTemp makes a new directory in dir on the remote machine, named from pattern, and returns its path.
func (r *RemoteHost) MkdirTemp(ctx context.Context, dir, pattern string) (string, error) {
	res, err := r.client.hostFile(ctx, "mkdirtemp", FileSpec{Op: FileMkdirTemp, Path: dir, Pattern: pattern}, dir)
	if err != nil {
		return "", err
	}
	return res.Path, nil
}

// MkdirAll makes path and any missing parents of it on the remote machine.
func (r *RemoteHost) MkdirAll(ctx context.Context, path string, perm fs.FileMode) error {
	_, err := r.client.hostFile(ctx, "mkdir", FileSpec{Op: FileMkdirAll, Path: path, Perm: perm}, path)
	return err
}

// Remove removes the file or empty directory at path on the remote machine.
func (r *RemoteHost) Remove(ctx context.Context, path string) error {
	_, err := r.client.hostFile(ctx, "remove", FileSpec{Op: FileRemove, Path: path}, path)
	return err
}

// RemoveAll removes path and everything under it on the remote machine. A path that does not exist is not an error.
func (r *RemoteHost) RemoveAll(ctx context.Context, path string) error {
	_, err := r.client.hostFile(ctx, "removeall", FileSpec{Op: FileRemoveAll, Path: path}, path)
	return err
}

// EvalSymlinks returns path on the remote machine with every symlink in it resolved.
func (r *RemoteHost) EvalSymlinks(ctx context.Context, path string) (string, error) {
	res, err := r.client.hostFile(ctx, "evalsymlinks", FileSpec{Op: FileEvalSymlinks, Path: path}, path)
	if err != nil {
		return "", err
	}
	return res.Path, nil
}

// SessionDir returns the directory of session's holder files under holders, as holder.SessionDir computes it. The hosts
// share the state layout, so the path is the same on the remote machine.
func (r *RemoteHost) SessionDir(holders, session string) (string, error) {
	dir, err := holder.SessionDir(holders, session)
	if err != nil {
		return "", fmt.Errorf("node: session dir of %s on %s: %w", session, r.client.cfg.Name, err)
	}
	return dir, nil
}

// Launch starts the child cfg describes under a holder on the remote machine, and returns it once the agent's holder is
// attached.
func (r *RemoteHost) Launch(ctx context.Context, cfg hostexec.Launch) (hostexec.Held, error) {
	return r.client.hostLaunch(ctx, cfg)
}

// Reattach takes over the gate holder that serves dir on the remote machine. It fails with an error matching
// hostexec.ErrGone when no holder holds the session.
func (r *RemoteHost) Reattach(ctx context.Context, dir, scope string) (hostexec.Held, error) {
	return r.client.hostReattach(ctx, dir, scope)
}

// Stop ends the holder that serves dir on the remote machine and leaves dir in place. A dir with no holder is not an error.
func (r *RemoteHost) Stop(ctx context.Context, dir string) error {
	return r.client.hostStop(ctx, dir)
}

// Clear stops the holder that serves dir on the remote machine, if one does, and removes dir with everything in it.
func (r *RemoteHost) Clear(ctx context.Context, dir string) error {
	return r.client.hostClear(ctx, dir)
}

// ExitRecord returns the exit record that the holder of dir left on the remote machine, and false when dir holds no
// readable record.
func (r *RemoteHost) ExitRecord(dir string) (hostexec.Exit, bool) {
	return r.client.hostExitRecord(dir)
}

// KillLeftovers kills the processes that a session's child left running in its process group on the remote machine.
func (r *RemoteHost) KillLeftovers(dir string) error {
	return r.client.hostKillLeftovers(dir)
}
