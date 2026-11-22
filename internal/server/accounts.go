package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/rpcretry"
	"github.com/joshsymonds/orrery/internal/store"
)

// accountStoreUnavailable is the fixed message for an account or route read the store cannot answer.
const accountStoreUnavailable = "account store unavailable"

// accountCommandsUnavailable is the fixed message for an account command when serve has no account command client.
const accountCommandsUnavailable = "account command service unavailable"

// accountsReadMessage is the internal message of an account listing the store fails to read.
const accountsReadMessage = "read accounts"

// accountsServer answers orrery.v1.Accounts. List reads the store reader, and a nil reader answers it unavailable. Add,
// Rename and Remove forward to commands with no caller headers, so orreryd records the owner as the one who made each
// change. A nil commands answers them unavailable. Each forwarded call runs under timeout and is retried only while
// rpcretry.NeverConnected accepts its error, as the contract commands are.
type accountsServer struct {
	reader   *store.Reader
	commands enginev1connect.AccountsClient
	timeout  time.Duration
	clock    rpcretry.Clock
}

// List answers every stored account, created ascending and then by id, with no token.
func (a accountsServer) List(
	ctx context.Context,
	_ *connect.Request[orreryv1.AccountsListRequest],
) (*connect.Response[orreryv1.AccountsListResponse], error) {
	if a.reader == nil {
		return nil, listError(connect.CodeUnavailable, accountStoreUnavailable)
	}
	stored, err := a.reader.Accounts(ctx)
	if err != nil {
		return nil, accountReadError(err, accountsReadMessage)
	}
	accounts := make([]*orreryv1.Account, 0, len(stored))
	for _, account := range stored {
		accounts = append(accounts, accountMessage(account))
	}
	return connect.NewResponse(&orreryv1.AccountsListResponse{Accounts: accounts}), nil
}

// Add forwards the new account's name, provider, plan and token to orreryd, and answers the account as orreryd stored
// it, which holds no token.
func (a accountsServer) Add(
	ctx context.Context,
	req *connect.Request[orreryv1.AccountsAddRequest],
) (*connect.Response[orreryv1.AccountsAddResponse], error) {
	name, provider, plan, token := req.Msg.GetName(), req.Msg.GetProvider(), req.Msg.GetPlan(), req.Msg.GetToken()
	var reply *connect.Response[enginev1.AddAccountResponse]
	err := forwardAccountCommand(ctx, a.commands, a.timeout, a.clock,
		func(ctx context.Context, commands enginev1connect.AccountsClient) error {
			var err error
			reply, err = commands.AddAccount(ctx, connect.NewRequest(&enginev1.AddAccountRequest{
				Name: name, Provider: provider, Plan: plan, Token: token,
			}))
			return wrapEngine("add account", err)
		})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.AccountsAddResponse{Account: reply.Msg.GetAccount()}), nil
}

// Rename forwards the rename of the named account to orreryd, and answers the account as orreryd stored it.
func (a accountsServer) Rename(
	ctx context.Context,
	req *connect.Request[orreryv1.AccountsRenameRequest],
) (*connect.Response[orreryv1.AccountsRenameResponse], error) {
	id, name := req.Msg.GetId(), req.Msg.GetName()
	var reply *connect.Response[enginev1.RenameAccountResponse]
	err := forwardAccountCommand(ctx, a.commands, a.timeout, a.clock,
		func(ctx context.Context, commands enginev1connect.AccountsClient) error {
			var err error
			reply, err = commands.RenameAccount(ctx, connect.NewRequest(&enginev1.RenameAccountRequest{
				Id: id, Name: name,
			}))
			return wrapEngine("rename account", err)
		})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.AccountsRenameResponse{Account: reply.Msg.GetAccount()}), nil
}

// Remove forwards the removal of the named account to orreryd.
func (a accountsServer) Remove(
	ctx context.Context,
	req *connect.Request[orreryv1.AccountsRemoveRequest],
) (*connect.Response[orreryv1.AccountsRemoveResponse], error) {
	id := req.Msg.GetId()
	err := forwardAccountCommand(ctx, a.commands, a.timeout, a.clock,
		func(ctx context.Context, commands enginev1connect.AccountsClient) error {
			_, err := commands.RemoveAccount(ctx, connect.NewRequest(&enginev1.RemoveAccountRequest{Id: id}))
			return wrapEngine("remove account", err)
		})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.AccountsRemoveResponse{}), nil
}

// forwardAccountCommand runs call against the accounts command client. A nil client answers unavailable. A call whose
// connection was never made is retried under timeout while rpcretry.NeverConnected accepts its error, on clock. Any
// other error answers as translateError does.
func forwardAccountCommand(
	ctx context.Context,
	commands enginev1connect.AccountsClient,
	timeout time.Duration,
	clock rpcretry.Clock,
	call func(context.Context, enginev1connect.AccountsClient) error,
) error {
	if commands == nil {
		return listError(connect.CodeUnavailable, accountCommandsUnavailable)
	}
	err := retryUnder(ctx, clock, timeout, rpcretry.NeverConnected, func(ctx context.Context) error {
		return call(ctx, commands)
	})
	if err != nil {
		return translateError(err)
	}
	return nil
}

// accountReadError returns the error an account or route read answers with for err: unavailable when the store is, and
// internal with message otherwise. The store's own error never reaches the client.
func accountReadError(err error, message string) error {
	if errors.Is(err, store.ErrUnavailable) {
		return listError(connect.CodeUnavailable, accountStoreUnavailable)
	}
	return listError(connect.CodeInternal, message)
}

// accountMessage converts a stored account to its Account, which holds no token.
func accountMessage(account store.Account) *orreryv1.Account {
	return &orreryv1.Account{
		Id:          account.ID,
		Name:        account.Name,
		Provider:    account.Provider,
		Plan:        account.Plan,
		Fingerprint: account.Fingerprint,
		Created:     account.Created,
	}
}

// newAccountsHandler returns the path and handler of the Accounts service, reading accounts from reader and forwarding
// its commands to commands, as accountsServer does. A zero timeout is defaultControlTimeout. Each request reads at most
// maxControlBody bytes.
func newAccountsHandler(
	reader *store.Reader,
	commands enginev1connect.AccountsClient,
	timeout time.Duration,
	clock rpcretry.Clock,
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	options := append([]connect.HandlerOption{connect.WithReadMaxBytes(maxControlBody)}, opts...)
	return orreryv1connect.NewAccountsHandler(
		accountsServer{reader: reader, commands: commands, timeout: timeout, clock: clock},
		options...,
	)
}
