package stage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// Shown stores a shown mark for each row a contract version the owner was shown, and answers the rows it marked. Only
// the owner through serve may call it, so a call whose caller resolves to a session is refused as permission denied.
// The contract must be proposed, and version must be its newest version: a version that is not the newest is refused
// as a failed precondition that names the newest. Each row must be a release step S<n> of that version, or the call is
// refused as an invalid argument that names the row and marks none. A row that already has a mark keeps its first mark.
// The answer names each row the call named, once, in the order it named them.
func (s *Service) Shown(
	ctx context.Context,
	req *connect.Request[v1.ShownRequest],
) (*connect.Response[v1.ShownResponse], error) {
	contract, version, rows := req.Msg.GetContract(), req.Msg.GetVersion(), req.Msg.GetRows()
	if err := requireField("contract", contract); err != nil {
		return nil, err
	}
	if err := s.requireOwner(ctx, req.Header()); err != nil {
		return nil, err
	}
	record, err := s.cfg.Store.Contract(ctx, contract)
	if errors.Is(err, store.ErrNoContract) {
		return nil, s.unknownContract(ctx, contract)
	}
	if err != nil {
		return nil, internalError(fmt.Errorf("read contract %s: %w", contract, err))
	}
	if record.State != store.ContractProposed {
		return nil, refuse(connect.CodeFailedPrecondition, "state: not proposed")
	}
	if version != record.Version {
		return nil, refuse(connect.CodeFailedPrecondition,
			fmt.Sprintf("version: %d is not the newest, which is %d", version, record.Version))
	}
	err = requireReleaseSteps(record, rows)
	if err != nil {
		return nil, err
	}
	if err = s.cfg.Store.MarkContractShown(ctx, contract, version, rows, s.cfg.Now().Unix()); err != nil {
		return nil, shownError(err, contract)
	}
	return connect.NewResponse(&v1.ShownResponse{Rows: distinctRows(rows)}), nil
}

// requireOwner returns nil when the caller of a call resolves to no session, as the owner's call through serve does,
// and a permission denied refusal when it resolves to a session.
func (s *Service) requireOwner(ctx context.Context, header http.Header) error {
	session, err := s.callerSession(ctx, callerOf(header))
	if err != nil {
		return err
	}
	if session != "" {
		return refuse(connect.CodePermissionDenied,
			"caller: resolves to a session; shown is serve's call, which carries none")
	}
	return nil
}

// unknownContract returns the refusal of a contract id no record holds. A draft holds the id, which is not proposed,
// and an id no one holds is not found.
func (s *Service) unknownContract(ctx context.Context, contract string) error {
	_, err := s.cfg.Store.ContractDraft(ctx, contract)
	switch {
	case err == nil:
		return refuse(connect.CodeFailedPrecondition, "state: not proposed")
	case errors.Is(err, store.ErrNoDraft):
		return errors.Join(connect.NewError(connect.CodeNotFound,
			fmt.Errorf("contract %s: %w", contract, store.ErrNoContract)))
	default:
		return internalError(fmt.Errorf("read draft of contract %s: %w", contract, err))
	}
}

// releaseStepsOf returns how many release steps the newest version of record holds.
func releaseStepsOf(record store.ContractRecord) (int, error) {
	for _, version := range record.Versions {
		if version.Version != record.Version {
			continue
		}
		body, err := decodeBody(version.Body)
		if err != nil {
			return 0, fmt.Errorf("read version %d of contract %s: %w", version.Version, record.ID, err)
		}
		return len(body.GetReleaseSteps()), nil
	}
	return 0, fmt.Errorf("contract %s: no version %d", record.ID, record.Version)
}

// requireReleaseSteps returns nil when each of rows is a release step S<n> of the newest version of record. The first row
// that is not one is refused as an invalid argument that names it. A newest version that cannot be read is an internal
// error.
func requireReleaseSteps(record store.ContractRecord, rows []string) error {
	steps, err := releaseStepsOf(record)
	if err != nil {
		return internalError(err)
	}
	for _, row := range rows {
		if kind, number, ok := parseRow(row); !ok || kind != 'S' || number > steps {
			refusal := &rowError{row: row, reason: "is not a release step the contract holds"}
			return refuse(connect.CodeInvalidArgument, refusal.Error())
		}
	}
	return nil
}

// shownError returns the error Shown answers with for err, which the store returned for contract. A version that is not
// the newest is a failed precondition that names the newest, a contract that is not proposed is a failed precondition
// named state, a contract no one holds is not found, and any other error is internal.
func shownError(err error, contract string) error {
	switch {
	case errors.Is(err, store.ErrContractVersion):
		return refuse(connect.CodeFailedPrecondition, err.Error())
	case errors.Is(err, store.ErrContractState):
		return refuse(connect.CodeFailedPrecondition, "state: not proposed")
	case errors.Is(err, store.ErrNoContract):
		return errors.Join(connect.NewError(connect.CodeNotFound, err))
	default:
		return internalError(fmt.Errorf("mark rows shown of contract %s: %w", contract, err))
	}
}

// distinctRows returns rows with each row once, in the order each row first appears.
func distinctRows(rows []string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if !slices.Contains(out, row) {
			out = append(out, row)
		}
	}
	return out
}
