package stepmcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/contracttext"
	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// contractTestSession is the sessions row the contract tests' caller resolves to, the writer of each proposal.
const contractTestSession = "engine-1"

// contractTestID is the id of the synthetic contract the tests propose.
const contractTestID = "demo-0a1b2c3/epic-login"

// contractTestWords are the words the tests give with each write.
const contractTestWords = "Synthetic words."

// contractTestNow is the fixed clock of the contract daemon: the time each stored version and state carries.
func contractTestNow() time.Time { return time.Unix(1700004000, 0) }

// contractTestGetenv is the environment the daemon's checks resolve in: a home with no pool file.
func contractTestGetenv(key string) string {
	if key == "HOME" {
		return "/nonexistent/orrery-test-home"
	}
	return ""
}

// contractTestCaller is the environment the MCP client runs in: the session contractTestSession.
func contractTestCaller(name string) string {
	if name == protocol.EnvSession {
		return contractTestSession
	}
	return ""
}

// startContractDaemon serves the Contracts service over a store seeded with the session contractTestSession, on a
// sessions socket in a temporary directory. It returns the client a session dials it with. The server stops at cleanup.
func startContractDaemon(t *testing.T) enginev1connect.ContractsClient {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, storetest.NoSync())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	engine := store.EngineAdd{ID: contractTestSession, Dir: t.TempDir()}
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{engine}, contractTestNow()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	socket := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := enginehost.Listen(socket)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	contracts := &enginehost.Contracts{Store: st, Now: contractTestNow, Getenv: contractTestGetenv}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- enginehost.Serve(ctx, ln, nil, nil, nil, nil, nil, nil, nil, nil, nil, contracts) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return enginev1connect.NewContractsClient(cli.SessionHTTPClient(socket), cli.SessionBaseURL,
		connect.WithInterceptors(CallerInterceptor(contractTestCaller)))
}

// contractPeer serves an MCP server whose contract tools call contracts and completes the handshake.
func contractPeer(t *testing.T, contracts enginev1connect.ContractsClient) *peer {
	t.Helper()
	server, err := NewServer(nil, nil, contracts, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	p, _ := servePipes(t, server)
	initialize(p)
	return p
}

// contractTestGit runs git in dir with the test identity and returns its trimmed output. It fails the test on error.
func contractTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test",
		"-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// contractTestRepo makes a git repository under t.TempDir() with one commit on main. It returns the directory and HEAD.
func contractTestRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	contractTestGit(t, dir, "init", "-q", "-b", "main")
	contractTestGit(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	return dir, contractTestGit(t, dir, "rev-parse", "HEAD")
}

// contractTestBody returns a valid contract body under contractTestID, based at base in the repository repo.
func contractTestBody(repo, base string) map[string]any {
	item := map[string]any{
		"kind": "test", "capture": []string{"scripts/check"}, "limit": 60, "conclusion": "Synthetic conclusion.",
		"judge": "reviewer", "tests": []string{"internal/synthetic/check_test.go"},
	}
	return map[string]any{
		"id":         contractTestID,
		"repository": repo,
		"title":      "Synthetic contract",
		"approved":   "Synthetic approval sentence.",
		"intent":     "Synthetic intent.",
		"failures": []map[string]any{
			{"id": "F1", "text": "Synthetic failure one.", "rating": "serious", "response": "Synthetic response one."},
			{"id": "F2", "text": "Synthetic failure two.", "rating": "severe", "response": "Synthetic response two."},
		},
		"care": "serious",
		"additions": []map[string]any{
			{
				"location": "R1",
				"what":     "Synthetic addition one.",
				"why":      "Synthetic reason.",
				"cost":     "Synthetic cost.",
			},
			{"location": "docs/guide.md", "what": "Synthetic note.", "why": "Synthetic reason.", "cost": "None."},
		},
		"requirements": []map[string]any{
			{"id": "R1", "text": "Synthetic requirement one.", "item": item},
			{"id": "R2", "text": "Synthetic requirement two.", "item": item},
		},
		"premises": []map[string]any{
			{"id": "P1", "text": "Synthetic premise one.", "ifWrong": "Synthetic consequence one."},
		},
		"exclusions": []string{"Synthetic exclusion one.", "Synthetic exclusion two."},
		"approach":   "Synthetic approach.",
		"alternatives": []map[string]any{
			{
				"alternative": "Synthetic alternative one.",
				"whyNot":      "Synthetic reason.",
				"reconsider":  "Synthetic trigger.",
			},
			{"alternative": "Synthetic alternative two.", "whyNot": "Synthetic reason.", "reconsider": "Never."},
		},
		"releaseSteps": []map[string]any{
			{"step": "Synthetic release one.", "target": "Synthetic target one.", "undo": "Synthetic undo one."},
		},
		"releaseThen": "Synthetic release line.",
		"checks":      "go test ./...\nQuality Bar: " + contracttext.QualityBar,
		"base":        base,
		"gates": []map[string]any{
			{"name": "check", "argv": []string{"scripts/check"}, "limit": 60},
			{"name": "review", "agent": "reviewer", "limit": 300, "must": []string{"Synthetic statement holds."}},
		},
		"doneGates": []string{"check"},
	}
}

// contractTestDecisions returns two decisions: D1, open, and D2, answered.
func contractTestDecisions() []map[string]any {
	return []map[string]any{
		{
			"id": "D1", "question": "Synthetic question one.",
			"options":        []string{"Synthetic option one.", "Synthetic option two."},
			"recommendation": "Synthetic option one.", "reason": "Synthetic reason.", "row": "F1",
		},
		{
			"id": "D2", "question": "Synthetic question two.", "options": []string{"Synthetic option three."},
			"recommendation": "Synthetic option three.", "reason": "Synthetic reason.",
			"answer": "Synthetic option three.",
		},
	}
}

// writeContractFile writes a contract file holding contract and decisions under a temporary directory, and returns its
// path.
func writeContractFile(t *testing.T, contract map[string]any, decisions []map[string]any) string {
	t.Helper()
	file := map[string]any{"decisions": decisions}
	for key, value := range contract {
		file[key] = value
	}
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal contract file: %v", err)
	}
	path := filepath.Join(t.TempDir(), "contract.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write contract file: %v", err)
	}
	return path
}

