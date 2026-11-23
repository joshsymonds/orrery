package enginehost

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceBriefGatherers is the EvidenceGatherers of the brief tests. It records the epic each wake names, in order.
type evidenceBriefGatherers struct {
	mu    sync.Mutex
	epics []string
}

// Wake records epic as woken.
func (g *evidenceBriefGatherers) Wake(_ context.Context, epic string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.epics = append(g.epics, epic)
}

// woken returns the epics woken, in order.
func (g *evidenceBriefGatherers) woken() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.epics)
}

// evidenceBriefNew returns an Evidence over st and steps that files its briefs for gatherers, whose blobs and capture worktrees
// live under a scratch state directory, which it returns. Close runs when the test ends.
func evidenceBriefNew(
	t *testing.T, st *store.Store, steps EvidenceSteps, gatherers EvidenceGatherers,
) (*Evidence, string) {
	t.Helper()
	stateDir := t.TempDir()
	ev := NewEvidence(EvidenceConfig{Store: st, Steps: steps, StateDir: stateDir, Briefs: st, Gatherers: gatherers})
	t.Cleanup(ev.Close)
	return ev, stateDir
}

// evidenceBriefWantNoWorktree fails the test unless no directory was added under the capture worktrees of stateDir.
func evidenceBriefWantNoWorktree(t *testing.T, stateDir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(stateDir, "evidence-worktrees"))
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatalf("read evidence worktrees: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("%d directories under evidence-worktrees, want none", len(entries))
	}
}

// evidenceBriefFiling is an entry point of Evidence that files a brief. file runs it for the item named item at revision in
// phase, and the brief it files holds spec and, when id is set, is stored under id.
type evidenceBriefFiling struct {
	name string
	id   string
	item string
	spec evidence.Item
	file func(ctx context.Context, ev *Evidence, revision, phase string) (store.EvidenceRun, error)
}

// evidenceBriefFilings returns the entry points of Evidence that file a brief. Run, RunItem and RunTask run Requirement R1's item
// and task T1's, and RunAmendment runs amended for R1.
func evidenceBriefFilings(amended evidence.Item) []evidenceBriefFiling {
	item := evidenceRunItem()
	return []evidenceBriefFiling{
		{
			name: "Run", item: "R1", spec: item,
			file: func(ctx context.Context, ev *Evidence, revision, phase string) (store.EvidenceRun, error) {
				return ev.Run(ctx, evidenceRunEpic, "R1", revision, phase)
			},
		},
		{
			name: "RunItem", id: evidenceItemRunID, item: "R1", spec: item,
			file: func(ctx context.Context, ev *Evidence, revision, phase string) (store.EvidenceRun, error) {
				return ev.RunItem(ctx, evidenceItemRunID, evidenceRunEpic, "R1", revision, phase)
			},
		},
		{
			name: "RunTask", id: evidenceTaskRunID, item: "T1", spec: item,
			file: func(ctx context.Context, ev *Evidence, revision, phase string) (store.EvidenceRun, error) {
				return ev.RunTask(ctx, evidenceTaskRunID, evidenceRunEpic, 1, revision, phase)
			},
		},
		{
			name: "RunAmendment", id: evidenceTaskRunID, item: "R1", spec: amended,
			file: func(ctx context.Context, ev *Evidence, revision, phase string) (store.EvidenceRun, error) {
				return ev.RunAmendment(ctx, evidenceTaskRunID, evidenceRunEpic, "R1", amended, revision, phase)
			},
		},
	}
}

// evidenceBriefWantFiled fails the test unless started, the brief a filing returned, is stored waiting with the item, commit, phase
// and caller's id the filing named, the spec it holds, and nothing recorded for a capture, grant or judge. The brief is open for
// gatherers.
func evidenceBriefWantFiled(
	t *testing.T, st *store.Store, started store.EvidenceRun, filing evidenceBriefFiling, head, phase string,
) {
	t.Helper()
	evidenceRunWantStarted(t, started, filing.item, head, phase, evidence.SourceCaptured)
	if filing.id != "" && started.ID != filing.id {
		t.Fatalf("%s stored the brief under id %q, want the caller's %q", filing.name, started.ID, filing.id)
	}
	stored, err := st.EvidenceRun(t.Context(), started.ID)
	if err != nil {
		t.Fatalf("read brief %s: %v", started.ID, err)
	}
	if stored.Epic != evidenceRunEpic || stored.State != store.RunWaiting ||
		!reflect.DeepEqual(stored.Spec, filing.spec) {
		t.Fatalf("stored brief = epic %q state %q spec %+v, want epic %s waiting with spec %+v",
			stored.Epic, stored.State, stored.Spec, evidenceRunEpic, filing.spec)
	}
	if stored.Grant != "" || stored.Capture != "" || stored.JudgeStep != "" || stored.LogHash != "" ||
		len(stored.Artifacts) != 0 {
		t.Fatalf("stored brief records a grant %q, capture %q, judge %q, log %q and %d artifacts, want none",
			stored.Grant, stored.Capture, stored.JudgeStep, stored.LogHash, len(stored.Artifacts))
	}
	open, err := st.OpenBriefs(t.Context(), evidenceRunEpic)
	if err != nil {
		t.Fatalf("OpenBriefs: %v", err)
	}
	if len(open) != 1 || open[0].ID != started.ID {
		t.Fatalf("open briefs = %d, want the one brief %s", len(open), started.ID)
	}
}

