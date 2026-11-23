package server_test

import (
	"bytes"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// noTokenMarker is the invented token every Add in TestAccountsNoTokenAnywhere carries. No answer, error or log may
// hold it.
const noTokenMarker = "marker-token-7f3a9c"

// noTokenFingerprintPattern matches an account fingerprint: 12 lowercase hex characters.
var noTokenFingerprintPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)

// noTokenLogs is the buffer the store's logger writes to. The store may log from more than one goroutine, so its writes
// and its reads are locked.
type noTokenLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the buffer.
func (l *noTokenLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// String returns every line written so far.
func (l *noTokenLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// noTokenRegistry returns the route registry the test replaces the stored one with: a forward seat, and the context
// ctx-marker, which sends through that seat with account. An account of "" is the caller's own login.
func noTokenRegistry(account string) llmroutes.Registry {
	return llmroutes.Registry{
		DefaultContext: "ctx-marker",
		Seats: []llmroutes.Seat{
			{ID: "seat-forward", AuthMode: llmroutes.AuthForward, Upstream: llmroutes.AnthropicUpstream},
		},
		Contexts: []llmroutes.Context{{ID: "ctx-marker", DefaultSeat: "seat-forward", Account: account}},
	}
}

// noTokenCheckText fails t when where holds the marker.
func noTokenCheckText(t *testing.T, where, text string) {
	t.Helper()
	if strings.Contains(text, noTokenMarker) {
		t.Errorf("%s carries the token: %s", where, text)
	}
}

// noTokenCheckAnswer fails t when the answer msg, marshaled with protojson, holds the marker.
func noTokenCheckAnswer(t *testing.T, where string, msg proto.Message) {
	t.Helper()
	wire, err := protojson.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal %s answer: %v", where, err)
	}
	noTokenCheckText(t, where+" answer", string(wire))
}

// noTokenCheckFingerprint fails t unless fingerprint is the fingerprint of the marker: 12 lowercase hex characters that
// are not a substring of the marker.
func noTokenCheckFingerprint(t *testing.T, fingerprint string) {
	t.Helper()
	if want := store.AccountFingerprint(noTokenMarker); fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q", fingerprint, want)
	}
	if !noTokenFingerprintPattern.MatchString(fingerprint) {
		t.Fatalf("fingerprint = %q, want 12 lowercase hex characters", fingerprint)
	}
	if strings.Contains(noTokenMarker, fingerprint) {
		t.Fatalf("fingerprint %q is a substring of the token", fingerprint)
	}
}

// noTokenWantRefusal fails t unless err is a connect error with code and message. It then checks the error's text and
// each of its details for the marker.
func noTokenWantRefusal(t *testing.T, where string, err error, code connect.Code, message string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("%s: err = %v, want a connect error", where, err)
	}
	if connectErr.Code() != code || connectErr.Message() != message {
		t.Fatalf("%s: err = %v, want %v %q", where, err, code, message)
	}
	noTokenCheckText(t, where+" error", err.Error())
	for _, detail := range connectErr.Details() {
		noTokenCheckText(t, where+" error detail type", detail.Type())
		noTokenCheckText(t, where+" error detail", string(detail.Bytes()))
	}
}

