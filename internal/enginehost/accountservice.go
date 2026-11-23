package enginehost

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/llmroutes"
	"github.com/joshsymonds/orrery/internal/store"
)

// The messages the Accounts service refuses with. A refusal names the field that is wrong, or the store's state, and
// never a token or the store's own error text.
const (
	accountUnavailableMessage = "account store unavailable"
	accountStoreMessage       = "account store failed"
	accountNameMessage        = "name: empty"
	accountProviderMessage    = "provider: want anthropic"
	accountUnknownMessage     = "account: unknown id"
	accountUnknownContext     = "context: unknown id"
	accountInUseMessage       = "account: in use"
	accountInUsePrefix        = "account: in use by context "
	routesRefusedPrefix       = "routes: "
)

// accountProvider is the one provider an account may be with.
const accountProvider = "anthropic"

// accountIDBytes is how many bytes of a digest a minted account id keeps. Hex encoding them gives 32 digits.
const accountIDBytes = 16

// AccountStore is the set of account and route operations the Accounts service answers with. *store.Store satisfies it.
type AccountStore interface {
	AddAccount(ctx context.Context, account store.NewAccount) (store.Account, error)
	RenameAccount(ctx context.Context, id, name string) (store.Account, error)
	RemoveAccount(ctx context.Context, id string) error
	ReplaceRoutes(ctx context.Context, reg llmroutes.Registry) error
	SetContextAccount(ctx context.Context, contextID, account string) error
	Routes(ctx context.Context) (llmroutes.Registry, error)
}

// Accounts is what orreryd's Accounts service answers with. Store holds the accounts and the route registry. Now is the
// clock an account's creation time is read from, and must be set. NewID mints an account's id; nil mints 32 lowercase
// hex digits from crypto/rand.
type Accounts struct {
	Store AccountStore
	Now   func() time.Time
	NewID func() string
}

// accountService implements the Accounts RPCs over accounts. A nil accounts answers every RPC as unavailable.
type accountService struct {
	accounts *Accounts
}

