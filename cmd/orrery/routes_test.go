package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// routesTestAccount is the id of the invented account the routes import tests store routes under. Its token is invented
// and never leaves the store.
const (
	routesTestAccount = "acct-synthetic"
	routesTestToken   = "sk-ant-oat01-synthetic-token"
)

// routesImportEveryField is a routes file that uses every field orrery stores. Its seats and contexts are not in id
// order, so the import shows that they are stored in id order.
const routesImportEveryField = `{
  "default_context": "ctx-main",
  "seats": {
    "seat-inject": {
      "auth_mode": "inject", "upstream": "http://inject.invalid:8080", "model": "synthetic-model",
      "api_key_env_file": "SYNTHETIC_API_KEY", "billing": "metered", "speed": "fast",
      "insecure": true, "strip_cache_control": true
    },
    "seat-forward": {
      "auth_mode": "forward", "upstream": "https://api.anthropic.com", "model": "synthetic-forward-model",
      "cache_ttl": "1h", "max_input_tokens": 200000, "strict_structured_output": true
    },
    "seat-batch": {
      "auth_mode": "sigv4", "aws_profile": "synthetic-profile", "aws_region": "us-test-1",
      "model_map_env_file": "SYNTHETIC_MODEL_MAP", "max_input_tokens": 1000, "strip_betas": ["synthetic-beta"]
    }
  },
  "contexts": {
    "ctx-main": {
      "default_seat": "seat-forward",
      "account": "acct-synthetic",
      "models": {"claude-synthetic-model": "seat-forward", "synthetic-model": "seat-inject"},
      "subagents": {
        "default_seat": "seat-inject",
        "agent_types": {"synthetic-agent": "seat-batch"},
        "models": {"synthetic-subagent-model": "seat-forward"}
      }
    },
    "ctx-batch": {"default_seat": "seat-batch", "models": {"synthetic-batch-model": "seat-batch"}}
  }
}`

// routesTestNow is the fixed clock the Accounts service reads as now.
func routesTestNow() time.Time { return time.Date(2026, time.November, 24, 9, 0, 0, 0, time.UTC) }

// routesTestBaseline is the registry each test stores first, so a refused or replaced import has routes to keep or
// lose: one forward seat, and the context that routes through it with no account.
func routesTestBaseline() llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "ctx-base",
		Seats: []llmroutes.Seat{
			{ID: "seat-base", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
		},
		Contexts: []llmroutes.Context{
			{ID: "ctx-base", DefaultSeat: "seat-base", Models: map[string]string{}},
		},
	}
}

// routesImportEveryFieldWant is the registry routesImportEveryField stores.
func routesImportEveryFieldWant() llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "ctx-main",
		Seats: []llmroutes.Seat{
			{
				ID: "seat-batch", AuthMode: llmroutes.AuthSigV4, AWSProfile: "synthetic-profile",
				AWSRegion: "us-test-1", ModelMapEnvFile: "SYNTHETIC_MODEL_MAP", MaxInputTokens: 1000,
				StripBetas: []string{"synthetic-beta"},
			},
			{
				ID: "seat-forward", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream,
				Model: "synthetic-forward-model", CacheTTL: "1h", MaxInputTokens: 200000,
				StrictStructuredOutput: true,
			},
			{
				ID: "seat-inject", AuthMode: llmroutes.AuthInject, Upstream: "http://inject.invalid:8080",
				Model: "synthetic-model", APIKeyEnvFile: "SYNTHETIC_API_KEY", Billing: "metered", Speed: "fast",
				Insecure: true, StripCacheControl: true,
			},
		},
		Contexts: []llmroutes.Context{
			{
				ID: "ctx-batch", DefaultSeat: "seat-batch",
				Models: map[string]string{"synthetic-batch-model": "seat-batch"},
			},
			{
				ID: "ctx-main", DefaultSeat: "seat-forward", Account: routesTestAccount,
				Models: map[string]string{"claude-synthetic-model": "seat-forward", "synthetic-model": "seat-inject"},
				Subagents: &llmroutes.Subagents{
					DefaultSeat: "seat-inject",
					AgentTypes:  map[string]string{"synthetic-agent": "seat-batch"},
					Models:      map[string]string{"synthetic-subagent-model": "seat-forward"},
				},
			},
		},
	}
}

// startRoutesRig opens a temp store that holds the invented account, and serves its Accounts service on a session
// socket in a temp directory. It returns the store, and the daemon socket path that `--socket` names, beside which the
// session socket lies. The serving and the store end at cleanup.
func startRoutesRig(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orrery.db")
	storetest.Seed(t, dbPath)
	st, err := store.Open(t.Context(), dbPath, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store Close: %v", err)
		}
	})
	if _, err = st.AddAccount(t.Context(), store.NewAccount{
		ID: routesTestAccount, Name: "Synthetic account", Provider: "anthropic", Token: routesTestToken,
		Created: routesTestNow().Unix(),
	}); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}

	ln, err := enginehost.Listen(filepath.Join(dir, "sessions.sock"))
	if err != nil {
		t.Fatalf("enginehost.Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	accounts := &enginehost.Accounts{Store: st, Now: routesTestNow}
	go func() { done <- enginehost.ServeWith(ctx, ln, enginehost.ServeConfig{Accounts: accounts}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith after cleanup: %v", err)
		}
	})
	return st, filepath.Join(dir, "orreryd.sock")
}

