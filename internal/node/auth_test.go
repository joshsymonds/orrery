package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync/atomic"
	"syscall"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/holderhost"
)

const (
	testController = "vermissian"
	testLogin      = "josh@example.com"
	otherLogin     = "someone@example.com"
)

// fakeWhoIs names each peer by its address, as tailscaled's WhoIs names a connection's peer. An address with no answer
// fails.
type fakeWhoIs struct {
	answers map[string]whoIsAnswer
}

type whoIsAnswer struct {
	resp *apitype.WhoIsResponse
	err  error
}

func (f fakeWhoIs) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	answer, ok := f.answers[addr]
	if !ok {
		return nil, fmt.Errorf("fake WhoIs: no peer at %s", addr)
	}
	return answer.resp, answer.err
}

// hungWhoIs answers as fakeWhoIs does, except for hung, whose WhoIs signals entered and then waits until its context
// ends, as a hung tailscaled would.
type hungWhoIs struct {
	fakeWhoIs
	hung    string
	entered chan<- struct{}
}

func (h hungWhoIs) WhoIs(ctx context.Context, addr string) (*apitype.WhoIsResponse, error) {
	if addr != h.hung {
		return h.fakeWhoIs.WhoIs(ctx, addr)
	}
	h.entered <- struct{}{}
	<-ctx.Done()
	return nil, fmt.Errorf("fake WhoIs of %s: %w", addr, ctx.Err())
}

// fakeHost is a holderhost.Host that counts the calls the agent makes to it. The agent calls only Sessions, which
// answers with one session id.
type fakeHost struct {
	holderhost.Host
	calls atomic.Int64
}

func (h *fakeHost) Sessions(context.Context) ([]string, error) {
	h.calls.Add(1)
	return []string{"s1"}, nil
}

func myProfile(login string) *tailcfg.UserProfile {
	return &tailcfg.UserProfile{LoginName: login}
}

// ownAnswer is the WhoIs answer for the agent's own address: its own node, signed in as the owner.
func ownAnswer() whoIsAnswer {
	return whoIsAnswer{resp: &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{ComputedName: "orreryd-box"},
		UserProfile: myProfile(testLogin),
	}}
}

// controllerAnswer is the WhoIs answer for a peer that is the controller node under the owner's login.
func controllerAnswer() whoIsAnswer {
	return whoIsAnswer{resp: &apitype.WhoIsResponse{
		Node:        &tailcfg.Node{ComputedName: testController, Name: testController + ".tail1234.ts.net."},
		UserProfile: myProfile(testLogin),
	}}
}

// listenLoopback listens on a loopback address, which the test serves an Agent on.
func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// peerAddr returns a loopback address that no socket holds, for a client to dial from. The fake WhoIs names the client
// by that address before the client dials, so the agent's WhoIs of the connection finds its answer.
func peerAddr(t *testing.T) string {
	t.Helper()
	ln := listenLoopback(t)
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// dialFrom dials the agent at addr from the local address from.
func dialFrom(t *testing.T, from, addr string) net.Conn {
	t.Helper()
	local, err := net.ResolveTCPAddr("tcp", from)
	if err != nil {
		t.Fatal(err)
	}
	dialer := net.Dialer{LocalAddr: local}
	conn, err := dialer.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
	})
	return conn
}

// startAgent serves an Agent on ln, with whois and holders, until the test ends. The test fails when Serve returns an
// error.
func startAgent(t *testing.T, ln net.Listener, whois WhoIs, holders holderhost.Host) {
	t.Helper()
	agent, err := NewAgent(AgentConfig{Controller: testController, WhoIs: whois, Holders: holders})
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
}