// AddAccount stores an account from the request's name, provider and plan, with its token trimmed of surrounding
// whitespace, created at the clock's time. It answers the account as stored, which holds no token.
func (s *accountService) AddAccount(
	ctx context.Context,
	req *connect.Request[v1.AddAccountRequest],
) (*connect.Response[v1.AddAccountResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := checkAccountName(req.Msg.GetName()); err != nil {
		return nil, err
	}
	if req.Msg.GetProvider() != accountProvider {
		return nil, refuseEpic(connect.CodeInvalidArgument, accountProviderMessage)
	}
	token := strings.TrimSpace(req.Msg.GetToken())
	if token == "" || strings.ContainsFunc(token, unicode.IsSpace) {
		return nil, refuseEpic(connect.CodeInvalidArgument, "token: empty or holds whitespace")
	}
	account, err := s.accounts.Store.AddAccount(ctx, store.NewAccount{
		ID:       s.accounts.accountID(),
		Name:     req.Msg.GetName(),
		Provider: accountProvider,
		Plan:     req.Msg.GetPlan(),
		Token:    token,
		Created:  s.accounts.Now().Unix(),
	})
	if err != nil {
		return nil, accountStoreError(err)
	}
	return connect.NewResponse(&v1.AddAccountResponse{Account: accountOf(account)}), nil
}

// RenameAccount gives the account named by the request's id the request's name, and answers the account as it stands.
func (s *accountService) RenameAccount(
	ctx context.Context,
	req *connect.Request[v1.RenameAccountRequest],
) (*connect.Response[v1.RenameAccountResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := checkAccountName(req.Msg.GetName()); err != nil {
		return nil, err
	}
	account, err := s.accounts.Store.RenameAccount(ctx, req.Msg.GetId(), req.Msg.GetName())
	if err != nil {
		return nil, accountStoreError(err)
	}
	return connect.NewResponse(&v1.RenameAccountResponse{Account: accountOf(account)}), nil
}

// RemoveAccount deletes the account named by the request's id. A route context that names the account holds it, so
// its removal is refused.
func (s *accountService) RemoveAccount(
	ctx context.Context,
	req *connect.Request[v1.RemoveAccountRequest],
) (*connect.Response[v1.RemoveAccountResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	err := s.accounts.Store.RemoveAccount(ctx, req.Msg.GetId())
	if errors.Is(err, store.ErrAccountInUse) {
		return nil, s.inUseRefusal(ctx, req.Msg.GetId())
	}
	if err != nil {
		return nil, accountStoreError(err)
	}
	return connect.NewResponse(&v1.RemoveAccountResponse{}), nil
}

// ReplaceRoutes stores the request's route registry in place of the one stored, and answers the registry as stored. A
// registry the gateway refuses is an InvalidArgument that carries the validation error.
func (s *accountService) ReplaceRoutes(
	ctx context.Context,
	req *connect.Request[v1.ReplaceRoutesRequest],
) (*connect.Response[v1.ReplaceRoutesResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	err := s.accounts.Store.ReplaceRoutes(ctx, llmroutes.FromProto(req.Msg.GetRegistry()))
	if errors.Is(err, store.ErrRoutesRefused) {
		// The store wraps the validation error after ErrRoutesRefused and a colon, so the rest of its text is that error.
		detail := strings.TrimPrefix(err.Error(), store.ErrRoutesRefused.Error()+": ")
		return nil, refuseEpic(connect.CodeInvalidArgument, routesRefusedPrefix+detail)
	}
	if err != nil {
		return nil, accountStoreError(err)
	}
	reg, err := s.storedRoutes(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.ReplaceRoutesResponse{Registry: reg}), nil
}

// SetContextAccount sets the account the request's context's forward seats send, and answers the registry as it stands.
// An empty account makes the context send its caller's own login.
func (s *accountService) SetContextAccount(
	ctx context.Context,
	req *connect.Request[v1.SetContextAccountRequest],
) (*connect.Response[v1.SetContextAccountResponse], error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	err := s.accounts.Store.SetContextAccount(ctx, req.Msg.GetContext(), req.Msg.GetAccount())
	if errors.Is(err, store.ErrNoRouteContext) {
		return nil, refuseEpic(connect.CodeNotFound, accountUnknownContext)
	}
	if err != nil {
		return nil, accountStoreError(err)
	}
	reg, err := s.storedRoutes(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&v1.SetContextAccountResponse{Registry: reg}), nil
}

// ready returns the refusal every Accounts RPC gets when there is no account store.
func (s *accountService) ready() error {
	if s.accounts == nil {
		return refuseEpic(connect.CodeUnavailable, accountUnavailableMessage)
	}
	return nil
}

// inUseRefusal answers a removal the store refused because a route context names the account. It names the first such
// context in id order, which the route registry reads in. The context can change between the two reads, so when none
// names the account any more, the refusal says only that the account is in use.
func (s *accountService) inUseRefusal(ctx context.Context, id string) error {
	reg, err := s.accounts.Store.Routes(ctx)
	if err != nil {
		return accountStoreError(err)
	}
	for _, route := range reg.Contexts {
		if route.Account == id {
			return refuseEpic(connect.CodeFailedPrecondition, accountInUsePrefix+route.ID)
		}
	}
	return refuseEpic(connect.CodeFailedPrecondition, accountInUseMessage)
}

// storedRoutes returns the route registry the store holds, in its protobuf form.
func (s *accountService) storedRoutes(ctx context.Context) (*orreryv1.RouteRegistry, error) {
	reg, err := s.accounts.Store.Routes(ctx)
	if err != nil {
		return nil, accountStoreError(err)
	}
	return llmroutes.ToProto(reg), nil
}

// accountID returns the id a new account is stored under: what NewID mints, or a fresh id when NewID is nil.
func (a *Accounts) accountID() string {
	if a.NewID != nil {
		return a.NewID()
	}
	return mintAccountID()
}

// mintAccountID returns 32 lowercase hex digits. rand.Text reads crypto/rand and never fails, so its text is hashed and
// the digest's first accountIDBytes are kept, which holds the 128 random bits rand.Text carries.
func mintAccountID() string {
	sum := sha256.Sum256([]byte(rand.Text()))
	return hex.EncodeToString(sum[:accountIDBytes])
}

// checkAccountName refuses a name that is empty or only whitespace.
func checkAccountName(name string) error {
	if strings.TrimSpace(name) == "" {
		return refuseEpic(connect.CodeInvalidArgument, accountNameMessage)
	}
	return nil
}

// accountStoreError maps an error from the account store to its refusal: NotFound for an account the store does not
// hold, and otherwise Internal with a fixed message, so the store's text is never sent.
func accountStoreError(err error) error {
	if errors.Is(err, store.ErrNoAccount) {
		return refuseEpic(connect.CodeNotFound, accountUnknownMessage)
	}
	return refuseEpic(connect.CodeInternal, accountStoreMessage)
}

// accountOf returns the protobuf form of account, which holds no token.
func accountOf(account store.Account) *orreryv1.Account {
	return &orreryv1.Account{
		Id:          account.ID,
		Name:        account.Name,
		Provider:    account.Provider,
		Plan:        account.Plan,
		Fingerprint: account.Fingerprint,
		Created:     account.Created,
	}
}
