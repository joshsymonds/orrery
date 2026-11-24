package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"testing"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// fakePane is the pane that fakePanes answers, and fakeSeq is the seq of the delta that its WatchConversation streams.
const (
	fakePane = "%1"
	fakeSeq  = 7
)

// fakePanes is the orrery.v1.Panes service of these tests. Screen answers fakePane and fails every other pane with
// CodeNotFound. WatchConversation streams one delta for fakePane, then ends with CodeResourceExhausted.
type fakePanes struct {
	orreryv1connect.UnimplementedPanesHandler
}

// Screen answers with the screen of fakePane, and fails any other pane with CodeNotFound.
func (fakePanes) Screen(
	_ context.Context, req *connect.Request[orreryv1.ScreenRequest],
) (*connect.Response[orreryv1.ScreenResponse], error) {
	if req.Msg.GetPane() != fakePane {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such pane"))
	}
	return connect.NewResponse(&orreryv1.ScreenResponse{Screen: []byte("screen of " + fakePane)}), nil
}

// WatchConversation streams one delta for fakePane, then ends with CodeResourceExhausted.
func (fakePanes) WatchConversation(
	_ context.Context,
	req *connect.Request[orreryv1.WatchConversationRequest],
	stream *connect.ServerStream[orreryv1.WatchConversationResponse],
) error {
	if req.Msg.GetPane() != fakePane {
		return connect.NewError(connect.CodeNotFound, errors.New("no such pane"))
	}
	if err := stream.Send(&orreryv1.WatchConversationResponse{Transcript: "t1", Seq: fakeSeq}); err != nil {
		return fmt.Errorf("send the delta: %w", err)
	}
	return connect.NewError(connect.CodeResourceExhausted, errors.New("fake stream ends"))
}

// fakeHostPanes is the HostPanes of these tests. Its handler serves fakePanes. Its Watch sends events, then waits for
// its ctx, and signals ended when it returns, so a test that watches gives it a buffered ended channel. Its Load
// answers load.
type fakeHostPanes struct {
	events []PaneEvent
	load   Load
	ended  chan struct{}
}

// Handler serves fakePanes.
func (*fakeHostPanes) Handler() http.Handler {
	_, handler := orreryv1connect.NewPanesHandler(fakePanes{})
	return handler
}

// Watch sends each of the fake's events in order, then waits until ctx ends.
func (p *fakeHostPanes) Watch(ctx context.Context, send func(PaneEvent) error) error {
	defer func() { p.ended <- struct{}{} }()
	for _, event := range p.events {
		if err := send(event); err != nil {
			return fmt.Errorf("send a pane event: %w", err)
		}
	}
	<-ctx.Done()
	return fmt.Errorf("the watch ends: %w", ctx.Err())
}

// Load answers with the fake's load.
func (p *fakeHostPanes) Load(context.Context) (Load, error) {
	return p.load, nil
}

// fakeEvents returns the events that the fake watch sends: a full listing, then a change that moved the pane's state,
// then a change that did not.
func fakeEvents() []PaneEvent {
	pane := PaneSnapshot{
		Server:  "default",
		Pane:    fakePane,
		ID:      "p1",
		Session: "work",
		Window:  2,
		Fields:  store.Fields{State: "working", Label: "fix it"},
	}
	moved := pane
	moved.Fields.State = "done"
	return []PaneEvent{
		{Full: true, Listing: []PaneSnapshot{pane}},
		{Pane: &moved, Transition: &PaneTransition{
			From:        protocol.StateWorking,
			To:          protocol.StateDone,
			Cwd:         "/work",
			Transcript:  "t1",
			LastMessage: "done",
		}},
		{Pane: &pane},
	}
}

