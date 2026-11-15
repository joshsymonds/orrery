package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// readoptEpic returns an epic whose repository is a new git repository under a new home, with epicGates as its gates,
// and the getenv that reads that home. Project discovery and the machine pool file read HOME, so the repository sits
// there and no test sets the process environment.
func readoptEpic(t *testing.T, epicGates ...store.EpicGate) (store.EpicRow, func(string) string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "src", "app")
	if err = os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	buildRun(t, repo, "init", "-q")
	getenv := func(name string) string {
		if name == "HOME" {
			return home
		}
		return ""
	}
	return store.EpicRow{ID: "acme/epic", Repository: repo, Gates: epicGates}, getenv
}

// readoptCommandGate is an epic gate that gates.Define accepts as a command gate.
func readoptCommandGate(name string) store.EpicGate {
	return store.EpicGate{
		Name:  name,
		Argv:  []string{"/opt/tools/run", "-c", "check"},
		Limit: 60,
		Needs: []store.EpicGateNeed{{Pool: "gate", Count: 1}},
	}
}

// readoptReviewGate is an epic agent gate named review that runs the reviewer agent.
func readoptReviewGate() store.EpicGate {
	return store.EpicGate{Name: "review", Agent: "reviewer", Limit: 60, Must: []string{"the diff is correct"}}
}

// writeReadoptFile writes content to path, creating its directory.
func writeReadoptFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeEpics is an epic reader that returns the epics it holds and fails every other id.
type fakeEpics map[string]store.EpicRow

func (f fakeEpics) Epic(_ context.Context, id string) (store.EpicRow, error) {
	epic, ok := f[id]
	if !ok {
		return store.EpicRow{}, fmt.Errorf("no epic %s", id)
	}
	return epic, nil
}

func TestResolveGateFindsEpicGate(t *testing.T) {
	t.Parallel()
	epic, getenv := readoptEpic(t, readoptCommandGate("check"))
	got, err := ResolveGate(context.Background(), epic, "check", getenv)
	if err != nil {
		t.Fatalf("ResolveGate(check) = %v, want nil", err)
	}
	if got.Name != "check" || !slices.Equal(got.Argv, []string{"/opt/tools/run", "-c", "check"}) {
		t.Errorf("ResolveGate(check) = %+v, want the epic's check command gate", got)
	}
}

func TestResolveGateFallsBackToProjectFile(t *testing.T) {
	t.Parallel()
	epic, getenv := readoptEpic(t)
	writeReadoptFile(t, filepath.Join(epic.Repository, ".orrery", "project.toml"),
		"[gates]\n[gates.lint]\nargv = [\"/opt/tools/lint\"]\nlimit = 60\n")
	got, err := ResolveGate(context.Background(), epic, "lint", getenv)
	if err != nil {
		t.Fatalf("ResolveGate(lint) = %v, want nil", err)
	}
	if got.Name != "lint" || !slices.Equal(got.Argv, []string{"/opt/tools/lint"}) {
		t.Errorf("ResolveGate(lint) = %+v, want the project file's lint gate", got)
	}
}

func TestResolveGateUnknownNameWrapsErrUnknown(t *testing.T) {
	t.Parallel()
	epic, getenv := readoptEpic(t, readoptCommandGate("check"))
	_, err := ResolveGate(context.Background(), epic, "missing", getenv)
	if !errors.Is(err, gates.ErrUnknown) {
		t.Fatalf("ResolveGate(missing) = %v, want an error wrapping gates.ErrUnknown", err)
	}
}

func TestReadoptCheckRestoresVerifierCheckForVerifierRow(t *testing.T) {
	t.Parallel()
	check, err := ReadoptCheck()(context.Background(), store.StepRow{ID: "s1", Schema: VerifierSchema})
	if err != nil {
		t.Fatalf("ReadoptCheck(verifier) error = %v, want nil", err)
	}
	if check == nil {
		t.Fatal("ReadoptCheck(verifier) = nil, want VerifierCheck")
	}
	two := []byte(`{"status":"done","results":[{"id":"F1","evidence":"e","result":"confirmed"},` +
		`{"id":"F2","evidence":"e","result":"dropped"}]}`)
	want := []string{"results: want exactly one result, got 2"}
	if got := check(two); !slices.Equal(got, want) {
		t.Errorf("check of a return with two results = %q, want %q", got, want)
	}
}

