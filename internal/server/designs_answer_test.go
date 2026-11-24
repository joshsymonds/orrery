package server_test

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/stage"
	"github.com/joshsymonds/orrery/internal/store"
)

// designAnswerDesign is the design the answer tests ask their question on, and designAnswerSession is the sessions row
// a session header names in the answer tests.
const (
	designAnswerDesign  = "design-answer"
	designAnswerSession = "session-answer"
)

// designAnswerTime is the unix seconds the answer tests store as the creation of the design, the question and the
// answer.
const designAnswerTime int64 = 1_700_000_300

// seedDesignQuestion creates the design designAnswerDesign in st with one open question, which recommends its first
// option.
func seedDesignQuestion(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := t.Context()
	if err := st.CreateDesign(ctx, store.Design{
		ID: designAnswerDesign, Title: "Design answer", Conversation: designSession,
		Created: time.Unix(designAnswerTime, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	if _, err := st.AskDesignQuestion(ctx, store.DesignQuestion{
		Design:         designAnswerDesign,
		Text:           "Should the scout stay?",
		Options:        []string{"Keep the scout", "Drop the scout"},
		Recommendation: "Keep the scout",
		Asked:          time.Unix(designAnswerTime, 0),
	}); err != nil {
		t.Fatalf("AskDesignQuestion: %v", err)
	}
}

// serveStageEngine serves orreryd's Stage service over st on a loopback listener until the test ends, with the fixed
// answer clock. It returns a generated Stage client for the listener.
func serveStageEngine(t *testing.T, st *store.Store) enginev1connect.StageClient {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	stages := stage.NewService(stage.Config{
		Store: st,
		Now:   func() time.Time { return time.Unix(designAnswerTime, 0) },
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- enginehost.ServeWith(ctx, ln, enginehost.ServeConfig{Stage: stages}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith: %v", err)
		}
	})
	return enginev1connect.NewStageClient(http.DefaultClient, "http://"+ln.Addr().String())
}

// designsAnswerHandler returns the guarded handler of a server reading designs from reader and forwarding the answers
// to stages, which is nil for none.
func designsAnswerHandler(t *testing.T, reader *store.Reader, stages enginev1connect.StageClient) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:        []string{allowedLogin},
		Tmux:          newTmuxClient(t),
		Push:          newPushStore(t),
		Sessions:      reader,
		StageCommands: stages,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

func TestStageQuestionsServeAnswerStoresOwnerServe(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedDesignQuestion(t, st)
	seedContractSession(t, st, designAnswerSession)
	client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), serveStageEngine(t, st)))

	// The owner's call names a session in its header. serve forwards no header, so orreryd records the owner through
	// serve.
	req := connect.NewRequest(&orreryv1.DesignsAnswerRequest{
		Design: designAnswerDesign, Question: 1, Answer: "Keep the scout",
	})
	req.Header().Set(protocol.HeaderSession, designAnswerSession)
	reply, err := client.Answer(t.Context(), req)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if got := reply.Msg.GetAnswer(); got != "Keep the scout" {
		t.Fatalf("Answer = %q, want the owner's answer as stored", got)
	}

	stored, err := st.DesignQuestions(t.Context(), designAnswerDesign)
	if err != nil {
		t.Fatalf("DesignQuestions: %v", err)
	}
	if len(stored) != 1 || stored[0].Answer != "Keep the scout" || stored[0].Maker.Kind != store.StageMakerOwnerServe ||
		stored[0].Answerer != "" || stored[0].Answered.Unix() != designAnswerTime {
		t.Fatalf("stored questions = %+v, want the answer made by owner_serve with no answering session", stored)
	}
}

func TestStageQuestionsServeAnswerPassesRefusalThrough(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedDesignQuestion(t, st)
	client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), serveStageEngine(t, st)))
	answer := &orreryv1.DesignsAnswerRequest{Design: designAnswerDesign, Question: 1, Answer: "Keep the scout"}
	if _, err := client.Answer(t.Context(), connect.NewRequest(answer)); err != nil {
		t.Fatalf("first Answer: %v", err)
	}

	_, err := client.Answer(t.Context(), connect.NewRequest(answer))
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("second Answer code = %v, want %v (err %v)", code, connect.CodeFailedPrecondition, err)
	}
}

func TestStageQuestionsServeAnswerWithoutStageAnswersUnavailable(t *testing.T) {
	t.Parallel()
	client := designsClient(t, designsAnswerHandler(t, nil, nil))
	_, err := client.Answer(t.Context(), connect.NewRequest(&orreryv1.DesignsAnswerRequest{
		Design: designAnswerDesign, Question: 1, Answer: "Keep the scout",
	}))
	if code := connect.CodeOf(err); code != connect.CodeUnavailable {
		t.Fatalf("Answer code = %v, want %v (err %v)", code, connect.CodeUnavailable, err)
	}
}
