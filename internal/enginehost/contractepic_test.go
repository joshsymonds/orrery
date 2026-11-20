package enginehost

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// contractEpicNoCommit is a base that names no commit of any repository the tests use.
const contractEpicNoCommit = "0123456789abcdef0123456789abcdef01234567"

// TestContractEpicCreateStoresAcceptedVersionUnderItsID asserts that CreateEpic makes the epic under the contract's id
// from the accepted version's body, with the request's base in place of the body's, and stores that body's other fields
// unchanged.
func TestContractEpicCreateStoresAcceptedVersionUnderItsID(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	body := fullEpicRequest(repo, epicServiceID).Contract
	body.Base = contractEpicNoCommit
	acceptEpicContract(t, st, body)
	base := epicTestBase(repo)

	created, err := client.CreateEpic(t.Context(), connect.NewRequest(&v1.CreateEpicRequest{
		Id:        epicServiceID,
		Base:      base,
		Decisions: fullEpicRequest(repo, epicServiceID).Decisions,
	}))
	if err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}

	want, ok := proto.Clone(body).(*v1.EpicContract)
	if !ok {
		t.Fatalf("clone of the body is %T, want *v1.EpicContract", body)
	}
	want.Base = base
	// A stored Requirement reads with the state its runs give; none has run yet.
	for _, requirement := range want.GetRequirements() {
		requirement.State = "pending"
	}
	stored := created.Msg.GetEpic()
	if !proto.Equal(stored.GetContract(), want) {
		t.Fatalf("created contract = %v, want the accepted body with base %s: %v", stored.GetContract(), base, want)
	}
}

// TestContractEpicCreateTakesTheAcceptedRevision asserts that CreateEpic makes the epic from the version the accept
// froze, not from the contract's first version: the contract is revised before it is accepted, and the stored epic
// carries the revision's words.
func TestContractEpicCreateTakesTheAcceptedRevision(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	c := fullEpicRequest(repo, epicServiceID)
	storeProposedContract(t, st, c.Contract)
	revised, ok := proto.Clone(c.Contract).(*v1.EpicContract)
	if !ok {
		t.Fatalf("clone of the contract is %T, want *v1.EpicContract", c.Contract)
	}
	revised.Title = "Epic service, revised"
	revised.Intent = "Synthetic revised intent."
	version, err := contractVersionOf(
		epicServiceID, revised, nil, "Synthetic revision.", contractEpicWriter, contractEpicAt+1,
	)
	if err != nil {
		t.Fatalf("contractVersionOf %s: %v", epicServiceID, err)
	}
	version.Version = 2
	if err = st.ReviseContract(t.Context(), epicServiceID, version); err != nil {
		t.Fatalf("ReviseContract %s: %v", epicServiceID, err)
	}
	if err = st.AcceptContract(t.Context(), epicServiceID, store.ContractChange{At: contractEpicAt + 2}); err != nil {
		t.Fatalf("AcceptContract %s: %v", epicServiceID, err)
	}

	created, err := client.CreateEpic(t.Context(), connect.NewRequest(c.request()))
	if err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}

	stored := created.Msg.GetEpic().GetContract()
	if stored.GetTitle() != revised.GetTitle() || stored.GetIntent() != revised.GetIntent() {
		t.Fatalf("created title %q intent %q, want the accepted revision's %q and %q",
			stored.GetTitle(), stored.GetIntent(), revised.GetTitle(), revised.GetIntent())
	}
}

// TestContractEpicCreateRunsBaseChecks asserts that CreateEpic runs the base checks on the base the request names, and
// that a base which names no commit is refused and stores nothing.
func TestContractEpicCreateRunsBaseChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, base, want string
	}{
		{name: "base empty", base: " \t ", want: "base: empty"},
		{name: "base names no commit", base: "no-such-commit", want: "base: not a commit in the repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			client := newEpicsClient(t, startEpicServer(t, st))
			c := fullEpicRequest(stepGitTestRepo(t), epicServiceID)
			acceptEpicContract(t, st, c.Contract)

			_, err := client.CreateEpic(t.Context(), connect.NewRequest(&v1.CreateEpicRequest{
				Id: epicServiceID, Base: tc.base, Decisions: c.Decisions,
			}))
			wantConnectError(t, err, connect.CodeInvalidArgument, tc.want)
			if _, err = st.Epic(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoEpic) {
				t.Fatalf("refused epic stored: Epic err = %v, want ErrNoEpic", err)
			}
		})
	}
}