// TestEvidenceBriefFilesAWaitingBriefForEachEntryPoint has each entry point that files a brief run a captured item at red and at
// green. Each stores a waiting brief with its item, commit, phase and the caller's id, wakes its epic's gatherer, and starts
// nothing: no directory is added under the capture worktrees, no grant is recorded, and no judge starts.
func TestEvidenceBriefFilesAWaitingBriefForEachEntryPoint(t *testing.T) {
	t.Parallel()
	amended := evidenceRunItem()
	amended.Conclusion = "the amended build passes"
	for _, filing := range evidenceBriefFilings(amended) {
		for _, phase := range []string{evidence.PhaseRed, evidence.PhaseGreen} {
			t.Run(filing.name+" "+phase, func(t *testing.T) {
				t.Parallel()
				repo, head := evidenceRunRepo(t, "exit 0\n")
				st := epicTestStore(t)
				evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
				steps := &evidenceRunSteps{}
				gatherers := &evidenceBriefGatherers{}
				ev, stateDir := evidenceBriefNew(t, st, steps, gatherers)

				started, err := filing.file(t.Context(), ev, head, phase)
				if err != nil {
					t.Fatalf("%s: %v", filing.name, err)
				}
				evidenceBriefWantFiled(t, st, started, filing, head, phase)
				ev.Close()
				if woken := gatherers.woken(); !slices.Equal(woken, []string{evidenceRunEpic}) {
					t.Fatalf("gatherers woken = %q, want the epic %s once", woken, evidenceRunEpic)
				}
				evidenceBriefWantNoWorktree(t, stateDir)
				if n := steps.startCount(); n != 0 {
					t.Fatalf("%d judge steps started for a brief, want none", n)
				}
			})
		}
	}
}

// TestEvidenceBriefSuppliedItemKeepsItsFilesAndFilesNoBrief runs a supplied item at red. Its files are read from the commit and
// kept, its judge gives the verdict, and no brief is filed: no gatherer is woken and no run waits for one.
func TestEvidenceBriefSuppliedItemKeepsItsFilesAndFilesNoBrief(t *testing.T) {
	t.Parallel()
	repo := stepGitTestRepo(t)
	head := stepGitTestCommit(t, repo, map[string]string{"report.txt": "committed\n"})
	st := epicTestStore(t)
	supplied := evidence.Item{Supplied: []string{"report.txt"}, Conclusion: "the build passes", Judge: "reviewer"}
	evidenceRunSeed(t, st, repo, supplied, supplied)
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	gatherers := &evidenceBriefGatherers{}
	ev, _ := evidenceBriefNew(t, st, steps, gatherers)

	started, got := evidenceRunOnce(t, ev, st, head, evidence.PhaseRed)
	evidenceRunWantStarted(t, started, "R1", head, evidence.PhaseRed, evidence.SourceSupplied)
	if len(got.Artifacts) != 1 || got.Artifacts[0].Path != "report.txt" || got.Artifacts[0].State != artifactKept {
		t.Fatalf("artifacts = %+v, want report.txt kept from the commit", got.Artifacts)
	}
	if got.Verdict != evidence.VerdictHolds || steps.startCount() != 1 {
		t.Fatalf("run verdict %q with %d judge starts, want holds with one judge", got.Verdict, steps.startCount())
	}
	if woken := gatherers.woken(); len(woken) != 0 {
		t.Fatalf("gatherers woken = %q, want none for a supplied item", woken)
	}
	open, err := st.OpenBriefs(t.Context(), evidenceRunEpic)
	if err != nil {
		t.Fatalf("OpenBriefs: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open briefs = %d, want none for a supplied item", len(open))
	}
}

// TestEvidenceBriefResumeLeavesABriefToItsGatherer files a brief, then resumes on a fresh Evidence over the same store, as a
// restarted orreryd does. Resume starts nothing for the brief and wakes no gatherer, and the brief stays waiting for one.
func TestEvidenceBriefResumeLeavesABriefToItsGatherer(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	first, _ := evidenceBriefNew(t, st, &evidenceRunSteps{}, &evidenceBriefGatherers{})
	brief, err := first.Run(t.Context(), evidenceRunEpic, "R1", head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	first.Close()

	steps := &evidenceRunSteps{}
	gatherers := &evidenceBriefGatherers{}
	resumed, stateDir := evidenceBriefNew(t, st, steps, gatherers)
	if err = resumed.Resume(t.Context()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	resumed.Close()

	stored, err := st.EvidenceRun(t.Context(), brief.ID)
	if err != nil {
		t.Fatalf("read brief %s: %v", brief.ID, err)
	}
	if stored.State != store.RunWaiting {
		t.Fatalf("brief after Resume = state %q, want waiting for its gatherer", stored.State)
	}
	if n := steps.startCount(); n != 0 {
		t.Fatalf("%d judge steps started by Resume for a brief, want none", n)
	}
	if woken := gatherers.woken(); len(woken) != 0 {
		t.Fatalf("gatherers woken by Resume = %q, want none", woken)
	}
	evidenceBriefWantNoWorktree(t, stateDir)
}

// TestEvidenceBriefFilingAfterCloseIsRefused closes the runner, then runs a captured item. The run is refused with
// errEvidenceClosed, and no gatherer is woken for it.
func TestEvidenceBriefFilingAfterCloseIsRefused(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	gatherers := &evidenceBriefGatherers{}
	ev, _ := evidenceBriefNew(t, st, &evidenceRunSteps{}, gatherers)
	ev.Close()

	_, err := ev.Run(t.Context(), evidenceRunEpic, "R1", head, evidence.PhaseGreen)
	if !errors.Is(err, errEvidenceClosed) {
		t.Fatalf("Run after Close = %v, want errEvidenceClosed", err)
	}
	if woken := gatherers.woken(); len(woken) != 0 {
		t.Fatalf("gatherers woken after Close = %q, want none", woken)
	}
}
