package interpret

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// briefWakes is the EvidenceGatherers of the brief tests. It records the epic each wake names, in order.
type briefWakes struct {
	mu    sync.Mutex
	epics []string
}

// Wake records epic as woken.
func (w *briefWakes) Wake(_ context.Context, epic string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.epics = append(w.epics, epic)
}

// woken returns the epics woken, in order.
func (w *briefWakes) woken() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.epics)
}

// briefEvidence returns an enginehost.Evidence over s, which files its briefs in s and wakes the gatherers in the briefWakes it
// returns. Close runs when the test ends.
func briefEvidence(t *testing.T, s *store.Store) (*enginehost.Evidence, *briefWakes) {
	t.Helper()
	wakes := &briefWakes{}
	ev := enginehost.NewEvidence(enginehost.EvidenceConfig{
		Store: s, StateDir: t.TempDir(), Briefs: s, Gatherers: wakes,
	})
	t.Cleanup(ev.Close)
	return ev, wakes
}

// briefRunner returns a runner over s whose evidence is ev, with the fakes of the node tests for everything else.
func briefRunner(s *store.Store, ev Evidence) *Runner {
	cfg := nodeConfig(s, newNodeFakes(s))
	cfg.Evidence = ev
	return New(cfg)
}

// briefWantFiled fails the test unless the run with id is stored as a captured brief of R1 of runEpic, waiting for its gatherer,
// at revision in phase, with no grant and no capture.
func briefWantFiled(t *testing.T, s *store.Store, id, revision, phase string) {
	t.Helper()
	run, err := s.EvidenceRun(t.Context(), id)
	if err != nil {
		t.Fatalf("read brief %s: %v", id, err)
	}
	if run.Epic != runEpic || run.Item != "R1" || run.Revision != revision || run.Phase != phase ||
		run.Source != evidence.SourceCaptured || run.State != store.RunWaiting || run.Grant != "" || run.Capture != "" {
		t.Fatalf(
			"brief %s = epic %q item %q revision %q phase %q source %q state %q grant %q capture %q, "+
				"want a waiting brief of R1 at %s in %s",
			id, run.Epic, run.Item, run.Revision, run.Phase, run.Source, run.State, run.Grant, run.Capture,
			revision, phase,
		)
	}
}

// TestEvidenceBriefEvidenceNodeFilesABriefAtBaseAndAtHead runs the evidence node of one flow at base, and of another at head,
// through a real enginehost.Evidence over the store. The node at base files a red brief at the base, and the node at head a green
// brief at the head of the epic's branch. Each wakes the epic's gatherer.
func TestEvidenceBriefEvidenceNodeFilesABriefAtBaseAndAtHead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, flow, phase string
	}{
		{"at base", nodeEvidenceFlow, evidence.PhaseRed},
		{"at head", nodeEvidenceHeadFlow, evidence.PhaseGreen},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, base := nodeRepo(t)
			head := nodeEpicBranch(t, repo, "b.go")
			s := openRunStore(t)
			createNodeEpic(t, s, repo, base, tc.flow)
			ev, wakes := briefEvidence(t, s)

			briefRunner(s, ev).Pass(t.Context())

			revision := base
			if tc.phase == evidence.PhaseGreen {
				revision = head
			}
			briefWantFiled(t, s, workID(t, s, "evidence", "R1"), revision, tc.phase)
			if woken := wakes.woken(); !slices.Equal(woken, []string{runEpic}) {
				t.Fatalf("gatherers woken = %q, want the epic once", woken)
			}
		})
	}
}

// TestEvidenceBriefCapturesFileGreenBriefs runs the capture of R1 that the integrate, sync and release kinds each start, at the
// commit each captures, through a real enginehost.Evidence over the store. All three start their capture through Runner.capture,
// so each is driven through it with the action its kind gives: the capture files a green brief at that commit and wakes the epic's
// gatherer, and nothing starts.
func TestEvidenceBriefCapturesFileGreenBriefs(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{kindIntegrate, kindSync, kindRelease} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			repo, base := nodeRepo(t)
			head := nodeEpicBranch(t, repo, "b.go")
			s := openRunStore(t)
			createNodeEpic(t, s, repo, base, nodeEvidenceHeadFlow)
			ev, wakes := briefEvidence(t, s)
			id := store.NodeWorkID(kind, 1, "capture R1")

			err := briefRunner(s, ev).capture(t.Context(), epicOf(t, s), Action{
				NodeKind: kind, Run: store.NodeRun{Revision: head}, IDs: []string{id}, Items: []string{"R1"},
			})
			if err != nil {
				t.Fatalf("capture of %s: %v", kind, err)
			}
			briefWantFiled(t, s, id, head, evidence.PhaseGreen)
			if woken := wakes.woken(); !slices.Equal(woken, []string{runEpic}) {
				t.Fatalf("gatherers woken = %q, want the epic once", woken)
			}
		})
	}
}

// TestEvidenceBriefAmendmentRedAndGreenFileBriefs drives an evidence amendment of R1 through a real enginehost.Evidence over the
// store. Its red run files a brief at the epic's base, holding the amended item. Its green run, which an amendment in its capturing
// state starts at the stored base it names, files a brief there, and each wakes the epic's gatherer.
func TestEvidenceBriefAmendmentRedAndGreenFileBriefs(t *testing.T) {
	t.Parallel()
	w := newAmendWorld(t)
	item := amendedTestItem("cmd/quiet_test.go")
	amended := amendR1(t, w.s, item)
	ev, wakes := briefEvidence(t, w.s)
	r := briefRunner(w.s, ev)
	epic := epicOf(t, w.s)

	if err := r.driveAmendment(t.Context(), epic, amended); err != nil {
		t.Fatalf("drive the red of the amendment: %v", err)
	}
	briefWantFiled(t, w.s, amended.RedRun, w.base, evidence.PhaseRed)
	red, err := w.s.EvidenceRun(t.Context(), amended.RedRun)
	if err != nil {
		t.Fatalf("read red brief: %v", err)
	}
	if !reflect.DeepEqual(red.Spec, item) {
		t.Fatalf("red brief spec = %+v, want the amended item %+v", red.Spec, item)
	}

	// The green run of an amendment is driven from its capturing state, at the stored base the amendment names, under the id the
	// store gives that run. The state is given here, since moving an amendment to capturing needs the candidate of an integrate
	// run, which these tests do not run. Its reproduction check is left out, so only the green run is filed.
	green := amended
	green.State, green.StoredBase, green.ReproCheck = store.AmendmentCapturing, w.base, ""
	green.GreenRun = store.AmendmentWorkID(amended.Epic, amended.Seq, store.AmendmentGreen, 0)
	if err = r.driveAmendment(t.Context(), epic, green); err != nil {
		t.Fatalf("drive the green of the amendment: %v", err)
	}
	briefWantFiled(t, w.s, green.GreenRun, w.base, evidence.PhaseGreen)
	if woken := wakes.woken(); !slices.Equal(woken, []string{runEpic, runEpic}) {
		t.Fatalf("gatherers woken = %q, want the epic once for each brief", woken)
	}
}