// exchange sends req on conn and returns the one Response the agent answers with.
func exchange(t *testing.T, conn net.Conn, req Request) Response {
	t.Helper()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// expectRefused sends req on conn, and checks that the agent closes conn without answering. The agent closes a refused
// connection before it reads, so the send can meet a reset, and the read ends in EOF or a reset.
func expectRefused(t *testing.T, conn net.Conn, req Request) {
	t.Helper()
	if err := json.NewEncoder(conn).Encode(req); err != nil && !closedByPeer(err) {
		t.Fatal(err)
	}
	got, err := io.ReadAll(conn)
	if len(got) != 0 {
		t.Fatalf("refused peer got a reply: %q", got)
	}
	if err != nil && !closedByPeer(err) {
		t.Fatal(err)
	}
}

func closedByPeer(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}

func TestNodeAuthServesControllerUnderMyLogin(t *testing.T) {
	ln := listenLoopback(t)
	controller := peerAddr(t)
	wrongVersion := peerAddr(t)
	whois := fakeWhoIs{answers: map[string]whoIsAnswer{
		ln.Addr().String(): ownAnswer(),
		controller:         controllerAnswer(),
		wrongVersion:       controllerAnswer(),
	}}
	host := &fakeHost{}
	startAgent(t, ln, whois, host)

	resp := exchange(t, dialFrom(t, controller, ln.Addr().String()), Request{Version: Version, Op: OpSessions})
	if !slices.Equal(resp.Sessions, []string{"s1"}) {
		t.Fatalf("controller got sessions %v, want [s1]", resp.Sessions)
	}
	if n := host.calls.Load(); n != 1 {
		t.Fatalf("Host got %d calls for the controller's request, want 1", n)
	}

	resp = exchange(t, dialFrom(t, wrongVersion, ln.Addr().String()), Request{Version: "node.v0", Op: OpSessions})
	if resp.Code != CodeFailed {
		t.Fatalf("a request naming another version got code %q, want %q", resp.Code, CodeFailed)
	}
	if n := host.calls.Load(); n != 1 {
		t.Fatalf("Host got %d calls after a request naming another version, want 1", n)
	}
}

func TestNodeAuthRefusesEveryOtherPeer(t *testing.T) {
	cases := []struct {
		name   string
		answer whoIsAnswer
	}{
		{"other node under my login", whoIsAnswer{resp: &apitype.WhoIsResponse{
			Node:        &tailcfg.Node{ComputedName: "laptop", Name: "laptop.tail1234.ts.net."},
			UserProfile: myProfile(testLogin),
		}}},
		{"another login", whoIsAnswer{resp: &apitype.WhoIsResponse{
			Node:        &tailcfg.Node{ComputedName: testController, Name: testController + ".tail1234.ts.net."},
			UserProfile: myProfile(otherLogin),
		}}},
		{"tagged node", whoIsAnswer{resp: &apitype.WhoIsResponse{
			Node:        &tailcfg.Node{ComputedName: testController, Tags: []string{"tag:server"}},
			UserProfile: myProfile(testLogin),
		}}},
		{"no node", whoIsAnswer{resp: &apitype.WhoIsResponse{UserProfile: myProfile(testLogin)}}},
		{"failed WhoIs", whoIsAnswer{err: errors.New("tailscaled is not running")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ln := listenLoopback(t)
			peer := peerAddr(t)
			whois := fakeWhoIs{answers: map[string]whoIsAnswer{
				ln.Addr().String(): ownAnswer(),
				peer:               tc.answer,
			}}
			host := &fakeHost{}
			startAgent(t, ln, whois, host)

			expectRefused(t, dialFrom(t, peer, ln.Addr().String()), Request{Version: Version, Op: OpSessions})
			if n := host.calls.Load(); n != 0 {
				t.Fatalf("Host got %d calls for a refused peer, want 0", n)
			}
		})
	}
}

func TestNodeAuthHungWhoIsHoldsOnlyItsConnection(t *testing.T) {
	ln := listenLoopback(t)
	hung := peerAddr(t)
	controller := peerAddr(t)
	entered := make(chan struct{}, 1)
	whois := hungWhoIs{
		fakeWhoIs: fakeWhoIs{answers: map[string]whoIsAnswer{
			ln.Addr().String(): ownAnswer(),
			controller:         controllerAnswer(),
		}},
		hung:    hung,
		entered: entered,
	}
	host := &fakeHost{}
	startAgent(t, ln, whois, host)

	dialFrom(t, hung, ln.Addr().String())
	<-entered

	resp := exchange(t, dialFrom(t, controller, ln.Addr().String()), Request{Version: Version, Op: OpSessions})
	if !slices.Equal(resp.Sessions, []string{"s1"}) {
		t.Fatalf("controller got sessions %v while another peer's WhoIs hung, want [s1]", resp.Sessions)
	}
}

func TestNodeAuthServeRefusesToStart(t *testing.T) {
	cases := []struct {
		name   string
		answer whoIsAnswer
	}{
		{"WhoIs of its own address fails", whoIsAnswer{err: errors.New("tailscaled is not running")}},
		{"own address names no node", whoIsAnswer{resp: &apitype.WhoIsResponse{UserProfile: myProfile(testLogin)}}},
		{"own address names no user profile", whoIsAnswer{resp: &apitype.WhoIsResponse{
			Node: &tailcfg.Node{ComputedName: "orreryd-box"},
		}}},
		{"own address names a tagged node", whoIsAnswer{resp: &apitype.WhoIsResponse{
			Node:        &tailcfg.Node{ComputedName: "orreryd-box", Tags: []string{"tag:server"}},
			UserProfile: myProfile(testLogin),
		}}},
		{"own address names no login", whoIsAnswer{resp: &apitype.WhoIsResponse{
			Node:        &tailcfg.Node{ComputedName: "orreryd-box"},
			UserProfile: myProfile(""),
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ln := listenLoopback(t)
			whois := fakeWhoIs{answers: map[string]whoIsAnswer{ln.Addr().String(): tc.answer}}
			host := &fakeHost{}
			agent, err := NewAgent(AgentConfig{Controller: testController, WhoIs: whois, Holders: host})
			if err != nil {
				t.Fatal(err)
			}
			if err := agent.Serve(t.Context(), ln); err == nil {
				t.Fatal("Serve started, want an error")
			}
			if err := ln.Close(); err != nil {
				t.Fatal(err)
			}
			if n := host.calls.Load(); n != 0 {
				t.Fatalf("Host got %d calls from an agent that did not start, want 0", n)
			}
		})
	}
}

func TestNodeAuthAuthorize(t *testing.T) {
	cases := []struct {
		name       string
		controller string
		resp       *apitype.WhoIsResponse
		admitted   bool
	}{
		{"nil response", testController, nil, false},
		{"response with no node", testController, &apitype.WhoIsResponse{UserProfile: myProfile(testLogin)}, false},
		{"tagged node", testController, &apitype.WhoIsResponse{
			Node:        &tailcfg.Node{ComputedName: testController, Tags: []string{"tag:server"}},
			UserProfile: myProfile(testLogin),
		}, false},
		{"node named otherwise", testController, &apitype.WhoIsResponse{
			Node:        &tailcfg.Node{ComputedName: "laptop", Name: "laptop.tail1234.ts.net."},
			UserProfile: myProfile(testLogin),
		}, false},
		{"node named by ComputedName", testController, controllerAnswer().resp, true},
		{"node named by Name without its trailing dot", "vermissian.tail1234.ts.net", &apitype.WhoIsResponse{
			Node:        &tailcfg.Node{Name: "vermissian.tail1234.ts.net."},
			UserProfile: myProfile(testLogin),
		}, true},
		{"node with no user profile", testController, &apitype.WhoIsResponse{
			Node: &tailcfg.Node{ComputedName: testController},
		}, false},
		{"another login", testController, &apitype.WhoIsResponse{
			Node:        &tailcfg.Node{ComputedName: testController},
			UserProfile: myProfile(otherLogin),
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Authorize(tc.resp, tc.controller, testLogin)
			if tc.admitted && err != nil {
				t.Fatalf("Authorize refused the peer: %v", err)
			}
			if !tc.admitted && err == nil {
				t.Fatal("Authorize admitted the peer, want a refusal")
			}
		})
	}
}
