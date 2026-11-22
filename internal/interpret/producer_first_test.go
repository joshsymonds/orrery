package interpret

import (
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// producerFirstBatch is the path of the integrate run that batches tasks 3 and 5 of the producer-first plan: the settle
// walk of task 5, the highest done task.
const producerFirstBatch = "plan/S5/integrate"

// producerFirstPlan returns a planner return of five tasks, each on its own file, in which task 3 depends on task 5.
func producerFirstPlan() store.EpicPlan {
	consumer := runTask("third", "c.go")
	consumer.DependsOn = []string{"fifth"}
	return store.EpicPlan{
		Decisions: []string{},
		Tasks: []store.EpicTaskInput{
			runTask("first", "a.go"), runTask("second", "b.go"), consumer, runTask("fourth", "d.go"),
			runTask("fifth", "e.go"),
		},
		Outline: "five tasks", Outcome: "complete",
	}
}

// producerFirstMerges returns the subjects of the first-parent merge commits of the candidate that compose adds to base,
// oldest first.
func producerFirstMerges(w *integrateWorld, base, candidate string) []string {
	w.t.Helper()
	return strings.Split(w.git("log", "--first-parent", "--reverse", "--format=%s", base+".."+candidate), "\n")
}

// TestProducerFirstComposesProducerBeforeConsumer: task 3 depends on task 5, and both are accepted in one batch. The batch
// composes task 5 first, so the candidate's first-parent merges read Merge T5 and then Merge T3.
func TestProducerFirstComposesProducerBeforeConsumer(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	completeTurn(t, w.s, producerFirstPlan())
	w.pass()
	consumer := w.commit(w.base, "c.go", "consumer\n")
	producer := w.commit(w.base, "e.go", "producer\n")
	w.accept(3, w.base, consumer)
	w.accept(5, w.base, producer)
	w.pass()
	candidate := w.candidate(producerFirstBatch)
	want := []string{"Merge T5 " + producer, "Merge T3 " + consumer}
	if got := producerFirstMerges(w, w.base, candidate); !reflect.DeepEqual(got, want) {
		t.Errorf("merges = %q, want %q", got, want)
	}
}

// producerFirstTask returns the stored task seq, which depends on the tasks deps.
func producerFirstTask(seq int64, deps ...int64) store.EpicTask {
	return store.EpicTask{Seq: seq, DependsOn: deps}
}

// TestProducerFirstOrder: composeOrder orders a batch by the depends_on edges among its tasks, a chain of them through
// another batch task included, and otherwise by task number. An edge to a task outside the batch orders nothing, and
// tasks that depend on one another in a cycle have no order.
func TestProducerFirstOrder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		tasks []store.EpicTask
		seqs  []int64
		want  []int64
		cycle bool
	}{
		{
			name:  "chain through a batch task",
			tasks: []store.EpicTask{producerFirstTask(1, 3), producerFirstTask(2), producerFirstTask(3, 2)},
			seqs:  []int64{1, 2, 3},
			want:  []int64{2, 3, 1},
		},
		{
			name: "dependency outside the batch orders nothing",
			tasks: []store.EpicTask{
				producerFirstTask(1, 4), producerFirstTask(2), producerFirstTask(4, 2),
			},
			seqs: []int64{1, 2},
			want: []int64{1, 2},
		},
		{
			name:  "cycle has no order",
			tasks: []store.EpicTask{producerFirstTask(1, 2), producerFirstTask(2, 1)},
			seqs:  []int64{1, 2},
			cycle: true,
		},
		{
			name:  "task that depends on itself has no order",
			tasks: []store.EpicTask{producerFirstTask(1, 1)},
			seqs:  []int64{1},
			cycle: true,
		},
	}
	for _, tc := range cases {
		got, err := composeOrder(tc.tasks, tc.seqs)
		if tc.cycle {
			if err == nil {
				t.Errorf("%s: order = %v, want a refusal", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: composeOrder: %v", tc.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: order = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestProducerFirstIndependentInTaskOrder: tasks 1, 2 and 4 depend on nothing in their batch, so the batch composes them
// in task-number order, whatever order they were accepted in.
func TestProducerFirstIndependentInTaskOrder(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	completeTurn(t, w.s, producerFirstPlan())
	w.pass()
	fourth := w.commit(w.base, "d.go", "four\n")
	second := w.commit(w.base, "b.go", "two\n")
	first := w.commit(w.base, "a.go", "one\n")
	w.accept(4, w.base, fourth)
	w.accept(2, w.base, second)
	w.accept(1, w.base, first)
	w.pass()
	candidate := w.candidate("plan/S4/integrate")
	want := []string{"Merge T1 " + first, "Merge T2 " + second, "Merge T4 " + fourth}
	if got := producerFirstMerges(w, w.base, candidate); !reflect.DeepEqual(got, want) {
		t.Errorf("merges = %q, want %q", got, want)
	}
}
