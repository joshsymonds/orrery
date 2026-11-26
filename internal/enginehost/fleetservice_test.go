package enginehost

import (
	"context"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/deskstream"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// fleetRouter is a Router that keeps the seen reports it receives. The Fleet handler calls Seen on the server's
// goroutine, so the reports are kept under a lock.
type fleetRouter struct {
	fakeRouter
	mu sync.Mutex
}

func (r *fleetRouter) Seen(ctx context.Context, key notice.Key, seen int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fakeRouter.Seen(ctx, key, seen)
}

// reports returns the seen reports the router has received.
func (r *fleetRouter) reports() []seenReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]seenReport(nil), r.fakeRouter.seens...)
}

// fleetClient serves the Fleet service over httptest with fleet, and returns the client that dials it. The server
// closes at cleanup.
func fleetClient(t *testing.T, fleet *Fleet) enginev1connect.FleetClient {
	t.Helper()
	srv := httptest.NewServer(NewHandler(ServeConfig{Fleet: fleet}))
	t.Cleanup(srv.Close)
	return enginev1connect.NewFleetClient(srv.Client(), srv.URL)
}

// watchDesk opens a WatchDesk stream on client. The stream ends with the test's context.
func watchDesk(
	t *testing.T,
	client enginev1connect.FleetClient,
) *connect.ServerStreamForClient[v1.FleetWatchDeskResponse] {
	t.Helper()
	stream, err := client.WatchDesk(t.Context(), connect.NewRequest(&v1.FleetWatchDeskRequest{}))
	if err != nil {
		t.Fatalf("WatchDesk: %v", err)
	}
	return stream
}

// assertFleetCode fails the test unless err carries the connect code want.
func assertFleetCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if got := connect.CodeOf(err); got != want {
		t.Errorf("code = %v, want %v (err %v)", got, want, err)
	}
}

func TestFleetServiceReportSeen(t *testing.T) {
	t.Parallel()
	t.Run("seen report keyed with caller host b", func(t *testing.T) {
		t.Parallel()
		router := &fleetRouter{}
		client := fleetClient(t, &Fleet{Router: router})
		req := connect.NewRequest(&v1.FleetReportSeenRequest{Pane: "%3", Seen: 1700000000})
		req.Header().Set(protocol.HeaderHost, "b")
		if _, err := client.ReportSeen(t.Context(), req); err != nil {
			t.Fatalf("ReportSeen: %v", err)
		}
		want := []seenReport{{key: notice.Key{Pane: "%3", Host: "b"}, seen: 1700000000}}
		if got := router.reports(); !reflect.DeepEqual(got, want) {
			t.Errorf("reports = %+v, want %+v", got, want)
		}
	})
	t.Run("empty caller host refused as invalid argument", func(t *testing.T) {
		t.Parallel()
		router := &fleetRouter{}
		client := fleetClient(t, &Fleet{Router: router})
		req := connect.NewRequest(&v1.FleetReportSeenRequest{Pane: "%3", Seen: 1700000000})
		_, err := client.ReportSeen(t.Context(), req)
		assertFleetCode(t, err, connect.CodeInvalidArgument)
		if got := router.reports(); len(got) != 0 {
			t.Errorf("reports = %+v, want none", got)
		}
	})
	t.Run("no router unavailable", func(t *testing.T) {
		t.Parallel()
		client := fleetClient(t, &Fleet{Desk: deskstream.NewHub("c", nil)})
		req := connect.NewRequest(&v1.FleetReportSeenRequest{Pane: "%3", Seen: 1700000000})
		req.Header().Set(protocol.HeaderHost, "b")
		_, err := client.ReportSeen(t.Context(), req)
		assertFleetCode(t, err, connect.CodeUnavailable)
	})
	t.Run("no fleet unavailable", func(t *testing.T) {
		t.Parallel()
		client := fleetClient(t, nil)
		req := connect.NewRequest(&v1.FleetReportSeenRequest{Pane: "%3", Seen: 1700000000})
		req.Header().Set(protocol.HeaderHost, "b")
		_, err := client.ReportSeen(t.Context(), req)
		assertFleetCode(t, err, connect.CodeUnavailable)
	})
}

func TestFleetServiceWatchDesk(t *testing.T) {
	t.Parallel()
	t.Run("hello line then a posted notice line", func(t *testing.T) {
		t.Parallel()
		desk := deskstream.NewHub("c", nil)
		stream := watchDesk(t, fleetClient(t, &Fleet{Desk: desk}))
		if !stream.Receive() {
			t.Fatalf("no hello line: %v", stream.Err())
		}
		wantHello := &v1.FleetWatchDeskResponse{Kind: deskstream.KindHello, Host: "c"}
		if !proto.Equal(stream.Msg(), wantHello) {
			t.Errorf("hello line = %v, want %v", stream.Msg(), wantHello)
		}
		desk.Post(deskstream.Notice{State: "waiting", Title: "orrery · s:1", Body: "Which branch?", Quiet: true})
		if !stream.Receive() {
			t.Fatalf("no notice line: %v", stream.Err())
		}
		wantNotice := &v1.FleetWatchDeskResponse{
			Kind:  deskstream.KindNotice,
			Host:  "c",
			State: "waiting",
			Title: "orrery · s:1",
			Body:  "Which branch?",
			Quiet: true,
		}
		if !proto.Equal(stream.Msg(), wantNotice) {
			t.Errorf("notice line = %v, want %v", stream.Msg(), wantNotice)
		}
	})
	t.Run("closed hub ends an open watch cleanly", func(t *testing.T) {
		t.Parallel()
		desk := deskstream.NewHub("c", nil)
		stream := watchDesk(t, fleetClient(t, &Fleet{Desk: desk}))
		if !stream.Receive() {
			t.Fatalf("no hello line: %v", stream.Err())
		}
		desk.Close()
		if stream.Receive() {
			t.Errorf("line after close = %v, want none", stream.Msg())
		}
		if err := stream.Err(); err != nil {
			t.Errorf("Err = %v, want a clean end", err)
		}
	})
	t.Run("closed hub ends a watch opened after it", func(t *testing.T) {
		t.Parallel()
		desk := deskstream.NewHub("c", nil)
		desk.Close()
		stream := watchDesk(t, fleetClient(t, &Fleet{Desk: desk}))
		if stream.Receive() {
			t.Errorf("line from a closed hub = %v, want none", stream.Msg())
		}
		if err := stream.Err(); err != nil {
			t.Errorf("Err = %v, want a clean end", err)
		}
	})
	t.Run("no hub unavailable", func(t *testing.T) {
		t.Parallel()
		stream := watchDesk(t, fleetClient(t, &Fleet{Router: &fleetRouter{}}))
		if stream.Receive() {
			t.Errorf("line with no hub = %v, want none", stream.Msg())
		}
		assertFleetCode(t, stream.Err(), connect.CodeUnavailable)
	})
	t.Run("no fleet unavailable", func(t *testing.T) {
		t.Parallel()
		stream := watchDesk(t, fleetClient(t, nil))
		if stream.Receive() {
			t.Errorf("line with no fleet = %v, want none", stream.Msg())
		}
		assertFleetCode(t, stream.Err(), connect.CodeUnavailable)
	})
}
