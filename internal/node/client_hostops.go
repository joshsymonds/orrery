package node

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path/filepath"
	"time"

	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/hostexec"
)

// Compile-time check that *Client is the grants.HostProcesses of its machine.
var _ grants.HostProcesses = (*Client)(nil)

// ProcessAlive reports whether the process with pid lives on the remote machine under the start time start, as the agent
// answers it.
func (c *Client) ProcessAlive(ctx context.Context, pid int, start int64) (bool, error) {
	resp, err := c.hostCall(ctx, Request{Version: Version, Op: OpProcessAlive, PID: pid, Start: start})
	if err != nil {
		return false, err
	}
	return resp.Alive, nil
}

// HolderAlive reports whether the holder directory dir lives on the remote machine, as the agent answers it.
func (c *Client) HolderAlive(ctx context.Context, dir string) (bool, error) {
	resp, err := c.hostCall(ctx, Request{Version: Version, Op: OpHolderAlive, HolderDir: dir})
	if err != nil {
		return false, err
	}
	return resp.Alive, nil
}

// hostGit runs the git run call describes on the remote machine, and returns git's stdout untrimmed, as Local does. A run
// that git ended with a non-zero exit or a kill returns a *hostexec.GitError with the stdout and stderr git wrote. A git
// that could not start returns an error that is not one. A call that names a WaitDelay fails before the agent is asked,
// since the agent's run has no way to take one.
func (c *Client) hostGit(ctx context.Context, call hostexec.GitCall) ([]byte, error) {
	if call.WaitDelay != 0 {
		return nil, fmt.Errorf("node: git on %s: a WaitDelay is not carried to a remote host", c.cfg.Name)
	}
	spec := GitSpec{Dir: call.Dir, Args: call.Args, Env: call.Env, Stdin: call.Stdin, Remote: call.Remote}
	resp, err := c.hostCall(ctx, Request{Version: Version, Op: OpGit, Git: &spec})
	if err != nil {
		return nil, err
	}
	res := resp.Git
	switch {
	case res == nil:
		return nil, fmt.Errorf("node: git on %s: the answer names no git result", c.cfg.Name)
	case !res.Started:
		return nil, fmt.Errorf("node: git on %s could not start: %s", c.cfg.Name, resp.Message)
	case res.ExitCode != 0:
		return res.Stdout, &hostexec.GitError{
			Args:     call.Args,
			ExitCode: res.ExitCode,
			Stdout:   res.Stdout,
			Stderr:   res.Stderr,
			Err:      gitCause(res.ExitCode),
		}
	}
	return res.Stdout, nil
}

// gitCause returns the cause that a GitError of the agent's run carries. The agent reports the exit code only, so the cause
// names the code, or says that a signal killed git when the code is -1.
func gitCause(code int) error {
	if code < 0 {
		return errors.New("git was killed by a signal")
	}
	return fmt.Errorf("exit status %d", code)
}

// hostFile calls the file op spec on the remote machine. op names the file method the op calls, and where the path the
// op acts on, for the errors it returns. An op that the agent answers with an errno returns the fs error of that kind, so
// errors.Is holds as it does for Local.
func (c *Client) hostFile(ctx context.Context, op string, spec FileSpec, where string) (*FileResult, error) {
	resp, err := c.hostCall(ctx, Request{Version: Version, Op: OpFile, File: &spec})
	if err != nil {
		return nil, err
	}
	res := resp.File
	if res == nil {
		return nil, fmt.Errorf("node: %s %s on %s: the answer names no file result", op, where, c.cfg.Name)
	}
	if res.Errno != "" {
		return nil, fileErrnoErr(res.Errno, op, where)
	}
	return res, nil
}

// fileErrnoErr returns the error that an errno names for the op on where: the fs error of its kind, as os returns it, or
// hostexec.ErrNotRegular.
func fileErrnoErr(errno FileErrno, op, where string) error {
	var kind error
	switch errno {
	case ErrnoNotExist:
		kind = fs.ErrNotExist
	case ErrnoExist:
		kind = fs.ErrExist
	case ErrnoPermission:
		kind = fs.ErrPermission
	case ErrnoNotRegular:
		return fmt.Errorf("%w: %s", hostexec.ErrNotRegular, where)
	default:
		return fmt.Errorf("node: %s %s: the agent names an unknown errno %q", op, where, errno)
	}
	return &fs.PathError{Op: op, Path: where, Err: kind}
}

// hostInfo returns the fs.FileInfo that a stat or lstat op answers with.
func (c *Client) hostInfo(ctx context.Context, op string, spec FileSpec, where string) (fs.FileInfo, error) {
	res, err := c.hostFile(ctx, op, spec, where)
	if err != nil {
		return nil, err
	}
	if res.Info == nil {
		return nil, fmt.Errorf("node: %s %s on %s: the answer names no file info", op, where, c.cfg.Name)
	}
	return remoteInfo{info: *res.Info}, nil
}