// TestContractEpicCreateRefusesAnUnknownID asserts that CreateEpic refuses an id no contract holds as not found, and
// stores nothing.
func TestContractEpicCreateRefusesAnUnknownID(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))

	_, err := client.CreateEpic(t.Context(), connect.NewRequest(&v1.CreateEpicRequest{
		Id: epicServiceID, Base: epicTestBase(stepGitTestRepo(t)),
	}))
	wantConnectError(t, err, connect.CodeNotFound, "contract "+epicServiceID+" not found")
	if _, err = st.Epic(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoEpic) {
		t.Fatalf("refused epic stored: Epic err = %v, want ErrNoEpic", err)
	}
}

// TestContractEpicCreateRefusesAContractNotAccepted asserts that CreateEpic refuses a contract whose newest state is
// proposed or set aside, naming that state, and stores nothing.
func TestContractEpicCreateRefusesAContractNotAccepted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T, st *store.Store, contract *v1.EpicContract)
		want  string
	}{
		{
			name: "proposed",
			setup: func(t *testing.T, st *store.Store, contract *v1.EpicContract) {
				t.Helper()
				storeProposedContract(t, st, contract)
			},
			want: "state: proposed, not accepted",
		},
		{
			name: "set aside",
			setup: func(t *testing.T, st *store.Store, contract *v1.EpicContract) {
				t.Helper()
				storeProposedContract(t, st, contract)
				if err := st.SetAsideContract(
					t.Context(),
					contract.GetId(),
					store.ContractChange{At: contractEpicAt},
				); err != nil {
					t.Fatalf("SetAsideContract %s: %v", contract.GetId(), err)
				}
			},
			want: "state: set_aside, not accepted",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st := epicTestStore(t)
			client := newEpicsClient(t, startEpicServer(t, st))
			c := fullEpicRequest(stepGitTestRepo(t), epicServiceID)
			tc.setup(t, st, c.Contract)

			_, err := client.CreateEpic(t.Context(), connect.NewRequest(c.request()))
			wantConnectError(t, err, connect.CodeFailedPrecondition, tc.want)
			if _, err = st.Epic(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoEpic) {
				t.Fatalf("refused epic stored: Epic err = %v, want ErrNoEpic", err)
			}
		})
	}
}

// contractEpicSetAside is the EpicCommands the race test serves: the store, except that its CreateContractEpic first sets
// the contract id aside as its owner. That write lands between the service's contract read and its store write.
type contractEpicSetAside struct {
	*store.Store
	id string
}

// CreateContractEpic sets the contract aside, then stores row through the store.
func (s contractEpicSetAside) CreateContractEpic(ctx context.Context, row store.EpicRow, version int64) error {
	if err := s.SetAsideContract(ctx, s.id, store.ContractChange{At: contractEpicAt + 1}); err != nil {
		return err
	}
	return s.Store.CreateContractEpic(ctx, row, version)
}

// TestContractEpicCreateRefusesAContractSetAsideBeforeItsWrite asserts that CreateEpic refuses a contract set aside
// between its read of the contract and its store write as a failed precondition that names the state, and that the
// refused epic is not stored and the contract stays set aside.
func TestContractEpicCreateRefusesAContractSetAsideBeforeItsWrite(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, contractEpicSetAside{Store: st, id: epicServiceID}))
	c := fullEpicRequest(stepGitTestRepo(t), epicServiceID)
	acceptEpicContract(t, st, c.Contract)

	_, err := client.CreateEpic(t.Context(), connect.NewRequest(c.request()))
	wantConnectError(t, err, connect.CodeFailedPrecondition, "state: no longer accepted")
	if _, err = st.Epic(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoEpic) {
		t.Fatalf("refused epic stored: Epic err = %v, want ErrNoEpic", err)
	}
	record, err := st.Contract(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("Contract %s: %v", epicServiceID, err)
	}
	if record.State != store.ContractSetAside {
		t.Fatalf("contract state = %q, want %q", record.State, store.ContractSetAside)
	}
}