func TestReadoptCheckIsNilForOtherRows(t *testing.T) {
	t.Parallel()
	rows := map[string]store.StepRow{
		"no schema":    {ID: "s2"},
		"other schema": {ID: "s3", Schema: `{"type":"object"}`},
	}
	for name, row := range rows {
		check, err := ReadoptCheck()(context.Background(), row)
		if err != nil {
			t.Errorf("ReadoptCheck(%s) error = %v, want nil", name, err)
		}
		if check != nil {
			t.Errorf("ReadoptCheck(%s) returned a check, want nil", name)
		}
	}
}

func TestReadoptGateRebuildsReviewGateOfTaskStep(t *testing.T) {
	t.Parallel()
	epic, getenv := readoptEpic(t, readoptReviewGate())
	row := store.StepRow{ID: "s1", Epic: epic.ID, Task: "T3", ReadOnly: true, GateName: "review"}
	gate, err := ReadoptGate(fakeEpics{epic.ID: epic}, getenv)(context.Background(), row)
	if err != nil {
		t.Fatalf("ReadoptGate(T3 review) error = %v, want nil", err)
	}
	if gate == nil {
		t.Fatal("ReadoptGate(T3 review) = nil, want the review agent gate")
	}
	if gate.Name != "review" || gate.Agent != "reviewer" {
		t.Errorf("ReadoptGate(T3 review) = %+v, want gate review running agent reviewer", *gate)
	}
}

func TestReadoptGateIsNilForRowsThatRunNoTaskReviewGate(t *testing.T) {
	t.Parallel()
	epic, getenv := readoptEpic(t, readoptReviewGate())
	rows := map[string]store.StepRow{
		"evidence judge": {
			ID: "s2", Epic: epic.ID, Task: "evidence-0123456789abcdef0123456789abcdef", ReadOnly: true,
			GateName: "review",
		},
		"writing row": {ID: "s3", Epic: epic.ID, Task: "T3", ReadOnly: false, GateName: "review"},
		"no gate":     {ID: "s4", Epic: epic.ID, Task: "T3", ReadOnly: true},
		"task zero":   {ID: "s5", Epic: epic.ID, Task: "T0", ReadOnly: true, GateName: "review"},
	}
	for name, row := range rows {
		gate, err := ReadoptGate(fakeEpics{epic.ID: epic}, getenv)(context.Background(), row)
		if err != nil {
			t.Errorf("ReadoptGate(%s) error = %v, want nil", name, err)
		}
		if gate != nil {
			t.Errorf("ReadoptGate(%s) = %+v, want nil", name, *gate)
		}
	}
}

func TestReadoptGateErrorsWhenEpicDoesNotRead(t *testing.T) {
	t.Parallel()
	epic, getenv := readoptEpic(t, readoptReviewGate())
	row := store.StepRow{ID: "s1", Epic: epic.ID, Task: "T3", ReadOnly: true, GateName: "review"}
	if _, err := ReadoptGate(fakeEpics{}, getenv)(context.Background(), row); err == nil {
		t.Fatal("ReadoptGate with an epic that does not read = nil error, want one")
	}
}

func TestReadoptGateErrorsForCommandGate(t *testing.T) {
	t.Parallel()
	epic, getenv := readoptEpic(t, readoptCommandGate("check"))
	row := store.StepRow{ID: "s1", Epic: epic.ID, Task: "T3", ReadOnly: true, GateName: "check"}
	if _, err := ReadoptGate(fakeEpics{epic.ID: epic}, getenv)(context.Background(), row); err == nil {
		t.Fatal("ReadoptGate of a command gate = nil error, want one")
	}
}
