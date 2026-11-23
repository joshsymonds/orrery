// Package hostexec is the seam through which engine code reaches git, the file system and session holders on one
// machine. A Host bundles those three; a Hosts resolves host names to Hosts. Local is the Host for this machine.
package hostexec

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
)

// ErrUnknownHost means a Hosts has no Host under the name asked for.
var ErrUnknownHost = errors.New("hostexec: unknown host")

// ErrNotRegular means a path named by ReadRooted is not a regular file.
var ErrNotRegular = errors.New("hostexec: not a regular file")

// ErrGone means no holder holds the session: the session has ended. It is holder.ErrGone.
var ErrGone = holder.ErrGone

// Reasons an Exit gives for the child's end. They equal holder's.
const (
	// ReasonExited means the child ended on its own.
	ReasonExited = holder.ReasonExited
	// ReasonLimit means a gate child ran past its limit, so the holder stopped it.
	ReasonLimit = holder.ReasonLimit
	// ReasonStopped means a controller's stop, or a signal to the holder, stopped the child.
	ReasonStopped = holder.ReasonStopped
)

// Exit records how a session's child ended. It is holder.Exit.
type Exit = holder.Exit

// Host is one machine that engine work runs on: its git, its file system and the session holders that run gates and
// sessions. Its methods are safe for concurrent use.
type Host interface {
	// Git runs git -C call.Dir call.Args... with that argv and no shell. It returns git's stdout exactly as git wrote
	// it, untrimmed, on success and on failure. When git ran and exited non-zero or was killed, Git returns a *GitError.
	// A git that could not start returns a wrapped error that is not a *GitError.
	Git(ctx context.Context, call GitCall) ([]byte, error)
	Files
	Processes
}

// GitCall describes one git run on a Host.
type GitCall struct {
	// Dir is the repository git runs in, passed as git -C Dir.
	Dir string
	// Args are git's arguments after -C Dir. git runs with this argv and no shell.
	Args []string
	// Env is added to the host's environment for this run. Nil adds nothing.
	Env []string
	// Stdin is fed to git. Nil feeds git nothing.
	Stdin []byte
	// Remote marks a run that reaches a remote. Such a run sets GIT_TERMINAL_PROMPT=0, so git fails where it would
	// prompt for a login, and it runs in a process group of its own, which the end of ctx kills whole.
	Remote bool
	// WaitDelay bounds how long the run waits, after ctx ends, for git's output to finish, as exec.Cmd.WaitDelay does.
	// Zero waits without a bound.
	WaitDelay time.Duration
}

// GitError is the error Git returns when git ran and exited non-zero or was killed. Git returns it as a *GitError.
type GitError struct {
	// Args are the arguments git ran with, after -C Dir.
	Args []string
	// ExitCode is git's exit status, or -1 when a signal ended git.
	ExitCode int
	// Stdout is what git wrote to stdout, untrimmed.
	Stdout []byte
	// Stderr is what git wrote to stderr, untrimmed.
	Stderr []byte
	// Err is the cause the run reported, such as an *exec.ExitError.
	Err error
}

// Error reports the failed run as "git <Args joined by spaces>: <Err>: <Stderr with surrounding space trimmed>".
func (e *GitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.Args, " "), e.Err, strings.TrimSpace(string(e.Stderr)))
}

// Unwrap returns Err, so errors.Is and errors.As reach the cause.
func (e *GitError) Unwrap() error {
	return e.Err
}

// Files is the file system on a Host. Every method takes ctx first. Its errors keep the fs semantics of the os function
// each names, so errors.Is(err, fs.ErrNotExist) holds for a missing path.
type Files interface {
	// ReadFile reads the file at path.
	ReadFile(ctx context.Context, path string) ([]byte, error)
	// ReadRooted reads the regular file name inside root. It opens name through an os.Root and follows no symlink. A
	// file that is not regular fails with an error matching ErrNotRegular.
	ReadRooted(ctx context.Context, root, name string) ([]byte, error)
	// Stat describes the file at path, following symlinks.
	Stat(ctx context.Context, path string) (fs.FileInfo, error)
	// Lstat describes the file at path without following a final symlink.
	Lstat(ctx context.Context, path string) (fs.FileInfo, error)
	// ReadDir lists the directory at path.
	ReadDir(ctx context.Context, path string) ([]fs.DirEntry, error)
	// WriteFile writes data to the file at path, creating it with perm when it is missing.
	WriteFile(ctx context.Context, path string, data []byte, perm fs.FileMode) error
	// Symlink makes link a symbolic link to target.
	Symlink(ctx context.Context, target, link string) error
	// MkdirTemp makes a new directory in dir, named from pattern as os.MkdirTemp names it, and returns its path.
	MkdirTemp(ctx context.Context, dir, pattern string) (string, error)
	// MkdirAll makes path and any missing parents of it.
	MkdirAll(ctx context.Context, path string, perm fs.FileMode) error
	// Remove removes the file or empty directory at path.
	Remove(ctx context.Context, path string) error
	// RemoveAll removes path and everything under it. A path that does not exist is not an error.
	RemoveAll(ctx context.Context, path string) error
	// EvalSymlinks returns path with every symlink in it resolved.
	EvalSymlinks(ctx context.Context, path string) (string, error)
}

