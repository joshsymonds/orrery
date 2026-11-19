package interpret

import (
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// orphaned returns the epic's orphaned runs, in the order of the rows: each running run whose nearest enclosing run
// (enclosing) has ended. A plan or loop ends the runs under it as it ends (endsUnder), so a run is orphaned only when it
// turns running again after that, as an owner's retry of a failed or parked integrate run under an ended plan does. The
// walk has passed the run that ended, so nothing else decides an orphaned run. It reads each run's Path and State alone.
func (p pass) orphaned() []store.NodeRun {
	var runs []store.NodeRun
	for _, run := range p.rows.Runs {
		if run.State != store.NodeRunning {
			continue
		}
		if parent, ok := p.enclosing(run.Path); ok && parent.State == store.NodeEnded {
			runs = append(runs, run)
		}
	}
	return runs
}

// enclosing returns the nearest run that the run at path sits under (within), and whether there is one: the run at the
// longest prefix of path that ends before a "/" or an "@".
func (p pass) enclosing(path string) (store.NodeRun, bool) {
	for i := len(path) - 1; i > 0; i-- {
		if path[i] != '/' && path[i] != '@' {
			continue
		}
		if run, ok := p.byPath[path[:i]]; ok {
			return run, true
		}
	}
	return store.NodeRun{}, false
}

// orphans decides each orphaned run, and reports whether the epic has one. While one runs, the walk waits, so nothing
// else moves the epic's branch beside it: an orphaned integrate run is driven to its end, its advance included, as its
// plan would have driven it.
func (p pass) orphans() ([]Action, bool) {
	runs := p.orphaned()
	acts := make([]Action, 0, len(runs))
	for _, run := range runs {
		acts = append(acts, p.orphan(run)...)
	}
	return acts, len(runs) > 0
}

// orphan decides the orphaned run as the walk of its enclosing run would have. A sync run is decided by the sync kind
// (decideSync). A run of a check or integrate node of the workflow whose kind is registered is decided by that kind as
// a node of its enclosing run's body (running): a plan body's, where a failed end wakes the planner, or a loop body's
// when the enclosing run is a loop. An integrate run's end is its batch, which wakes the planner. Any other run, a plan,
// loop or build run, or one whose node the workflow does not hold or whose kind is not registered, has nothing that
// decides it, so it ends with no outcome, with the runs under it.
func (p pass) orphan(run store.NodeRun) []Action {
	if kind, ok := p.reg[kindSync]; ok && run.Kind == kindSync {
		return p.decideSync(kind, run)
	}
	def, found := nodeByID(p.flow.Nodes, run.Node)
	_, registered := p.reg[def.Kind]
	if !found || !registered || def.Loop != nil || def.Kind != run.Kind || def.Kind == kindPlan ||
		def.Kind == kindBuild {
		return []Action{{Kind: ActEnd, Ends: p.endsUnder(run.Path, "", "")}}
	}
	at := scope{round: run.Round, where: planBody}
	if parent, _ := p.enclosing(run.Path); parent.Kind == kindLoop {
		at.where = loopBody
	}
	return p.running(def, run, at)
}

// nodeByID returns the node of nodes, or of their bodies, whose id is id, and whether there is one. Every id is unique
// across a checked workflow (workflow.Check).
func nodeByID(nodes []workflow.Node, id string) (workflow.Node, bool) {
	for _, node := range nodes {
		if node.ID == id {
			return node, true
		}
		if inner, ok := nodeByID(node.Body, id); ok {
			return inner, true
		}
	}
	return workflow.Node{}, false
}
