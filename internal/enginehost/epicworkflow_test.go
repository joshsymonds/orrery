package enginehost

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// pinTestEpicPath is the repository path of the workflow the tests commit under the name epic.
const pinTestEpicPath = ".orrery/workflows/epic.yaml"

// pinTestEpicText is the canonical text of the built-in epic, as E41 freezes it, and pinTestEpicSHA its sha256.
const pinTestEpicText = `name: epic
nodes:
  - id: red
    kind: evidence
    at: base
  - id: plan
    kind: plan
    body:
      - id: build
        kind: build
      - id: integrate
        kind: integrate
  - id: review
    kind: review
  - id: correct
    loop:
      until: closure.passed
      spent: continue
    body:
      - id: fix
        kind: plan
        body:
          - id: rebuild
            kind: build
          - id: reintegrate
            kind: integrate
      - id: closure
        kind: closure
  - id: conclude
    kind: conclude
  - id: release
    kind: release
`

const pinTestEpicSHA = "e12a2b6d237e3f15cc4bcbd17924dc2a27cdb498668c595e33d63fd0526fe796"

// pinTestOutline is a committed epic: an outline plan holding one write build.
const pinTestOutline = "name: epic\nnodes:\n  - id: outline\n    kind: plan\n    body:\n      - id: write\n        kind: build\n"

// pinTestDrafted is a later commit's edit of pinTestOutline, under other ids. It is also what an epic including
// pinTestShared expands to.
const pinTestDrafted = "name: epic\nnodes:\n  - id: draft\n    kind: plan\n    body:\n      - id: make\n        kind: build\n"

// pinTestShared is a committed workflow an epic includes. Its name is its file's name.
const pinTestShared = "name: shared\nnodes:\n  - id: draft\n    kind: plan\n    body:\n      - id: make\n        kind: build\n"

// pinTestLoop is a committed epic with a loop over a plan holding a build, then a second plan at the top level.
const pinTestLoop = `name: epic
nodes:
  - id: rounds
    loop:
      max: 3
      until: plan.complete
    body:
      - id: plan
        kind: plan
        body:
          - id: build
            kind: build
  - id: final
    kind: plan
    body:
      - id: ship
        kind: build
`

