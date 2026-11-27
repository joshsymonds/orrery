package node

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/hostexec"
)

// startTimeIndex is the index of field 22, the start time, among the stat fields after the command name, which begin at
// field 3.
const startTimeIndex = 22 - 3

// git answers with the result of the git run that spec describes, which the agent's Local runs inside its allowed
// directories, with spec's WaitDelay. A run that git ends with a non-zero exit, or a kill, answers with git's exit code,
// stdout and stderr. A git that could not start answers with Started false, and Message says why.
func (a *Agent) git(ctx context.Context, spec *GitSpec) Response {
	if spec == nil {
		return Response{Code: CodeFailed, Message: "node: git op names no git run"}
	}
	if err := a.checkPaths(spec.Dir); err != nil {
		return refusal(err)
	}
	out, err := a.cfg.Local.Git(ctx, hostexec.GitCall{
		Dir:       spec.Dir,
		Args:      spec.Args,
		Env:       spec.Env,
		Stdin:     spec.Stdin,
		Remote:    spec.Remote,
		WaitDelay: spec.WaitDelay,
	})
	var gitErr *hostexec.GitError
	switch {
	case err == nil:
		return Response{Git: &GitResult{Started: true, Stdout: out}}
	case errors.As(err, &gitErr):
		return Response{Git: &GitResult{
			Started:  true,
			ExitCode: gitErr.ExitCode,
			Stdout:   gitErr.Stdout,
			Stderr:   gitErr.Stderr,
		}}
	default:
		return Response{Git: &GitResult{Started: false}, Message: err.Error()}
	}
}

// file answers with the result of the file-system method that spec names, which the agent's Local calls inside its
// allowed directories. An fs error of a kind FileErrno names is answered in the result's Errno, and any other error is
// CodeFailed.
func (a *Agent) file(ctx context.Context, spec *FileSpec) Response {
	if spec == nil {
		return Response{Code: CodeFailed, Message: "node: file op names no file spec"}
	}
	if err := a.checkFile(spec); err != nil {
		return refusal(err)
	}
	res, err := a.runFile(ctx, spec)
	if err == nil {
		return Response{File: res}
	}
	if errno := fileErrno(err); errno != "" {
		return Response{File: &FileResult{Errno: errno}}
	}
	return failed(err)
}

// checkFile returns nil when every path that spec names lies under one of the agent's allowed directories, or, for an op
// that only reads, in a place checkRead allows. A read rooted names its root, which the read opens without following a
// symlink. A symlink names its link, and the path its target points at.
func (a *Agent) checkFile(spec *FileSpec) error {
	switch spec.Op {
	case FileReadRooted:
		return a.checkRead(spec.Op, spec.Root)
	case FileRead, FileStat, FileLstat, FileReadDir, FileEvalSymlinks:
		return a.checkRead(spec.Op, spec.Path)
	case FileSymlink:
		if err := a.checkPaths(spec.Path); err != nil {
			return err
		}
		target, err := linkTarget(spec.Path, spec.Target)
		if err != nil {
			return err
		}
		return a.checkPaths(target)
	default:
		return a.checkPaths(spec.Path)
	}
}

// linkTarget returns the path that a symlink at link to target points at. A relative target is taken from the directory
// that link lies in, as the kernel takes it, so that directory is resolved first. link is absolute, since checkFile
// checks it first.
func linkTarget(link, target string) (string, error) {
	if filepath.IsAbs(target) {
		return target, nil
	}
	parent := "/"
	if i := strings.LastIndexByte(link, '/'); i > 0 {
		resolved, err := resolvePath(link[:i])
		if err != nil {
			return "", err
		}
		parent = resolved
	}
	return parent + "/" + target, nil
}

// runFile calls the one file-system method that spec names on the agent's Local, and returns the result it gives. An op
// that names no method this agent serves is an error.
func (a *Agent) runFile(ctx context.Context, spec *FileSpec) (*FileResult, error) {
	switch spec.Op {
	case FileRead, FileReadRooted, FileStat, FileLstat, FileReadDir, FileEvalSymlinks:
		return a.readFile(ctx, spec)
	case FileWrite, FileSymlink, FileMkdirTemp, FileMkdirAll, FileRemove, FileRemoveAll:
		return a.changeFile(ctx, spec)
	default:
		return nil, fmt.Errorf("node: file op %q is not served", spec.Op)
	}
}