// hostEntries returns the directory entries that a read-dir op answers with.
func (c *Client) hostEntries(ctx context.Context, path string) ([]fs.DirEntry, error) {
	res, err := c.hostFile(ctx, "readdir", FileSpec{Op: FileReadDir, Path: path}, path)
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, 0, len(res.Entries))
	for _, info := range res.Entries {
		entries = append(entries, remoteEntry{info: remoteInfo{info: info}})
	}
	return entries, nil
}

// remoteInfo is the fs.FileInfo of a file that the agent described.
type remoteInfo struct {
	info FileInfo
}

// Name returns the base name of the file.
func (i remoteInfo) Name() string { return i.info.Name }

// Size returns the length of the file in bytes.
func (i remoteInfo) Size() int64 { return i.info.Size }

// Mode returns the file mode bits of the file.
func (i remoteInfo) Mode() fs.FileMode { return i.info.Mode }

// ModTime returns the modification time of the file.
func (i remoteInfo) ModTime() time.Time { return i.info.ModTime }

// IsDir reports whether the file is a directory.
func (i remoteInfo) IsDir() bool { return i.info.IsDir }

// Sys returns nil, since the agent sends no underlying system data.
func (i remoteInfo) Sys() any { return nil }

// remoteEntry is the fs.DirEntry of an entry that the agent listed.
type remoteEntry struct {
	info remoteInfo
}

// Name returns the base name of the entry.
func (e remoteEntry) Name() string { return e.info.Name() }

// IsDir reports whether the entry is a directory.
func (e remoteEntry) IsDir() bool { return e.info.IsDir() }

// Type returns the type bits of the entry's mode.
func (e remoteEntry) Type() fs.FileMode { return e.info.Mode().Type() }

// Info returns the file info that the agent described for the entry.
func (e remoteEntry) Info() (fs.FileInfo, error) { return e.info, nil }

// hostLaunch starts the child that cfg describes under a holder on the remote machine, and attaches to it, as Local does.
// The session's holder under cfg.StateDir is cleared first, as Local's launch clears it. The agent then launches the gate,
// and the Client attaches to the holder through gate-attach, so the returned Held is the attached holder's Process. A
// launch needs a state directory that the controller names, since the holder's directory is computed from it, and it
// fails on a LimitSignal, which is a test seam that does not travel to the agent.
func (c *Client) hostLaunch(ctx context.Context, cfg hostexec.Launch) (hostexec.Held, error) {
	switch {
	case cfg.StateDir == "":
		return nil, fmt.Errorf("node: launch of %s on %s names no state directory", cfg.Session, c.cfg.Name)
	case cfg.LimitSignal != "":
		return nil, fmt.Errorf("node: launch of %s on %s names a limit signal, which a remote launch cannot carry",
			cfg.Session, c.cfg.Name)
	}
	dir, err := holder.SessionDir(filepath.Join(cfg.StateDir, holdersName), cfg.Session)
	if err != nil {
		return nil, fmt.Errorf("node: launch of %s on %s: %w", cfg.Session, c.cfg.Name, err)
	}
	if clearErr := c.hostClear(ctx, dir); clearErr != nil {
		return nil, clearErr
	}
	spec := GateSpec{
		Scope:    cfg.Scope,
		StateDir: cfg.StateDir,
		Session:  cfg.Session,
		Path:     cfg.Path,
		Args:     cfg.Args,
		Dir:      cfg.Dir,
		Env:      cfg.Env,
		Limit:    cfg.Limit,
		Tail:     cfg.Tail,
	}
	if _, err = c.hostCall(ctx, Request{Version: Version, Op: OpGateLaunch, Gate: &spec}); err != nil {
		return nil, err
	}
	process, err := c.attachGate(ctx, c.gateLink(dir, cfg.Scope))
	if err != nil {
		// The gate runs with no controller now, so it is cleared, as Local's launch abandons its holder when the attach fails.
		cleanCtx, cancel := c.bound(context.WithoutCancel(ctx))
		defer cancel()
		return nil, errors.Join(fmt.Errorf("node: attach %s on %s: %w", cfg.Session, c.cfg.Name, err),
			c.hostClear(cleanCtx, dir))
	}
	return process, nil
}

// hostReattach takes over the gate holder that serves dir on the remote machine, launched under scope, through gate-attach.
// It fails with an error matching ErrGone when no holder holds the session.
func (c *Client) hostReattach(ctx context.Context, dir, scope string) (hostexec.Held, error) {
	process, err := c.attachGate(ctx, c.gateLink(dir, scope))
	if err != nil {
		return nil, fmt.Errorf("node: reattach %s on %s: %w", dir, c.cfg.Name, err)
	}
	return process, nil
}

