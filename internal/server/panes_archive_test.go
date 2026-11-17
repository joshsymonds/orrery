package server_test

import "testing"

// archiveTaggedSession is the synthetic @orrery_id value of the pane the archive fixture tags.
const archiveTaggedSession = "orrery-archive-tagged"

func TestServePanesArchivePaneRow(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	taggedPane := startTurnPane(t, tmux, "archive-tagged", archiveTaggedSession)
	plainPane := startTurnPane(t, tmux, "archive-plain", "")

	listed := listPanes(t, newHandler(t, tmux))
	if len(listed) != 2 {
		t.Fatalf("got %d panes, want 2", len(listed))
	}
	ids := make(map[string]string, len(listed))
	for _, pane := range listed {
		ids[pane.GetId()] = pane.GetOrreryId()
	}
	if got := ids[taggedPane]; got != archiveTaggedSession {
		t.Errorf("tagged pane orrery_id = %q, want %q", got, archiveTaggedSession)
	}
	if got := ids[plainPane]; got != "" {
		t.Errorf("pane without @orrery_id orrery_id = %q, want empty", got)
	}
}
