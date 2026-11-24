package hostrelay

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/hosts"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/protocol"
)

const (
	testLogin  = "josh@example.com"
	otherLogin = "someone@example.com"
	callPath   = "/calls/ping"
)

// testAgents are the agents of hosts.toml that the relay admits.
func testAgents() []hosts.Host {
	return []hosts.Host{
		{Name: "a", Address: "a.tail1234.ts.net:7443"},
		{Name: "b", Address: "b.tail1234.ts.net:7443"},
	}
}

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
		return nil, errors.New("fake WhoIs: no peer at " + addr)
	}
	return answer.resp, answer.err
}

// ownAnswer is the WhoIs answer for the relay's own address: its own node, signed in as the owner.
func ownAnswer() whoIsAnswer {
	return peerAnswer(testLogin, &tailcfg.Node{ComputedName: "orreryd-box"})
}

// peerAnswer is the WhoIs answer for a peer whose node is peer, signed in as login.
func peerAnswer(login string, peer *tailcfg.Node) whoIsAnswer {
	return whoIsAnswer{resp: &apitype.WhoIsResponse{
		Node:        peer,
		UserProfile: &tailcfg.UserProfile{LoginName: login},
	}}
}

// call is one request that the spy handler recorded: its path, and the Orrery-Host it carried.
type call struct {
	path string
	host string
}

// spy is the handler that the relay serves admitted calls to. It records each request before it answers, so a client
// that has its answer finds the record in seen.
type spy struct {
	seen chan call
}

func newSpy() spy {
	return spy{seen: make(chan call, 16)}
}

func (s spy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	s.seen <- call{path: req.URL.Path, host: req.Header.Get(protocol.HeaderHost)}
	w.WriteHeader(http.StatusNoContent)
}

// freeLoopbackAddr returns a loopback address that no socket holds, for a listener or a client to use.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// startRelay listens on addr with agents, whois and a spy handler, and serves the relay until the test ends. It returns
// the spy's record of the calls. The test fails when Listen or Serve returns an error.
func startRelay(t *testing.T, addr string, agents []hosts.Host, whois node.WhoIs) chan call {
	t.Helper()
	spied := newSpy()
	relay, err := Listen(t.Context(), Config{Address: addr, Hosts: agents, WhoIs: whois, Handler: spied})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- relay.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	return spied.seen
}

