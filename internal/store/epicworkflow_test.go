package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"reflect"
	"testing"
)

// builtInEpicText is the canonical text of the built-in workflow `epic`, the workflow every epic stored before
// migration 00022 pins.
const builtInEpicText = "name: epic\nnodes:\n  - id: plan\n    kind: plan\n    body:\n      - id: build\n        kind: build\n"

// textSHA256 returns the lowercase hex sha256 of text.
func textSHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// builtInEpicWorkflow returns the workflow every epic stored before migration 00022 pins: the built-in epic, its text,
// the sha256 of that text, and its two nodes.
func builtInEpicWorkflow() EpicWorkflow {
	return EpicWorkflow{
		Name:   "epic",
		Text:   builtInEpicText,
		SHA256: textSHA256(builtInEpicText),
		Nodes: []EpicWorkflowNode{
			{ID: "plan", Parent: "", Kind: "plan"},
			{ID: "build", Parent: "plan", Kind: "build"},
		},
	}
}

func TestEpicStoredAtVersion21PinsBuiltInEpicWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")

	fixture := openStoreAtVersion(t, path, 21)
	seedEpic(t, fixture)
	if err := fixture.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	s, err := Open(ctx, path, noSync())
	if err != nil {
		t.Fatalf("Open at version 21: %v", err)
	}
	defer s.Close()

	want := builtInEpicWorkflow()
	got, err := s.Epic(ctx, seededEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !reflect.DeepEqual(got.Workflow, want) {
		t.Errorf("Epic workflow stored at version 21 = %+v, want %+v", got.Workflow, want)
	}

	epics, err := s.Epics(ctx)
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	if len(epics) != 1 || !reflect.DeepEqual(epics[0].Workflow, want) {
		t.Errorf("Epics workflow stored at version 21 = %+v, want one epic with %+v", epics, want)
	}
}

func TestCreatedEpicReadsBackWorkflowNodesInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"

	row := fullEpic(id)
	text := "name: retry-review\nnodes:\n  - id: plan\n    kind: plan\n    body:\n      - id: build\n        kind: build\n" +
		"  - id: retry\n    loop:\n      max: 3\n      until: polish.complete\n      spent: park\n    body:\n" +
		"      - id: polish\n        kind: plan\n        body:\n          - id: polish-build\n            kind: build\n"
	row.Workflow = EpicWorkflow{
		Name:   "retry-review",
		Text:   text,
		SHA256: textSHA256(text),
		Nodes: []EpicWorkflowNode{
			{ID: "plan", Parent: "", Kind: "plan"},
			{ID: "build", Parent: "plan", Kind: "build"},
			{ID: "retry", Parent: "", Kind: "loop"},
			{ID: "polish", Parent: "retry", Kind: "plan"},
			{ID: "polish-build", Parent: "polish", Kind: "build"},
		},
	}
	createEpic(t, s, row)

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := fullEpic(id)
	want.Decisions = seededDecisions()
	want.Workflow = row.Workflow
	want.Revision = createdRevision
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Epic round trip with a workflow differs:\n got %+v\nwant %+v", got, want)
	}

	epics, err := s.Epics(ctx)
	if err != nil {
		t.Fatalf("Epics: %v", err)
	}
	if wantHead := headOnly(row); len(epics) != 1 || !reflect.DeepEqual(epics[0], wantHead) {
		t.Errorf("Epics = %+v, want %+v", epics, wantHead)
	}
}

func TestCreatedEpicWithoutWorkflowReadsBackNoWorkflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	got, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !reflect.DeepEqual(got.Workflow, EpicWorkflow{}) {
		t.Errorf("workflow of an epic created without one = %+v, want none", got.Workflow)
	}
}
