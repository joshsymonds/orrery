package build

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceBriefWakes is the EvidenceGatherers of the brief tests. It records the epic each wake names, in order.
type evidenceBriefWakes struct {
	mu    sync.Mutex
	epics []string
}

// Wake records epic as woken.
func (w *evidenceBriefWakes) Wake(_ context.Context, epic string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.epics = append(w.epics, epic)
}

// woken returns the epics woken, in order.
func (w *evidenceBriefWakes) woken() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.epics)
}

// evidenceBriefWantFiled fails the test unless the run with id of f's epic is stored as a brief of task 1 waiting for its
// gatherer, at revision in phase, with no capture.
func evidenceBriefWantFiled(t *testing.T, f *passFixture, id, revision, phase string) {
	t.Helper()
	run, err := f.store.EvidenceRun(f.ctx, id)
	if err != nil {
		t.Fatalf("read brief %s: %v", id, err)
	}
	if run.Epic != passEpic || run.Item != "T1" || run.Revision != revision || run.Phase != phase ||
		run.Source != evidence.SourceCaptured || run.State != store.RunWaiting || run.Capture != "" {
		t.Fatalf(
			"brief %s = epic %q item %q revision %q phase %q source %q state %q capture %q, "+
				"want a waiting brief of task 1 at %s in %s",
			id, run.Epic, run.Item, run.Revision, run.Phase, run.Source, run.State, run.Capture,
			revision, phase,
		)
	}
}

// TestEvidenceBriefTaskRedAndGreenFileBriefs runs a task's red, and then its green, through a real enginehost.Evidence over the
// store. Each files a brief for the task's item at its revision and phase and wakes the epic's gatherer, and starts nothing: no
// directory is added under the evidence worktrees.
func TestEvidenceBriefTaskRedAndGreenFileBriefs(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	wakes := &evidenceBriefWakes{}
	ev := enginehost.NewEvidence(enginehost.EvidenceConfig{
		Store: f.store, StateDir: f.stateDir, Briefs: f.store, Gatherers: wakes,
	})
	t.Cleanup(ev.Close)
	f.runner = New(Config{
		Store: f.store, Steps: f.steps, Evidence: ev, Getenv: f.getenv, StateDir: f.stateDir, Now: passClock,
	})

	f.pass()
	evidenceBriefWantFiled(t, f, passRunID(StageRed, 0), f.base, evidence.PhaseRed)
	f.setBuild(store.TaskBuild{Stage: StageGreen, Base: f.base, Attempt: 1, Revision: f.base})
	f.pass()
	evidenceBriefWantFiled(t, f, passRunID(StageGreen, 1), f.base, evidence.PhaseGreen)

	ev.Close()
	if woken := wakes.woken(); !slices.Equal(woken, []string{passEpic, passEpic}) {
		t.Fatalf("gatherers woken = %q, want the epic once for each brief", woken)
	}
	if _, err := os.Stat(filepath.Join(f.stateDir, "evidence-worktrees")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("evidence worktrees stat = %v, want no directory added", err)
	}
}
