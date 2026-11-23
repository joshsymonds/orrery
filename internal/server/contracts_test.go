package server_test

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/enginehost"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// The ids of the contracts the tests seed. Contract A is proposed with two open decisions. Contract B is proposed,
// revised and accepted. Contract D is proposed, then moved through each command.
const (
	contractIDA = "synthetic-repo/alpha"
	contractIDB = "synthetic-repo/beta"
	contractIDD = "synthetic-repo/delta"
	contractIDG = "synthetic-repo/gone"
)

// The times the tests store, in unix seconds. The engine's clock reads contractTimeEngine.
const (
	contractTimeA      int64 = 1_700_000_100
	contractTimeB      int64 = 1_700_000_110
	contractTimeBRev   int64 = 1_700_000_120
	contractTimeBAcc   int64 = 1_700_000_130
	contractTimeD      int64 = 1_700_000_140
	contractTimeEngine int64 = 1_700_000_500
)

// contractSessionID is the engine session row the command test's calls name in their Orrery-Session header.
const contractSessionID = "synthetic-session"

// contractWriterA, contractWriterB and contractWriterD are the writers of the seeded contracts. Each names a session
// row the tests seed, so a forwarded session header would resolve to it.
const (
	contractWriterA = "synthetic-writer-a"
	contractWriterB = "synthetic-writer-b"
	contractWriterD = "synthetic-writer-d"
)

// contractTestBody returns an engine contract body with id, title and care, one failure and one requirement.
func contractTestBody(id, title, care string) *enginev1.EpicContract {
	return &enginev1.EpicContract{
		Id:         id,
		Repository: "/synthetic/repo",
		Title:      title,
		Care:       care,
		Intent:     "Synthetic intent.",
		Failures: []*enginev1.EpicFailure{{
			Id: "F1", Text: "Synthetic failure.", Rating: "serious", Response: "prevent",
		}},
		Requirements: []*enginev1.EpicRequirement{{
			Id: "R1", Text: "Synthetic requirement.", Evidence: "Synthetic evidence.", Kind: "test",
		}},
	}
}

// contractTestVersion returns the store version of body as version number, written by writer with words at at.
func contractTestVersion(
	t *testing.T,
	body *enginev1.EpicContract,
	number int64,
	writer, words string,
	at int64,
	decisions []store.ContractDecision,
) store.ContractVersion {
	t.Helper()
	encoded, err := protojson.Marshal(body)
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	return store.ContractVersion{
		Version: number, Repository: body.GetRepository(), Title: body.GetTitle(), Care: body.GetCare(),
		Body: string(encoded), Decisions: decisions, Words: words, Writer: writer, At: at,
	}
}

// contractTestDecisionD1 returns decision D1 of a contract, answered with answer, or open when answer is empty.
func contractTestDecisionD1(answer string) store.ContractDecision {
	return store.ContractDecision{
		ID:             "D1",
		Question:       "Synthetic question.",
		Options:        []string{"Synthetic option one.", "Synthetic option two."},
		Recommendation: "Synthetic option one.",
		Reason:         "Synthetic reason.",
		Row:            "F1",
		Answer:         answer,
	}
}

// wantContractDecisionD1 is the public message of contractTestDecisionD1 with answer.
func wantContractDecisionD1(answer string) *orreryv1.ContractDecision {
	return &orreryv1.ContractDecision{
		Id:             "D1",
		Question:       "Synthetic question.",
		Options:        []string{"Synthetic option one.", "Synthetic option two."},
		Recommendation: "Synthetic option one.",
		Reason:         "Synthetic reason.",
		Row:            "F1",
		Answer:         answer,
	}
}

// wantContractSections is the public EpicContract of a body with title and care, as a version carries it.
func wantContractSections(title, care string) *orreryv1.EpicContract {
	return &orreryv1.EpicContract{
		Title:  title,
		Care:   care,
		Intent: "Synthetic intent.",
		Failures: []*orreryv1.EpicFailure{{
			Id: "F1", Text: "Synthetic failure.", Rating: "serious", Response: "prevent",
		}},
	}
}

// wantContractRequirements is the public requirement list of contractTestBody.
func wantContractRequirements() []*orreryv1.EpicRequirement {
	return []*orreryv1.EpicRequirement{{
		Id: "R1", Text: "Synthetic requirement.", Evidence: "Synthetic evidence.", Kind: "test",
	}}
}

// openContractStore opens a seeded store at a temporary path, closed when the test ends, and returns it with its path.
func openContractStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close store: %v", err)
		}
	})
	return st, path
}

