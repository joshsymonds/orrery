package node

import (
	"io/fs"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
)

// Version is the protocol version a controller and an agent speak. A Request names it, and an agent refuses a Request
// that names another version.
const Version = "node.v2"

// Op names an operation a controller asks of an agent. Each op is answered by one Response, and an op that carries a
// stream says what follows the Response.
type Op string

const (
	// OpLaunch starts the session that the request's Launch describes under a holder, as holderhost.Host.Launch does.
	// Its Response comes once the holder serves its socket.
	OpLaunch Op = "launch"
	// OpAttach splices the connection, byte for byte, onto the session's holder socket. Once its Response carries no
	// Code, the controller speaks the holder wire over the connection itself: its hello and attach, at the offset it
	// chooses, and every frame after them. The holder's frames run back unchanged. A Response with a Code means the
	// splice did not start.
	OpAttach Op = "attach"
	// OpStop ends the session's holder, as holder.Stop does.
	OpStop Op = "stop"
	// OpKill ends the session's holder without asking it, as holder.Kill does.
	OpKill Op = "kill"
	// OpClear empties the session's holder directory, as holder.Clear does.
	OpClear Op = "clear"
	// OpStopOrphan stops the child that the session's gone holder recorded, as holder.StopOrphan does.
	OpStopOrphan Op = "stop-orphan"
	// OpGone answers with the session's gone holder: its exit record in the Response, when the holder wrote one. The
	// spooled stdout from the request's Offset to its end follows.
	OpGone Op = "gone"
	// OpSessions answers with the ids of the sessions that have a holder directory on the agent's machine.
	OpSessions Op = "sessions"
	// OpEnd waits until the session's holder has ended, then answers with its exit record. The record is absent when the
	// holder wrote none, as after a kill.
	OpEnd Op = "end"
	// OpPing answers at once, so a controller learns that the agent is reachable.
	OpPing Op = "ping"
	// OpPanes carries the agent's Panes service to the controller. Once its Response carries no Code, the connection
	// carries HTTP/1.1 requests to the agent's Panes handler until either side closes. The agent's HostPanes serves it.
	OpPanes Op = "panes"
	// OpWatchPanes streams the agent's pane events. After its Response, the agent writes one JSON PaneEvent per line
	// until the controller disconnects or the agent stops.
	OpWatchPanes Op = "watch-panes"
	// OpLoad answers with the host's Load, which the Response's Load carries.
	OpLoad Op = "load"
	// OpFolders answers with the agent's known folders, which the Response's Folders carries.
	OpFolders Op = "folders"
	// OpFolder checks that the request's Dir is an existing directory under the agent's Root, and answers its resolved
	// path in the Response's Folder.
	OpFolder Op = "folder"
	// OpWakePane types the request's Text into pane Pane of the tmux server Server when the pane is idle, and answers in
	// the Response's Woke whether it did.
	OpWakePane Op = "wake-pane"
	// OpGit runs the request's Git as git -C Dir with its argv, env and stdin, and answers with the run's Git result. The
	// controller sends nothing after the request, so the run ends with the connection: the agent kills git once the
	// controller's connection closes, as Local kills git when its ctx ends.
	OpGit Op = "git"
	// OpFile runs the one file-system method the request's File names, and answers with its File result.
	OpFile Op = "file"
	// OpGateLaunch starts the child that the request's Gate describes under a holder, as hostexec.Host.Launch does. Its
	// Response comes once the holder serves its socket.
	OpGateLaunch Op = "gate-launch"
	// OpGateAttach splices the connection, byte for byte, onto the holder socket of the gate directory HolderDir, which
	// the request's Scope names, as hostexec.Host.Reattach does. A Response with a Code means the splice did not start.
	OpGateAttach Op = "gate-attach"
	// OpGateStop ends the holder of the gate directory HolderDir and leaves the directory in place, as hostexec.Host.Stop
	// does.
	OpGateStop Op = "gate-stop"
	// OpGateClear stops the holder of the gate directory HolderDir and removes the directory, as hostexec.Host.Clear does.
	OpGateClear Op = "gate-clear"
	// OpExitRecord answers with the exit record that the holder of HolderDir left, in the Response's Exit, when it left
	// one.
	OpExitRecord Op = "exit-record"
	// OpKillLeftovers kills the processes that the child of the gate directory HolderDir left running, as
	// hostexec.Host.KillLeftovers does.
	OpKillLeftovers Op = "kill-leftovers"
	// OpProcessAlive answers in the Response's Alive whether the process with the request's PID lives under the start
	// time Start. A Start of 0 is a start time the controller could not read, the start of a client on this machine, and
	// asks only whether PID lives.
	OpProcessAlive Op = "process-alive"
	// OpHolderAlive answers in the Response's Alive whether the holder of the directory HolderDir lives.
	OpHolderAlive Op = "holder-alive"
)

