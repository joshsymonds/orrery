// Package paneregistry models Claude Code's session registry: one <pid>.json file per running Claude process under
// ~/.claude/sessions. Each file names the session, its working directory and the tmux pane it runs in, and its status.
package paneregistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Statuses that Claude Code writes to an entry's status field. A session that is still starting has written its entry
// without one, so an entry with no status is a session that has not reported a status yet.
const (
	StatusBusy    = "busy"
	StatusWaiting = "waiting"
	StatusIdle    = "idle"
	StatusShell   = "shell"
)

// EntrypointCLI is the entrypoint of a session started from the command line. A session started by the SDK has
// entrypoint "sdk-cli".
const EntrypointCLI = "cli"

// Values of an entry's waitingFor field while its status is waiting.
const (
	WaitingPermission = "permission prompt"
	WaitingInput      = "input needed"
	WaitingDialog     = "dialog open"
)

// Entry is one session registry file, <pid>.json.
type Entry struct {
	PID        int    `json:"pid"`
	SessionID  string `json:"sessionId"`
	Cwd        string `json:"cwd"`
	Entrypoint string `json:"entrypoint"`
	Status     string `json:"status"`
	WaitingFor string `json:"waitingFor"`
	Tmux       string `json:"tmux"`
}

// Parse decodes a session registry entry. An entry with no status parses, with an empty Status, since Claude Code writes
// a starting session's entry before its first status. It returns an error naming the cause when data is not a JSON
// object, when the entry lacks sessionId, or when it has a status that is not busy, waiting, idle or shell.
func Parse(data []byte) (Entry, error) {
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, fmt.Errorf("paneregistry: entry is not a JSON object: %w", err)
	}
	if entry.SessionID == "" {
		return Entry{}, errors.New("paneregistry: entry lacks sessionId")
	}
	switch entry.Status {
	case "", StatusBusy, StatusWaiting, StatusIdle, StatusShell:
	default:
		return Entry{}, fmt.Errorf("paneregistry: entry status %q is not busy, waiting, idle or shell", entry.Status)
	}
	return entry, nil
}

// Starting reports whether the entry is of a session that has not yet written a status.
func (e Entry) Starting() bool {
	return e.Status == ""
}

// Pane returns the id of the tmux pane the session runs in: the part of the tmux field after its last '.', so
// "mentat:@21.%21" gives "%21". A tmux field with no '.' names no pane and gives "". When the tmux field is empty,
// Pane returns env's TMUX_PANE, which is "" when env does not set it.
func (e Entry) Pane(env map[string]string) string {
	if e.Tmux == "" {
		return env["TMUX_PANE"]
	}
	dot := strings.LastIndexByte(e.Tmux, '.')
	if dot < 0 {
		return ""
	}
	return e.Tmux[dot+1:]
}

// TranscriptPath returns the path of the Claude transcript of the session id run in cwd, under home:
// <home>/.claude/projects/<cwd with each rune that is not an ASCII letter or digit replaced by '-'>/<id>.jsonl.
// It returns "" when home or id is empty, or when id holds a path separator and so names no file in that directory.
func TranscriptPath(home, cwd, id string) string {
	if home == "" || id == "" || strings.ContainsRune(id, filepath.Separator) {
		return ""
	}
	project := strings.Map(func(r rune) rune {
		if isASCIIAlnum(r) {
			return r
		}
		return '-'
	}, cwd)
	return filepath.Join(home, ".claude", "projects", project, id+".jsonl")
}

// isASCIIAlnum reports whether r is an ASCII letter or digit.
func isASCIIAlnum(r rune) bool {
	return ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
}

// Binding is what a live registry entry binds: its entry file, the process and session it names, the tmux pane and
// transcript the session runs in, and its status. Tmux and BaseURL come from the process's TMUX and ANTHROPIC_BASE_URL
// environment rather than from the entry.
type Binding struct {
	File       string
	PID        int
	Pane       string
	Tmux       string
	SessionID  string
	Cwd        string
	Transcript string
	BaseURL    string
	Status     string
	WaitingFor string
}