// writeRoutesFile writes doc to a file in a temp directory, and returns its path.
func writeRoutesFile(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatalf("write routes file: %v", err)
	}
	return path
}

// runRoutesCLI runs `orrery --socket DAEMON routes ARGS...` and returns its exit code and what it wrote to stdout and
// stderr.
func runRoutesCLI(t *testing.T, daemon string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	argv := append([]string{"--socket", daemon, "routes"}, args...)
	code := run(argv, nil, &stdout, &stderr, envFrom(nil))
	return code, stdout.String(), stderr.String()
}

// checkStoredRoutes fails the test unless the store's routes, as Routes reads them, equal want.
func checkStoredRoutes(t *testing.T, st *store.Store, want llmroutes.Registry) {
	t.Helper()
	got, err := st.Routes(t.Context())
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	if !proto.Equal(llmroutes.ToProto(got), llmroutes.ToProto(want)) {
		t.Fatalf("stored routes = %v, want %v", llmroutes.ToProto(got), llmroutes.ToProto(want))
	}
}

// TestRoutesImportStoresEveryStoredField checks that importing a routes file that uses every stored field replaces the
// stored routes with its registry, and prints the seat and context counts the store holds.
func TestRoutesImportStoresEveryStoredField(t *testing.T) {
	t.Parallel()
	st, daemon := startRoutesRig(t)
	if err := st.ReplaceRoutes(t.Context(), routesTestBaseline()); err != nil {
		t.Fatalf("ReplaceRoutes baseline: %v", err)
	}
	file := writeRoutesFile(t, routesImportEveryField)

	code, stdout, stderr := runRoutesCLI(t, daemon, "import", file)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr %q", code, exitOK, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	if want := "stored 3 seats and 2 contexts\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	checkStoredRoutes(t, st, routesImportEveryFieldWant())
}

// TestRoutesImportRefusesUnknownAccountAndKeepsRoutes checks that a file whose context names an account the store does
// not hold is refused, prints the refusal and exits non-zero, and leaves the stored routes as they were.
func TestRoutesImportRefusesUnknownAccountAndKeepsRoutes(t *testing.T) {
	t.Parallel()
	st, daemon := startRoutesRig(t)
	if err := st.ReplaceRoutes(t.Context(), routesTestBaseline()); err != nil {
		t.Fatalf("ReplaceRoutes baseline: %v", err)
	}
	file := writeRoutesFile(t, `{"default_context":"ctx-new",`+
		`"seats":{"seat-new":{"auth_mode":"forward","upstream":"https://api.anthropic.com"}},`+
		`"contexts":{"ctx-new":{"default_seat":"seat-new","account":"acct-missing","models":{}}}}`)

	code, stdout, stderr := runRoutesCLI(t, daemon, "import", file)
	if code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	if want := `account "acct-missing" is not one of the accounts`; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to name the refusal %q", stderr, want)
	}
	checkStoredRoutes(t, st, routesTestBaseline())
}

// TestRoutesImportRefusesUnstoredFieldAndKeepsRoutes checks that a file naming a field orrery does not store is refused
// before anything is sent, names the field, exits non-zero, and leaves the stored routes as they were.
func TestRoutesImportRefusesUnstoredFieldAndKeepsRoutes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		field, value string
	}{
		{field: "extra_body", value: `{"metadata":{}}`},
		{field: "max_output_tokens", value: `4096`},
		{field: "capture_bodies", value: `true`},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			st, daemon := startRoutesRig(t)
			if err := st.ReplaceRoutes(t.Context(), routesTestBaseline()); err != nil {
				t.Fatalf("ReplaceRoutes baseline: %v", err)
			}
			file := writeRoutesFile(t, `{"default_context":"ctx-new",`+
				`"seats":{"seat-new":{"auth_mode":"forward","upstream":"https://api.anthropic.com","`+
				tc.field+`":`+tc.value+`}},`+
				`"contexts":{"ctx-new":{"default_seat":"seat-new","models":{}}}}`)

			code, stdout, stderr := runRoutesCLI(t, daemon, "import", file)
			if code != exitFailure {
				t.Fatalf("exit code = %d, want %d", code, exitFailure)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			if !strings.Contains(stderr, tc.field) {
				t.Errorf("stderr = %q, want it to name %q", stderr, tc.field)
			}
			checkStoredRoutes(t, st, routesTestBaseline())
		})
	}
}

// TestRoutesImportRequiresImportAndFile checks that `orrery routes` with anything but `import FILE` is a usage error,
// which exits 2 and calls nothing, so no daemon socket is dialed.
func TestRoutesImportRequiresImportAndFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
	}{
		{name: "no verb"},
		{name: "unknown verb", args: []string{"list"}},
		{name: "no file", args: []string{"import"}},
		{name: "two files", args: []string{"import", "a.json", "b.json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			daemon := filepath.Join(t.TempDir(), "orreryd.sock")
			code, stdout, stderr := runRoutesCLI(t, daemon, tc.args...)
			if code != exitUsage {
				t.Fatalf("exit code = %d, want %d; stderr %q", code, exitUsage, stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			if want := "routes: want import FILE"; !strings.Contains(stderr, want) {
				t.Errorf("stderr = %q, want it to name the form %q", stderr, want)
			}
		})
	}
}