// FileOp names the one hostexec.Files method a file op calls. Each names the method of the same meaning.
type FileOp string

const (
	// FileRead calls ReadFile on the request's Path.
	FileRead FileOp = "read"
	// FileReadRooted calls ReadRooted on the request's Root and Name.
	FileReadRooted FileOp = "read-rooted"
	// FileStat calls Stat on the request's Path.
	FileStat FileOp = "stat"
	// FileLstat calls Lstat on the request's Path.
	FileLstat FileOp = "lstat"
	// FileReadDir calls ReadDir on the request's Path.
	FileReadDir FileOp = "read-dir"
	// FileWrite calls WriteFile on the request's Path, with its Data and Perm.
	FileWrite FileOp = "write"
	// FileSymlink calls Symlink with the request's Target, and links it at the request's Path.
	FileSymlink FileOp = "symlink"
	// FileMkdirTemp calls MkdirTemp in the request's Path, with its Pattern.
	FileMkdirTemp FileOp = "mkdir-temp"
	// FileMkdirAll calls MkdirAll on the request's Path, with its Perm.
	FileMkdirAll FileOp = "mkdir-all"
	// FileRemove calls Remove on the request's Path.
	FileRemove FileOp = "remove"
	// FileRemoveAll calls RemoveAll on the request's Path.
	FileRemoveAll FileOp = "remove-all"
	// FileEvalSymlinks calls EvalSymlinks on the request's Path.
	FileEvalSymlinks FileOp = "eval-symlinks"
)

// Request is the one line a controller sends first on a connection. Version names the protocol and Op the operation.
// The other fields are those the op reads, and the op ignores the rest.
type Request struct {
	Version string `json:"version"`
	Op      Op     `json:"op"`
	// Session names the session an op acts on. The sessions and ping ops read none.
	Session string `json:"session,omitempty"`
	// Launch is the launch that the launch op starts.
	Launch *LaunchSpec `json:"launch,omitempty"`
	// Offset is the stdout offset that a gone op streams from.
	Offset int64 `json:"offset,omitempty"`
	// Dir is the directory that a folder op checks.
	Dir string `json:"dir,omitempty"`
	// Server is the tmux server column, and Pane the pane id, that a wake-pane op types into.
	Server string `json:"server,omitempty"`
	Pane   string `json:"pane,omitempty"`
	// Text is what a wake-pane op types into the pane.
	Text string `json:"text,omitempty"`
	// Git is the git run that a git op makes.
	Git *GitSpec `json:"git,omitempty"`
	// File is the file-system method that a file op calls.
	File *FileSpec `json:"file,omitempty"`
	// Gate is the child that a gate-launch op starts.
	Gate *GateSpec `json:"gate,omitempty"`
	// HolderDir is the holder directory that a gate op, exit-record, kill-leftovers or holder-alive acts on.
	HolderDir string `json:"holderDir,omitempty"`
	// Scope is the systemd-run path that a gate-attach op reattaches the holder under, or empty when it was started
	// plainly.
	Scope string `json:"scope,omitempty"`
	// PID and Start name the process that a process-alive op checks: its pid, and the start time it must have, or 0 for
	// any start time.
	PID   int   `json:"pid,omitempty"`
	Start int64 `json:"start,omitempty"`
	// Secret is the fleet secret that the controller presents, which the agent checks against its own before it serves the
	// Request.
	Secret string `json:"secret,omitempty"`
}

// GitSpec is the git run that a git op makes: git -C Dir with Args, as hostexec.GitCall describes it.
type GitSpec struct {
	Dir   string   `json:"dir"`
	Args  []string `json:"args"`
	Env   []string `json:"env,omitempty"`
	Stdin []byte   `json:"stdin,omitempty"`
	// Remote marks a run that reaches a remote, as hostexec.GitCall.Remote does.
	Remote bool `json:"remote,omitempty"`
	// WaitDelay is the bound on the run's wait after its ctx ends, as hostexec.GitCall.WaitDelay is. Zero is no bound.
	WaitDelay time.Duration `json:"waitDelay,omitempty"`
}

// FileSpec is the file-system method that a file op calls. Op names the method, and the other fields are the arguments
// that method reads, which the op ignores the rest of. Path is the path an op acts on, and for symlink the link.
type FileSpec struct {
	Op      FileOp      `json:"op"`
	Path    string      `json:"path,omitempty"`
	Root    string      `json:"root,omitempty"`
	Name    string      `json:"name,omitempty"`
	Target  string      `json:"target,omitempty"`
	Pattern string      `json:"pattern,omitempty"`
	Data    []byte      `json:"data,omitempty"`
	Perm    fs.FileMode `json:"perm,omitempty"`
}

