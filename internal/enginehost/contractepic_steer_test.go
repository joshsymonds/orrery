package enginehost

import (
	"maps"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// goneWriter is the session that proposes a contract and has no sessions row, as a session never stored.
const goneWriter = "session-writer-gone"

// proposeAcceptedBy stores contract in st as version 1 of a contract written by writer, then accepts it as the owner.
// It fails the test on error.
func proposeAcceptedBy(t *testing.T, st *store.Store, contract *v1.EpicContract, writer string) {
	t.Helper()
	id := contract.GetId()
	version, err := contractVersionOf(id, contract, nil, "Synthetic words.", writer, contractEpicAt)
	if err != nil {
		t.Fatalf("contractVersionOf %s: %v", id, err)
	}
	if err = st.ProposeContract(t.Context(), id, version); err != nil {
		t.Fatalf("ProposeContract %s: %v", id, err)
	}
	if err = st.AcceptContract(t.Context(), id, store.ContractChange{At: contractEpicAt}); err != nil {
		t.Fatalf("AcceptContract %s: %v", id, err)
	}
}

// TestContractEpicSteererIsTheContractConversation asserts that an epic made from a contract is steered from the
// contract's conversation, the writer of its version 1, whichever caller makes the create: the owner, another
// conversation, or the writer itself. No steering session starts, because the writer's sessions row is stored.
func TestContractEpicSteererIsTheContractConversation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// caller returns the caller headers of the create, given the writer and another conversation.
		caller func(writer, other string) http.Header
	}{
		{name: "owner", caller: func(string, string) http.Header { return nil }},
		{name: "another conversation", caller: func(_, other string) http.Header { return sessionHeader(other) }},
		{name: "the writer", caller: func(writer, _ string) http.Header { return sessionHeader(writer) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, path := newTestManager(t, fakeClaudePath(t))
			client := steererSetClient(t, m)
			writer := startedConversation(t, m)
			other := startedConversation(t, m)
			c := fullEpicRequest(stepGitTestRepo(t), epicServiceID)
			proposeAcceptedBy(t, concreteStore(t, m), c.Contract, writer)
			before := countRows(t, path, "sessions")

			req := connect.NewRequest(c.request())
			maps.Copy(req.Header(), tc.caller(writer, other))
			if _, err := client.CreateEpic(t.Context(), req); err != nil {
				t.Fatalf("CreateEpic: %v", err)
			}
			if n := countRows(t, path, "sessions"); n != before {
				t.Fatalf("sessions rows = %d, want %d: a stored writer starts no session", n, before)
			}
			row, err := concreteStore(t, m).Epic(t.Context(), epicServiceID)
			if err != nil {
				t.Fatalf("Epic: %v", err)
			}
			if row.Steerer != writer {
				t.Fatalf("steerer = %q, want the contract's conversation %q", row.Steerer, writer)
			}
		})
	}
}

// TestContractEpicSteererStartsOneSessionWhenWriterIsGone asserts that when the contract's conversation has no sessions
// row, an epic created by the owner or by another conversation gets one steering session started for it, which steers.
func TestContractEpicSteererStartsOneSessionWhenWriterIsGone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		// caller returns the caller headers of the create, starting any conversation it names on m.
		caller func(t *testing.T, m *Manager) http.Header
	}{
		{name: "owner", caller: func(*testing.T, *Manager) http.Header { return nil }},
		{name: "another conversation", caller: func(t *testing.T, m *Manager) http.Header {
			t.Helper()
			return sessionHeader(startedConversation(t, m))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, path := newTestManager(t, fakeClaudePath(t))
			client := steererSetClient(t, m)
			c := fullEpicRequest(stepGitTestRepo(t), epicServiceID)
			proposeAcceptedBy(t, concreteStore(t, m), c.Contract, goneWriter)
			caller := tc.caller(t, m)
			before := countRows(t, path, "sessions")

			req := connect.NewRequest(c.request())
			maps.Copy(req.Header(), caller)
			if _, err := client.CreateEpic(t.Context(), req); err != nil {
				t.Fatalf("CreateEpic: %v", err)
			}
			wantStartedSteerer(t, m, path, c.Contract.GetRepository(), before)
		})
	}
}
