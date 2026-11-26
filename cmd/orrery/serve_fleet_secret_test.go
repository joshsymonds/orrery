package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/exectest"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Synthetic values only. fleetSecretText is the fleet secret the --fleet-secret file of these tests holds, and
// fleetSecretAgentName is the hosts.toml agent that serve calls, whose fake listener answers in its place.
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
// ends before serve sends one.
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
	t.Fatal("serve sent no request to the fake agent")
	return node.Request{}
}

// fleetSecretEnv returns the getenv of a serve test whose XDG_CONFIG_HOME holds a hosts file that lists the fake agent at
// addr, and whose XDG_STATE_HOME is a directory of the test's own.
func fleetSecretEnv(t *testing.T, addr string) func(string) string {
	t.Helper()
	config := t.TempDir()
	dir := filepath.Join(config, "orrery")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("make hosts directory: %v", err)
	}
	hostsFile := "[[hosts]]\nname = \"" + fleetSecretAgentName + "\"\naddress = \"" + addr + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "hosts.toml"), []byte(hostsFile), 0o600); err != nil {
		t.Fatalf("write hosts file: %v", err)
	}
	env := map[string]string{"XDG_CONFIG_HOME": config, "XDG_STATE_HOME": t.TempDir()}
	return func(key string) string { return env[key] }
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

// fleetSecretStatus writes the host status command that serve runs for Host.Status: one that prints an empty list of
// stats, so Host.Status goes on to ask each listed agent for its load.
func fleetSecretStatus(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "host-status")
	exectest.WriteFile(t, path, []byte("#!/bin/sh\necho '[]'\n"), 0o755)
	return path
}

// TestFleetSecretServe runs serve through runServe, with a hosts file that lists the fake agent, and shows that the
// node.v1 Request the agent receives carries the secret of the --fleet-secret file. It also shows that serve with a host
// listed and no usable fleet secret fails, and names --fleet-secret, before any request reaches the agent.
func TestFleetSecretServe(t *testing.T) {
	t.Run("the agent's request carries the flag's secret", func(t *testing.T) {
		agent := startFleetSecretAgent(t)
		getenv := fleetSecretEnv(t, agent.addr)
		opts := globals{server: tmuxctl.Server{Name: "orrery-serve-test-unstarted"}}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done, addr := serveStart(ctx, t, opts, getenv,
			"--allow-login", serveTestLogin,
			"--fleet-secret", fleetSecretFileWith(t, fleetSecretText),
			"--host-status-cmd", fleetSecretStatus(t))

		// Host.Status asks each listed agent for its load, which is the call that reaches the agent.
		hostClient := orreryv1connect.NewHostClient(
			&http.Client{Transport: serveLoginTransport{base: http.DefaultTransport}}, "http://"+addr)
		if _, err := hostClient.Status(ctx, connect.NewRequest(&orreryv1.HostStatusRequest{})); err != nil {
			t.Fatalf("Host.Status: %v", err)
		}

		req := agent.receive(t)
		if req.Version != node.Version || req.Op != node.OpLoad {
			t.Errorf("the agent's request = version %q op %q, want version %q op %q",
				req.Version, req.Op, node.Version, node.OpLoad)
		}
		if req.Secret != fleetSecretText {
			t.Errorf("the agent's request carries secret %q, want the flag's file's %q", req.Secret, fleetSecretText)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("runServe returned %v, want nil", err)
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
			getenv := fleetSecretEnv(t, agent.addr)
			opts := globals{server: tmuxctl.Server{Name: "orrery-serve-test-unstarted"}}
			args := []string{"--listen", "127.0.0.1:0", "--allow-login", serveTestLogin}
			if tc.secretFile != "" {
				args = append(args, "--fleet-secret", tc.secretFile)
			}
			err := runServe(t.Context(), opts, args, getenv, io.Discard)
			if err == nil {
				t.Fatal("runServe started with a host listed and no usable --fleet-secret, want an error")
			}
			if !strings.Contains(err.Error(), "--fleet-secret") {
				t.Errorf("runServe error = %v, want one naming --fleet-secret", err)
			}
			select {
			case got := <-agent.requests:
				t.Errorf("the agent received a request %+v before serve refused to start", got.req)
			default:
			}
		})
	}
}
