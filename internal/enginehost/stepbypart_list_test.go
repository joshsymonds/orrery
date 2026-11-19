package enginehost

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// callerAsk is one CallerListings call with the caller and epic it received.
type callerAsk struct {
	caller store.EpicCaller
	epic   string
}

// callerListSteps is a StepCommands that answers CallerListings with listings and records each call's caller and epic.
// Its other methods are fakeStepCommands', so a list without caller headers is recorded there.
type callerListSteps struct {
	*fakeStepCommands
	mu       sync.Mutex
	listings []store.StepListing
	asked    []callerAsk
}

func (f *callerListSteps) CallerListings(
	_ context.Context, caller store.EpicCaller, epic string,
) ([]store.StepListing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, callerAsk{caller: caller, epic: epic})
	return f.listings, nil
}

func (f *callerListSteps) recordedAsks() []callerAsk {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

// listStepsAs calls ListSteps with msg, with session as the caller header when it is not empty, and returns the reply's
// message or the call's error.
func listStepsAs(
	client enginev1connect.StepsClient,
	msg *v1.ListStepsRequest,
	session string,
) (*v1.ListStepsResponse, error) {
	req := connect.NewRequest(msg)
	if session != "" {
		req.Header().Set(protocol.HeaderSession, session)
	}
	resp, err := client.ListSteps(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func TestStepByPartListCaller(t *testing.T) {
	t.Run("caller lists the session's steps in its headers, narrowed to the epic named", func(t *testing.T) {
		fake := &callerListSteps{
			fakeStepCommands: &fakeStepCommands{},
			listings:         []store.StepListing{fullStepRow().Listing()},
		}
		path, stop := startServerWithSteps(t, nil, fake)
		client := newStepClient(t, path)

		listed, err := listStepsAs(client, &v1.ListStepsRequest{Epic: "orrery", Caller: true}, "session-7")
		if err != nil {
			t.Fatalf("ListSteps: %v", err)
		}
		if err := stop(); err != nil {
			t.Fatalf("Serve: %v", err)
		}

		want := []callerAsk{{caller: store.EpicCaller{Session: "session-7"}, epic: "orrery"}}
		if got := fake.recordedAsks(); !reflect.DeepEqual(got, want) {
			t.Fatalf("ListSteps reached CallerListings as %+v, want %+v", got, want)
		}
		if got := fake.recordedLists(); len(got) != 0 {
			t.Fatalf("ListSteps with caller reached Listings as %+v, want none", got)
		}
		wantSteps := []*v1.Step{listedStep(fullStep())}
		got := listed.GetSteps()
		if len(got) != len(wantSteps) || !proto.Equal(got[0], wantSteps[0]) {
			t.Fatalf("ListSteps steps = %v, want %v", got, wantSteps)
		}
		if ended := listed.GetEnded(); len(ended) != 0 {
			t.Fatalf("ListSteps ended = %v, want none for a caller listing", ended)
		}
	})

	t.Run("caller with running is invalid_argument", func(t *testing.T) {
		fake := &callerListSteps{fakeStepCommands: &fakeStepCommands{}}
		path, _ := startServerWithSteps(t, nil, fake)
		client := newStepClient(t, path)

		_, err := listStepsAs(client, &v1.ListStepsRequest{Caller: true, Running: true}, "session-7")
		requireInvalidArgument(t, err, "caller cannot be combined with running or task")
		if got := fake.recordedAsks(); len(got) != 0 {
			t.Fatalf("ListSteps refused with running reached CallerListings as %+v", got)
		}
	})

	t.Run("caller with task is invalid_argument", func(t *testing.T) {
		fake := &callerListSteps{fakeStepCommands: &fakeStepCommands{}}
		path, _ := startServerWithSteps(t, nil, fake)
		client := newStepClient(t, path)

		_, err := listStepsAs(client, &v1.ListStepsRequest{Caller: true, Task: "h3e1"}, "session-7")
		requireInvalidArgument(t, err, "caller cannot be combined with running or task")
		if got := fake.recordedAsks(); len(got) != 0 {
			t.Fatalf("ListSteps refused with task reached CallerListings as %+v", got)
		}
	})

	t.Run("caller without caller headers is failed_precondition", func(t *testing.T) {
		fake := &callerListSteps{fakeStepCommands: &fakeStepCommands{}}
		path, _ := startServerWithSteps(t, nil, fake)
		client := newStepClient(t, path)

		_, err := listStepsAs(client, &v1.ListStepsRequest{Caller: true, Epic: "orrery"}, "")
		if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
			t.Fatalf("ListSteps code = %v, want %v (error %v)", got, connect.CodeFailedPrecondition, err)
		}
		if got := fake.recordedAsks(); len(got) != 0 {
			t.Fatalf("ListSteps without headers reached CallerListings as %+v", got)
		}
	})

	t.Run("a request without caller still lists through Listings", func(t *testing.T) {
		fake := &callerListSteps{fakeStepCommands: &fakeStepCommands{
			listings: []store.StepListing{fullStepRow().Listing()},
		}}
		path, stop := startServerWithSteps(t, nil, fake)
		client := newStepClient(t, path)

		if _, err := listStepsAs(client, &v1.ListStepsRequest{Epic: "orrery"}, ""); err != nil {
			t.Fatalf("ListSteps: %v", err)
		}
		if err := stop(); err != nil {
			t.Fatalf("Serve: %v", err)
		}

		want := []listCall{{epic: "orrery", listings: true}}
		if got := fake.recordedLists(); !reflect.DeepEqual(got, want) {
			t.Fatalf("ListSteps reached commands as %+v, want %+v", got, want)
		}
		if got := fake.recordedAsks(); len(got) != 0 {
			t.Fatalf("ListSteps without caller reached CallerListings as %+v", got)
		}
	})

	t.Run("a step store without caller listings refuses a caller listing as unimplemented", func(t *testing.T) {
		path, _ := startServerWithSteps(t, nil, &fakeStepCommands{})
		client := newStepClient(t, path)

		_, err := listStepsAs(client, &v1.ListStepsRequest{Caller: true}, "session-7")
		if got := connect.CodeOf(err); got != connect.CodeUnimplemented {
			t.Fatalf("ListSteps code = %v, want %v (error %v)", got, connect.CodeUnimplemented, err)
		}
	})

	t.Run("Steps.CallerListings resolves the session over a real store", func(t *testing.T) {
		st := seededCallerStore(t)
		steps := &Steps{store: st}

		listed, err := steps.CallerListings(t.Context(), store.EpicCaller{Session: "session-7"}, "")
		if err != nil {
			t.Fatalf("CallerListings: %v", err)
		}
		if got, want := listingIDs(listed), []string{"first", "second"}; !slices.Equal(got, want) {
			t.Fatalf("CallerListings of session-7 = %v, want %v", got, want)
		}
	})

	t.Run("Steps.CallerListings returns an empty list for a caller with no sessions row", func(t *testing.T) {
		st := seededCallerStore(t)
		steps := &Steps{store: st}

		none, err := steps.CallerListings(t.Context(), store.EpicCaller{Session: "session-9"}, "")
		if err != nil {
			t.Fatalf("CallerListings of a caller with no row: %v", err)
		}
		if len(none) != 0 {
			t.Fatalf("CallerListings of a caller with no row = %v, want none", listingIDs(none))
		}
	})
}

// seededCallerStore opens a store that holds two sessions and, under session-7, two steps that start at one time: first
// unlabelled, then second under the epic orrery. A step under session-8 is in it too, and so is a step with no caller,
// which a caller with no sessions row would be given if the caller were not checked first.
func seededCallerStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store Close: %v", err)
		}
	})
	session7 := store.EpicCaller{Session: "session-7"}
	ops := []store.EngineOp{
		store.EngineAdd{ID: "session-7", Dir: "/work/a", State: "idle"},
		store.EngineAdd{ID: "session-8", Dir: "/work/b", State: "idle"},
		store.StepAdd{ID: "first", Agent: "implementer", Prompt: "do it", Caller: session7},
		store.StepAdd{ID: "second", Agent: "implementer", Prompt: "do it", Epic: "orrery", Caller: session7},
		store.StepAdd{
			ID: "theirs", Agent: "implementer", Prompt: "do it", Caller: store.EpicCaller{Session: "session-8"},
		},
		store.StepAdd{ID: "orphan", Agent: "implementer", Prompt: "do it"},
	}
	if err := st.ApplyEngine(ctx, ops, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	return st
}

// listingIDs returns the id of each listing in order.
func listingIDs(listings []store.StepListing) []string {
	ids := make([]string, 0, len(listings))
	for _, listing := range listings {
		ids = append(ids, listing.ID)
	}
	return ids
}
