package node

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holderhost"
)

// testSecret is the fleet secret that the agents and clients of these tests share, and otherSecret is one they do not.
const (
	testSecret  = "fleet-secret-for-tests"
	otherSecret = "fleet-secret-of-another-fleet"
)

// errFakeLaunch is the error that launchCounter's Launch returns, so that a test can tell a launch that reached the
// holders from a launch the agent refused.
var errFakeLaunch = errors.New("fake holders refuse the launch")

// launchCounter is the Holders of the secret tests. Its Launch counts each call and refuses it with errFakeLaunch. Its
// other ops are not called.
type launchCounter struct {
	holderhost.Host
	launches atomic.Int64
}

// Launch counts the call, and refuses it with errFakeLaunch.
func (h *launchCounter) Launch(context.Context, holderhost.Spec) (*session.Session, error) {
	h.launches.Add(1)
	return nil, errFakeLaunch
}

// syncBuffer is a bytes.Buffer that the agent's goroutines write to and a test reads, so each access holds its lock.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the buffer.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns what the buffer holds.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// writeSecretFile writes content to a new file of mode, and returns its path. The mode is set exactly, whatever the umask.
func writeSecretFile(t *testing.T, mode fs.FileMode, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReadSecret reads a fleet secret from a file. A good file yields its secret, with the whitespace around it trimmed.
// A file that is missing, empty, or readable by its group is refused, and the refusal names the file.
func TestReadSecret(t *testing.T) {
	t.Run("good file", func(t *testing.T) {
		path := writeSecretFile(t, 0o600, testSecret)
		got, err := ReadSecret(path)
		if err != nil {
			t.Fatalf("read the secret: %v", err)
		}
		if got != testSecret {
			t.Fatalf("secret = %q, want %q", got, testSecret)
		}
	})
	t.Run("trailing whitespace", func(t *testing.T) {
		path := writeSecretFile(t, 0o600, " \t"+testSecret+" \n\n")
		got, err := ReadSecret(path)
		if err != nil {
			t.Fatalf("read the secret: %v", err)
		}
		if got != testSecret {
			t.Fatalf("secret = %q, want %q with its whitespace trimmed", got, testSecret)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing")
		got, err := ReadSecret(path)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("read a missing secret: err = %v, want one that matches fs.ErrNotExist", err)
		}
		if got != "" || !strings.Contains(err.Error(), path) {
			t.Fatalf("read a missing secret = %q, %v, want no secret and an error that names %s", got, err, path)
		}
	})
	t.Run("empty file", func(t *testing.T) {
		path := writeSecretFile(t, 0o600, " \n")
		got, err := ReadSecret(path)
		if err == nil || got != "" || !strings.Contains(err.Error(), path) {
			t.Fatalf("read an empty secret = %q, %v, want no secret and an error that names %s", got, err, path)
		}
	})
	t.Run("group-readable file", func(t *testing.T) {
		path := writeSecretFile(t, 0o640, testSecret)
		got, err := ReadSecret(path)
		if err == nil || got != "" || !strings.Contains(err.Error(), path) {
			t.Fatalf("read a group-readable secret = %q, %v, want no secret and an error that names %s", got, err, path)
		}
	})
}

// startSecretAgent serves an Agent with holders, and the fleet secret testSecret, on a loopback listener until the test
// ends. Its log goes to logs. It returns the listener's address.
func startSecretAgent(t *testing.T, holders holderhost.Host, logs *syncBuffer) string {
	t.Helper()
	ln := listenLoopback(t)
	agent, err := NewAgent(AgentConfig{
		Controller: testController,
		WhoIs:      peerWhoIs{own: ln.Addr().String()},
		Holders:    holders,
		StateDir:   t.TempDir(),
		Secret:     testSecret,
		Logger:     slog.New(slog.NewTextHandler(logs, nil)),
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

// launchRequest is a Request that launches the session s1 in a directory of its own, under secret.
func launchRequest(t *testing.T, secret string) Request {
	t.Helper()
	return Request{Version: Version, Secret: secret, Op: OpLaunch, Session: "s1", Launch: &LaunchSpec{Dir: t.TempDir()}}
}

// TestAgentSecret checks the fleet secret that an agent admits. A call with the right secret is answered. A call with no
// secret or another one is refused before its op runs, so the launch never reaches the holders. An agent with no secret
// is not built.
func TestAgentSecret(t *testing.T) {
	t.Run("ping with the right secret is answered", func(t *testing.T) {
		addr := startSecretAgent(t, &launchCounter{}, &syncBuffer{})
		if resp := ask(t, addr, Request{Version: Version, Secret: testSecret, Op: OpPing}); resp.Code != "" {
			t.Fatalf("ping with the right secret = %+v, want no code", resp)
		}
	})
	t.Run("launch with the right secret is answered", func(t *testing.T) {
		holders := &launchCounter{}
		addr := startSecretAgent(t, holders, &syncBuffer{})
		resp := ask(t, addr, launchRequest(t, testSecret))
		if resp.Code != CodeFailed || resp.Message != errFakeLaunch.Error() {
			t.Fatalf("launch with the right secret = %+v, want the holders' refusal %q", resp, errFakeLaunch)
		}
		if n := holders.launches.Load(); n != 1 {
			t.Fatalf("holders got %d launches for the right secret, want 1", n)
		}
	})
	for _, tc := range []struct {
		name   string
		secret string
	}{
		{name: "no secret", secret: ""},
		{name: "another secret", secret: otherSecret},
	} {
		t.Run("launch with "+tc.name+" is refused", func(t *testing.T) {
			holders := &launchCounter{}
			logs := &syncBuffer{}
			addr := startSecretAgent(t, holders, logs)
			resp, reader, _ := openStream(t, addr, launchRequest(t, tc.secret))
			if resp.Code != CodeFailed || !strings.Contains(resp.Message, "fleet secret") {
				t.Fatalf("launch with %s = %+v, want code %q that names the fleet secret", tc.name, resp, CodeFailed)
			}
			if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
				t.Fatalf("after the refusal the connection read %v, want it closed", err)
			}
			if n := holders.launches.Load(); n != 0 {
				t.Fatalf("holders got %d launches for %s, want 0", n, tc.name)
			}
			if !strings.Contains(logs.String(), "refused a request without the fleet secret") {
				t.Fatalf("the refusal is not logged: %q", logs.String())
			}
			for _, secret := range []string{testSecret, otherSecret} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("the refusal logged the secret %q", secret)
				}
			}
		})
	}
	t.Run("NewAgent refuses an empty secret", func(t *testing.T) {
		_, err := NewAgent(AgentConfig{
			Controller: testController,
			WhoIs:      peerWhoIs{},
			Holders:    &launchCounter{},
		})
		if err == nil || !strings.Contains(err.Error(), "secret") {
			t.Fatalf("NewAgent with no secret: err = %v, want an error that names the secret", err)
		}
	})
}
