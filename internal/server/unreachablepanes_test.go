package server_test

import (
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/hoststatus"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// unreachableCalls is how many calls of a controller wait on host b's bound at once: Panes.List and Host.Status.
const unreachableCalls = 2

// unreachableRows are the rows the store holds for host b, which an unreachable b lists its panes from. Their tmux pane
// ids differ from the controller's, so a listed pane of b is never mistaken for one of the controller's.
func unreachableRows() []store.Pane {
	return []store.Pane{
		{ID: "b-1", Pane: "%7", Fields: store.Fields{
			State: "waiting", Changed: "1700000100", Seen: "1700000200", Question: "b question", Summary: "b summary",
			Label: "b label", Context: "b context", Tool: "b tool", ClaudeSession: "b-claude-session",
		}},
		{ID: "b-2", Pane: "%8", Fields: store.Fields{State: "idle"}},
	}
}

// unreachablePanes returns the Panes that unreachableRows list as: each row's tmux pane id, orrery id and mirrored
// options, with unreachable set and the other fields zero.
func unreachablePanes() []*orreryv1.Pane {
	return []*orreryv1.Pane{
		{
			Id: "%7", Host: routingHost, OrreryId: "b-1", State: "waiting", Summary: "b summary", Label: "b label",
			Changed: 1700000100, Seen: 1700000200, Question: "b question", Context: "b context", Tool: "b tool",
			ClaudeSession: "b-claude-session", Unreachable: true,
		},
		{Id: "%8", Host: routingHost, OrreryId: "b-2", State: "idle", Unreachable: true},
	}
}

// seedUnreachableStore writes a store at a temporary path with unreachableRows under host b, and returns the path.
func seedUnreachableStore(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err = s.SyncServer(ctx, routingHost, "-L b", unreachableRows(), time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}
	return path
}

// startHungAddress listens on a loopback address, accepts each connection to it and holds it open without answering,
// until the test ends. It returns the address.
func startHungAddress(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	held := make(chan []net.Conn, 1)
	go func() {
		var conns []net.Conn
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				held <- conns
				return
			}
			conns = append(conns, conn)
		}
	}()
	t.Cleanup(func() {
		if closeErr := ln.Close(); closeErr != nil {
			t.Errorf("close the listener: %v", closeErr)
		}
		for _, conn := range <-held {
			if closeErr := conn.Close(); closeErr != nil {
				t.Errorf("close a held connection: %v", closeErr)
			}
		}
	})
	return ln.Addr().String()
}

// unreachableHandler serves a controller over the tmux server tmux, whose hosts are clients and whose store is at
// storePath, and returns its handler.
func unreachableHandler(t *testing.T, tmux *tmuxctl.Client, storePath string, clients []*node.Client) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:     []string{allowedLogin},
		HostName:   routingController,
		Tmux:       tmux,
		Push:       newPushStore(t),
		HostStatus: hoststatus.New(writeHostStatusScript(t, "[]", 0), statusNow),
		LocalLoad: func() (node.Load, error) {
			return node.Load{LoadPercent: statusLocalLoad, MemoryPercent: statusLocalMemory}, nil
		},
		Sessions: store.NewReader(storePath),
		Hosts:    clients,
		NewTimer: neverTimer,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// listResult is the answer of one Panes.List call: its panes, or the error it answered with.
type listResult struct {
	panes []*orreryv1.Pane
	err   error
}

// startList calls Panes.List through handler on a goroutine, and returns the channel its answer arrives on. The call
// runs apart from the test, so the test can move the clock while the call waits on it.
func startList(t *testing.T, handler http.Handler) <-chan listResult {
	t.Helper()
	panes := panesClient(t, handler)
	done := make(chan listResult, 1)
	go func() {
		got, err := panes.List(t.Context(), connect.NewRequest(&orreryv1.PanesListRequest{}))
		if err != nil {
			done <- listResult{err: err}
			return
		}
		done <- listResult{panes: got.Msg.GetPanes()}
	}()
	return done
}

// startSendText calls Panes.SendText of routingPane on routingHost through handler on a goroutine, and returns the
// channel its error arrives on.
func startSendText(t *testing.T, handler http.Handler) <-chan error {
	t.Helper()
	panes := panesClient(t, handler)
	done := make(chan error, 1)
	go func() {
		_, err := panes.SendText(t.Context(), connect.NewRequest(&orreryv1.SendTextRequest{
			Pane: routingPane, Text: routedText, Host: routingHost,
		}))
		done <- err
	}()
	return done
}

// awaitBound waits until the clock has armed a timer of the client bound, so a call waiting on the clock is waiting on
// its bound. A timer of another duration, such as a refused dial's retry, is read past.
func awaitBound(clock *statusClock) {
	for d := range clock.armed {
		if d == node.DefaultBound {
			return
		}
	}
}