// startPanesAgent serves an Agent whose panes are panes, on a loopback listener until the test ends. Its holders are a
// fakeHost, which no pane op calls. A nil panes makes the agent serve no panes. It returns the listener's address.
func startPanesAgent(t *testing.T, panes HostPanes) string {
	t.Helper()
	ln := listenLoopback(t)
	agent, err := NewAgent(AgentConfig{
		Controller: testController,
		WhoIs:      peerWhoIs{own: ln.Addr().String()},
		Holders:    &fakeHost{},
		Panes:      panes,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- agent.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	return ln.Addr().String()
}

// newTestClient returns a Client of the agent at addr, with testBound and a manualClock, so that no test waits on a
// real timer.
func newTestClient(addr string) *Client {
	return NewClient(ClientConfig{
		Name:    "remote",
		Address: addr,
		Dial:    (&net.Dialer{}).DialContext,
		Bound:   testBound,
		Clock:   newManualClock(),
	})
}

// TestNodePanesCall makes a unary call and opens a WatchConversation stream through the Panes client of a Client. Each
// reaches the fake handler, and the Connect error that the handler answers keeps its code through the Client.
func TestNodePanesCall(t *testing.T) {
	panes := newTestClient(startPanesAgent(t, &fakeHostPanes{})).Panes()
	ctx := t.Context()

	shown, err := panes.Screen(ctx, connect.NewRequest(&orreryv1.ScreenRequest{Pane: fakePane}))
	if err != nil {
		t.Fatalf("screen of %s: %v", fakePane, err)
	}
	if got := string(shown.Msg.GetScreen()); got != "screen of "+fakePane {
		t.Fatalf("screen = %q, want the screen of %s", got, fakePane)
	}
	_, err = panes.Screen(ctx, connect.NewRequest(&orreryv1.ScreenRequest{Pane: "%9"}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("screen of an unknown pane has code %v, want %v (err %v)", code, connect.CodeNotFound, err)
	}

	stream, err := panes.WatchConversation(ctx, connect.NewRequest(&orreryv1.WatchConversationRequest{Pane: fakePane}))
	if err != nil {
		t.Fatalf("watch conversation of %s: %v", fakePane, err)
	}
	if !stream.Receive() {
		t.Fatalf("watch conversation receives no delta: %v", stream.Err())
	}
	if seq := stream.Msg().GetSeq(); seq != fakeSeq {
		t.Fatalf("delta seq = %d, want %d", seq, fakeSeq)
	}
	if stream.Receive() {
		t.Fatal("watch conversation receives a second delta")
	}
	if code := connect.CodeOf(stream.Err()); code != connect.CodeResourceExhausted {
		t.Fatalf("watch conversation ends with code %v, want %v (err %v)",
			code, connect.CodeResourceExhausted, stream.Err())
	}
}

// TestNodePanesWatch opens the pane watch of a Client and reads the events that the agent's watch sends, in order.
// Close ends the watch, and the agent's watch ends once the controller has disconnected.
func TestNodePanesWatch(t *testing.T) {
	panes := &fakeHostPanes{events: fakeEvents(), ended: make(chan struct{}, 1)}
	watch, err := newTestClient(startPanesAgent(t, panes)).WatchPanes(t.Context())
	if err != nil {
		t.Fatalf("watch panes: %v", err)
	}
	for i, want := range panes.events {
		got, nextErr := watch.Next()
		if nextErr != nil {
			t.Fatalf("event %d: %v", i, nextErr)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("event %d = %+v, want %+v", i, got, want)
		}
	}
	if err = watch.Close(); err != nil {
		t.Fatalf("close the watch: %v", err)
	}
	<-panes.ended
}

// TestNodePanesLoad asks a Client for the host's load, which the agent's HostPanes answers. A load above 100 comes
// through unchanged, since LoadPercent is uncapped.
func TestNodePanesLoad(t *testing.T) {
	want := Load{LoadPercent: 250, MemoryPercent: 61.5}
	got, err := newTestClient(startPanesAgent(t, &fakeHostPanes{load: want})).Load(t.Context())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatalf("load = %+v, want %+v", got, want)
	}
}

// TestNodePanesBound makes each pane call to an agent that accepts its connection and never answers. Each call closes
// the connection once the clock passes Bound, and fails with an error that matches holderhost.ErrUnreachable.
func TestNodePanesBound(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(context.Context, *Client) error
	}{
		{name: "load", call: func(ctx context.Context, client *Client) error {
			_, err := client.Load(ctx)
			return err
		}},
		{name: "watch", call: func(ctx context.Context, client *Client) error {
			_, err := client.WatchPanes(ctx)
			return err
		}},
		{name: "unary call", call: func(ctx context.Context, client *Client) error {
			_, err := client.Panes().List(ctx, connect.NewRequest(&orreryv1.PanesListRequest{}))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, accepted, closed := silentAgent(t)
			clock := newManualClock()
			client := NewClient(ClientConfig{
				Name:    "silent",
				Address: addr,
				Dial:    (&net.Dialer{}).DialContext,
				Bound:   testBound,
				Clock:   clock,
			})
			errs := make(chan error, 1)
			go func() { errs <- tc.call(t.Context(), client) }()
			<-accepted
			clock.await(testBound).fire()
			if err := <-errs; !errors.Is(err, holderhost.ErrUnreachable) {
				t.Fatalf("%s = %v, want an error matching holderhost.ErrUnreachable", tc.name, err)
			}
			if err := <-closed; err != nil {
				t.Fatalf("the client's close of the silent connection: %v", err)
			}
		})
	}
}

// TestNodePanesPipelined sends the OpPanes Request and an HTTP request in one write. The agent's HTTP server reads that
// request from the bytes that follow the Request line, so its response follows the Response line.
func TestNodePanesPipelined(t *testing.T) {
	addr := startPanesAgent(t, &fakeHostPanes{})
	conn := dialController(t, addr)
	line, err := json.Marshal(Request{Version: Version, Op: OpPanes})
	if err != nil {
		t.Fatal(err)
	}
	httpReq := "GET /orrery.v1.Panes/Missing HTTP/1.1\r\nHost: remote\r\nConnection: close\r\n\r\n"
	if _, err = conn.Write(append(append(line, '\n'), httpReq...)); err != nil {
		t.Fatalf("write the pipelined request: %v", err)
	}
	reader := bufio.NewReader(conn)
	responseLine, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read the response line: %v", err)
	}
	var resp Response
	if err = json.Unmarshal(responseLine, &resp); err != nil {
		t.Fatalf("decode the response line: %v", err)
	}
	if resp.Code != "" {
		t.Fatalf("panes answered code %q, want no code", resp.Code)
	}
	httpResp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("read the HTTP response: %v", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusNotFound {
		t.Fatalf("HTTP status = %d, want %d for a path the Panes service does not serve", httpResp.StatusCode,
			http.StatusNotFound)
	}
}

// handshakeAgent accepts a connection, answers its OpPanes Request with no Code, and then reads the HTTP request that
// follows without answering it. It sends on requested once that request's headers are read, or the error that stopped
// reading them, and it sends on closed the error of reading the connection to its end.
func handshakeAgent(t *testing.T) (string, <-chan error, <-chan error) {
	t.Helper()
	ln := listenLoopback(t)
	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close the handshake agent: %v", err)
		}
	})
	requested := make(chan error, 1)
	closed := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			requested <- err
			return
		}
		reader := bufio.NewReader(conn)
		if _, err = reader.ReadBytes('\n'); err != nil {
			requested <- err
			return
		}
		if _, err = conn.Write([]byte("{}\n")); err != nil {
			requested <- err
			return
		}
		if _, err = http.ReadRequest(reader); err != nil {
			requested <- err
			return
		}
		requested <- nil
		_, readErr := io.Copy(io.Discard, reader)
		closed <- errors.Join(readErr, conn.Close())
	}()
	return ln.Addr().String(), requested, closed
}