// seedContractSession adds the engine session row id to st.
func seedContractSession(t *testing.T, st *store.Store, id string) {
	t.Helper()
	add := store.EngineAdd{ID: id, Dir: "/synthetic/dir"}
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{add}, time.Unix(contractTimeA, 0)); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// deleteContractSessionRow removes the sessions row id from the store file at path, as a session whose pane is gone
// loses its row.
func deleteContractSessionRow(t *testing.T, path, id string) {
	t.Helper()
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open raw handle: %v", err)
	}
	defer func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw handle: %v", err)
		}
	}()
	if _, err := raw.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
		t.Fatalf("delete sessions row %s: %v", id, err)
	}
}

// seedContractHistory proposes contract A, with decisions D1 and D2 both open, and contract B, which is proposed,
// revised with D1 answered, and accepted by the owner, in st.
func seedContractHistory(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := t.Context()
	decisionsA := []store.ContractDecision{
		contractTestDecisionD1(""),
		{
			ID: "D2", Question: "Synthetic question two.", Options: []string{"Synthetic option three."},
			Recommendation: "Synthetic option three.", Reason: "Synthetic reason two.", Row: "R1",
		},
	}
	proposalA := contractTestVersion(t, contractTestBody(contractIDA, "Synthetic one", "serious"), 1,
		contractWriterA, "Synthetic proposal.", contractTimeA, decisionsA)
	if err := st.ProposeContract(ctx, contractIDA, proposalA); err != nil {
		t.Fatalf("ProposeContract A: %v", err)
	}
	proposalB := contractTestVersion(t, contractTestBody(contractIDB, "Synthetic two", "limited"), 1,
		contractWriterB, "Synthetic proposal.", contractTimeB, nil)
	if err := st.ProposeContract(ctx, contractIDB, proposalB); err != nil {
		t.Fatalf("ProposeContract B: %v", err)
	}
	revision := contractTestVersion(t, contractTestBody(contractIDB, "Synthetic two revised", "serious"), 2,
		contractWriterB, "Synthetic revision.", contractTimeBRev, []store.ContractDecision{
			contractTestDecisionD1("Synthetic option one."),
		})
	revision.ChangedRows = []string{"R1", "D1"}
	revision.ChangedSections = []string{"title", "care"}
	if err := st.ReviseContract(ctx, contractIDB, revision); err != nil {
		t.Fatalf("ReviseContract B: %v", err)
	}
	if err := st.AcceptContract(ctx, contractIDB, store.ContractChange{
		Words: "Synthetic accept.", At: contractTimeBAcc,
	}); err != nil {
		t.Fatalf("AcceptContract B: %v", err)
	}
}

// contractsHandler returns the guarded handler of a server reading contracts from reader and forwarding the commands
// to commands, which is nil for none.
func contractsHandler(
	t *testing.T,
	reader *store.Reader,
	commands enginev1connect.ContractsClient,
) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:           []string{allowedLogin},
		Tmux:             newTmuxClient(t),
		Push:             newPushStore(t),
		Sessions:         reader,
		ContractCommands: commands,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// contractsClient serves handler on an httptest server and returns a generated Contracts client whose requests carry
// the allowed login and the client header.
func contractsClient(t *testing.T, handler http.Handler) orreryv1connect.ContractsClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewContractsClient(httpClient, srv.URL)
}

