// Package view arranges panes into the project groups and order shown to the user.
package view

import (
	"cmp"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// Unseen reports whether p changed after it was last seen.
func Unseen(p protocol.Pane) bool {
	return p.State != "" && p.Changed > p.Seen
}

// Group is one project: the panes whose working directory is Dir.
type Group struct {
	Dir   string
	Panes []protocol.Pane
}

// Groups returns the project groups for panes, with shell-session panes dropped.
// Panes sort by rank, then host, session, window, and pane. Groups sort by the
// rank of their first pane, then by Dir. The input slice is not modified.
func Groups(panes []protocol.Pane) []Group {
	byDir := make(map[string][]protocol.Pane)
	for _, p := range panes {
		if strings.HasPrefix(p.Session, protocol.ShellSessionPrefix) {
			continue
		}
		byDir[p.Cwd] = append(byDir[p.Cwd], p)
	}

	groups := make([]Group, 0, len(byDir))
	for dir, group := range byDir {
		slices.SortFunc(group, comparePanes)
		groups = append(groups, Group{Dir: dir, Panes: group})
	}
	slices.SortFunc(groups, compareGroups)
	return groups
}

// Pane ranks, smallest first. Waiting always sorts first.
const (
	rankWaiting = iota
	rankUnseen
	rankWorking
	rankRest
)

// rank orders panes for display: waiting first, then other unseen panes,
// then working panes that are already seen, then everything else.
func rank(pane protocol.Pane) int {
	switch {
	case pane.State == protocol.StateWaiting:
		return rankWaiting
	case Unseen(pane):
		return rankUnseen
	case pane.State == protocol.StateWorking:
		return rankWorking
	default:
		return rankRest
	}
}

func comparePanes(left, right protocol.Pane) int {
	return cmp.Or(
		cmp.Compare(rank(left), rank(right)),
		cmp.Compare(left.Host, right.Host),
		cmp.Compare(left.Session, right.Session),
		cmp.Compare(left.WindowIndex, right.WindowIndex),
		cmp.Compare(left.PaneIndex, right.PaneIndex),
	)
}

func compareGroups(left, right Group) int {
	return cmp.Or(
		cmp.Compare(rank(left.Panes[0]), rank(right.Panes[0])),
		cmp.Compare(left.Dir, right.Dir),
	)
}
