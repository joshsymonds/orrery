package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/gateway"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// The gateway tests use invented values only: the upstream is a fake on a loopback address, and the keys are synthetic.
const (
	gatewayTestCallerKey   = "synthetic-caller-key"
	gatewayTestUpstreamKey = "synthetic-upstream-key"
	gatewayTestKeyEnv      = "SYNTHETIC_UPSTREAM_KEY_FILE"
	gatewayTestAnswer      = `{"id":"msg_synthetic","type":"message","content":[{"type":"text","text":"synthetic"}]}`
	gatewayTestMessages    = `{"model":"synthetic-model","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`
)

// TestGatewayServesInjectSeat starts the command on a free loopback port, over a store whose routes hold an inject seat
// to a fake upstream. A POST to /v1/messages that presents the caller key gets the upstream's answer, and the upstream
// receives the seat's key.
func TestGatewayServesInjectSeat(t *testing.T) {
	t.Parallel()
	upstream, seen := startFakeUpstream(t)
	getenv, _ := gatewayTestEnv(t, upstream)
	run := startGatewayRun(t, getenv)

	status, body := postGatewayMessages(t, run.addr, gatewayTestCallerKey)
	if status != http.StatusOK || body != gatewayTestAnswer {
		t.Errorf("POST /v1/messages = %d %q, want 200 with the upstream's answer", status, body)
	}
	select {
	case header := <-seen:
		if got := header.Get("Authorization"); got != "Bearer "+gatewayTestUpstreamKey {
			t.Errorf("upstream Authorization = %q, want the seat's key", got)
		}
	default:
		t.Errorf("the fake upstream received no request")
	}
	if err := run.stop(); err != nil {
		t.Fatalf("orrery gateway stopped with %v, want nil", err)
	}
}

// TestGatewayServesWithoutLedger starts the command when the ledger cannot open, because its path is a directory. The
// gateway still answers the request from the upstream, and logs the ledger failure.
func TestGatewayServesWithoutLedger(t *testing.T) {
	t.Parallel()
	upstream, _ := startFakeUpstream(t)
	getenv, storePath := gatewayTestEnv(t, upstream)
	if err := os.Mkdir(ledger.Path(storePath), 0o700); err != nil {
		t.Fatalf("make the ledger path a directory: %v", err)
	}
	run := startGatewayRun(t, getenv)

	status, body := postGatewayMessages(t, run.addr, gatewayTestCallerKey)
	if status != http.StatusOK || body != gatewayTestAnswer {
		t.Errorf("POST /v1/messages = %d %q, want 200 with the upstream's answer", status, body)
	}
	if err := run.stop(); err != nil {
		t.Fatalf("orrery gateway stopped with %v, want nil", err)
	}
	if !strings.Contains(run.out.String(), "open the ledger") {
		t.Errorf("stderr = %q, want the ledger failure logged", run.out.String())
	}
}

// TestGatewayServesDefaultListen checks that `orrery gateway` without --listen listens on 127.0.0.1:4110.
func TestGatewayServesDefaultListen(t *testing.T) {
	t.Parallel()
	listen, err := parseGatewayFlags(nil)
	if err != nil {
		t.Fatalf("parseGatewayFlags(nil): %v", err)
	}
	if want := "127.0.0.1:4110"; listen != want || gateway.DefaultListen != want {
		t.Errorf("default listen = %q, gateway.DefaultListen = %q, want %q", listen, gateway.DefaultListen, want)
	}
}

// TestGatewayServesRefusesNonLoopback checks that an address which is not a loopback IP literal is refused before
// anything is bound, so the command returns the refusal without a store or a ledger.
func TestGatewayServesRefusesNonLoopback(t *testing.T) {
	t.Parallel()
	for _, listen := range []string{"0.0.0.0:4110", "192.0.2.1:4110"} {
		t.Run(listen, func(t *testing.T) {
			t.Parallel()
			err := runGateway(t.Context(), []string{"--listen", listen}, io.Discard, envFrom(nil))
			if err == nil {
				t.Fatalf("orrery gateway --listen %s returned nil, want a refusal", listen)
			}
			if !strings.Contains(err.Error(), "not a loopback IP literal") {
				t.Errorf("error = %v, want the loopback refusal", err)
			}
		})
	}
}

