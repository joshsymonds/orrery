package stepmcp

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/joshsymonds/orrery/internal/cli"
	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/stage"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// keepStrikeSession is the sessions row of the design's conversation, which the MCP client runs in, and
// keepStrikeContract is the proposed contract the tools keep and strike the additions of.
const (
	keepStrikeSession  = "conversation-keep-strike"
	keepStrikeContract = "keep-strike-contract"
)

// keepStrikeNow is the synthetic clock the Stage service stamps its records with.
func keepStrikeNow() time.Time { return time.Unix(1700005000, 0) }

// keepStrikeStage serves the Stage service over a store seeded with keepStrikeSession and with keepStrikeContract
// proposed with the additions A1 and A2. It returns the store, and the Stage client a session dials that names
// keepStrikeSession as its caller. The server stops at cleanup.
func keepStrikeStage(t *testing.T) (*store.Store, enginev1connect.StageClient) {
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
	engine := store.EngineAdd{ID: keepStrikeSession, Dir: t.TempDir()}
	if err := st.ApplyEngine(t.Context(), []store.EngineOp{engine}, keepStrikeNow()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	body, err := protojson.Marshal(&v1.EpicContract{
		Title: "Keep and strike", Care: "serious", Intent: "Synthetic intent.",
		Additions: []*v1.EpicAddition{
			{Location: "internal/stage", What: "first", Why: "Needed", Cost: "None"},
			{Location: "internal/store", What: "second", Why: "Needed", Cost: "None"},
		},
	})
	if err != nil {
		t.Fatalf("Marshal body: %v", err)
	}
	if err := st.ProposeContract(t.Context(), keepStrikeContract, store.ContractVersion{
		Version: 1, Repository: "/synthetic/repo", Title: "Keep and strike", Care: "serious", Body: string(body),
		Words: "Propose the contract", Writer: keepStrikeSession, At: keepStrikeNow().Unix(),
	}); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	socket := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := enginehost.Listen(socket)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	stages := stage.NewService(stage.Config{Store: st, Now: keepStrikeNow})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- enginehost.ServeWith(ctx, ln, enginehost.ServeConfig{Stage: stages}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith: %v", err)
		}
	})
	getenv := func(name string) string {
		if name == protocol.EnvSession {
			return keepStrikeSession
		}
		return ""
	}
	return st, enginev1connect.NewStageClient(cli.SessionHTTPClient(socket), cli.SessionBaseURL,
		connect.WithInterceptors(CallerInterceptor(getenv)))
}

// keepStrikeCall calls the tool name of p with arguments, fails the test when the tool refuses, and returns its text.
func keepStrikeCall(t *testing.T, p *peer, name, arguments string) string {
	t.Helper()
	outcome := p.tool(name, arguments)
	if outcome.isError {
		t.Fatalf("%s refused: %s", name, outcome.text)
	}
	return outcome.text
}

// TestRecordsKeepStrikeMCPToolsKeepAndStrikeWithTheirMakers checks that contract_keep and contract_strike, called
// through the MCP server from the design's conversation session, store their writes with the makers they name, and that
// the striking removes the row that depends on the addition, which contract_row wrote through the same server.
func TestRecordsKeepStrikeMCPToolsKeepAndStrikeWithTheirMakers(t *testing.T) {
	t.Parallel()
	st, stages := keepStrikeStage(t)
	server, err := NewServer(nil, nil, nil, nil, WithStage(stages))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	p, _ := servePipes(t, server)
	initialize(p)
	at := keepStrikeNow().Unix()

	var kept v1.KeepResponse
	if err := protojson.Unmarshal([]byte(keepStrikeCall(t, p, "contract_keep",
		`{"contract":"keep-strike-contract","addition":"A1","words":"Keep the first.","maker":"conversation",`+
			`"reason":"The owner left the additions to the conversation","delegation":"Decide the additions"}`),
	), &kept); err != nil {
		t.Fatalf("decode contract_keep: %v", err)
	}
	if kept.GetWrite().GetRow() != "A1" || kept.GetWrite().GetAction() != store.RowActionKeep {
		t.Fatalf("contract_keep wrote %v, want a keep of A1", kept.GetWrite())
	}

	keepStrikeCall(t, p, "contract_row", `{"contract":"keep-strike-contract","row":"R1",`+
		`"value":"{\"text\":\"Depends on the second\",\"evidence\":\"Test passes\",\"kind\":\"test\"}",`+
		`"depends":"A2","words":"Depends on the second.","maker":"owner_conversation",`+
		`"quote":"Make the requirement depend on the second addition."}`)

	var struck v1.StrikeResponse
	if err := protojson.Unmarshal([]byte(keepStrikeCall(t, p, "contract_strike",
		`{"contract":"keep-strike-contract","addition":"A2","words":"Strike the second.","maker":"owner_conversation",`+
			`"quote":"Strike the second addition."}`),
	), &struck); err != nil {
		t.Fatalf("decode contract_strike: %v", err)
	}
	if !reflect.DeepEqual(struck.GetRemoved(), []string{"R1"}) {
		t.Fatalf("contract_strike removed %q, want the row that depends on A2", struck.GetRemoved())
	}

	want := []store.ContractRowWrite{
		{
			Contract: keepStrikeContract, Seq: 1, Version: 1, Row: "A1", Action: store.RowActionKeep,
			Words: "Keep the first.", Maker: store.StageMakerConversation,
			MakerReason: "The owner left the additions to the conversation", Delegation: "Decide the additions",
			Session: keepStrikeSession, At: at,
		},
		{
			Contract: keepStrikeContract, Seq: 2, Version: 1, Row: "R1", Action: store.RowActionWrite, Depends: "A2",
			Words: "Depends on the second.", Maker: store.StageMakerOwnerConversation,
			Quote: "Make the requirement depend on the second addition.", Session: keepStrikeSession, At: at,
		},
		{
			Contract: keepStrikeContract, Seq: 3, Version: 1, Row: "A2", Action: store.RowActionStrike,
			Words: "Strike the second.", Maker: store.StageMakerOwnerConversation, Quote: "Strike the second addition.",
			Session: keepStrikeSession, At: at,
		},
		{
			Contract: keepStrikeContract, Seq: 4, Version: 1, Row: "R1", Action: store.RowActionRemove, Depends: "A2",
			Words: "Strike the second.", Maker: store.StageMakerOwnerConversation, Quote: "Strike the second addition.",
			Session: keepStrikeSession, At: at,
		},
	}
	stored, err := st.ContractRowWrites(t.Context(), keepStrikeContract)
	if err != nil {
		t.Fatalf("ContractRowWrites: %v", err)
	}
	if !reflect.DeepEqual(stored, want) {
		t.Fatalf("row writes = %+v, want %+v", stored, want)
	}
}