// GateSpec is the child that a gate-launch op starts: a hostexec.Launch, whose fields it mirrors.
type GateSpec struct {
	Scope    string        `json:"scope,omitempty"`
	StateDir string        `json:"stateDir,omitempty"`
	Session  string        `json:"session"`
	Path     string        `json:"path"`
	Args     []string      `json:"args,omitempty"`
	Dir      string        `json:"dir,omitempty"`
	Env      []string      `json:"env,omitempty"`
	Limit    time.Duration `json:"limit,omitempty"`
	Tail     int           `json:"tail,omitempty"`
}

// LaunchSpec is the launch that a launch op starts: a holderhost.Spec without its session, which the Request names.
type LaunchSpec struct {
	Dir    string `json:"dir"`
	Resume string `json:"resume,omitempty"`
	Model  string `json:"model,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Agents string `json:"agents,omitempty"`
	Schema string `json:"schema,omitempty"`
	Step   bool   `json:"step,omitempty"`
}

// Response is the one line an agent answers each Request with, before any stream the op carries. Code is empty when
// the op succeeded. Otherwise Code classifies the failure, and Message describes it.
type Response struct {
	Code    Code   `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	// Sessions are the session ids that a sessions op found.
	Sessions []string `json:"sessions,omitempty"`
	// Exit is the holder's exit record, which an end or gone op answers with when the holder wrote one.
	Exit *holder.Exit `json:"exit,omitempty"`
	// Load is the host's Load, which a load op answers with.
	Load *Load `json:"load,omitempty"`
	// Folders are the folders that a folders op found.
	Folders []string `json:"folders,omitempty"`
	// Folder is the resolved path that a folder op checked.
	Folder string `json:"folder,omitempty"`
	// Roots are the absolute directories whose folders a folders op reports.
	Roots []string `json:"roots,omitempty"`
	// Woke reports whether a wake-pane op typed its text into the pane.
	Woke bool `json:"woke,omitempty"`
	// Git is the result of a git op.
	Git *GitResult `json:"git,omitempty"`
	// File is the result of a file op.
	File *FileResult `json:"file,omitempty"`
	// Alive reports whether a process-alive or holder-alive op found its process or holder alive.
	Alive bool `json:"alive,omitempty"`
}

// GitResult is the result of a git op: what git wrote and how it ended. Started is false when git could not start, and
// then ExitCode, Stdout and Stderr are empty.
type GitResult struct {
	Started  bool   `json:"started"`
	ExitCode int    `json:"exitCode"`
	Stdout   []byte `json:"stdout,omitempty"`
	Stderr   []byte `json:"stderr,omitempty"`
}

// FileResult is the result of a file op. Data is what a read returns, Path the path that mkdir-temp or eval-symlinks
// returns, Info what stat or lstat describes, and Entries what read-dir lists. Errno names the fs error the method
// returned, when it returned one of the kinds FileErrno names.
type FileResult struct {
	Data    []byte     `json:"data,omitempty"`
	Path    string     `json:"path,omitempty"`
	Info    *FileInfo  `json:"info,omitempty"`
	Entries []FileInfo `json:"entries,omitempty"`
	Errno   FileErrno  `json:"errno,omitempty"`
}

// FileInfo is a fs.FileInfo as JSON carries it: the name, size, mode and modification time of a file, and whether it is
// a directory.
type FileInfo struct {
	Name    string      `json:"name"`
	Size    int64       `json:"size"`
	Mode    fs.FileMode `json:"mode"`
	ModTime time.Time   `json:"modTime"`
	IsDir   bool        `json:"isDir"`
}

// FileErrno names an fs error a file op's result carries, so that the controller returns the error the os function
// returned for it.
type FileErrno string

const (
	// ErrnoNotExist is fs.ErrNotExist.
	ErrnoNotExist FileErrno = "not-exist"
	// ErrnoExist is fs.ErrExist.
	ErrnoExist FileErrno = "exist"
	// ErrnoPermission is fs.ErrPermission.
	ErrnoPermission FileErrno = "permission"
	// ErrnoNotRegular is hostexec.ErrNotRegular.
	ErrnoNotRegular FileErrno = "not-regular"
)

// Code classifies a failed op, so that the controller returns the error the holder package returns for it.
type Code string

const (
	// CodeGone is holder.ErrGone: no holder holds the session's lock.
	CodeGone Code = "gone"
	// CodeUnattachable is holder.ErrUnattachable: a live holder could not be attached.
	CodeUnattachable Code = "unattachable"
	// CodeNoAnswer is holder.ErrNoAnswer: a live holder did not answer within the attach's bound.
	CodeNoAnswer Code = "no-answer"
	// CodeRefused is a Request for a path outside the agent's roots, which the agent refuses.
	CodeRefused Code = "refused"
	// CodeVersion is a Request of another protocol version than the agent serves.
	CodeVersion Code = "version"
	// CodeFailed is any other failure, which the Response's Message describes.
	CodeFailed Code = "failed"
)