// clientFrom returns an HTTP client that dials from the local address from, and opens a connection for each request.
func clientFrom(t *testing.T, from string) *http.Client {
	t.Helper()
	local, err := net.ResolveTCPAddr("tcp", from)
	if err != nil {
		t.Fatal(err)
	}
	dialer := net.Dialer{LocalAddr: local}
	transport := &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

// callRequest returns a call to the relay at addr, which sends its own Orrery-Host.
func callRequest(t *testing.T, addr string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+callPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(protocol.HeaderHost, "spoofed")
	return req
}

// expectAdmitted calls the relay at addr from client, and checks that the handler saw the call with Orrery-Host host,
// and that the client got the handler's answer.
func expectAdmitted(t *testing.T, client *http.Client, addr string, seen chan call, host string) {
	t.Helper()
	resp, err := client.Do(callRequest(t, addr))
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Error(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	select {
	case got := <-seen:
		if got.path != callPath {
			t.Fatalf("handler saw path %q, want %q", got.path, callPath)
		}
		if got.host != host {
			t.Fatalf("handler saw Orrery-Host %q, want %q", got.host, host)
		}
	default:
		t.Fatal("handler did not see the admitted call")
	}
}

// expectRefused calls the relay at addr from client, and checks that the call fails and that the handler saw nothing.
func expectRefused(t *testing.T, client *http.Client, addr string, seen chan call) {
	t.Helper()
	resp, err := client.Do(callRequest(t, addr))
	if err == nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatalf("refused peer got status %d", resp.StatusCode)
	}
	select {
	case got := <-seen:
		t.Fatalf("handler saw a call from a refused peer: %+v", got)
	default:
	}
}

// expectNoListener checks that nothing listens on addr, by dialing it and expecting the dial to fail.
func expectNoListener(t *testing.T, addr string) {
	t.Helper()
	var dialer net.Dialer
	conn, err := dialer.DialContext(t.Context(), "tcp", addr)
	if err == nil {
		if closeErr := conn.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatalf("dialed %s, which should have no listener", addr)
	}
}

func TestRelayAuthServesHostsAgent(t *testing.T) {
	relayAddr := freeLoopbackAddr(t)
	agentAddr := freeLoopbackAddr(t)
	whois := fakeWhoIs{answers: map[string]whoIsAnswer{
		relayAddr: ownAnswer(),
		agentAddr: peerAnswer(testLogin, &tailcfg.Node{
			Name:         "b.tail1234.ts.net.",
			ComputedName: "b",
		}),
	}}
	seen := startRelay(t, relayAddr, testAgents(), whois)
	expectAdmitted(t, clientFrom(t, agentAddr), relayAddr, seen, "b")
}

func TestRelayAuthRefusesUnlistedNode(t *testing.T) {
	relayAddr := freeLoopbackAddr(t)
	peerAddr := freeLoopbackAddr(t)
	whois := fakeWhoIs{answers: map[string]whoIsAnswer{
		relayAddr: ownAnswer(),
		peerAddr: peerAnswer(testLogin, &tailcfg.Node{
			Name:         "c.tail1234.ts.net.",
			ComputedName: "c",
		}),
	}}
	seen := startRelay(t, relayAddr, testAgents(), whois)
	expectRefused(t, clientFrom(t, peerAddr), relayAddr, seen)
}

func TestRelayAuthRefusesOtherLogin(t *testing.T) {
	relayAddr := freeLoopbackAddr(t)
	peerAddr := freeLoopbackAddr(t)
	whois := fakeWhoIs{answers: map[string]whoIsAnswer{
		relayAddr: ownAnswer(),
		peerAddr: peerAnswer(otherLogin, &tailcfg.Node{
			Name:         "b.tail1234.ts.net.",
			ComputedName: "b",
		}),
	}}
	seen := startRelay(t, relayAddr, testAgents(), whois)
	expectRefused(t, clientFrom(t, peerAddr), relayAddr, seen)
}

func TestRelayAuthRefusesFailedWhoIs(t *testing.T) {
	relayAddr := freeLoopbackAddr(t)
	peerAddr := freeLoopbackAddr(t)
	whois := fakeWhoIs{answers: map[string]whoIsAnswer{
		relayAddr: ownAnswer(),
		peerAddr:  {err: errors.New("tailscaled is not running")},
	}}
	seen := startRelay(t, relayAddr, testAgents(), whois)
	expectRefused(t, clientFrom(t, peerAddr), relayAddr, seen)
}

func TestRelayAuthRefusesTaggedNode(t *testing.T) {
	relayAddr := freeLoopbackAddr(t)
	peerAddr := freeLoopbackAddr(t)
	whois := fakeWhoIs{answers: map[string]whoIsAnswer{
		relayAddr: ownAnswer(),
		peerAddr: peerAnswer(testLogin, &tailcfg.Node{
			Name:         "b.tail1234.ts.net.",
			ComputedName: "b",
			Tags:         []string{"tag:server"},
		}),
	}}
	seen := startRelay(t, relayAddr, testAgents(), whois)
	expectRefused(t, clientFrom(t, peerAddr), relayAddr, seen)
}

// TestRelayAuthMatchesHostByNameOrAddress checks each rule by which a node matches an agent's address: its Name without
// the trailing dot, its ComputedName in any case, and an IP literal that one of its Addresses holds.
func TestRelayAuthMatchesHostByNameOrAddress(t *testing.T) {
	relayAddr := freeLoopbackAddr(t)
	cases := []struct {
		name string
		peer tailcfg.Node
		host string // the host that the node is admitted as, or empty when it is refused
	}{
		{
			name: "Name with its trailing dot",
			peer: tailcfg.Node{Name: "a.tail1234.ts.net.", ComputedName: "other"},
			host: "a",
		},
		{
			name: "ComputedName in another case",
			peer: tailcfg.Node{Name: "other.tail1234.ts.net.", ComputedName: "B.TAIL1234.TS.NET"},
			host: "b",
		},
		{
			name: "IP literal that one of its Addresses holds",
			peer: tailcfg.Node{
				ComputedName: "other",
				Addresses:    []netip.Prefix{netip.MustParsePrefix("100.64.0.2/32")},
			},
			host: "c",
		},
		{
			name: "IP literal that none of its Addresses holds",
			peer: tailcfg.Node{
				ComputedName: "other",
				Addresses:    []netip.Prefix{netip.MustParsePrefix("100.64.0.9/32")},
			},
		},
	}
	agents := []hosts.Host{
		{Name: "a", Address: "a.tail1234.ts.net:7443"},
		{Name: "b", Address: "b.tail1234.ts.net:7443"},
		{Name: "c", Address: "100.64.0.2:7443"},
	}
	answers := map[string]whoIsAnswer{relayAddr: ownAnswer()}
	peers := make([]string, len(cases))
	for i := range cases {
		peers[i] = freeLoopbackAddr(t)
		answers[peers[i]] = peerAnswer(testLogin, &cases[i].peer)
	}
	seen := startRelay(t, relayAddr, agents, fakeWhoIs{answers: answers})
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := clientFrom(t, peers[i])
			if tc.host == "" {
				expectRefused(t, client, relayAddr, seen)
				return
			}
			expectAdmitted(t, client, relayAddr, seen, tc.host)
		})
	}
}