// TestNodePanesHeadersBound makes a unary call to an agent that answers the OpPanes Request and then never answers the
// HTTP request. The call closes the connection once the clock passes Bound, and fails with an error that matches
// holderhost.ErrUnreachable.
func TestNodePanesHeadersBound(t *testing.T) {
	addr, requested, closed := handshakeAgent(t)
	clock := newManualClock()
	client := NewClient(ClientConfig{
		Name:    "silent",
		Address: addr,
		Dial:    (&net.Dialer{}).DialContext,
		Bound:   testBound,
		Clock:   clock,
	})
	errs := make(chan error, 1)
	go func() {
		_, err := client.Panes().List(t.Context(), connect.NewRequest(&orreryv1.PanesListRequest{}))
		errs <- err
	}()
	if err := <-requested; err != nil {
		t.Fatalf("the agent reads the HTTP request: %v", err)
	}
	// The handshake's bound is still pending, since its answer has come, and the headers' bound was set before the
	// request was written. Each await claims one of the two pending timers, so firing both fires the headers' bound,
	// and the handshake's bound has no effect once its call has returned.
	clock.await(testBound).fire()
	clock.await(testBound).fire()
	if err := <-errs; !errors.Is(err, holderhost.ErrUnreachable) {
		t.Fatalf("list = %v, want an error matching holderhost.ErrUnreachable", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("the client's close of the connection: %v", err)
	}
}

// TestNodePanesNone asks an agent with no Panes for each pane op. Each op is refused with CodeFailed "agent serves no
// panes".
func TestNodePanesNone(t *testing.T) {
	addr := startPanesAgent(t, nil)
	for _, op := range []Op{OpPanes, OpWatchPanes, OpLoad} {
		t.Run(string(op), func(t *testing.T) {
			resp := ask(t, addr, Request{Version: Version, Op: op})
			if resp.Code != CodeFailed || resp.Message != "agent serves no panes" {
				t.Fatalf("%s = code %q message %q, want code %q message %q",
					op, resp.Code, resp.Message, CodeFailed, "agent serves no panes")
			}
		})
	}
}
