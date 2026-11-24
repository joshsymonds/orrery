package compose

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/hostexec/hostexectest"
)

// hostOverlayRef is the pin TestComposeThroughHost gives its overlay, distinct from testRef, the pin of its compose.
const hostOverlayRef = "refs/orrery/test/host-overlay"

// TestComposeThroughHost checks that Compose and Overlay run their git in the repository through the Host they are
// given, and that Overlay makes and removes its scratch directory through it. The Host is a Recorder over the local
// Host, so a call that bypassed it would leave no record. Each pin is the one update-ref call of its commit. The test
// is not parallel: it points TMPDIR at its own directory, where Overlay's scratch directory is made.
func TestComposeThroughHost(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	ctx := t.Context()
	dir := composeRepo(t)
	base := composeRevision(t, dir, "", "base\n", map[string]string{"a": "one", "b": "bee"}, nil)
	revision := composeRevision(t, dir, base, "revision\n", map[string]string{"a": "changed"}, nil)
	recorder := hostexectest.NewRecorder(hostexec.NewLocal())

	result, err := Compose(ctx, recorder, dir, base, []Task{{Seq: 1, Base: base, Revision: revision}}, testRef)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if len(result.Composed) != 1 {
		t.Fatalf("Compose composed %d tasks, want 1", len(result.Composed))
	}
	calls := recorder.Calls()
	for _, call := range calls {
		if call.Op != "Git" || call.Path != dir {
			t.Errorf("Compose made a %s call on %q, want a git call in %s", call.Op, call.Path, dir)
		}
	}
	pin := []string{"update-ref", testRef, result.Candidate, ""}
	if !slices.ContainsFunc(calls, func(call hostexectest.Call) bool { return slices.Equal(call.Args, pin) }) {
		t.Errorf("Compose made no call through the host to pin %s at %s", testRef, result.Candidate)
	}

	before := len(calls)
	overlay, err := Overlay(ctx, recorder, dir, base, revision, []string{"a"}, hostOverlayRef)
	if err != nil {
		t.Fatalf("Overlay: %v", err)
	}
	calls = recorder.Calls()[before:]
	var scratch []hostexectest.Call
	for _, call := range calls {
		switch call.Op {
		case "Git":
			if call.Path != dir {
				t.Errorf("Overlay ran git in %q, want %s", call.Path, dir)
			}
		case "MkdirTemp", "RemoveAll":
			scratch = append(scratch, call)
		default:
			t.Errorf("Overlay made a %s call, want git and its scratch directory only", call.Op)
		}
	}
	pin = []string{"update-ref", hostOverlayRef, overlay, ""}
	if !slices.ContainsFunc(calls, func(call hostexectest.Call) bool { return slices.Equal(call.Args, pin) }) {
		t.Errorf("Overlay made no call through the host to pin %s at %s", hostOverlayRef, overlay)
	}
	if len(scratch) != 2 || scratch[0].Op != "MkdirTemp" || scratch[1].Op != "RemoveAll" {
		t.Fatalf("Overlay's scratch calls = %v, want one MkdirTemp then one RemoveAll", scratch)
	}
	made := scratch[1].Path
	if filepath.Dir(made) != tmp || !strings.HasPrefix(filepath.Base(made), "orrery-overlay-") {
		t.Errorf("Overlay removed %q, want the scratch directory it made in %s", made, tmp)
	}
	if _, err := os.Stat(made); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("scratch directory %s after Overlay: stat err = %v, want it removed", made, err)
	}
}