// pinTestSHA is the lowercase hex sha256 of text.
func pinTestSHA(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func TestPinWorkflowEmptyNamePinsBuiltInEpic(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)

	got, err := pinWorkflow(t.Context(), dir, "HEAD", "")
	if err != nil {
		t.Fatalf("pinWorkflow: %v", err)
	}
	want := store.EpicWorkflow{
		Name:   "epic",
		Text:   pinTestEpicText,
		SHA256: pinTestEpicSHA,
		Nodes: []store.EpicWorkflowNode{
			{ID: "red", Parent: "", Kind: "evidence"},
			{ID: "plan", Parent: "", Kind: "plan"},
			{ID: "build", Parent: "plan", Kind: "build"},
			{ID: "integrate", Parent: "plan", Kind: "integrate"},
			{ID: "review", Parent: "", Kind: "review"},
			{ID: "correct", Parent: "", Kind: "loop"},
			{ID: "fix", Parent: "correct", Kind: "plan"},
			{ID: "rebuild", Parent: "fix", Kind: "build"},
			{ID: "reintegrate", Parent: "fix", Kind: "integrate"},
			{ID: "closure", Parent: "correct", Kind: "closure"},
			{ID: "conclude", Parent: "", Kind: "conclude"},
			{ID: "release", Parent: "", Kind: "release"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pinWorkflow = %+v, want %+v", got, want)
	}
}

func TestPinWorkflowCommittedFileReplacesBuiltIn(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	base := stepGitTestCommit(t, dir, map[string]string{pinTestEpicPath: pinTestOutline})

	got, err := pinWorkflow(t.Context(), dir, base, "")
	if err != nil {
		t.Fatalf("pinWorkflow: %v", err)
	}
	want := store.EpicWorkflow{
		Name:   "epic",
		Text:   pinTestOutline,
		SHA256: pinTestSHA(pinTestOutline),
		Nodes: []store.EpicWorkflowNode{
			{ID: "outline", Parent: "", Kind: "plan"},
			{ID: "write", Parent: "outline", Kind: "build"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pinWorkflow = %+v, want %+v", got, want)
	}
}

func TestPinWorkflowPinsTheBaseNotLaterEdits(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	first := stepGitTestCommit(t, dir, map[string]string{pinTestEpicPath: pinTestOutline})
	second := stepGitTestCommit(t, dir, map[string]string{pinTestEpicPath: pinTestDrafted})
	// An uncommitted edit to the committed file, and a workflow file that exists only in the working tree.
	stepGitTestWrite(t, dir, pinTestEpicPath, pinTestLoop)
	stepGitTestWrite(t, dir, ".orrery/workflows/solo.yaml", pinTestOutline)

	atFirst, err := pinWorkflow(t.Context(), dir, first, "")
	if err != nil {
		t.Fatalf("pinWorkflow at first commit: %v", err)
	}
	if atFirst.Text != pinTestOutline {
		t.Fatalf("pin at the first commit has text %q, want the text committed there %q", atFirst.Text, pinTestOutline)
	}

	atSecond, err := pinWorkflow(t.Context(), dir, second, "")
	if err != nil {
		t.Fatalf("pinWorkflow at second commit: %v", err)
	}
	if atSecond.Text != pinTestDrafted {
		t.Fatalf("pin at the second commit has text %q, want %q", atSecond.Text, pinTestDrafted)
	}

	_, err = pinWorkflow(t.Context(), dir, second, "solo")
	want := fmt.Sprintf(
		`at base %s: workflow "solo": no file .orrery/workflows/solo.yaml and no built-in "solo"`, second,
	)
	if err == nil || err.Error() != want {
		t.Fatalf("pin of a working-tree-only file: err = %v, want %q", err, want)
	}
}

func TestPinWorkflowRefusesBrokenFileAtBaseNamingFileNodeKey(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	broken := "name: epic\nnodes:\n  - id: plan\n    kind: plan\n    body:\n      - id: build\n        kind: buildd\n"
	base := stepGitTestCommit(t, dir, map[string]string{pinTestEpicPath: broken})

	_, err := pinWorkflow(t.Context(), dir, base, "")
	want := fmt.Sprintf(
		`at base %s: .orrery/workflows/epic.yaml:7: node build: key kind: unknown kind "buildd"`, base,
	)
	if err == nil || err.Error() != want {
		t.Fatalf("pinWorkflow: err = %v, want %q", err, want)
	}
}

func TestPinWorkflowExpandsIncludeOfCommittedFile(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	includer := "name: epic\nnodes:\n  - include: shared\n"
	base := stepGitTestCommit(t, dir, map[string]string{
		pinTestEpicPath:                 includer,
		".orrery/workflows/shared.yaml": pinTestShared,
	})

	got, err := pinWorkflow(t.Context(), dir, base, "")
	if err != nil {
		t.Fatalf("pinWorkflow: %v", err)
	}
	if got.Text != pinTestDrafted {
		t.Fatalf("pinned text = %q, want the included nodes %q", got.Text, pinTestDrafted)
	}
	if got.SHA256 != pinTestSHA(pinTestDrafted) {
		t.Fatalf("sha256 = %s, want %s", got.SHA256, pinTestSHA(pinTestDrafted))
	}
}

func TestPinWorkflowRefusesSymlinkAtPath(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, ".orrery", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("shared.yaml", filepath.Join(dir, pinTestEpicPath)); err != nil {
		t.Fatal(err)
	}
	stepGitTestRun(t, dir, "add", pinTestEpicPath)
	stepGitTestRun(t, dir, "commit", "-q", "-m", "symlink")
	base := stepGitTestRun(t, dir, "rev-parse", "HEAD")

	_, err := pinWorkflow(t.Context(), dir, base, "")
	want := fmt.Sprintf("at base %s: %s: not a regular file (mode 120000)", base, pinTestEpicPath)
	if err == nil || err.Error() != want {
		t.Fatalf("pinWorkflow: err = %v, want %q", err, want)
	}
}

func TestPinWorkflowRefusesDirectoryAtPath(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	base := stepGitTestCommit(t, dir, map[string]string{pinTestEpicPath + "/inner.yaml": pinTestOutline})

	_, err := pinWorkflow(t.Context(), dir, base, "")
	want := fmt.Sprintf("at base %s: %s: not a regular file (mode 040000)", base, pinTestEpicPath)
	if err == nil || err.Error() != want {
		t.Fatalf("pinWorkflow: err = %v, want %q", err, want)
	}
}

func TestPinWorkflowReturnsGitFailureForBadBase(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)

	_, err := pinWorkflow(t.Context(), dir, "no-such-commit", "")
	if err == nil {
		t.Fatal("pinWorkflow with a base that does not resolve returned no error")
	}
	if !strings.HasPrefix(err.Error(), "resolve base: ") {
		t.Fatalf("pinWorkflow err = %q, want a resolve base refusal", err)
	}
}

func TestPinWorkflowNodeRowsArePreorderWithParents(t *testing.T) {
	t.Parallel()
	dir := stepGitTestRepo(t)
	base := stepGitTestCommit(t, dir, map[string]string{pinTestEpicPath: pinTestLoop})

	got, err := pinWorkflow(t.Context(), dir, base, "")
	if err != nil {
		t.Fatalf("pinWorkflow: %v", err)
	}
	want := []store.EpicWorkflowNode{
		{ID: "rounds", Parent: "", Kind: "loop"},
		{ID: "plan", Parent: "rounds", Kind: "plan"},
		{ID: "build", Parent: "plan", Kind: "build"},
		{ID: "final", Parent: "", Kind: "plan"},
		{ID: "ship", Parent: "final", Kind: "build"},
	}
	if !reflect.DeepEqual(got.Nodes, want) {
		t.Fatalf("nodes = %+v, want %+v", got.Nodes, want)
	}
}