// Processes is the session processes on a Host. Each session has a holder that runs its child and keeps its state in a
// session directory, which the holder's methods name by dir.
type Processes interface {
	// SessionDir returns the directory of session's holder files under holders, as holder.SessionDir does. It fails for
	// a session name that is not a single safe path element.
	SessionDir(holders, session string) (string, error)
	// Launch starts the child cfg describes under a holder and returns it once the holder serves its socket and the
	// attach succeeds. A holder that ends before it serves fails the launch.
	Launch(ctx context.Context, cfg Launch) (Held, error)
	// Reattach takes over the gate holder that serves dir, although this caller did not start it. scope is the
	// systemd-run path the holder was launched under, or empty when it was started plainly. Reattach fails with an
	// error matching ErrGone when no holder holds the session.
	Reattach(ctx context.Context, dir, scope string) (Held, error)
	// Stop ends the holder that serves dir and leaves dir in place. A dir with no holder is not an error.
	Stop(ctx context.Context, dir string) error
	// Clear stops the holder that serves dir, if one does, and removes dir with everything in it. A dir that does not
	// exist is not an error.
	Clear(ctx context.Context, dir string) error
	// ExitRecord returns the exit record that the holder of dir left, and false when dir holds no readable record.
	ExitRecord(dir string) (Exit, bool)
	// KillLeftovers kills the processes that a session's child left running in its process group after the child ended
	// on its own. A dir with no pids record has nothing to kill. Nothing is signaled when the child's start time is not
	// recorded, or when another process holds the child's pid.
	KillLeftovers(dir string) error
}

// Launch describes one session's launch on a Host. Its fields mirror the holder.LaunchConfig fields that a gate launch
// sets.
type Launch struct {
	// Scope is the systemd-run path that runs the holder in its own user scope. Empty starts the holder plainly.
	Scope string
	// StateDir is the state directory whose holders subdirectory holds the session. Empty takes a fresh directory that is
	// removed once the holder ends or the launch fails.
	StateDir string
	// Session names the session.
	Session string
	// Path is the absolute path of the child's program.
	Path string
	// Args are the child's arguments, not counting its program.
	Args []string
	// Dir is the child's working directory, or the current one when empty.
	Dir string
	// Env replaces the child's environment when non-nil, and is inherited when nil.
	Env []string
	// Limit makes the child a gate when positive: the holder stops the child once it has run that long, and the holder's
	// directory keeps the exit record for the caller to clear. Zero launches the child with no limit.
	Limit time.Duration
	// LimitSignal is the FIFO whose opening for writing reaches Limit early. It is a test seam, and it is used only with
	// a positive Limit.
	LimitSignal string
	// Tail is the number of newest output bytes a gate keeps in its exit record. Zero keeps the holder's default, and it
	// is used only with a positive Limit.
	Tail int
}

// Held is a session's child as its holder hands it over. *holder.Process satisfies it.
type Held interface {
	// Wait returns once the child has ended. It returns "signal: NAME" when a signal ended the child, and the holder's
	// own wait error otherwise.
	Wait() error
	// Detach ends this caller's hold on the child and leaves the child running under its holder.
	Detach() error
	// Exit returns the exit record once the holder has sent it, and false before.
	Exit() (Exit, bool)
}

// Compile-time check that the holder's process is a Held.
var _ Held = (*holder.Process)(nil)

// Hosts resolves host names to Hosts.
type Hosts interface {
	// Host returns the Host named name. A name that resolves to no Host fails with an error matching ErrUnknownHost.
	Host(name string) (Host, error)
}

// Map is a Hosts over a fixed set of named Hosts.
type Map map[string]Host

// Compile-time check that Map is a Hosts.
var _ Hosts = Map(nil)

// Host returns the Host named name. A name Map lacks fails with an error matching ErrUnknownHost that names it.
func (m Map) Host(name string) (Host, error) {
	host, ok := m[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownHost, name)
	}
	return host, nil
}

// LocalHosts returns the Hosts for this machine: every name resolves to one shared *Local.
func LocalHosts() Hosts {
	return localHosts{local: NewLocal()}
}

// localHosts resolves every name to one *Local.
type localHosts struct {
	local *Local
}

// Host returns the shared *Local, whatever name it is asked for.
func (h localHosts) Host(_ string) (Host, error) {
	return h.local, nil
}