// contractTestArgs returns the arguments of a tool call as a JSON object text.
func contractTestArgs(t *testing.T, arguments map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	return string(raw)
}

// decodeContractReply decodes a tool's text as the protobuf JSON of msg. It fails the test when the text does not decode.
func decodeContractReply(t *testing.T, text string, msg proto.Message) {
	t.Helper()
	if err := protojson.Unmarshal([]byte(text), msg); err != nil {
		t.Fatalf("decode reply %s: %v", text, err)
	}
}

// fakeContracts answers the write commands with a stored contract that carries the request's id, and records each
// request. The reads and the propose are not expected here.
type fakeContracts struct {
	mu        sync.Mutex
	revises   []*v1.ReviseContractRequest
	accepts   []*v1.AcceptContractRequest
	setAsides []*v1.SetAsideContractRequest
	reopens   []*v1.ReopenContractRequest
}

func (f *fakeContracts) ProposeContract(
	context.Context, *connect.Request[v1.ProposeContractRequest],
) (*connect.Response[v1.ProposeContractResponse], error) {
	return nil, errors.New("stepmcp test: ProposeContract is not expected")
}

func (f *fakeContracts) DraftContract(
	context.Context, *connect.Request[v1.DraftContractRequest],
) (*connect.Response[v1.DraftContractResponse], error) {
	return nil, errors.New("stepmcp test: DraftContract is not expected")
}

