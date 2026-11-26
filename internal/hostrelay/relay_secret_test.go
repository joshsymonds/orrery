package hostrelay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"tailscale.com/tailcfg"

	"github.com/joshsymonds/orrery/internal/protocol"
)

// otherSecret is a fleet secret that is not the relay's.
const otherSecret = "another-fleet-secret"

// expectUnauthenticated calls the relay at addr from client with secret as its fleet secret, and checks that the relay
// answers with a Connect unauthenticated error, and that the handler saw nothing.
func expectUnauthenticated(t *testing.T, client *http.Client, addr, secret string, seen chan call) {
	t.Helper()
	resp, err := client.Do(callWithSecret(t, addr, secret))
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Error(closeErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
	var wire struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode the refusal %q: %v", body, err)
	}
	if wire.Code != connect.CodeUnauthenticated.String() {
		t.Fatalf("refusal code %q, want %q", wire.Code, connect.CodeUnauthenticated)
	}
	select {
	case got := <-seen:
		t.Fatalf("handler saw a call without the fleet secret: %+v", got)
	default:
	}
}

// TestRelaySecret checks that the relay admits a call only when it carries the fleet secret: a call with the right
// secret reaches the handler without the header, a call with no secret or another one is refused unauthenticated before
// the handler runs, and Listen opens no socket with an empty secret.
func TestRelaySecret(t *testing.T) {
	relayAddr := freeLoopbackAddr(t)
	// Each subtest calls from an address of its own, since a client that disables keep-alives cannot bind the local port
	// of a connection that is still in TIME_WAIT.
	rightAddr := freeLoopbackAddr(t)
	noneAddr := freeLoopbackAddr(t)
	otherAddr := freeLoopbackAddr(t)
	agentNode := peerAnswer(testLogin, &tailcfg.Node{
		Name:         "b.tail1234.ts.net.",
		ComputedName: "b",
	})
	whois := fakeWhoIs{answers: map[string]whoIsAnswer{
		relayAddr: ownAnswer(),
		rightAddr: agentNode,
		noneAddr:  agentNode,
		otherAddr: agentNode,
	}}
	seen := startRelay(t, relayAddr, testAgents(), whois)

	t.Run("right secret reaches the handler without the header", func(t *testing.T) {
		client := clientFrom(t, rightAddr)
		resp, err := client.Do(callWithSecret(t, relayAddr, testSecret))
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
			if len(got.secrets) != 0 {
				t.Fatalf("handler saw the fleet secret %q, want the header deleted", got.secrets)
			}
		default:
			t.Fatal("handler did not see the admitted call")
		}
	})

	t.Run("no secret is refused unauthenticated", func(t *testing.T) {
		expectUnauthenticated(t, clientFrom(t, noneAddr), relayAddr, "", seen)
	})

	t.Run("another secret is refused unauthenticated", func(t *testing.T) {
		expectUnauthenticated(t, clientFrom(t, otherAddr), relayAddr, otherSecret, seen)
	})

	t.Run("Listen refuses an empty secret and opens no socket", func(t *testing.T) {
		addr := freeLoopbackAddr(t)
		whois := fakeWhoIs{answers: map[string]whoIsAnswer{addr: ownAnswer()}}
		relay, err := Listen(t.Context(), Config{
			Address: addr,
			Hosts:   testAgents(),
			WhoIs:   whois,
			Handler: newSpy(),
		})
		if err == nil {
			t.Fatal("Listen with no secret succeeded")
		}
		if !strings.Contains(err.Error(), "secret") {
			t.Fatalf("Listen error %q does not name the secret", err)
		}
		if relay != nil {
			t.Fatal("Listen failed but returned a relay")
		}
		expectNoListener(t, addr)
	})
}

// TestForwardSecret checks that Forward sets the fleet secret on each request it relays, replacing the one the client
// sent: the request that reaches the controller's relay address carries the secret, and no other value. A recording
// server stands in for the relay, which would delete the header before its handler runs.
func TestForwardSecret(t *testing.T) {
	arrived := make(chan []string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		arrived <- req.Header.Values(protocol.HeaderSecret)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	sock := startForward(t, upstream.Listener.Addr().String())
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://forward"+callPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(protocol.HeaderSecret, otherSecret)
	resp, err := unixClient(t, sock).Do(req)
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
	case got := <-arrived:
		if !slices.Equal(got, []string{testSecret}) {
			t.Fatalf("request arrived with fleet secret %q, want exactly %q", got, testSecret)
		}
	default:
		t.Fatal("the request did not reach the controller")
	}
}
