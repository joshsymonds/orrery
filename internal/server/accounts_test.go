package server_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// accountsEngineTime is the fixed clock of the engine Accounts service, in unix seconds. It dates each account added.
const accountsEngineTime int64 = 1_700_000_600

// accountsHarness holds the clients of one serve that forwards its account commands to an engine Accounts service over
// one store: Accounts and Routes are serve's clients, Engine is the engine's own client, and Store is the store behind
// both.
type accountsHarness struct {
	Accounts orreryv1connect.AccountsClient
	Routes   orreryv1connect.RoutesClient
	Engine   enginev1connect.AccountsClient
	Store    *store.Store
}

// newAccountsHarness opens a seeded store at a temporary path, closed when the test ends, with storetest.NoSync() and
// opts. It serves the engine Accounts service over that store on a loopback listener until the test ends, with the
// fixed engine clock. It then serves the handler of a server whose Sessions reads the store and whose AccountCommands
// is the engine client, and returns clients of its Accounts and Routes services, which carry the allowed login and the
// client header.
func newAccountsHarness(t *testing.T, opts ...store.Option) accountsHarness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, append([]store.Option{storetest.NoSync()}, opts...)...)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	accounts := &enginehost.Accounts{
		Store: st,
		Now:   func() time.Time { return time.Unix(accountsEngineTime, 0) },
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- enginehost.ServeWith(ctx, ln, enginehost.ServeConfig{Accounts: accounts}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith: %v", err)
		}
	})
	engine := enginev1connect.NewAccountsClient(http.DefaultClient, "http://"+ln.Addr().String())

	handler, err := server.New(server.Config{
		Logins:          []string{allowedLogin},
		Tmux:            newTmuxClient(t),
		Push:            newPushStore(t),
		Sessions:        store.NewReader(path),
		AccountCommands: engine,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return accountsHarness{
		Accounts: orreryv1connect.NewAccountsClient(httpClient, srv.URL),
		Routes:   orreryv1connect.NewRoutesClient(httpClient, srv.URL),
		Engine:   engine,
		Store:    st,
	}
}

// listAccountsOf lists the accounts through h's Accounts client and fails t unless it answers.
func listAccountsOf(t *testing.T, h accountsHarness) *orreryv1.AccountsListResponse {
	t.Helper()
	listed, err := h.Accounts.List(t.Context(), connect.NewRequest(&orreryv1.AccountsListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return listed.Msg
}

func TestAccountsRoundTripThroughServe(t *testing.T) {
	t.Parallel()
	h := newAccountsHarness(t)
	const token = "synthetic-token-roundtrip"
	added, err := h.Accounts.Add(t.Context(), connect.NewRequest(&orreryv1.AccountsAddRequest{
		Name: "Synthetic account", Provider: "anthropic", Plan: "synthetic-plan", Token: token,
	}))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	want := &orreryv1.Account{
		Id:          added.Msg.GetAccount().GetId(),
		Name:        "Synthetic account",
		Provider:    "anthropic",
		Plan:        "synthetic-plan",
		Fingerprint: store.AccountFingerprint(token),
		Created:     accountsEngineTime,
	}
	if !proto.Equal(added.Msg.GetAccount(), want) {
		t.Fatalf("Add answered %v, want %v", added.Msg.GetAccount(), want)
	}

	listed := listAccountsOf(t, h)
	if !proto.Equal(&orreryv1.AccountsListResponse{Accounts: []*orreryv1.Account{want}}, listed) {
		t.Fatalf("List answered %v, want the added account %v", listed, want)
	}
	wire, err := protojson.Marshal(listed)
	if err != nil {
		t.Fatalf("Marshal List: %v", err)
	}
	if strings.Contains(string(wire), token) {
		t.Fatalf("List carries the token: %s", wire)
	}

	want.Name = "Renamed account"
	renamed, err := h.Accounts.Rename(t.Context(), connect.NewRequest(&orreryv1.AccountsRenameRequest{
		Id: want.GetId(), Name: want.GetName(),
	}))
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if !proto.Equal(renamed.Msg.GetAccount(), want) {
		t.Fatalf("Rename answered %v, want %v", renamed.Msg.GetAccount(), want)
	}
	listed = listAccountsOf(t, h)
	if !proto.Equal(&orreryv1.AccountsListResponse{Accounts: []*orreryv1.Account{want}}, listed) {
		t.Fatalf("List after rename answered %v, want %v", listed, want)
	}

	if _, err = h.Accounts.Remove(t.Context(), connect.NewRequest(&orreryv1.AccountsRemoveRequest{
		Id: want.GetId(),
	})); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if listed = listAccountsOf(t, h); len(listed.GetAccounts()) != 0 {
		t.Fatalf("List after remove answered %v, want no accounts", listed.GetAccounts())
	}
}
