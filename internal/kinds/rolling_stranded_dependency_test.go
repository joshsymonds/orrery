package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// rollingFailedBatch returns the integrate run of rollingBatch, ended failed with result. A failed batch keeps the
// Result it composed.
func rollingFailedBatch(result string) store.NodeRun {
	run := rollingBatchRun(runEnded, result)
	run.Outcome = outcomeFailed
	return run
}

// rollingPassedBatch returns the integrate run of rollingBatch, ended passed with result.
func rollingPassedBatch(result string) store.NodeRun {
	run := rollingBatchRun(runEnded, result)
	run.Outcome = outcomePassed
	return run
}

// TestRollingIntegrateStrandedDependency checks that a dependency a failed batch composed is lost when Rows.Stranded
// holds it, the base a fresh task starts on lacking its work, and is integrated otherwise, whatever the outcome of the
// batch that composed it.
func TestRollingIntegrateStrandedDependency(t *testing.T) {
	t.Parallel()
	building := store.EpicTask{Seq: 2, Status: taskOpen, DependsOn: []int64{1}}
	claimed := []store.EpicTask{{Seq: 1, Status: taskDone, Batch: rollingBatch}, building}
	stranded := rollingRows(claimed, []store.NodeRun{rollingFailedBatch(rollingComposed)})
	stranded.Stranded = []int64{1}
	cases := []decideCase{
		{
			"dependency composed by a failed batch the base does not hold gaps the build",
			rollingNode(2),
			stranded,
			Action{
				Kind:   ActGap,
				Reason: "dependency T1 was composed by a failed batch whose work the base does not hold",
			},
		},
		{
			"dependency composed by a failed batch with nothing stranded runs",
			rollingNode(2),
			rollingRows(claimed, []store.NodeRun{rollingFailedBatch(rollingComposed)}),
			Action{Kind: ActRun},
		},
		{
			"dependency composed by a passed batch runs",
			rollingNode(2),
			rollingRows(claimed, []store.NodeRun{rollingPassedBatch(rollingComposed)}),
			Action{Kind: ActRun},
		},
	}
	checkDecisions(t, Build{}, cases)
}
