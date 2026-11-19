package enginehost

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestStepEndPushSubscribeStandalone covers a subscription NewStepSubscription returns and Offer feeds: it reports an offered
// end once on Ready and Take, ignores the rows of other ids and the repeats of a report, and its Close is safe.
func TestStepEndPushSubscribeStandalone(t *testing.T) {
	t.Parallel()
	sub := NewStepSubscription([]string{stepPushA})
	sub.Offer(store.StepRow{ID: stepPushB, End: "done"})
	select {
	case <-sub.Ready():
		t.Fatal("Ready is signaled for a row of another id")
	default:
	}

	sub.Offer(store.StepRow{ID: stepPushA, End: "done"})
	select {
	case <-sub.Ready():
	default:
		t.Fatal("Ready is not signaled for the offered end")
	}
	rows := sub.Take()
	if len(rows) != 1 || rows[0].ID != stepPushA || rows[0].End != "done" {
		t.Fatalf("Take = %+v, want the done row of %s once", rows, stepPushA)
	}

	sub.Offer(store.StepRow{ID: stepPushA, End: "done"})
	select {
	case <-sub.Ready():
		t.Fatal("Ready is signaled again for a repeat of a reported end")
	default:
	}
	if again := sub.Take(); len(again) != 0 {
		t.Fatalf("Take after a repeat = %d rows, want none", len(again))
	}

	sub.Close()
	sub.Close()
}