// readFile calls the method that reads or describes what spec names. It returns what the method returned, with its fs
// errors kept for fileErrno.
func (a *Agent) readFile(ctx context.Context, spec *FileSpec) (*FileResult, error) {
	files := a.cfg.Local
	switch spec.Op {
	case FileRead:
		data, err := files.ReadFile(ctx, spec.Path)
		return &FileResult{Data: data}, fileErr(err)
	case FileReadRooted:
		data, err := files.ReadRooted(ctx, spec.Root, spec.Name)
		return &FileResult{Data: data}, fileErr(err)
	case FileStat:
		return statResult(files.Stat(ctx, spec.Path))
	case FileLstat:
		return statResult(files.Lstat(ctx, spec.Path))
	case FileReadDir:
		return dirResult(files.ReadDir(ctx, spec.Path))
	default: // FileEvalSymlinks, the only other op readFile is called with.
		resolved, err := files.EvalSymlinks(ctx, spec.Path)
		return &FileResult{Path: resolved}, fileErr(err)
	}
}

// changeFile calls the method that writes or removes what spec names. It returns the path a mkdir-temp made, and no value
// for the other methods.
func (a *Agent) changeFile(ctx context.Context, spec *FileSpec) (*FileResult, error) {
	files := a.cfg.Local
	switch spec.Op {
	case FileWrite:
		return &FileResult{}, fileErr(files.WriteFile(ctx, spec.Path, spec.Data, spec.Perm))
	case FileSymlink:
		return &FileResult{}, fileErr(files.Symlink(ctx, spec.Target, spec.Path))
	case FileMkdirTemp:
		made, err := files.MkdirTemp(ctx, spec.Path, spec.Pattern)
		return &FileResult{Path: made}, fileErr(err)
	case FileMkdirAll:
		return &FileResult{}, fileErr(files.MkdirAll(ctx, spec.Path, spec.Perm))
	case FileRemove:
		return &FileResult{}, fileErr(files.Remove(ctx, spec.Path))
	default: // FileRemoveAll, the only other op changeFile is called with.
		return &FileResult{}, fileErr(files.RemoveAll(ctx, spec.Path))
	}
}

// statResult is the result of a stat or lstat that returned info and err.
func statResult(info fs.FileInfo, err error) (*FileResult, error) {
	if err != nil {
		return nil, fileErr(err)
	}
	described := toFileInfo(info)
	return &FileResult{Info: &described}, nil
}

// dirResult is the result of a read-dir that returned entries and err. Each entry's info is read, as the answer lists it.
func dirResult(entries []fs.DirEntry, err error) (*FileResult, error) {
	if err != nil {
		return nil, fileErr(err)
	}
	infos := make([]FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, fileErr(infoErr)
		}
		infos = append(infos, toFileInfo(info))
	}
	return &FileResult{Entries: infos}, nil
}

// toFileInfo returns info as a FileInfo, which JSON carries.
func toFileInfo(info fs.FileInfo) FileInfo {
	return FileInfo{
		Name:    info.Name(),
		Size:    info.Size(),
		Mode:    info.Mode(),
		ModTime: info.ModTime(),
		IsDir:   info.IsDir(),
	}
}

// fileErr names err as a file op's error, so that its message says where it came from. A nil err stays nil, and fileErrno
// still matches the error it wraps.
func fileErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("node: file op: %w", err)
}

// fileErrno returns the FileErrno of the kind of fs error err is, or "" when err is none of the kinds FileErrno names.
func fileErrno(err error) FileErrno {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ErrnoNotExist
	case errors.Is(err, fs.ErrExist):
		return ErrnoExist
	case errors.Is(err, fs.ErrPermission):
		return ErrnoPermission
	case errors.Is(err, hostexec.ErrNotRegular):
		return ErrnoNotRegular
	default:
		return ""
	}
}

// gateLaunch starts the child that gate describes under a holder, and detaches from it, as the agent's Local launches it.
// The gate's directory and its state directory must lie in the agent's allowed directories. A gate whose session has a
// live holder is refused, since a launch clears the holder it finds. An empty gate directory is the agent's working
// directory, which the child inherits, so that is the directory checked.
func (a *Agent) gateLaunch(ctx context.Context, gate *GateSpec) Response {
	if gate == nil {
		return Response{Code: CodeFailed, Message: "node: gate launch names no gate"}
	}
	dir := gate.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return failed(fmt.Errorf("node: working directory: %w", err))
		}
		dir = cwd
	}
	paths := []string{dir}
	if gate.StateDir != "" {
		paths = append(paths, gate.StateDir)
	}
	if err := a.checkPaths(paths...); err != nil {
		return refusal(err)
	}
	live, err := a.sessionLive(gate.StateDir, gate.Session)
	if err != nil {
		return failed(err)
	}
	if live {
		return Response{
			Code:    CodeFailed,
			Message: fmt.Sprintf("node: session %s already has a live holder", gate.Session),
		}
	}
	held, err := a.cfg.Local.Launch(ctx, hostexec.Launch{
		Scope:    gate.Scope,
		StateDir: gate.StateDir,
		Session:  gate.Session,
		Path:     gate.Path,
		Args:     gate.Args,
		Dir:      gate.Dir,
		Env:      gate.Env,
		Limit:    gate.Limit,
		Tail:     gate.Tail,
	})
	if err != nil {
		return failed(err)
	}
	if detachErr := held.Detach(); detachErr != nil {
		a.cfg.Logger.WarnContext(ctx, "node: detach a launched gate", "session", gate.Session, "err", detachErr)
	}
	return Response{}
}

