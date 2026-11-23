package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoAccount reports that no LLM account has the id asked for.
var ErrNoAccount = errors.New("no LLM account")

// ErrAccountInUse reports that a route context names the account, so the account cannot be removed.
var ErrAccountInUse = errors.New("LLM account in use")

// ErrNoRouteContext reports that no route context has the id asked for.
var ErrNoRouteContext = errors.New("no route context")

// ErrRoutesRefused reports that the route registry failed llmroutes.Validate, so the store kept its routes as they were.
var ErrRoutesRefused = errors.New("route registry refused")

// fingerprintLen is how many hex characters of its token's SHA-256 an account fingerprint keeps.
const fingerprintLen = 12

// Account is an LLM account as orrery lists it. Fingerprint identifies the account's token without showing it.
type Account struct {
	ID, Name, Provider, Plan, Fingerprint string
	Created                               int64
}

// NewAccount is an LLM account to store. Token is the account's token, which orrery keeps in plain text and never
// returns.
type NewAccount struct {
	ID, Name, Provider, Plan, Token string
	Created                         int64
}

// AccountFingerprint returns the first 12 lowercase hex characters of the SHA-256 of token. It identifies the token
// without showing it.
func AccountFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:fingerprintLen]
}

// AddAccount stores account with the fingerprint of its token, and returns the stored account without the token. It
// returns an error, and stores nothing, when an account already has account's id.
func (s *Store) AddAccount(ctx context.Context, account NewAccount) (Account, error) {
	stored := Account{
		ID: account.ID, Name: account.Name, Provider: account.Provider, Plan: account.Plan,
		Fingerprint: AccountFingerprint(account.Token), Created: account.Created,
	}
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		insertErr := queries.InsertLLMAccount(ctx, sqlcgen.InsertLLMAccountParams{
			ID: stored.ID, Name: stored.Name, Provider: stored.Provider, Plan: stored.Plan,
			Token: account.Token, Fingerprint: stored.Fingerprint, Created: stored.Created,
		})
		if insertErr != nil {
			return fmt.Errorf("insert account %s: %w", stored.ID, insertErr)
		}
		return nil
	})
	if err != nil {
		return Account{}, err
	}
	return stored, nil
}

// RenameAccount sets the name of the account with id, and returns the account. It returns an error that matches
// ErrNoAccount when no account has id.
func (s *Store) RenameAccount(ctx context.Context, id, name string) (Account, error) {
	var account Account
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		renamed, renameErr := queries.RenameLLMAccount(ctx, sqlcgen.RenameLLMAccountParams{Name: name, ID: id})
		if renameErr != nil {
			return fmt.Errorf("rename account %s: %w", id, renameErr)
		}
		if renamed == 0 {
			return fmt.Errorf("account %s: %w", id, ErrNoAccount)
		}
		row, readErr := queries.GetLLMAccount(ctx, id)
		if readErr != nil {
			return fmt.Errorf("read account %s: %w", id, readErr)
		}
		account = Account{
			ID: row.ID, Name: row.Name, Provider: row.Provider, Plan: row.Plan, Fingerprint: row.Fingerprint,
			Created: row.Created,
		}
		return nil
	})
	if err != nil {
		return Account{}, err
	}
	return account, nil
}

// RemoveAccount removes the account with id. It returns an error that matches ErrNoAccount when no account has id, and
// an error that matches ErrAccountInUse, naming the route contexts that name the account, when a context names it. A
// context that names no account, whose account is "", uses its caller's own login and never counts as using one.
func (s *Store) RemoveAccount(ctx context.Context, id string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		held, err := queries.CountLLMAccount(ctx, id)
		if err != nil {
			return fmt.Errorf("count account %s: %w", id, err)
		}
		if held == 0 {
			return fmt.Errorf("account %s: %w", id, ErrNoAccount)
		}
		contexts, err := queries.ListRouteContextsOfAccount(ctx, id)
		if err != nil {
			return fmt.Errorf("list route contexts of account %s: %w", id, err)
		}
		if len(contexts) > 0 {
			return fmt.Errorf("account %s is named by route contexts %s: %w",
				id, strings.Join(contexts, ", "), ErrAccountInUse)
		}
		if err = queries.DeleteLLMAccount(ctx, id); err != nil {
			return fmt.Errorf("delete account %s: %w", id, err)
		}
		return nil
	})
}

// Accounts returns every account, oldest first and ties by id. It never returns an account's token.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin accounts read: %w", err)
	}
	accounts, err := readAccounts(ctx, sqlcgen.New(tx))
	if err != nil {
		return nil, errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end accounts read: %w", err)
	}
	return accounts, nil
}

// Accounts returns the accounts as Store.Accounts does, read without writing the store.
func (r *Reader) Accounts(ctx context.Context) ([]Account, error) {
	var accounts []Account
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		accounts, readErr = readAccounts(ctx, queries)
		return readErr
	})
	return accounts, err
}

// readAccounts returns every account, oldest first and ties by id, read with queries. Its query selects no token.
func readAccounts(ctx context.Context, queries *sqlcgen.Queries) ([]Account, error) {
	rows, err := queries.ListLLMAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	accounts := make([]Account, 0, len(rows))
	for _, row := range rows {
		accounts = append(accounts, Account{
			ID: row.ID, Name: row.Name, Provider: row.Provider, Plan: row.Plan, Fingerprint: row.Fingerprint,
			Created: row.Created,
		})
	}
	return accounts, nil
}
