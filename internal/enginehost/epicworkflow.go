package enginehost

import (
	"context"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// defaultWorkflowName is the workflow an epic pins when it names none.
const defaultWorkflowName = "epic"

// gitModeFile and gitModeExecutable are the modes git records for a regular file. A symlink is a blob too, under mode
// 120000, so the mode is what tells them apart.
const (
	gitModeFile       = "100644"
	gitModeExecutable = "100755"
)

// workflowNodeLoop is the kind a loop node reports in the store.
const workflowNodeLoop = "loop"

// pinWorkflow loads the workflow name as the base commit of repository holds it, and returns it in the store form. An
// empty name pins the built-in epic. A workflow file the base commits under .orrery/workflows replaces the built-in.
// The working tree is never read, so neither an uncommitted edit nor a later commit can change what an epic pins.
func pinWorkflow(ctx context.Context, repository, base, name string) (store.EpicWorkflow, error) {
	if name == "" {
		name = defaultWorkflowName
	}
	commit, err := stepGitResolve(ctx, repository, base)
	if err != nil {
		return store.EpicWorkflow{}, fmt.Errorf("resolve base: %w", err)
	}
	pinned, err := workflow.Load(name, baseReader(ctx, repository, commit))
	if err != nil {
		return store.EpicWorkflow{}, fmt.Errorf("at base %s: %w", commit, err)
	}
	return epicWorkflowOf(pinned), nil
}

// baseReader returns the workflow.Reader that reads paths at commit. A path the commit does not hold is not found. A
// path it holds as anything but a regular file is refused, and a regular file's exact bytes are read from the commit.
func baseReader(ctx context.Context, repository, commit string) workflow.Reader {
	return func(path string) ([]byte, bool, error) {
		entry, err := stepGit(ctx, repository, "ls-tree", "--full-tree", commit, "--", path)
		if err != nil {
			return nil, false, err
		}
		if entry == "" {
			return nil, false, nil
		}
		meta, _, _ := strings.Cut(entry, "\t")
		mode, _, _ := strings.Cut(meta, " ")
		if mode != gitModeFile && mode != gitModeExecutable {
			return nil, false, fmt.Errorf("not a regular file (mode %s)", mode)
		}
		// The object is commit:path, which suppliedBlob reads as the exact bytes of a blob.
		text, err := suppliedBlob(ctx, repository, commit+":"+path)
		if err != nil {
			return nil, false, err
		}
		return text, true, nil
	}
}

// epicWorkflowOf returns pinned in the store form: its name, text and sha256, and its nodes in preorder.
func epicWorkflowOf(pinned workflow.Pinned) store.EpicWorkflow {
	return store.EpicWorkflow{
		Name:   pinned.Name,
		Text:   pinned.Text,
		SHA256: pinned.SHA256,
		Nodes:  workflowNodeRows(nil, pinned.Workflow.Nodes, ""),
	}
}

// workflowNodeRows appends the rows of nodes to out, each node before the nodes it holds. parent is the id of the node
// that holds nodes, "" at the top level.
func workflowNodeRows(out []store.EpicWorkflowNode, nodes []workflow.Node, parent string) []store.EpicWorkflowNode {
	for _, node := range nodes {
		kind := node.Kind
		if node.Loop != nil {
			kind = workflowNodeLoop
		}
		out = append(out, store.EpicWorkflowNode{ID: node.ID, Parent: parent, Kind: kind})
		out = workflowNodeRows(out, node.Body, node.ID)
	}
	return out
}