// noTokenServe sends the marker through every Accounts and Routes call serve answers, accepted and refused, and checks
// every answer and error for it.
func noTokenServe(t *testing.T, accounts orreryv1connect.AccountsClient, routes orreryv1connect.RoutesClient) {
	t.Helper()
	ctx := t.Context()

	added, err := accounts.Add(ctx, connect.NewRequest(&orreryv1.AccountsAddRequest{
		Name: "Marker account", Provider: "anthropic", Plan: "marker-plan", Token: noTokenMarker,
	}))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	noTokenCheckAnswer(t, "serve Add", added.Msg)
	noTokenCheckFingerprint(t, added.Msg.GetAccount().GetFingerprint())
	id := added.Msg.GetAccount().GetId()

	_, err = accounts.Add(ctx, connect.NewRequest(&orreryv1.AccountsAddRequest{
		Provider: "anthropic", Token: noTokenMarker,
	}))
	noTokenWantRefusal(t, "serve Add without a name", err, connect.CodeInvalidArgument, "name: empty")
	_, err = accounts.Add(ctx, connect.NewRequest(&orreryv1.AccountsAddRequest{
		Name: "Marker account", Provider: "openai", Token: noTokenMarker,
	}))
	noTokenWantRefusal(t, "serve Add with a bad provider", err, connect.CodeInvalidArgument,
		"provider: want anthropic")
	_, err = accounts.Add(ctx, connect.NewRequest(&orreryv1.AccountsAddRequest{
		Name: "Marker account", Provider: "anthropic", Token: noTokenMarker + " tail",
	}))
	noTokenWantRefusal(t, "serve Add with interior whitespace in the token", err, connect.CodeInvalidArgument,
		"token: empty or holds whitespace")

	listed, err := accounts.List(ctx, connect.NewRequest(&orreryv1.AccountsListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	noTokenCheckAnswer(t, "serve List", listed.Msg)
	if got := listed.Msg.GetAccounts(); len(got) != 1 || got[0].GetId() != id {
		t.Fatalf("List answered %v, want only account %s", got, id)
	}

	renamed, err := accounts.Rename(ctx, connect.NewRequest(&orreryv1.AccountsRenameRequest{
		Id: id, Name: "Renamed marker account",
	}))
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	noTokenCheckAnswer(t, "serve Rename", renamed.Msg)
	_, err = accounts.Rename(ctx, connect.NewRequest(&orreryv1.AccountsRenameRequest{
		Id: "acct-unknown", Name: "Renamed marker account",
	}))
	noTokenWantRefusal(t, "serve Rename of an unknown account", err, connect.CodeNotFound, "account: unknown id")

	replaced, err := routes.Replace(ctx, connect.NewRequest(&orreryv1.RoutesReplaceRequest{
		Registry: llmroutes.ToProto(noTokenRegistry(id)),
	}))
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	noTokenCheckAnswer(t, "serve Replace", replaced.Msg)
	if !proto.Equal(replaced.Msg.GetRegistry(), llmroutes.ToProto(noTokenRegistry(id))) {
		t.Fatalf("Replace answered %v, want the registry naming the account", replaced.Msg.GetRegistry())
	}
	_, err = routes.Replace(ctx, connect.NewRequest(&orreryv1.RoutesReplaceRequest{
		Registry: llmroutes.ToProto(llmroutes.Registry{
			DefaultContext: "ctx-missing",
			Seats:          noTokenRegistry(id).Seats,
			Contexts:       noTokenRegistry(id).Contexts,
		}),
	}))
	noTokenWantRefusal(t, "serve Replace of an invalid registry", err, connect.CodeInvalidArgument,
		`routes: default_context "ctx-missing" does not name a context`)

	got, err := routes.Get(ctx, connect.NewRequest(&orreryv1.RoutesGetRequest{}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	noTokenCheckAnswer(t, "serve Get", got.Msg)
	if !proto.Equal(got.Msg.GetRegistry(), llmroutes.ToProto(noTokenRegistry(id))) {
		t.Fatalf("Get answered %v, want the registry naming the account", got.Msg.GetRegistry())
	}

	_, err = routes.SetContextAccount(ctx, connect.NewRequest(&orreryv1.RoutesSetContextAccountRequest{
		Context: "ctx-unknown", Account: id,
	}))
	noTokenWantRefusal(t, "serve SetContextAccount of an unknown context", err, connect.CodeNotFound,
		"context: unknown id")
	_, err = accounts.Remove(ctx, connect.NewRequest(&orreryv1.AccountsRemoveRequest{Id: id}))
	noTokenWantRefusal(t, "serve Remove of an account in use", err, connect.CodeFailedPrecondition,
		"account: in use by context ctx-marker")

	cleared, err := routes.SetContextAccount(ctx, connect.NewRequest(&orreryv1.RoutesSetContextAccountRequest{
		Context: "ctx-marker",
	}))
	if err != nil {
		t.Fatalf("SetContextAccount: %v", err)
	}
	noTokenCheckAnswer(t, "serve SetContextAccount", cleared.Msg)
	if !proto.Equal(cleared.Msg.GetRegistry(), llmroutes.ToProto(noTokenRegistry(""))) {
		t.Fatalf("SetContextAccount answered %v, want the context without an account", cleared.Msg.GetRegistry())
	}

	removed, err := accounts.Remove(ctx, connect.NewRequest(&orreryv1.AccountsRemoveRequest{Id: id}))
	if err != nil {
		t.Fatalf("Remove after clearing: %v", err)
	}
	noTokenCheckAnswer(t, "serve Remove", removed.Msg)
}

// noTokenEngine sends the marker through every Accounts RPC of orreryd's socket, accepted and refused, and checks every
// answer and error for it. It runs on the store noTokenServe left, whose registry names no account.
func noTokenEngine(t *testing.T, engine enginev1connect.AccountsClient) {
	t.Helper()
	ctx := t.Context()

	added, err := engine.AddAccount(ctx, connect.NewRequest(&enginev1.AddAccountRequest{
		Name: "Marker account", Provider: "anthropic", Plan: "marker-plan", Token: noTokenMarker,
	}))
	if err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	noTokenCheckAnswer(t, "engine AddAccount", added.Msg)
	noTokenCheckFingerprint(t, added.Msg.GetAccount().GetFingerprint())
	id := added.Msg.GetAccount().GetId()

	_, err = engine.AddAccount(ctx, connect.NewRequest(&enginev1.AddAccountRequest{
		Provider: "anthropic", Token: noTokenMarker,
	}))
	noTokenWantRefusal(t, "engine AddAccount without a name", err, connect.CodeInvalidArgument, "name: empty")
	_, err = engine.AddAccount(ctx, connect.NewRequest(&enginev1.AddAccountRequest{
		Name: "Marker account", Provider: "openai", Token: noTokenMarker,
	}))
	noTokenWantRefusal(t, "engine AddAccount with a bad provider", err, connect.CodeInvalidArgument,
		"provider: want anthropic")
	_, err = engine.AddAccount(ctx, connect.NewRequest(&enginev1.AddAccountRequest{
		Name: "Marker account", Provider: "anthropic", Token: noTokenMarker + " tail",
	}))
	noTokenWantRefusal(t, "engine AddAccount with interior whitespace in the token", err, connect.CodeInvalidArgument,
		"token: empty or holds whitespace")

	renamed, err := engine.RenameAccount(ctx, connect.NewRequest(&enginev1.RenameAccountRequest{
		Id: id, Name: "Renamed marker account",
	}))
	if err != nil {
		t.Fatalf("RenameAccount: %v", err)
	}
	noTokenCheckAnswer(t, "engine RenameAccount", renamed.Msg)
	_, err = engine.RenameAccount(ctx, connect.NewRequest(&enginev1.RenameAccountRequest{
		Id: "acct-unknown", Name: "Renamed marker account",
	}))
	noTokenWantRefusal(t, "engine RenameAccount of an unknown account", err, connect.CodeNotFound,
		"account: unknown id")

	replaced, err := engine.ReplaceRoutes(ctx, connect.NewRequest(&enginev1.ReplaceRoutesRequest{
		Registry: llmroutes.ToProto(noTokenRegistry(id)),
	}))
	if err != nil {
		t.Fatalf("ReplaceRoutes: %v", err)
	}
	noTokenCheckAnswer(t, "engine ReplaceRoutes", replaced.Msg)
	if !proto.Equal(replaced.Msg.GetRegistry(), llmroutes.ToProto(noTokenRegistry(id))) {
		t.Fatalf("ReplaceRoutes answered %v, want the registry naming the account", replaced.Msg.GetRegistry())
	}
	_, err = engine.ReplaceRoutes(ctx, connect.NewRequest(&enginev1.ReplaceRoutesRequest{
		Registry: llmroutes.ToProto(llmroutes.Registry{
			DefaultContext: "ctx-missing",
			Seats:          noTokenRegistry(id).Seats,
			Contexts:       noTokenRegistry(id).Contexts,
		}),
	}))
	noTokenWantRefusal(t, "engine ReplaceRoutes of an invalid registry", err, connect.CodeInvalidArgument,
		`routes: default_context "ctx-missing" does not name a context`)

	_, err = engine.SetContextAccount(ctx, connect.NewRequest(&enginev1.SetContextAccountRequest{
		Context: "ctx-unknown", Account: id,
	}))
	noTokenWantRefusal(t, "engine SetContextAccount of an unknown context", err, connect.CodeNotFound,
		"context: unknown id")
	_, err = engine.RemoveAccount(ctx, connect.NewRequest(&enginev1.RemoveAccountRequest{Id: id}))
	noTokenWantRefusal(t, "engine RemoveAccount of an account in use", err, connect.CodeFailedPrecondition,
		"account: in use by context ctx-marker")

	cleared, err := engine.SetContextAccount(ctx, connect.NewRequest(&enginev1.SetContextAccountRequest{
		Context: "ctx-marker",
	}))
	if err != nil {
		t.Fatalf("SetContextAccount: %v", err)
	}
	noTokenCheckAnswer(t, "engine SetContextAccount", cleared.Msg)
	if !proto.Equal(cleared.Msg.GetRegistry(), llmroutes.ToProto(noTokenRegistry(""))) {
		t.Fatalf("SetContextAccount answered %v, want the context without an account", cleared.Msg.GetRegistry())
	}

	removed, err := engine.RemoveAccount(ctx, connect.NewRequest(&enginev1.RemoveAccountRequest{Id: id}))
	if err != nil {
		t.Fatalf("RemoveAccount after clearing: %v", err)
	}
	noTokenCheckAnswer(t, "engine RemoveAccount", removed.Msg)
}

// TestAccountsNoTokenAnywhere sends a marker token through every Accounts and Routes call on serve, and every Accounts
// RPC on orreryd's socket, accepted and refused. It fails on any answer, error or captured log line that holds the
// marker, and checks that the fingerprint is the short hash of the marker.
func TestAccountsNoTokenAnywhere(t *testing.T) {
	t.Parallel()
	logs := &noTokenLogs{}
	h := newAccountsHarness(t, store.WithLogger(slog.New(slog.NewTextHandler(logs, nil))))
	noTokenServe(t, h.Accounts, h.Routes)
	noTokenEngine(t, h.Engine)
	noTokenCheckText(t, "captured log", logs.String())
}