// serveContractEngine serves orreryd's engine Contracts service over st on a loopback listener until the test ends,
// with the fixed engine clock. It returns a generated Contracts client for the listener.
func serveContractEngine(t *testing.T, st *store.Store) enginev1connect.ContractsClient {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	contracts := &enginehost.Contracts{
		Store: st,
		Now:   func() time.Time { return time.Unix(contractTimeEngine, 0) },
		// The contracts of this test name a repository that is not on disk, so the accept reads a fixed origin main.
		OriginMain: func(context.Context, string) (string, error) { return "9a8b7c6d5e4f30211f0e9d8c7b6a5f4e3d2c1b0a", nil },
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- enginehost.Serve(ctx, ln, nil, nil, nil, nil, nil, nil, nil, nil, nil, contracts) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return enginev1connect.NewContractsClient(http.DefaultClient, "http://"+ln.Addr().String())
}

// contractsProcedures lists the Connect procedures of the Contracts service: the two reads and the three commands.
func contractsProcedures() []string {
	return []string{
		orreryv1connect.ContractsListProcedure,
		orreryv1connect.ContractsGetProcedure,
		orreryv1connect.ContractsAcceptProcedure,
		orreryv1connect.ContractsSetAsideProcedure,
		orreryv1connect.ContractsReopenProcedure,
	}
}

// assertContractStates fails t unless got holds exactly the state changes want holds.
func assertContractStates(t *testing.T, got, want []*orreryv1.ContractStateChange) {
	t.Helper()
	if !proto.Equal(&orreryv1.ContractsGetResponse{States: got}, &orreryv1.ContractsGetResponse{States: want}) {
		t.Fatalf("states = %v, want %v", got, want)
	}
}

func TestContractRecordListGivesEachContractsListing(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedContractHistory(t, st)
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), nil))

	reply, err := client.List(t.Context(), connect.NewRequest(&orreryv1.ContractsListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := &orreryv1.ContractsListResponse{Contracts: []*orreryv1.Contract{
		{
			Id: contractIDA, Repository: "/synthetic/repo", Title: "Synthetic one", Care: "serious",
			State: "proposed", Version: 1, OpenDecisions: 2, Conversation: contractWriterA,
			Created: contractTimeA, Changed: contractTimeA,
		},
		{
			Id: contractIDB, Repository: "/synthetic/repo", Title: "Synthetic two revised", Care: "serious",
			State: "accepted", Version: 2, OpenDecisions: 0, Conversation: contractWriterB,
			Created: contractTimeB, Changed: contractTimeBAcc,
		},
	}}
	if !proto.Equal(reply.Msg, want) {
		t.Fatalf("List = %v, want %v", reply.Msg, want)
	}
}

func TestContractRecordGetGivesVersionsAndStates(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedContractHistory(t, st)
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), nil))

	reply, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDB}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := &orreryv1.ContractsGetResponse{
		Contract: &orreryv1.Contract{
			Id: contractIDB, Repository: "/synthetic/repo", Title: "Synthetic two revised", Care: "serious",
			State: "accepted", Version: 2, OpenDecisions: 0, Conversation: contractWriterB,
			Created: contractTimeB, Changed: contractTimeBAcc,
		},
		Versions: []*orreryv1.ContractVersion{
			{
				Version:      1,
				Contract:     wantContractSections("Synthetic two", "limited"),
				Requirements: wantContractRequirements(),
				Words:        "Synthetic proposal.",
				Writer:       contractWriterB,
				Time:         contractTimeB,
			},
			{
				Version:         2,
				Contract:        wantContractSections("Synthetic two revised", "serious"),
				Requirements:    wantContractRequirements(),
				Decisions:       []*orreryv1.ContractDecision{wantContractDecisionD1("Synthetic option one.")},
				Words:           "Synthetic revision.",
				Writer:          contractWriterB,
				Time:            contractTimeBRev,
				ChangedRows:     []string{"R1", "D1"},
				ChangedSections: []string{"title", "care"},
			},
		},
		States: []*orreryv1.ContractStateChange{
			{State: "proposed", Version: 1, By: "session", Session: contractWriterB, Words: "Synthetic proposal.",
				Time: contractTimeB},
			{State: "accepted", Version: 2, By: "owner", Words: "Synthetic accept.", Time: contractTimeBAcc},
		},
	}
	if !proto.Equal(reply.Msg, want) {
		t.Fatalf("Get = %v, want %v", reply.Msg, want)
	}
}

func TestContractRecordReadsContractWhoseWriterSessionIsDeleted(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedContractSession(t, st, contractWriterD)
	proposal := contractTestVersion(t, contractTestBody(contractIDG, "Synthetic gone", "limited"), 1,
		contractWriterD, "Synthetic proposal.", contractTimeD, nil)
	if err := st.ProposeContract(t.Context(), contractIDG, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	deleteContractSessionRow(t, path, contractWriterD)
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), nil))

	list, err := client.List(t.Context(), connect.NewRequest(&orreryv1.ContractsListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	wantListing := &orreryv1.Contract{
		Id: contractIDG, Repository: "/synthetic/repo", Title: "Synthetic gone", Care: "limited",
		State: "proposed", Version: 1, Conversation: contractWriterD, Created: contractTimeD, Changed: contractTimeD,
	}
	if !proto.Equal(list.Msg.GetContracts()[0], wantListing) {
		t.Fatalf("List = %v, want %v", list.Msg.GetContracts()[0], wantListing)
	}
	get, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDG}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if writer := get.Msg.GetVersions()[0].GetWriter(); writer != contractWriterD {
		t.Fatalf("version 1 writer = %q, want %q", writer, contractWriterD)
	}
	if session := get.Msg.GetStates()[0].GetSession(); session != contractWriterD {
		t.Fatalf("proposed state session = %q, want %q", session, contractWriterD)
	}
}

func TestContractRecordGetUnknownIsNotFound(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedContractHistory(t, st)
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), nil))

	_, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: "synthetic-repo/none"}))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("Get unknown: code = %v, want %v (error %v)", code, connect.CodeNotFound, err)
	}
}