// gatewayTestRoutes returns a registry whose default context sends every request through one inject seat to upstream.
func gatewayTestRoutes(upstream string) llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "main",
		Seats: []llmroutes.Seat{{
			ID: "seat-inject", AuthMode: llmroutes.AuthInject, Upstream: upstream, Model: "synthetic-model",
			APIKeyEnvFile: gatewayTestKeyEnv, Billing: "self_hosted",
		}},
		Contexts: []llmroutes.Context{{ID: "main", DefaultSeat: "seat-inject"}},
	}
}

// gatewayTestEnv writes the invented key files in a temp directory, and stores in a temp store the routes that
// gatewayTestRoutes returns. It returns the environment the command reads, which places XDG_STATE_HOME at that store,
// and the path of the store.
func gatewayTestEnv(t *testing.T, upstream string) (func(string) string, string) {
	t.Helper()
	dir := t.TempDir()
	callerFile := filepath.Join(dir, "caller.key")
	upstreamFile := filepath.Join(dir, "upstream.key")
	if err := os.WriteFile(callerFile, []byte(gatewayTestCallerKey+"\n"), 0o600); err != nil {
		t.Fatalf("write caller key file: %v", err)
	}
	if err := os.WriteFile(upstreamFile, []byte(gatewayTestUpstreamKey+"\n"), 0o600); err != nil {
		t.Fatalf("write upstream key file: %v", err)
	}
	getenv := envFrom(map[string]string{
		"XDG_STATE_HOME":    dir,
		gatewayCallerKeyEnv: callerFile,
		gatewayTestKeyEnv:   upstreamFile,
	})
	storePath, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store.Path: %v", err)
	}
	storetest.Seed(t, storePath)
	st, err := store.Open(t.Context(), storePath, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err = st.ReplaceRoutes(t.Context(), gatewayTestRoutes(upstream)); err != nil {
		t.Fatalf("ReplaceRoutes: %v", err)
	}
	if err = st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	return getenv, storePath
}

// startFakeUpstream serves Anthropic's answer on a loopback address. Before it answers, it sends the header of the
// request it received on the returned channel, so that the test can read the credential the seat sent.
func startFakeUpstream(t *testing.T) (string, <-chan http.Header) {
	t.Helper()
	seen := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Header.Clone():
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, gatewayTestAnswer)
	}))
	t.Cleanup(upstream.Close)

	return upstream.URL, seen
}

// gatewayRun is an `orrery gateway` the test started in process on 127.0.0.1:0.
type gatewayRun struct {
	addr   string
	out    *gatewayOutput
	cancel context.CancelFunc
	done   chan error
}

// startGatewayRun runs the command on its own goroutine with --listen 127.0.0.1:0, and returns once the command has
// bound its listener. It fails the test if the command returns before that.
func startGatewayRun(t *testing.T, getenv func(string) string) *gatewayRun {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	out := &gatewayOutput{bound: make(chan string, 1)}
	done := make(chan error, 1)
	go func() {
		done <- runGateway(ctx, []string{"--listen", "127.0.0.1:0"}, out, getenv)
	}()

	var run *gatewayRun
	select {
	case addr := <-out.bound:
		if !strings.HasPrefix(addr, "127.0.0.1:") {
			t.Errorf("orrery gateway bound %q, want a 127.0.0.1 address", addr)
		}
		run = &gatewayRun{addr: addr, out: out, cancel: cancel, done: done}
	case err := <-done:
		t.Fatalf("orrery gateway returned before it bound a listener: %v", err)
	}

	return run
}

// stop ends the command, as the end of its context does, and returns what the command returned.
func (r *gatewayRun) stop() error {
	r.cancel()

	return <-r.done
}

// gatewayOutput is the stderr of a command the test starts. It sends the address of the listening line on bound, and
// keeps everything written for the test to read.
type gatewayOutput struct {
	mu    sync.Mutex
	text  strings.Builder
	bound chan string
}

// Write keeps p, and sends the address when p is the listening line.
func (o *gatewayOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.text.Write(p)
	if addr, ok := strings.CutPrefix(string(p), gatewayListeningPrefix); ok {
		select {
		case o.bound <- strings.TrimSpace(addr):
		default:
		}
	}

	return len(p), nil
}

// String returns everything written so far.
func (o *gatewayOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.text.String()
}

// postGatewayMessages posts the test's messages request to /v1/messages at addr, presenting callerKey, and returns the
// status and the body of the answer.
func postGatewayMessages(t *testing.T, addr, callerKey string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/v1/messages",
		strings.NewReader(gatewayTestMessages))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Patchbay-Key", callerKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/messages: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read answer: %v", err)
	}

	return resp.StatusCode, string(body)
}
