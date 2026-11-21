package enginehost

import (
	"reflect"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestPromptClientPaneSendRecordStoresPaneTextAndClient checks that Record stores the pane, text and client of a send,
// at the fake clock's time.
func TestPromptClientPaneSendRecordStoresPaneTextAndClient(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	svc := paneSendService{sends: s, now: fixedClock}

	_, err := svc.Record(t.Context(), connect.NewRequest(&v1.PaneSendsRecordRequest{
		Pane: "%7", Text: "hello", Client: "orrery-android",
	}))
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	at := fixedClock().Unix()
	got, err := store.NewReader(path).PaneSends(t.Context(), "%7", at, at)
	if err != nil {
		t.Fatalf("PaneSends: %v", err)
	}
	want := []store.PaneSend{{Text: "hello", Client: "orrery-android", At: at}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stored sends = %+v, want %+v", got, want)
	}
}

// TestPromptClientPaneSendRefusesMalformedSends checks that Record refuses a pane that is not a % then digits, an empty
// text and an empty client as InvalidArgument, and stores nothing for any of them.
func TestPromptClientPaneSendRefusesMalformedSends(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, pane, text, client string
	}{
		{name: "pane without a percent sign", pane: "7", text: "hello", client: "orrery-android"},
		{name: "pane with no digits", pane: "%", text: "hello", client: "orrery-android"},
		{name: "pane with a non-digit", pane: "%7x", text: "hello", client: "orrery-android"},
		{name: "empty text", pane: "%7", text: "", client: "orrery-android"},
		{name: "empty client", pane: "%7", text: "hello", client: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, path := openStore(t)
			svc := paneSendService{sends: s, now: fixedClock}

			_, err := svc.Record(t.Context(), connect.NewRequest(&v1.PaneSendsRecordRequest{
				Pane: c.pane, Text: c.text, Client: c.client,
			}))
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Fatalf("Record code = %v, want %v (err %v)", got, connect.CodeInvalidArgument, err)
			}
			got, err := store.NewReader(path).PaneSends(t.Context(), c.pane, 0, fixedClock().Unix()+1)
			if err != nil {
				t.Fatalf("PaneSends: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("refused send stored %+v, want nothing", got)
			}
		})
	}
}

// TestPromptClientPaneSendWithoutStoreIsUnavailable checks that Record with no store refuses as Unavailable.
func TestPromptClientPaneSendWithoutStoreIsUnavailable(t *testing.T) {
	t.Parallel()
	svc := paneSendService{now: fixedClock}

	_, err := svc.Record(t.Context(), connect.NewRequest(&v1.PaneSendsRecordRequest{
		Pane: "%7", Text: "hello", Client: "orrery-android",
	}))
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Fatalf("Record code = %v, want %v (err %v)", got, connect.CodeUnavailable, err)
	}
}