func (f *fakeContracts) ReviseContract(
	_ context.Context,
	req *connect.Request[v1.ReviseContractRequest],
) (*connect.Response[v1.ReviseContractResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revises = append(f.revises, req.Msg)
	return connect.NewResponse(&v1.ReviseContractResponse{Contract: &v1.StoredContract{Id: req.Msg.GetId()}}), nil
}

func (f *fakeContracts) AcceptContract(
	_ context.Context,
	req *connect.Request[v1.AcceptContractRequest],
) (*connect.Response[v1.AcceptContractResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accepts = append(f.accepts, req.Msg)
	return connect.NewResponse(&v1.AcceptContractResponse{Contract: &v1.StoredContract{Id: req.Msg.GetId()}}), nil
}

func (f *fakeContracts) SetAsideContract(
	_ context.Context,
	req *connect.Request[v1.SetAsideContractRequest],
) (*connect.Response[v1.SetAsideContractResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setAsides = append(f.setAsides, req.Msg)
	return connect.NewResponse(&v1.SetAsideContractResponse{Contract: &v1.StoredContract{Id: req.Msg.GetId()}}), nil
}

func (f *fakeContracts) ReopenContract(
	_ context.Context,
	req *connect.Request[v1.ReopenContractRequest],
) (*connect.Response[v1.ReopenContractResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reopens = append(f.reopens, req.Msg)
	return connect.NewResponse(&v1.ReopenContractResponse{Contract: &v1.StoredContract{Id: req.Msg.GetId()}}), nil
}

func (f *fakeContracts) GetContract(
	context.Context, *connect.Request[v1.GetContractRequest],
) (*connect.Response[v1.GetContractResponse], error) {
	return nil, errors.New("stepmcp test: GetContract is not expected")
}

func (f *fakeContracts) ListContracts(
	context.Context, *connect.Request[v1.ListContractsRequest],
) (*connect.Response[v1.ListContractsResponse], error) {
	return nil, errors.New("stepmcp test: ListContracts is not expected")
}

func (f *fakeContracts) ProposeWorking(
	context.Context, *connect.Request[v1.ProposeWorkingRequest],
) (*connect.Response[v1.ProposeWorkingResponse], error) {
	return nil, errors.New("stepmcp test: ProposeWorking is not expected")
}

// fakeContractCalls is a copy of what the fake recorded.
type fakeContractCalls struct {
	revises   []*v1.ReviseContractRequest
	accepts   []*v1.AcceptContractRequest
	setAsides []*v1.SetAsideContractRequest
	reopens   []*v1.ReopenContractRequest
}

func (f *fakeContracts) calls() fakeContractCalls {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeContractCalls{
		revises:   append([]*v1.ReviseContractRequest(nil), f.revises...),
		accepts:   append([]*v1.AcceptContractRequest(nil), f.accepts...),
		setAsides: append([]*v1.SetAsideContractRequest(nil), f.setAsides...),
		reopens:   append([]*v1.ReopenContractRequest(nil), f.reopens...),
	}
}

// TestContractRecordProposeStoresAndReadsBack checks that contract_propose over a real daemon stores the file's
// contract as version 1 in state proposed, at the clock's time, with the caller's session as writer, the words and the
// file's decisions, and that contract_get and contract_list read the same contract back.
func TestContractRecordProposeStoresAndReadsBack(t *testing.T) {
	t.Parallel()
	p := contractPeer(t, startContractDaemon(t))
	repo, base := contractTestRepo(t)
	path := writeContractFile(t, contractTestBody(repo, base), contractTestDecisions())

	proposed := p.tool("contract_propose", contractTestArgs(t, map[string]any{
		"id": contractTestID, "path": path, "words": contractTestWords,
	}))
	if proposed.isError {
		t.Fatalf("contract_propose refused: %s", proposed.text)
	}
	var reply v1.ProposeContractResponse
	decodeContractReply(t, proposed.text, &reply)
	stored := reply.GetContract()
	at := contractTestNow().Unix()
	if stored.GetId() != contractTestID || stored.GetState() != "proposed" || stored.GetVersion() != 1 {
		t.Fatalf("stored id, state and version = %s, %s, %d; want %s, proposed, 1",
			stored.GetId(), stored.GetState(), stored.GetVersion(), contractTestID)
	}
	if stored.GetCreated() != at || stored.GetChanged() != at {
		t.Errorf("created, changed = %d, %d; want the clock's %d", stored.GetCreated(), stored.GetChanged(), at)
	}
	if stored.GetConversation() != contractTestSession {
		t.Errorf("conversation = %q, want the caller's session %q", stored.GetConversation(), contractTestSession)
	}
	if len(stored.GetVersions()) != 1 {
		t.Fatalf("versions = %d, want 1", len(stored.GetVersions()))
	}
	version := stored.GetVersions()[0]
	if version.GetWords() != contractTestWords || version.GetWriter() != contractTestSession ||
		version.GetTime() != at {
		t.Errorf("version words, writer, time = %q, %q, %d; want %q, %q, %d", version.GetWords(), version.GetWriter(),
			version.GetTime(), contractTestWords, contractTestSession, at)
	}
	decisions := version.GetDecisions()
	if len(decisions) != 2 || decisions[0].GetId() != "D1" || decisions[1].GetId() != "D2" ||
		decisions[1].GetAnswer() != "Synthetic option three." {
		t.Errorf("version decisions = %v, want D1 open and D2 answered", decisions)
	}

	got := p.tool("contract_get", contractTestArgs(t, map[string]any{"id": contractTestID}))
	if got.isError {
		t.Fatalf("contract_get refused: %s", got.text)
	}
	var read v1.GetContractResponse
	decodeContractReply(t, got.text, &read)
	if !proto.Equal(read.GetContract(), stored) {
		t.Errorf("contract_get = %v, want the proposed contract %v", read.GetContract(), stored)
	}

	listed := p.tool("contract_list", `{}`)
	if listed.isError {
		t.Fatalf("contract_list refused: %s", listed.text)
	}
	var list v1.ListContractsResponse
	decodeContractReply(t, listed.text, &list)
	if len(list.GetContracts()) != 1 || list.GetContracts()[0].GetId() != contractTestID {
		t.Errorf("contract_list = %v, want the one proposed contract", list.GetContracts())
	}
}

// TestContractRecordProposeRefusesAPathThatDoesNotRead checks that contract_propose refuses a path that does not read,
// naming the path, and stores nothing.
func TestContractRecordProposeRefusesAPathThatDoesNotRead(t *testing.T) {
	t.Parallel()
	p := contractPeer(t, startContractDaemon(t))
	missing := filepath.Join(t.TempDir(), "missing.json")

	refused := p.tool("contract_propose", contractTestArgs(t, map[string]any{
		"id": contractTestID, "path": missing, "words": contractTestWords,
	}))
	if !refused.isError {
		t.Fatalf("contract_propose of a missing file succeeded: %s", refused.text)
	}
	if !strings.Contains(refused.text, missing) {
		t.Errorf("refusal %q does not name the path %s", refused.text, missing)
	}

	listed := p.tool("contract_list", `{}`)
	var list v1.ListContractsResponse
	decodeContractReply(t, listed.text, &list)
	if len(list.GetContracts()) != 0 {
		t.Errorf("contract_list after a refused propose = %v, want none", list.GetContracts())
	}
}

// TestContractRecordWriteToolsSendTheirRequests checks that contract_revise, contract_accept, contract_set_aside and
// contract_reopen send their request's id and words, and that contract_revise sends the file's body and decisions.
func TestContractRecordWriteToolsSendTheirRequests(t *testing.T) {
	t.Parallel()
	repo, base := contractTestRepo(t)
	path := writeContractFile(t, contractTestBody(repo, base), contractTestDecisions())

	t.Run("contract_revise", func(t *testing.T) {
		t.Parallel()
		fake := &fakeContracts{}
		p := contractPeer(t, fake)
		outcome := p.tool("contract_revise", contractTestArgs(t, map[string]any{
			"id": contractTestID, "path": path, "words": contractTestWords,
		}))
		if outcome.isError {
			t.Fatalf("contract_revise refused: %s", outcome.text)
		}
		revises := fake.calls().revises
		if len(revises) != 1 {
			t.Fatalf("revise requests = %d, want 1", len(revises))
		}
		req := revises[0]
		if req.GetId() != contractTestID || req.GetWords() != contractTestWords {
			t.Errorf("revise id, words = %q, %q; want %q, %q", req.GetId(), req.GetWords(), contractTestID,
				contractTestWords)
		}
		if req.GetContract().GetId() != contractTestID || req.GetContract().GetBase() != base ||
			req.GetContract().GetTitle() != "Synthetic contract" || len(req.GetContract().GetRequirements()) != 2 {
			t.Errorf("revise body = %v, want the file's contract", req.GetContract())
		}
		decisions := req.GetDecisions()
		if len(decisions) != 2 || decisions[0].GetId() != "D1" ||
			decisions[1].GetAnswer() != "Synthetic option three." {
			t.Errorf("revise decisions = %v, want the file's D1 and D2", decisions)
		}
	})

	t.Run("contract_accept", func(t *testing.T) {
		t.Parallel()
		fake := &fakeContracts{}
		p := contractPeer(t, fake)
		outcome := p.tool("contract_accept", contractTestArgs(t, map[string]any{
			"id": contractTestID, "words": "Synthetic accept.",
		}))
		if outcome.isError {
			t.Fatalf("contract_accept refused: %s", outcome.text)
		}
		accepts := fake.calls().accepts
		if len(accepts) != 1 || accepts[0].GetId() != contractTestID || accepts[0].GetWords() != "Synthetic accept." {
			t.Errorf("accept requests = %v, want one for %s with the words", accepts, contractTestID)
		}
	})

	t.Run("contract_set_aside", func(t *testing.T) {
		t.Parallel()
		fake := &fakeContracts{}
		p := contractPeer(t, fake)
		outcome := p.tool("contract_set_aside", contractTestArgs(t, map[string]any{
			"id": contractTestID, "words": "Synthetic set aside.",
		}))
		if outcome.isError {
			t.Fatalf("contract_set_aside refused: %s", outcome.text)
		}
		setAsides := fake.calls().setAsides
		if len(setAsides) != 1 || setAsides[0].GetId() != contractTestID ||
			setAsides[0].GetWords() != "Synthetic set aside." {
			t.Errorf("set-aside requests = %v, want one for %s with the words", setAsides, contractTestID)
		}
	})

	t.Run("contract_reopen", func(t *testing.T) {
		t.Parallel()
		fake := &fakeContracts{}
		p := contractPeer(t, fake)
		outcome := p.tool("contract_reopen", contractTestArgs(t, map[string]any{
			"id": contractTestID, "words": "Synthetic reopen.",
		}))
		if outcome.isError {
			t.Fatalf("contract_reopen refused: %s", outcome.text)
		}
		reopens := fake.calls().reopens
		if len(reopens) != 1 || reopens[0].GetId() != contractTestID || reopens[0].GetWords() != "Synthetic reopen." {
			t.Errorf("reopen requests = %v, want one for %s with the words", reopens, contractTestID)
		}
	})
}

// TestContractRecordCallContractToolRunsTheNamedTool checks that CallContractTool runs the contract tool it names, and
// refuses a name that no contract tool has.
func TestContractRecordCallContractToolRunsTheNamedTool(t *testing.T) {
	t.Parallel()
	fake := &fakeContracts{}
	result, err := CallContractTool(t.Context(), fake, "contract_accept",
		json.RawMessage(`{"id":"`+contractTestID+`","words":"Synthetic accept."}`))
	if err != nil {
		t.Fatalf("CallContractTool contract_accept: %v", err)
	}
	if result.IsError {
		t.Fatalf("contract_accept result is an error: %+v", result.Content)
	}
	accepts := fake.calls().accepts
	if len(accepts) != 1 || accepts[0].GetId() != contractTestID {
		t.Errorf("accept requests = %v, want one for %s", accepts, contractTestID)
	}

	if _, err := CallContractTool(t.Context(), fake, "epic_create", json.RawMessage(`{}`)); err == nil {
		t.Error("CallContractTool of an epic tool succeeded, want an unknown contract tool error")
	}
}

// TestContractRecordWriteToolsAreMutating checks that each contract write is a mutating tool, whose cancel does not
// reach its daemon call, and that the reads are not.
func TestContractRecordWriteToolsAreMutating(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"contract_propose", "contract_revise", "contract_accept", "contract_set_aside",
		"contract_reopen"} {
		if !mutatingTool(name) {
			t.Errorf("%s is not a mutating tool", name)
		}
	}
	for _, name := range []string{"contract_get", "contract_list"} {
		if mutatingTool(name) {
			t.Errorf("%s is a mutating tool", name)
		}
	}
}
