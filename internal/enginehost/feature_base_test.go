package enginehost

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// featureBaseCheckout makes a bare origin whose main is at one commit, and a checkout cloned from it. Then origin's main
// moves one commit ahead of the checkout. It returns the checkout's directory, the commit the checkout's HEAD names and
// the commit origin's main names.
func featureBaseCheckout(t *testing.T) (string, string, string) {
	t.Helper()
	origin := t.TempDir()
	stepGitTestRun(t, origin, "init", "--bare", "-q", "-b", "main")
	work := stepGitTestRepo(t)
	stepGitTestRun(t, work, "remote", "add", "origin", origin)
	stepGitTestRun(t, work, "push", "-q", "origin", "main")
	checkout := filepath.Join(t.TempDir(), "checkout")
	stepGitTestRun(t, work, "clone", "-q", origin, checkout)
	head := epicTestBase(checkout)
	stepGitTestRun(t, work, "commit", "-q", "--allow-empty", "-m", "ahead")
	stepGitTestRun(t, work, "push", "-q", "origin", "main")
	return checkout, head, stepGitTestRun(t, work, "rev-parse", "HEAD")
}

// TestFeatureBaseAcceptFetchesOriginAndStoresMainsCommit checks that an accept fetches origin's main into the contract's
// checkout and stores that commit as the contract's base. The checkout's HEAD is behind origin's main, and it is not the
// commit stored.
func TestFeatureBaseAcceptFetchesOriginAndStoresMainsCommit(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	checkout, head, main := featureBaseCheckout(t)
	if head == main {
		t.Fatalf("checkout HEAD and origin's main both name %s, want origin's main ahead", head)
	}
	storeProposedContract(t, st, fullEpicRequest(checkout, epicServiceID).Contract)
	server := startContractServer(t, st, &Contracts{Store: st, Now: contractTestAt, Getenv: epicTestGetenv})

	if err := acceptContract(t.Context(), server.contracts, ownerCaller, epicServiceID); err != nil {
		t.Fatalf("AcceptContract: %v", err)
	}

	record, err := st.Contract(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	accepted := record.States[len(record.States)-1]
	if accepted.State != store.ContractAccepted || accepted.Base != main {
		t.Fatalf("newest state %s with base %s, want accepted with origin's main %s (checkout HEAD is %s)",
			accepted.State, accepted.Base, main, head)
	}
}

// TestFeatureBaseCreateWithNoBaseStartsAtTheStoredBase checks that a create with no base starts the epic at the base its
// contract stored, though the repository's HEAD has moved past it, and that it records no decision for the base.
func TestFeatureBaseCreateWithNoBaseStartsAtTheStoredBase(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stored := epicTestBase(repo)
	stepGitTestRun(t, repo, "commit", "-q", "--allow-empty", "-m", "later")
	c := fullEpicRequest(repo, epicServiceID)
	storeProposedContract(t, st, c.Contract)
	change := store.ContractChange{At: contractEpicAt, Base: stored}
	if err := st.AcceptContract(t.Context(), epicServiceID, change); err != nil {
		t.Fatalf("AcceptContract %s: %v", epicServiceID, err)
	}

	created, err := client.CreateEpic(t.Context(), connect.NewRequest(&v1.CreateEpicRequest{
		Id: epicServiceID, Decisions: c.Decisions,
	}))
	if err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}

	epic := created.Msg.GetEpic()
	if got := epic.GetContract().GetBase(); got != stored {
		t.Fatalf("epic base = %s, want the stored base %s", got, stored)
	}
	if got, want := len(epic.GetDecisions()), len(c.Decisions); got != want {
		t.Fatalf("epic has %d decisions, want its %d seed decisions alone", got, want)
	}
}

