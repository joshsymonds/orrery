// Package hostexectest provides test doubles for hostexec.Host.
package hostexectest

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"sync"

	"github.com/joshsymonds/orrery/internal/hostexec"
)

// Call is one call a Recorder received.
type Call struct {
	// Op is the name of the method the call was made to.
	Op string
	// Path is the git Dir of a Git call, the file path of a file call (ReadRooted's is root joined with name, and
	// Symlink's is the link it creates), the holder dir of a process call that names one, and the state directory of a
	// Launch. It is empty for a call that names no path.
	Path string
	// Args are the git arguments of a Git call. They are nil for every other call.
	Args []string
}

// Recorder is a hostexec.Host that records each call before it delegates the call to its base Host. An error the base
// Host returns is wrapped with the name of the method it came from, and still matches under errors.Is and errors.As.
type Recorder struct {
	base hostexec.Host

	// mu guards calls.
	mu    sync.Mutex
	calls []Call
}

// Compile-time check that *Recorder is a Host.
var _ hostexec.Host = (*Recorder)(nil)

// NewRecorder returns a Recorder that records the calls made to it and delegates them to base.
func NewRecorder(base hostexec.Host) *Recorder {
	return &Recorder{base: base}
}

// Calls returns a copy of the calls recorded so far, in the order they were made.
func (r *Recorder) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := make([]Call, len(r.calls))
	for i, call := range r.calls {
		call.Args = slices.Clone(call.Args)
		calls[i] = call
	}
	return calls
}

// record appends call to the calls recorded so far. It keeps its own copy of call's arguments.
func (r *Recorder) record(call Call) {
	r.mu.Lock()
	defer r.mu.Unlock()
	call.Args = slices.Clone(call.Args)
	r.calls = append(r.calls, call)
}

// failure names err, which the base Host returned from the method op, with that method. It returns nil when err is nil.
func failure(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("hostexectest: %s: %w", op, err)
}

// Git records the call and delegates it to the base Host.
func (r *Recorder) Git(ctx context.Context, call hostexec.GitCall) ([]byte, error) {
	r.record(Call{Op: "Git", Path: call.Dir, Args: call.Args})
	out, err := r.base.Git(ctx, call)
	return out, failure("Git", err)
}

// ReadFile records the call and delegates it to the base Host.
func (r *Recorder) ReadFile(ctx context.Context, path string) ([]byte, error) {
	r.record(Call{Op: "ReadFile", Path: path})
	data, err := r.base.ReadFile(ctx, path)
	return data, failure("ReadFile", err)
}

// ReadRooted records the call and delegates it to the base Host.
func (r *Recorder) ReadRooted(ctx context.Context, root, name string) ([]byte, error) {
	r.record(Call{Op: "ReadRooted", Path: filepath.Join(root, name)})
	data, err := r.base.ReadRooted(ctx, root, name)
	return data, failure("ReadRooted", err)
}

// Stat records the call and delegates it to the base Host.
func (r *Recorder) Stat(ctx context.Context, path string) (fs.FileInfo, error) {
	r.record(Call{Op: "Stat", Path: path})
	info, err := r.base.Stat(ctx, path)
	return info, failure("Stat", err)
}

// Lstat records the call and delegates it to the base Host.
func (r *Recorder) Lstat(ctx context.Context, path string) (fs.FileInfo, error) {
	r.record(Call{Op: "Lstat", Path: path})
	info, err := r.base.Lstat(ctx, path)
	return info, failure("Lstat", err)
}

// ReadDir records the call and delegates it to the base Host.
func (r *Recorder) ReadDir(ctx context.Context, path string) ([]fs.DirEntry, error) {
	r.record(Call{Op: "ReadDir", Path: path})
	entries, err := r.base.ReadDir(ctx, path)
	return entries, failure("ReadDir", err)
}

// WriteFile records the call and delegates it to the base Host.
func (r *Recorder) WriteFile(ctx context.Context, path string, data []byte, perm fs.FileMode) error {
	r.record(Call{Op: "WriteFile", Path: path})
	return failure("WriteFile", r.base.WriteFile(ctx, path, data, perm))
}