// requireListedPanes requires listed to hold the controller's panes, each naming the controller and reachable, with
// aPane among them, and then exactly wantRemote, in order, as the panes of the other hosts.
func requireListedPanes(t *testing.T, listed []*orreryv1.Pane, aPane string, wantRemote []*orreryv1.Pane) {
	t.Helper()
	own := make([]*orreryv1.Pane, 0, len(listed))
	remote := make([]*orreryv1.Pane, 0, len(listed))
	for _, pane := range listed {
		if pane.GetHost() == routingController {
			own = append(own, pane)
		} else {
			remote = append(remote, pane)
		}
	}
	if !slices.ContainsFunc(own, func(pane *orreryv1.Pane) bool { return pane.GetId() == aPane }) {
		t.Fatalf("Panes.List lists no pane %s of %s among %v", aPane, routingController, own)
	}
	for _, pane := range own {
		if pane.GetUnreachable() {
			t.Fatalf("Panes.List marks %v of %s unreachable", pane, routingController)
		}
	}
	if len(remote) != len(wantRemote) {
		t.Fatalf("Panes.List lists the panes of other hosts %v, want %v", remote, wantRemote)
	}
	for index := range wantRemote {
		if !proto.Equal(wantRemote[index], remote[index]) {
			t.Errorf("pane %d of the other hosts = %v, want %v", index, remote[index], wantRemote[index])
		}
	}
}

// requireUnreachableAnswers runs a controller whose host b is at addr and whose store is at storePath. Both Panes.List
// and Host.Status wait on b's bound, and neither may answer before the clock passes it. Once it does, they answer for
// the controller and the other hosts, b's panes list as wantRemote says, and a SendText for a b pane fails unavailable.
func requireUnreachableAnswers(t *testing.T, addr, storePath string, wantRemote []*orreryv1.Pane) {
	t.Helper()
	tmux := newTmuxServer(t)
	aPane, _ := startRoutingPane(t, tmux, routingController)
	file := loadHostsFile(t, routingController, []hosts.Host{{Name: routingHost, Address: addr}})
	clock := newStatusClock()
	handler := unreachableHandler(t, tmux, storePath, statusClients(file, func(string) holder.Clock { return clock }))

	listed := startList(t, handler)
	status := startStatus(t, handler)
	bounds := 0
	for bounds < unreachableCalls {
		select {
		case d := <-clock.armed:
			if d == node.DefaultBound {
				bounds++
			}
		case early := <-listed:
			t.Fatalf("Panes.List answered before the clock passed the bound: %v, %v", early.panes, early.err)
		case early := <-status:
			t.Fatalf("Host.Status answered before the clock passed the bound: %v, %v", early.entries, early.err)
		}
	}
	select {
	case early := <-listed:
		t.Fatalf("Panes.List answered before the clock passed the bound: %v, %v", early.panes, early.err)
	case early := <-status:
		t.Fatalf("Host.Status answered before the clock passed the bound: %v, %v", early.entries, early.err)
	default:
	}
	clock.Advance(node.DefaultBound)

	got := <-listed
	if got.err != nil {
		t.Fatalf("Panes.List: %v", got.err)
	}
	requireListedPanes(t, got.panes, aPane, wantRemote)
	answer := <-status
	if answer.err != nil {
		t.Fatalf("Host.Status: %v", answer.err)
	}
	requireHostEntries(t, []*orreryv1.HostEntry{
		{Name: routingController, LoadPercent: statusLocalLoad, MemoryPercent: statusLocalMemory, Reachable: true},
		{Name: routingHost},
	}, answer.entries)

	sent := startSendText(t, handler)
	awaitBound(clock)
	clock.Advance(node.DefaultBound)
	requireRoutedError(t, "SendText to "+routingPane+" on "+routingHost, <-sent, connect.CodeUnavailable,
		"host unreachable")
}

// TestUnreachablePanesRefused answers over a controller whose host b refuses connections, with b's rows in the store.
// Once the clock passes b's bound, Panes.List and Host.Status answer for the controller and the other hosts, b's panes
// list from its stored rows as unreachable, and a SendText for a b pane fails unavailable.
func TestUnreachablePanesRefused(t *testing.T) {
	t.Parallel()
	requireUnreachableAnswers(t, refusedAddress(t), seedUnreachableStore(t), unreachablePanes())
}

// TestUnreachablePanesHung answers over a controller whose host b accepts connections and never answers, with b's rows
// in the store. The answers are those of TestUnreachablePanesRefused.
func TestUnreachablePanesHung(t *testing.T) {
	t.Parallel()
	requireUnreachableAnswers(t, startHungAddress(t), seedUnreachableStore(t), unreachablePanes())
}

// TestUnreachablePanesUnreadableStore answers over a controller whose host b refuses connections and whose store cannot
// be read. b lists no panes, and the controller's panes and Host.Status still answer.
func TestUnreachablePanesUnreadableStore(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing.db")
	requireUnreachableAnswers(t, refusedAddress(t), missing, nil)
}