// TestFeatureBaseCreateNamingAnotherBaseStartsThereAndRecordsADecisionNamingItAndItsMaker checks that a create naming a
// base other than the stored one starts the epic there, and records one decision after the seeds. The decision names
// the base the epic starts at and the stored base it replaces, and is made by the caller.
func TestFeatureBaseCreateNamingAnotherBaseStartsThereAndRecordsADecisionNamingItAndItsMaker(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	seedContractSessions(t, st)
	client := newEpicsClient(t, startEpicServer(t, st))
	repo := stepGitTestRepo(t)
	stored := epicTestBase(repo)
	stepGitTestRun(t, repo, "commit", "-q", "--allow-empty", "-m", "named")
	named := epicTestBase(repo)
	c := fullEpicRequest(repo, epicServiceID)
	storeProposedContract(t, st, c.Contract)
	change := store.ContractChange{At: contractEpicAt, Base: stored}
	if err := st.AcceptContract(t.Context(), epicServiceID, change); err != nil {
		t.Fatalf("AcceptContract %s: %v", epicServiceID, err)
	}

	request := connect.NewRequest(&v1.CreateEpicRequest{Id: epicServiceID, Base: named, Decisions: c.Decisions})
	headlessCaller(request.Header())
	created, err := client.CreateEpic(t.Context(), request)
	if err != nil {
		t.Fatalf("CreateEpic %s: %v", epicServiceID, err)
	}

	epic := created.Msg.GetEpic()
	if got := epic.GetContract().GetBase(); got != named {
		t.Fatalf("epic base = %s, want the named base %s", got, named)
	}
	decisions := epic.GetDecisions()
	if got, want := len(decisions), len(c.Decisions)+1; got != want {
		t.Fatalf("epic has %d decisions, want %d: the seeds and the base decision", got, want)
	}
	added := decisions[len(decisions)-1]
	if !strings.Contains(added.GetText(), named) || !strings.Contains(added.GetText(), stored) {
		t.Errorf("base decision text = %q, want it to name %s and the stored %s", added.GetText(), named, stored)
	}
	if added.GetBy() != store.MakerConversation || added.GetSession() != contractTestHeadless {
		t.Errorf("base decision maker = %s %s, want conversation %s",
			added.GetBy(), added.GetSession(), contractTestHeadless)
	}
}

// TestFeatureBaseCreateWithBlankBaseAndNoStoredBaseIsRefused checks that a create with no base is refused as base: empty
// when its contract stored no base, as a contract accepted before base was stored does, and that it stores nothing.
func TestFeatureBaseCreateWithBlankBaseAndNoStoredBaseIsRefused(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	c := fullEpicRequest(stepGitTestRepo(t), epicServiceID)
	acceptEpicContract(t, st, c.Contract)

	_, err := client.CreateEpic(t.Context(), connect.NewRequest(&v1.CreateEpicRequest{
		Id: epicServiceID, Decisions: c.Decisions,
	}))
	wantConnectError(t, err, connect.CodeInvalidArgument, "base: empty")
	if _, err = st.Epic(t.Context(), epicServiceID); !errors.Is(err, store.ErrNoEpic) {
		t.Fatalf("refused epic stored: Epic err = %v, want ErrNoEpic", err)
	}
}

// TestFeatureBaseAcceptIsRefusedWhenOriginCannotBeFetched checks that an accept of a contract whose repository has no
// origin to fetch is refused as a failed precondition that names the fetch, and that the contract stays proposed.
func TestFeatureBaseAcceptIsRefusedWhenOriginCannotBeFetched(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	server := startContractServer(t, st, &Contracts{Store: st, Now: contractTestAt, Getenv: epicTestGetenv})
	storeProposedContract(t, st, fullEpicRequest(server.repo, epicServiceID).Contract)

	err := acceptContract(t.Context(), server.contracts, ownerCaller, epicServiceID)
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("AcceptContract code = %v, want %v (%v)", got, connect.CodeFailedPrecondition, err)
	}
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error is %T, want *connect.Error", err)
	}
	if message := connectErr.Message(); !strings.HasPrefix(message, "base: fetch origin main: ") {
		t.Fatalf("message = %q, want it to start with %q", message, "base: fetch origin main: ")
	}

	record, err := st.Contract(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("Contract: %v", err)
	}
	if record.State != store.ContractProposed || len(record.States) != 1 {
		t.Fatalf("contract state = %s with %d states, want proposed alone", record.State, len(record.States))
	}
}
