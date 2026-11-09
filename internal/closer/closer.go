// Package closer plans and carries out the close of a tmux pane or a whole project: it archives each
// Claude pane before the pane closes, and moves the invoking client off a session the close destroys.
package closer

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/archive"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/shell"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Plan is what Close does for a pane or a project and the client that invoked it.
type Plan struct {
	// Dir is the archive Dir for every closed Claude pane.
	Dir string
	// ClientTTY is the tty of the client that invoked the close.
	ClientTTY string
	// Panes are closed in order.
	Panes []protocol.Pane
	// Kept are the panes whose Cwd is Dir in sessions the close leaves open. PlanPane leaves it empty.
	Kept []protocol.Pane
	// MoveTo is the session the client at ClientTTY switches to before any pane closes, or "" when it stays.
	MoveTo string
	// Detach is true when that client's session closes and no session remains to move it to.
	Detach bool
}

// clientFieldSeparator splits list-clients fields. tmux prints it raw, and it cannot appear in a tty
// name; a session name may hold a space, so the separator is not one.
const clientFieldSeparator = "\x1f"

// PlanPane plans the close of paneID for the client at clientTTY. The client moves only when the close
// destroys the session it shows; it moves to the most recently active other session that keeps a pane,
// skipping shell sessions, and detaches when no such session remains.
func PlanPane(ctx context.Context, client *tmuxctl.Client, paneID, clientTTY string) (Plan, error) {
	panes, err := client.ListPanes(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("closer: list panes: %w", err)
	}
	idx := slices.IndexFunc(panes, func(p protocol.Pane) bool { return p.ID == paneID })
	if idx < 0 {
		return Plan{}, fmt.Errorf("closer: pane %s is not listed", paneID)
	}
	target := panes[idx]
	plan := Plan{Dir: target.Cwd, ClientTTY: clientTTY, Panes: []protocol.Pane{target}}

	closing := make(map[string]bool)
	if closesSession(panes, target.Session, paneID) {
		closing[target.Session] = true
	}
	plan.MoveTo, plan.Detach, err = clientMove(ctx, client, panes, clientTTY, closing)
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// PlanProject plans the close of the project at dir: every session whose @orrery_dir is dir, and the
// shell session for dir when it exists. It closes every pane of those sessions, keeps the panes in dir
// of every other session, and moves the client at clientTTY as PlanPane does.
func PlanProject(ctx context.Context, client *tmuxctl.Client, dir, clientTTY string) (Plan, error) {
	// An empty dir matches every session without @orrery_dir, so it selects no project.
	if dir == "" {
		return Plan{}, fmt.Errorf("closer: no session for an empty directory")
	}
	dirs, err := sessionDirs(ctx, client)
	if err != nil {
		return Plan{}, err
	}
	shellName := shell.Name(dir)
	closing := make(map[string]bool)
	for name, sessionDir := range dirs {
		if sessionDir == dir || name == shellName {
			closing[name] = true
		}
	}
	if len(closing) == 0 {
		return Plan{}, fmt.Errorf("closer: no session for %s", dir)
	}

	panes, err := client.ListPanes(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("closer: list panes: %w", err)
	}
	plan := Plan{Dir: dir, ClientTTY: clientTTY}
	for _, pane := range panes {
		switch {
		case closing[pane.Session]:
			plan.Panes = append(plan.Panes, pane)
		case pane.Cwd == dir:
			plan.Kept = append(plan.Kept, pane)
		}
	}
	plan.MoveTo, plan.Detach, err = clientMove(ctx, client, panes, clientTTY, closing)
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// clientMove returns where the client at clientTTY goes when the close destroys the sessions in closing,
// and whether it detaches. The client moves only when it shows one of them. It moves to the most recently
// active other session that keeps a pane, skipping shell sessions, and detaches when none remains.
func clientMove(ctx context.Context, client *tmuxctl.Client, panes []protocol.Pane, clientTTY string,
	closing map[string]bool) (string, bool, error) {
	session, err := clientSession(ctx, client, clientTTY)
	if err != nil {
		return "", false, err
	}
	if session == "" || !closing[session] {
		return "", false, nil
	}

	var moveTo string
	var moveActivity int64
	for _, pane := range panes {
		if closing[pane.Session] || strings.HasPrefix(pane.Session, protocol.ShellSessionPrefix) {
			continue
		}
		newer := moveTo == "" || pane.Activity > moveActivity ||
			(pane.Activity == moveActivity && pane.Session < moveTo)
		if newer {
			moveTo, moveActivity = pane.Session, pane.Activity
		}
	}
	return moveTo, moveTo == "", nil
}

// sessionFieldSeparator splits sessionDirs fields. tmux prints it raw, and it cannot appear in a single-line option value.
const sessionFieldSeparator = "\x1f"

// sessionFormat is the list-sessions format: the session name, then its @orrery_dir.
const sessionFormat = "#{session_name}" + sessionFieldSeparator + "#{" + protocol.OptionDir + "}"

// sessionDirs maps each session on the server to its @orrery_dir, "" when unset.
func sessionDirs(ctx context.Context, client *tmuxctl.Client) (map[string]string, error) {
	out, err := client.Run(ctx, "list-sessions", "-F", sessionFormat)
	if err != nil {
		return nil, fmt.Errorf("closer: list sessions: %w", err)
	}
	dirs := make(map[string]string)
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		name, dir, ok := strings.Cut(line, sessionFieldSeparator)
		if !ok {
			return nil, fmt.Errorf("closer: list sessions line %q has no directory", line)
		}
		dirs[name] = dir
	}
	return dirs, nil
}

// clientSession returns the session the client at clientTTY shows, or "" when no client has that tty.
func clientSession(ctx context.Context, client *tmuxctl.Client, clientTTY string) (string, error) {
	out, err := client.Run(ctx, "list-clients", "-F", "#{client_tty}"+clientFieldSeparator+"#{client_session}")
	if err != nil {
		return "", fmt.Errorf("closer: list clients: %w", err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if line == "" {
			continue
		}
		tty, session, ok := strings.Cut(line, clientFieldSeparator)
		if !ok {
			return "", fmt.Errorf("closer: list clients line %q has no session", line)
		}
		if tty == clientTTY {
			return session, nil
		}
	}
	return "", nil
}

// closesSession reports whether every pane of session is the pane paneID, so that closing paneID
// destroys session.
func closesSession(panes []protocol.Pane, session, paneID string) bool {
	for _, pane := range panes {
		if pane.Session == session && pane.ID != paneID {
			return false
		}
	}
	return true
}

// Close archives every Claude pane in plan, then moves the client, then closes the panes. A pane whose
// archive write fails stays open, and nothing moves or closes.
func Close(ctx context.Context, client *tmuxctl.Client, plan Plan, archivePath string, now time.Time) error {
	for _, pane := range plan.Panes {
		if err := archivePane(ctx, client, plan.Dir, pane, archivePath, now); err != nil {
			return err
		}
	}
	if plan.MoveTo != "" {
		if _, err := client.Run(ctx, "switch-client", "-c", plan.ClientTTY, "-t", "="+plan.MoveTo); err != nil {
			return fmt.Errorf("closer: move client %s to %q: %w", plan.ClientTTY, plan.MoveTo, err)
		}
	}
	for _, pane := range plan.Panes {
		if _, err := client.Run(ctx, "kill-pane", "-t", pane.ID); err != nil {
			return fmt.Errorf("closer: close pane %s: %w", pane.ID, err)
		}
	}
	return nil
}

// archivePane appends a record for pane when it holds a Claude session. A pane without one is skipped.
func archivePane(ctx context.Context, client *tmuxctl.Client, dir string, pane protocol.Pane,
	archivePath string, now time.Time) error {
	session, _, err := client.PaneOption(ctx, pane.ID, protocol.OptionClaudeSession)
	if err != nil {
		return fmt.Errorf("closer: read %s on pane %s: %w", protocol.OptionClaudeSession, pane.ID, err)
	}
	if session == "" {
		return nil
	}
	transcript, _, err := client.PaneOption(ctx, pane.ID, protocol.OptionTranscript)
	if err != nil {
		return fmt.Errorf("closer: read %s on pane %s: %w", protocol.OptionTranscript, pane.ID, err)
	}
	record := archive.Record{
		Dir:           dir,
		Session:       pane.Session,
		ClaudeSession: session,
		Transcript:    transcript,
		Label:         pane.Label,
		Summary:       pane.Summary,
		Closed:        now.Unix(),
	}
	if err = archive.Append(archivePath, record); err != nil {
		return fmt.Errorf("closer: archive pane %s: %w", pane.ID, err)
	}
	return nil
}