// sessionLive reports whether the holder of session is live under stateDir. An empty stateDir holds no holder of the
// session, since a launch with none makes a fresh directory for it.
func (a *Agent) sessionLive(stateDir, session string) (bool, error) {
	if stateDir == "" {
		return false, nil
	}
	dir, err := a.cfg.Local.SessionDir(filepath.Join(stateDir, holdersName), session)
	if err != nil {
		return false, fmt.Errorf("node: holder of session %s: %w", session, err)
	}
	live, err := holder.Live(dir)
	if err != nil {
		return false, fmt.Errorf("node: holder of session %s: %w", session, err)
	}
	return live, nil
}

// gateAttach splices conn onto the holder socket of the gate directory that req names, as attach splices a session's
// holder. The splice is keyed by the directory's base name, which is the session's name for a gate under the state
// directory, so no keeper of that session attaches while the splice is open. The splice carries bytes only, so req's
// Scope is not needed to make it.
func (a *Agent) gateAttach(ctx context.Context, conn net.Conn, reader *bufio.Reader, req Request) {
	if err := a.checkPaths(req.HolderDir); err != nil {
		a.reply(ctx, conn, refusal(err))
		return
	}
	a.spliceHolder(ctx, conn, reader, filepath.Base(req.HolderDir), req.HolderDir)
}

// gateStop ends the holder of the gate directory dir and leaves dir in place, as the agent's Local stops it.
func (a *Agent) gateStop(ctx context.Context, dir string) Response {
	if err := a.checkPaths(dir); err != nil {
		return refusal(err)
	}
	return noValue(a.cfg.Local.Stop(ctx, dir))
}

// gateClear stops the holder of the gate directory dir and removes dir, as the agent's Local clears it.
func (a *Agent) gateClear(ctx context.Context, dir string) Response {
	if err := a.checkPaths(dir); err != nil {
		return refusal(err)
	}
	return noValue(a.cfg.Local.Clear(ctx, dir))
}

// exitRecord answers with the exit record that the holder of the gate directory dir left, when it left one.
func (a *Agent) exitRecord(dir string) Response {
	if err := a.checkPaths(dir); err != nil {
		return refusal(err)
	}
	exit, found := a.cfg.Local.ExitRecord(dir)
	if !found {
		return Response{}
	}
	return Response{Exit: &exit}
}

// killLeftovers kills the processes that the child of the gate directory dir left running, as the agent's Local does.
func (a *Agent) killLeftovers(dir string) Response {
	if err := a.checkPaths(dir); err != nil {
		return refusal(err)
	}
	return noValue(a.cfg.Local.KillLeftovers(dir))
}

// holderAlive answers in Alive whether the holder of the gate directory dir lives, which is whether its session lock is
// held. A directory with no holder answers false.
func (a *Agent) holderAlive(dir string) Response {
	if err := a.checkPaths(dir); err != nil {
		return refusal(err)
	}
	alive, err := holder.Live(dir)
	if err != nil {
		return failed(fmt.Errorf("node: holder of %s: %w", dir, err))
	}
	return Response{Alive: alive}
}

// noValue returns the Response of an op that answers with no value: failed(err) when it failed, and the empty Response when
// err is nil.
func noValue(err error) Response {
	if err != nil {
		return failed(err)
	}
	return Response{}
}

// processAlive reports whether the process with pid lives under the start time start, as grants.processAlive checks it on
// this machine: /proc must report the same start time for pid. A pid whose stat cannot be read counts as gone, so that a
// pid reused by another process is not the client. A start of 0 is one the controller could not read, for a client that
// runs on this machine, and any start time /proc reports for pid matches it.
func processAlive(pid int, start int64) bool {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	got, err := procStart(string(raw))
	return err == nil && (start == 0 || got == start)
}

// procStart returns field 22, the start time, of a /proc/PID/stat line. The command name is in parentheses and may hold
// spaces and parentheses, so the fields are counted after its last closing parenthesis.
func procStart(stat string) (int64, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, fmt.Errorf("node: stat has no command name: %q", stat)
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) <= startTimeIndex {
		return 0, fmt.Errorf("node: stat has %d fields after its command name", len(fields))
	}
	start, err := strconv.ParseInt(fields[startTimeIndex], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("node: start time of stat: %w", err)
	}
	return start, nil
}