// attachGate takes over the gate holder that link reaches, as holder.ReattachLink does. While the agent cannot be reached,
// it attaches again after each retry interval, until the agent answers or ctx ends.
func (c *Client) attachGate(ctx context.Context, link *nodeLink) (*holder.Process, error) {
	for {
		process, _, err := holder.ReattachLink(ctx, link, 0, holder.WireVersion)
		if err == nil {
			return process, nil
		}
		if !errors.Is(err, holderhost.ErrUnreachable) {
			return nil, fmt.Errorf("node: attach the gate holder: %w", err)
		}
		if pauseErr := c.pause(ctx); pauseErr != nil {
			return nil, errors.Join(err, pauseErr)
		}
	}
}

// gateLink returns the holder.Link of the gate holder that serves dir on the agent, launched under scope. Its end is the
// agent's end op for the session, which the holder's directory name gives.
func (c *Client) gateLink(dir, scope string) *nodeLink {
	return &nodeLink{
		client:    c,
		id:        filepath.Base(dir),
		holderDir: dir,
		scope:     scope,
		lost:      make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
}

// watchGateEnd learns the end of a gate's holder, and then closes done. The agent's end op cannot name a gate's holder,
// since it finds a holder by its session under the agent's own state directory, so the watch asks by the holder's
// directory instead. Each time a connection to the holder closes, as the holder's end closes it, and after each retry
// interval, it asks the agent whether the holder lives. Once the holder does not, it reads the exit record the holder
// left, and closes done.
func (l *nodeLink) watchGateEnd() {
	client := l.client
	for {
		select {
		case <-l.lost:
		case <-client.cfg.Clock.After(client.cfg.Bound / retriesPerBound):
		}
		exit, ended, err := client.gateEnded(l.holderDir)
		if err != nil {
			client.cfg.Logger.Debug("node: gate end watch", "host", client.cfg.Name, "dir", l.holderDir, "err", err)
			continue
		}
		if ended {
			if exit != nil {
				l.exit, l.found = *exit, true
			}
			close(l.done)
			return
		}
	}
}

// gateEnded reports whether the gate holder of dir has ended on the remote machine, and the exit record it left once it
// has, or nil when it left none.
func (c *Client) gateEnded(dir string) (*hostexec.Exit, bool, error) {
	ctx := context.Background()
	alive, err := c.HolderAlive(ctx, dir)
	if err != nil || alive {
		return nil, false, err
	}
	resp, err := c.hostCall(ctx, Request{Version: Version, Op: OpExitRecord, HolderDir: dir})
	if err != nil {
		return nil, false, err
	}
	return resp.Exit, true, nil
}

// gateConn is a connection that a gate's link dialed to the holder. Its close sends on the link's lost, so the link's end
// watch asks whether the holder has ended: a Process closes its connection when the holder's stream ends, and when the
// connection fails.
type gateConn struct {
	net.Conn
	lost chan<- struct{}
}

// Close closes the connection, and tells the end watch that a connection to the holder has closed.
func (c *gateConn) Close() error {
	select {
	case c.lost <- struct{}{}:
	default:
	}
	if err := c.Conn.Close(); err != nil {
		return fmt.Errorf("node: close the gate holder's connection: %w", err)
	}
	return nil
}

// hostStop ends the gate holder that serves dir on the remote machine, and leaves dir in place.
func (c *Client) hostStop(ctx context.Context, dir string) error {
	_, err := c.hostCall(ctx, Request{Version: Version, Op: OpGateStop, HolderDir: dir})
	return err
}

// hostClear stops the gate holder that serves dir on the remote machine, if one does, and removes dir.
func (c *Client) hostClear(ctx context.Context, dir string) error {
	_, err := c.hostCall(ctx, Request{Version: Version, Op: OpGateClear, HolderDir: dir})
	return err
}

// hostExitRecord returns the exit record that the holder of dir left on the remote machine, and whether it left one. The
// Exit takes no ctx, so the agent is asked within Bound. An answer that fails reads as no record, and the failure is logged.
func (c *Client) hostExitRecord(dir string) (hostexec.Exit, bool) {
	ctx, cancel := c.bound(context.Background())
	defer cancel()
	resp, err := c.hostCall(ctx, Request{Version: Version, Op: OpExitRecord, HolderDir: dir})
	if err != nil {
		c.cfg.Logger.Warn("node: exit record", "host", c.cfg.Name, "dir", dir, "err", err)
		return hostexec.Exit{}, false
	}
	if resp.Exit == nil {
		return hostexec.Exit{}, false
	}
	return *resp.Exit, true
}

// hostKillLeftovers kills the processes that the child of the gate holder dir left running on the remote machine. The call
// takes no ctx, so the agent is asked within Bound.
func (c *Client) hostKillLeftovers(dir string) error {
	ctx, cancel := c.bound(context.Background())
	defer cancel()
	_, err := c.hostCall(ctx, Request{Version: Version, Op: OpKillLeftovers, HolderDir: dir})
	return err
}
