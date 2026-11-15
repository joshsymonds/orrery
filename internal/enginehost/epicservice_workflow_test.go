package enginehost

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// builtInEpicText is the canonical text of the built-in epic, and builtInEpicSHA256 the sha256 of that text.
const (
	builtInEpicText = "name: epic\nnodes:\n  - id: red\n    kind: evidence\n    at: base\n  - id: plan\n    kind: plan\n" +
		"    body:\n      - id: build\n        kind: build\n      - id: integrate\n        kind: integrate\n"
	builtInEpicSHA256 = "c1e3aa00af4e050003c2ba37afc2c9ca346233cbaed0dcab92df836c370ddf7b"
)

// demoWorkflowFile is a repository workflow named demo. Its comment is not in its canonical text.
const demoWorkflowFile = "# Synthetic demo workflow.\nname: demo\nnodes:\n  - id: plan\n    kind: plan\n    body:\n" +
	"      - id: build\n        kind: build\n"

// demoWorkflowText is the canonical text of demoWorkflowFile.
const demoWorkflowText = "name: demo\nnodes:\n  - id: plan\n    kind: plan\n    body:\n      - id: build\n        kind: build\n"

// editedDemoWorkflowFile is demoWorkflowFile with its build node renamed.
const editedDemoWorkflowFile = "name: demo\nnodes:\n  - id: plan\n    kind: plan\n    body:\n" +
	"      - id: build_it\n        kind: build\n"

// brokenWorkflowFile is a workflow whose build node carries a key no kind has, on line 8.
const brokenWorkflowFile = "name: broken\nnodes:\n  - id: plan\n    kind: plan\n    body:\n" +
	"      - id: build\n        kind: build\n        bogus: 1\n"

// workflowSHA256 returns the lowercase hex sha256 of text.
func workflowSHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// wantNoEpicStored checks that no epic was stored under epicServiceID.
func wantNoEpicStored(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.Epic(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoEpic) {
		t.Fatalf("store Epic after a refusal = %v, want ErrNoEpic", err)
	}
}

func TestEpicCreatePinsTheBuiltInEpicWhenNoWorkflowIsNamed(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	req := fullEpicRequest(stepGitTestRepo(t), epicServiceID)

	stored := createEpicOK(t, client, req)

	want := &v1.EpicWorkflow{Name: "epic", Text: builtInEpicText, Sha256: builtInEpicSHA256}
	if !proto.Equal(stored.GetWorkflow(), want) {
		t.Fatalf("created workflow = %v, want %v", stored.GetWorkflow(), want)
	}
	if got := getEpicOK(t, client).GetWorkflow(); !proto.Equal(got, want) {
		t.Fatalf("GetEpic workflow = %v, want %v", got, want)
	}
}

func TestEpicCreatePinsTheWorkflowCommittedAtBaseAndKeepsItAfterAnEdit(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stepGitTestCommit(t, repo, map[string]string{".orrery/workflows/demo.yaml": demoWorkflowFile})
	req := fullEpicRequest(repo, epicServiceID)
	req.Workflow = "demo"

	stored := createEpicOK(t, client, req)

	want := &v1.EpicWorkflow{Name: "demo", Text: demoWorkflowText, Sha256: workflowSHA256(demoWorkflowText)}
	if !proto.Equal(stored.GetWorkflow(), want) {
		t.Fatalf("created workflow = %v, want %v", stored.GetWorkflow(), want)
	}

	stepGitTestCommit(t, repo, map[string]string{".orrery/workflows/demo.yaml": editedDemoWorkflowFile})
	if got := getEpicOK(t, client).GetWorkflow(); !proto.Equal(got, want) {
		t.Fatalf("GetEpic after the file was edited and committed = %v, want %v", got, want)
	}
}

func TestEpicCreateRefusesABrokenWorkflowAndStoresNothing(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stepGitTestCommit(t, repo, map[string]string{".orrery/workflows/broken.yaml": brokenWorkflowFile})
	base := epicTestBase(repo)
	req := fullEpicRequest(repo, epicServiceID)
	req.Workflow = "broken"

	_, err := client.CreateEpic(t.Context(), connect.NewRequest(req))
	wantConnectError(t, err, connect.CodeInvalidArgument,
		"workflow: at base "+base+": .orrery/workflows/broken.yaml:8: node build: key bogus: unknown key")
	wantNoEpicStored(t, st)
}

func TestEpicCreateRefusesAnUnknownWorkflowAndStoresNothing(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	base := epicTestBase(repo)
	req := fullEpicRequest(repo, epicServiceID)
	req.Workflow = "missing"

	_, err := client.CreateEpic(t.Context(), connect.NewRequest(req))
	wantConnectError(t, err, connect.CodeInvalidArgument,
		"workflow: at base "+base+": workflow \"missing\": "+
			"no file .orrery/workflows/missing.yaml and no built-in \"missing\"")
	wantNoEpicStored(t, st)
}