func TestContractRecordCommandsRecordTheOwner(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedContractSession(t, st, contractSessionID)
	proposal := contractTestVersion(t, contractTestBody(contractIDD, "Synthetic delta", "limited"), 1,
		contractWriterD, "Synthetic proposal.", contractTimeD, nil)
	if err := st.ProposeContract(t.Context(), contractIDD, proposal); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	client := contractsClient(t, contractsHandler(t, store.NewReader(path), serveContractEngine(t, st)))
	// Each call names a session row in its header. serve must not forward the header, so orreryd records the owner.
	withSessionHeader := func(message connect.AnyRequest) {
		message.Header().Set(protocol.HeaderSession, contractSessionID)
	}
	deltaAt := func(state string, changed int64) *orreryv1.Contract {
		return &orreryv1.Contract{
			Id: contractIDD, Repository: "/synthetic/repo", Title: "Synthetic delta", Care: "limited",
			State: state, Version: 1, Conversation: contractWriterD, Created: contractTimeD, Changed: changed,
		}
	}

	accept := connect.NewRequest(&orreryv1.ContractsAcceptRequest{Id: contractIDD, Words: "Synthetic accept."})
	withSessionHeader(accept)
	acceptReply, err := client.Accept(t.Context(), accept)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if want := deltaAt("accepted", contractTimeEngine); !proto.Equal(acceptReply.Msg.GetContract(), want) {
		t.Fatalf("Accept = %v, want %v", acceptReply.Msg.GetContract(), want)
	}

	setAside := connect.NewRequest(&orreryv1.ContractsSetAsideRequest{Id: contractIDD, Words: "Synthetic set aside."})
	withSessionHeader(setAside)
	setAsideReply, err := client.SetAside(t.Context(), setAside)
	if err != nil {
		t.Fatalf("SetAside: %v", err)
	}
	if want := deltaAt("set_aside", contractTimeEngine); !proto.Equal(setAsideReply.Msg.GetContract(), want) {
		t.Fatalf("SetAside = %v, want %v", setAsideReply.Msg.GetContract(), want)
	}

	reopen := connect.NewRequest(&orreryv1.ContractsReopenRequest{Id: contractIDD, Words: "Synthetic reopen."})
	withSessionHeader(reopen)
	reopenReply, err := client.Reopen(t.Context(), reopen)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	if want := deltaAt("proposed", contractTimeEngine); !proto.Equal(reopenReply.Msg.GetContract(), want) {
		t.Fatalf("Reopen = %v, want %v", reopenReply.Msg.GetContract(), want)
	}

	got, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDD}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertContractStates(t, got.Msg.GetStates(), []*orreryv1.ContractStateChange{
		{State: "proposed", Version: 1, By: "session", Session: contractWriterD, Words: "Synthetic proposal.",
			Time: contractTimeD},
		{State: "accepted", Version: 1, By: "owner", Words: "Synthetic accept.", Time: contractTimeEngine},
		{State: "set_aside", Version: 1, By: "owner", Words: "Synthetic set aside.", Time: contractTimeEngine},
		{State: "proposed", Version: 1, By: "owner", Words: "Synthetic reopen.", Time: contractTimeEngine},
	})
}

func TestContractRecordWithoutStoreOrEngineAnswersUnavailable(t *testing.T) {
	t.Parallel()
	client := contractsClient(t, contractsHandler(t, nil, nil))

	calls := map[string]func() error{
		"List": func() error {
			_, err := client.List(t.Context(), connect.NewRequest(&orreryv1.ContractsListRequest{}))
			return err
		},
		"Get": func() error {
			_, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.ContractsGetRequest{Id: contractIDA}))
			return err
		},
		"Accept": func() error {
			_, err := client.Accept(t.Context(), connect.NewRequest(&orreryv1.ContractsAcceptRequest{Id: contractIDA}))
			return err
		},
	}
	for name, call := range calls {
		if code := connect.CodeOf(call()); code != connect.CodeUnavailable {
			t.Fatalf("%s: code = %v, want %v", name, code, connect.CodeUnavailable)
		}
	}
}

func TestContractRecordGuardRequiresLoginAndClientHeaderOnEveryPath(t *testing.T) {
	t.Parallel()
	handler := contractsHandler(t, nil, nil)
	probes := []struct {
		name   string
		method string
		header http.Header
	}{
		{"POST no login", http.MethodPost, connectHeader("", "application/proto", true)},
		{"POST stranger login", http.MethodPost, connectHeader(otherLogin, "application/proto", true)},
		{"POST no client header", http.MethodPost, connectHeader(allowedLogin, "application/proto", false)},
		{"GET no client header", http.MethodGet, connectHeader(allowedLogin, "", false)},
	}
	for _, path := range contractsProcedures() {
		for _, probe := range probes {
			rec := serve(t, handler, probe.method, path, probe.header, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s with %s: status = %d, want 403", probe.method, path, probe.name, rec.Code)
			}
		}
		getPath := path + "?connect=v1&encoding=json&message=%7B%7D"
		rec := serve(t, handler, http.MethodGet, getPath, connectHeader(allowedLogin, "", true), "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s with login and client header: status = %d, want 405", path, rec.Code)
		}
	}
}
