package build

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestSharedFilesAcceptedGrantOfHeldFile: an orchestrator grant of a file an open task owns, or one an accepted task
// changed and no batch has integrated, with no after_tasks, is refused by no rule: the file is granted at once.
func TestSharedFilesAcceptedGrantOfHeldFile(t *testing.T) {
	t.Parallel()
	task := store.EpicTask{Seq: 1, Files: []string{"mine.go"}, Status: taskOpen}
	peers := []Peer{{Task: task}, openPeer(2, "owned.go"), acceptedPeer(3, "changed.go")}
	check := OrchestratorCheck(task, peers)
	for _, tc := range []struct {
		name string
		file string
	}{
		{"an open task owns the file", "owned.go"},
		{"an accepted task changed the file", "changed.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ret := orchReturn(t, map[string]any{"decision": "grant", "version": filesVersion(tc.file)})
			if got := check(json.RawMessage(ret)); len(got) != 0 {
				t.Errorf("grant of %s = %q, want no refusal", tc.file, got)
			}
		})
	}
}

// TestSharedFilesAcceptedOrchestratorRole: the role text no longer makes a held file wait behind its holder, or says
// orreryd refuses any other grant of it, and says a held file is granted at once.
func TestSharedFilesAcceptedOrchestratorRole(t *testing.T) {
	t.Parallel()
	for _, old := range []string{"is granted only behind that task", "orreryd refuses any other grant"} {
		if strings.Contains(OrchestratorRole, old) {
			t.Errorf("orchestrator role still says %q", old)
		}
	}
	if want := "Such a file is granted at once, as a free file is."; !strings.Contains(OrchestratorRole, want) {
		t.Errorf("orchestrator role does not say %q", want)
	}
}
