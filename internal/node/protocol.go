package node

import "github.com/joshsymonds/orrery/internal/holder"

// Version is the protocol version a controller and an agent speak. A Request names it, and an agent refuses a Request
// that names another version.
const Version = "node.v1"

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
}

// Code classifies a failed op, so that the controller returns the error the holder package returns for it.
type Code string

const (
	// CodeGone is holder.ErrGone: no holder holds the session's lock.
	CodeGone Code = "gone"
	// CodeUnattachable is holder.ErrUnattachable: a live holder could not be attached.
	CodeUnattachable Code = "unattachable"
	// CodeNoAnswer is holder.ErrNoAnswer: a live holder did not answer within the attach's bound.
	CodeNoAnswer Code = "no-answer"
	// CodeFailed is any other failure, which the Response's Message describes.
	CodeFailed Code = "failed"
)
