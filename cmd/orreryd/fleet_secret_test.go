package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/node"
)

// Synthetic values only. fleetSecretText is the fleet secret the --fleet-secret file of these tests holds, and
// fleetSecretAgentName is the hosts.toml agent that the controller follows, whose fake listener answers in its place.
const (
	fleetSecretText      = "synthetic-fleet-secret"
	fleetSecretAgentName = "fleet-agent"
)

// fleetSecretRequest is one Request that a fake agent read, with the error that reading it or answering it failed with.
type fleetSecretRequest struct {
	req node.Request
	err error
}

// fleetSecretAgent is a fake agent listener of these tests. It reads the Request that each connection opens with, and
// sends it on requests. Each answers with a Response that carries a Code, so the controller's call fails at once.
type fleetSecretAgent struct {
	addr     string
	requests chan fleetSecretRequest
}

// startFleetSecretAgent starts a fake agent on a loopback port of its own, which runs until the test ends.
func startFleetSecretAgent(t *testing.T) fleetSecretAgent {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the fake agent: %v", err)
	}
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		if err := ln.Close(); err != nil {
			t.Errorf("close the fake agent: %v", err)
		}
	})
	agent := fleetSecretAgent{addr: ln.Addr().String(), requests: make(chan fleetSecretRequest, 8)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go agent.answer(conn, done)
		}
	}()
	return agent
}

// answer reads the Request that conn opens with, sends it on the agent's requests, and answers it with a failure. A
// read or answer that fails is sent with the request, and so is a conn that fails to close.
func (a fleetSecretAgent) answer(conn net.Conn, done <-chan struct{}) {
	var got fleetSecretRequest
	got.err = json.NewDecoder(conn).Decode(&got.req)
	if got.err == nil {
		got.err = json.NewEncoder(conn).Encode(node.Response{Code: node.CodeFailed, Message: "fleet secret test"})
	}
	got.err = errors.Join(got.err, conn.Close())
	select {
	case a.requests <- got:
	case <-done:
	}
}

// receive returns the first Request that the fake agent read. It fails the test when that read failed, or when the test
// ends before the controller sends one.
func (a fleetSecretAgent) receive(t *testing.T) node.Request {
	t.Helper()
	select {
	case got := <-a.requests:
		if got.err != nil {
			t.Fatalf("the fake agent read its request: %v", got.err)
		}
		return got.req
	case <-t.Context().Done():
	}
	t.Fatal("the controller sent no request to the fake agent")
	return node.Request{}
}

// fleetSecretHostsFile returns a hosts file that lists the one agent of these tests, at addr.
func fleetSecretHostsFile(addr string) string {
	return "[[hosts]]\nname = \"" + fleetSecretAgentName + "\"\naddress = \"" + addr + "\"\n"
}

// fleetSecretFileWith writes data to a file of the test's own that only its owner can read, which --fleet-secret names,
// and returns the file's path.
func fleetSecretFileWith(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fleet-secret")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write the fleet secret file: %v", err)
	}
	return path
}

// TestFleetSecretOrreryd starts orreryd, with a hosts file that lists the fake agent, and shows that the node.v1 Request
// the agent receives carries the secret of the --fleet-secret file. It also shows that a start with a host listed and no
// usable fleet secret fails, and names --fleet-secret, before any request reaches the agent.
func TestFleetSecretOrreryd(t *testing.T) {
	t.Run("the agent's request carries the flag's secret", func(t *testing.T) {
		agent := startFleetSecretAgent(t)
		getenv, root := hostMigrationEnv(t, fleetSecretHostsFile(agent.addr))
		args := []string{
			"--socket", filepath.Join(root, "orreryd.sock"), "--host", hostMigrationName,
			"--fleet-secret", fleetSecretFileWith(t, fleetSecretText),
		}
		opts, err := parseFlags(args, getenv)
		if err != nil {
			t.Fatalf("parseFlags: %v", err)
		}
		startHostMigrationServe(t, getenv, opts)

		req := agent.receive(t)
		if req.Version != node.Version || req.Op != node.OpWatchPanes {
			t.Errorf("the agent's request = version %q op %q, want version %q op %q",
				req.Version, req.Op, node.Version, node.OpWatchPanes)
		}
		if req.Secret != fleetSecretText {
			t.Errorf("the agent's request carries secret %q, want the flag's file's %q", req.Secret, fleetSecretText)
		}
	})

	refusals := []struct {
		name       string
		secretFile string
	}{
		{name: "no --fleet-secret"},
		{name: "a --fleet-secret file that holds no secret", secretFile: fleetSecretFileWith(t, " \n")},
	}
	for _, tc := range refusals {
		t.Run("a host listed with "+tc.name+" fails naming the flag", func(t *testing.T) {
			agent := startFleetSecretAgent(t)
			getenv, root := hostMigrationEnv(t, fleetSecretHostsFile(agent.addr))
			args := []string{"--socket", filepath.Join(root, "orreryd.sock"), "--host", hostMigrationName}
			if tc.secretFile != "" {
				args = append(args, "--fleet-secret", tc.secretFile)
			}
			opts, err := parseFlags(args, getenv)
			if err != nil {
				t.Fatalf("parseFlags: %v", err)
			}
			err = serve(t.Context(), opts, slog.New(slog.DiscardHandler), getenv)
			if err == nil {
				t.Fatal("serve started with a host listed and no usable --fleet-secret, want an error")
			}
			if !strings.Contains(err.Error(), "--fleet-secret") {
				t.Errorf("serve error = %v, want one naming --fleet-secret", err)
			}
			select {
			case got := <-agent.requests:
				t.Errorf("the agent received a request %+v before serve refused to start", got.req)
			default:
			}
		})
	}
}