// Symlink records the call and delegates it to the base Host. The Call holds link, the path the symlink is created at.
func (r *Recorder) Symlink(ctx context.Context, target, link string) error {
	r.record(Call{Op: "Symlink", Path: link})
	return failure("Symlink", r.base.Symlink(ctx, target, link))
}

// MkdirTemp records the call and delegates it to the base Host.
func (r *Recorder) MkdirTemp(ctx context.Context, dir, pattern string) (string, error) {
	r.record(Call{Op: "MkdirTemp", Path: dir})
	made, err := r.base.MkdirTemp(ctx, dir, pattern)
	return made, failure("MkdirTemp", err)
}

// MkdirAll records the call and delegates it to the base Host.
func (r *Recorder) MkdirAll(ctx context.Context, path string, perm fs.FileMode) error {
	r.record(Call{Op: "MkdirAll", Path: path})
	return failure("MkdirAll", r.base.MkdirAll(ctx, path, perm))
}

// Remove records the call and delegates it to the base Host.
func (r *Recorder) Remove(ctx context.Context, path string) error {
	r.record(Call{Op: "Remove", Path: path})
	return failure("Remove", r.base.Remove(ctx, path))
}

// RemoveAll records the call and delegates it to the base Host.
func (r *Recorder) RemoveAll(ctx context.Context, path string) error {
	r.record(Call{Op: "RemoveAll", Path: path})
	return failure("RemoveAll", r.base.RemoveAll(ctx, path))
}

// EvalSymlinks records the call and delegates it to the base Host.
func (r *Recorder) EvalSymlinks(ctx context.Context, path string) (string, error) {
	r.record(Call{Op: "EvalSymlinks", Path: path})
	resolved, err := r.base.EvalSymlinks(ctx, path)
	return resolved, failure("EvalSymlinks", err)
}

// SessionDir records the call and delegates it to the base Host. The Call holds the directory the session names under
// holders, which is what holder.SessionDir returns for a valid session.
func (r *Recorder) SessionDir(holders, session string) (string, error) {
	r.record(Call{Op: "SessionDir", Path: filepath.Join(holders, session)})
	dir, err := r.base.SessionDir(holders, session)
	return dir, failure("SessionDir", err)
}

// Launch records the call and delegates it to the base Host. The Call holds the launch's state directory, which is
// empty when the launch takes a fresh one.
func (r *Recorder) Launch(ctx context.Context, cfg hostexec.Launch) (hostexec.Held, error) {
	r.record(Call{Op: "Launch", Path: cfg.StateDir})
	held, err := r.base.Launch(ctx, cfg)
	return held, failure("Launch", err)
}

// Reattach records the call and delegates it to the base Host.
func (r *Recorder) Reattach(ctx context.Context, dir, scope string) (hostexec.Held, error) {
	r.record(Call{Op: "Reattach", Path: dir})
	held, err := r.base.Reattach(ctx, dir, scope)
	return held, failure("Reattach", err)
}

// Stop records the call and delegates it to the base Host.
func (r *Recorder) Stop(ctx context.Context, dir string) error {
	r.record(Call{Op: "Stop", Path: dir})
	return failure("Stop", r.base.Stop(ctx, dir))
}

// Clear records the call and delegates it to the base Host.
func (r *Recorder) Clear(ctx context.Context, dir string) error {
	r.record(Call{Op: "Clear", Path: dir})
	return failure("Clear", r.base.Clear(ctx, dir))
}

// ExitRecord records the call and delegates it to the base Host.
func (r *Recorder) ExitRecord(dir string) (hostexec.Exit, bool) {
	r.record(Call{Op: "ExitRecord", Path: dir})
	return r.base.ExitRecord(dir)
}

// KillLeftovers records the call and delegates it to the base Host.
func (r *Recorder) KillLeftovers(dir string) error {
	r.record(Call{Op: "KillLeftovers", Path: dir})
	return failure("KillLeftovers", r.base.KillLeftovers(dir))
}
