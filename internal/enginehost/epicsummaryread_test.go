package enginehost

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// summaryReadTextSize is the size of the intent and the workflow text each epic of the summary read test holds.
const summaryReadTextSize = 64 << 10

// countingEpicStore is the epic store a summary read test serves. It counts the calls to EpicListings and to Epics, and
// fails the test on a call to Epics: a listing reads no epic's full row.
type countingEpicStore struct {
	*store.Store
	t            *testing.T
	epicsCalls   atomic.Int64
	listingCalls atomic.Int64
}

func (c *countingEpicStore) Epics(ctx context.Context) ([]store.EpicRow, error) {
	c.epicsCalls.Add(1)
	c.t.Error("ListEpics read Epics, want EpicListings")
	return c.Store.Epics(ctx)
}

func (c *countingEpicStore) EpicListings(ctx context.Context) ([]store.EpicRow, error) {
	c.listingCalls.Add(1)
	return c.Store.EpicListings(ctx)
}

func TestEpicSummaryReadMatchesFullRead(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	text := strings.Repeat("x", summaryReadTextSize)
	const epics = 4
	for n := range epics {
		id := fmt.Sprintf("synthetic-repo-0000000/summary-read-%d", n)
		if err := st.CreateEpic(ctx, store.EpicRow{
			ID: id, Repository: "/work/synthetic-repo", Title: "Summary read", Phase: "accepted",
			Approved: "Synthetic approval.", Intent: text, Care: "serious", Approach: "Synthetic approach.",
			Checks:  "just check",
			Created: time.Unix(1700000000+int64(10*n), 0), Changed: time.Unix(1700000060+int64(10*n), 0),
			Workflow: store.EpicWorkflow{
				Name: "summary", Text: text, SHA256: "sum",
				Nodes: []store.EpicWorkflowNode{{ID: "node", Kind: "plan"}},
			},
		}); err != nil {
			t.Fatalf("CreateEpic %s: %v", id, err)
		}
		if n%2 == 0 {
			if err := st.SeeEpic(ctx, id, time.Unix(int64(1700000100+n), 0)); err != nil {
				t.Fatalf("SeeEpic %s: %v", id, err)
			}
		}
	}

	counting := &countingEpicStore{Store: st, t: t}
	client := newEpicsClient(t, startEpicServer(t, counting))
	listed, err := client.ListEpics(ctx, connect.NewRequest(&v1.ListEpicsRequest{}))
	if err != nil {
		t.Fatalf("ListEpics: %v", err)
	}
	got := listed.Msg.GetEpics()
	if len(got) != epics {
		t.Fatalf("ListEpics returned %d epics, want %d", len(got), epics)
	}
	for _, summary := range got {
		full, err := st.Epic(ctx, summary.GetId())
		if err != nil {
			t.Fatalf("Epic %s: %v", summary.GetId(), err)
		}
		want, err := summaryMessage(full)
		if err != nil {
			t.Fatalf("summaryMessage %s: %v", summary.GetId(), err)
		}
		if !proto.Equal(summary, want) {
			t.Errorf("summary of epic %s = %v, want %v", summary.GetId(), summary, want)
		}
	}
	if n := counting.listingCalls.Load(); n != 1 {
		t.Errorf("ListEpics called EpicListings %d times, want 1", n)
	}
	if n := counting.epicsCalls.Load(); n != 0 {
		t.Errorf("ListEpics called Epics %d times, want 0", n)
	}
}
