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
	reply, err := forwardAccount(ctx, a.commands, a.timeout, a.clock, "add account",
		func(ctx context.Context, c enginev1connect.AccountsClient) (*connect.Response[enginev1.AddAccountResponse], error) {
			return c.AddAccount(ctx, connect.NewRequest(&enginev1.AddAccountRequest{
				Name: req.Msg.GetName(), Provider: req.Msg.GetProvider(), Plan: req.Msg.GetPlan(), Token: req.Msg.GetToken(),
			}))
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
	reply, err := forwardAccount(ctx, a.commands, a.timeout, a.clock, "rename account",
		func(ctx context.Context, c enginev1connect.AccountsClient) (*connect.Response[enginev1.RenameAccountResponse], error) {
			return c.RenameAccount(ctx, connect.NewRequest(&enginev1.RenameAccountRequest{
				Id: req.Msg.GetId(), Name: req.Msg.GetName(),
			}))
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
	_, err := forwardAccount(ctx, a.commands, a.timeout, a.clock, "remove account",
		func(ctx context.Context, c enginev1connect.AccountsClient) (*connect.Response[enginev1.RemoveAccountResponse], error) {
			return c.RemoveAccount(ctx, connect.NewRequest(&enginev1.RemoveAccountRequest{Id: req.Msg.GetId()}))
		})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&orreryv1.AccountsRemoveResponse{}), nil
}

// forwardAccount runs call against the accounts command client and returns orreryd's reply. A nil client answers
// unavailable. A call whose connection was never made is retried under timeout while rpcretry.NeverConnected accepts
// its error, on clock. A failed call's error is named op and translated as translateError does.
func forwardAccount[Resp any](
	ctx context.Context,
	commands enginev1connect.AccountsClient,
	timeout time.Duration,
	clock rpcretry.Clock,
	op string,
	call func(context.Context, enginev1connect.AccountsClient) (*connect.Response[Resp], error),
) (*connect.Response[Resp], error) {
	if commands == nil {
		return nil, listError(connect.CodeUnavailable, accountCommandsUnavailable)
	}
	var reply *connect.Response[Resp]
	err := retryUnder(ctx, clock, timeout, rpcretry.NeverConnected, func(ctx context.Context) error {
		var callErr error
		reply, callErr = call(ctx, commands)
		return callErr
	})
	if err != nil {
		return nil, translateError(wrapEngine(op, err))
	}
	return reply, nil
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