func TestRelayAuthNoHostsOpensNothing(t *testing.T) {
	addr := freeLoopbackAddr(t)
	relay, err := Listen(t.Context(), Config{
		Address: addr,
		WhoIs:   fakeWhoIs{},
		Handler: newSpy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if relay != nil {
		t.Fatal("Listen with no hosts returned a relay")
	}
	expectNoListener(t, addr)
}

// TestRelayAuthRefusesOwnAddressWithoutLogin checks that Listen fails, and closes its listener, when WhoIs of the
// relay's own address does not name an untagged node with a login.
func TestRelayAuthRefusesOwnAddressWithoutLogin(t *testing.T) {
	cases := []struct {
		name   string
		answer whoIsAnswer
	}{
		{"WhoIs of its own address fails", whoIsAnswer{err: errors.New("tailscaled is not running")}},
		{"own address names no node", whoIsAnswer{resp: &apitype.WhoIsResponse{
			UserProfile: &tailcfg.UserProfile{LoginName: testLogin},
		}}},
		{"own address names no user profile", whoIsAnswer{resp: &apitype.WhoIsResponse{
			Node: &tailcfg.Node{ComputedName: "orreryd-box"},
		}}},
		{"own address names a tagged node", peerAnswer(testLogin, &tailcfg.Node{
			ComputedName: "orreryd-box",
			Tags:         []string{"tag:server"},
		})},
		{"own address names no login", peerAnswer("", &tailcfg.Node{ComputedName: "orreryd-box"})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := freeLoopbackAddr(t)
			whois := fakeWhoIs{answers: map[string]whoIsAnswer{addr: tc.answer}}
			relay, err := Listen(t.Context(), Config{
				Address: addr,
				Hosts:   testAgents(),
				WhoIs:   whois,
				Handler: newSpy(),
			})
			if err == nil {
				t.Fatal("Listen succeeded")
			}
			if relay != nil {
				t.Fatal("Listen failed but returned a relay")
			}
			expectNoListener(t, addr)
		})
	}
}

// TestRelayAuthNeedsWhoIsAndHandler checks that Listen fails, and opens no socket, when cfg has no WhoIs or no handler.
func TestRelayAuthNeedsWhoIsAndHandler(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"no WhoIs", Config{Hosts: testAgents(), Handler: newSpy()}},
		{"no handler", Config{Hosts: testAgents(), WhoIs: fakeWhoIs{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := freeLoopbackAddr(t)
			tc.cfg.Address = addr
			relay, err := Listen(t.Context(), tc.cfg)
			if err == nil {
				t.Fatal("Listen succeeded")
			}
			if relay != nil {
				t.Fatal("Listen failed but returned a relay")
			}
			expectNoListener(t, addr)
		})
	}
}
