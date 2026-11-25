package server_test

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/stage"
	"github.com/joshsymonds/orrery/internal/store"
)

// continueServedDesign is the design the continue tests continue, and continueSession is the session that continues it.
const (
	continueServedDesign = "design-continue-served"
	continueSession      = "synthetic-continued"
)

// continueSessions is a session starter that starts continueSession in the store, as the engine does, with the
// directory it is asked for.
type continueSessions struct {
	store *store.Store
}

// Start stores the sessions row of continueSession and returns its id.
func (c continueSessions) Start(ctx context.Context, req enginehost.StartRequest) (string, error) {
	add := store.EngineAdd{ID: continueSession, Dir: req.Dir}
	if err := c.store.ApplyEngine(ctx, []store.EngineOp{add}, time.Unix(designAnswerTime, 0)); err != nil {
		return "", err
	}
	return continueSession, nil
}

// Stop stops nothing: the continue tests never fail a store write after a start.
func (c continueSessions) Stop(context.Context, string) error {
	return nil
}

// serveContinueStage serves a Stage service over st whose sessions start through continueSessions, and returns its
// client. The service is stopped when the test ends.
func serveContinueStage(t *testing.T, st *store.Store) enginev1connect.StageClient {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	stages := stage.NewService(stage.Config{
		Store:    st,
		Sessions: continueSessions{store: st},
		Now:      func() time.Time { return time.Unix(designAnswerTime, 0) },
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

// TestRecordsConversationDesignsContinueServesNewConversation checks that Designs.Continue starts a session through the
// stage's session starter and makes it the design's working conversation, which Designs.Get, Designs.List and the
// design's writes all follow: the conversation the design held is then refused a Stage write that names the new one,
// and the new session is admitted. The owner's call through serve, which names no session, passes.
func TestRecordsConversationDesignsContinueServesNewConversation(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	ctx := t.Context()
	if err := st.CreateDesign(ctx, store.Design{
		ID: continueServedDesign, Title: "Design continued", Conversation: designSession,
		Created: time.Unix(designTime, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	seedContractSession(t, st, designSession)
	stages := serveContinueStage(t, st)
	client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), stages))

	cont, err := client.Continue(ctx, connect.NewRequest(&orreryv1.DesignsContinueRequest{Id: continueServedDesign}))
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if got := cont.Msg.GetSession(); got != continueSession {
		t.Fatalf("Continue session = %q, want %q", got, continueSession)
	}
	if got := cont.Msg.GetDesign().GetConversation(); got != continueSession {
		t.Fatalf("Continue design conversation = %q, want %q", got, continueSession)
	}

	get, err := client.Get(ctx, connect.NewRequest(&orreryv1.DesignsGetRequest{Id: continueServedDesign}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := get.Msg.GetDesign().GetConversation(); got != continueSession {
		t.Fatalf("Get conversation = %q, want the working conversation %q", got, continueSession)
	}
	list, err := client.List(ctx, connect.NewRequest(&orreryv1.DesignsListRequest{}))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, design := range list.Msg.GetDesigns() {
		if design.GetId() == continueServedDesign && design.GetConversation() != continueSession {
			t.Fatalf("List conversation = %q, want %q", design.GetConversation(), continueSession)
		}
	}

	// approachFrom records an approach on the design from session, as the caller of that session makes it. The owner's
	// call through serve names no session.
	approachFrom := func(session string) error {
		req := connect.NewRequest(&enginev1.ApproachRequest{
			Design: continueServedDesign, Name: "Keep the scout", Shape: "a scout beside the design",
		})
		if session != "" {
			req.Header().Set(protocol.HeaderSession, session)
		}
		_, err := stages.Approach(ctx, req)
		return err
	}
	err = approachFrom(designSession)
	if connect.CodeOf(err) != connect.CodePermissionDenied || !strings.Contains(err.Error(), continueSession) {
		t.Fatalf("Approach from the old conversation = %v, want permission denied naming %q", err, continueSession)
	}
	if err = approachFrom(continueSession); err != nil {
		t.Fatalf("Approach from the new conversation: %v", err)
	}
	if err = approachFrom(""); err != nil {
		t.Fatalf("owner Approach through serve: %v", err)
	}
}
