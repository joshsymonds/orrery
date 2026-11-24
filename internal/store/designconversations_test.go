package store

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// conversationDesign is the design the working-conversation tests continue, and conversationFirst, conversationSecond
// and conversationThird are the sessions it has in turn. conversationContract is the contract of a design of one, whose
// conversation is conversationFirst.
const (
	conversationDesign   = "design-conversations"
	conversationFirst    = "session-conversation-first"
	conversationSecond   = "session-conversation-second"
	conversationThird    = "session-conversation-third"
	conversationContract = "synthetic-repo/contract-conversations"
)

// conversationAt returns the time the working-conversation tests stamp their nth continuation at, which is one second
// after the design's creation for n 1, two for n 2, and so on.
func conversationAt(n int64) time.Time {
	return time.Unix(designLogStart+n, 0)
}

// seedConversationSessions adds each of ids to the sessions of s, as the engine starts them.
func seedConversationSessions(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	ops := make([]EngineOp, 0, len(ids))
	for _, id := range ids {
		ops = append(ops, EngineAdd{ID: id, Dir: "/synthetic/dir"})
	}
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(designLogStart, 0)); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// seedConversationDesign creates conversationDesign in s, whose conversation is conversationFirst.
func seedConversationDesign(t *testing.T, s *Store) {
	t.Helper()
	if err := s.CreateDesign(t.Context(), Design{
		ID: conversationDesign, Title: "Design conversations", Conversation: conversationFirst,
		Created: time.Unix(designLogStart, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
}

// conversationOf returns the Conversation of the design record among records with id, or "" when none has it.
func conversationOf(records []DesignRecord, id string) string {
	for _, record := range records {
		if record.ID == id {
			return record.Conversation
		}
	}
	return ""
}

// requireWorkingConversation fails the test unless every read of design names want as its working conversation: the
// store's DesignConversation, Design and Designs, and the Reader's DesignConversation, Design and Designs over path.
func requireWorkingConversation(t *testing.T, s *Store, path, design, want string) {
	t.Helper()
	ctx := t.Context()
	reader := NewReader(path)
	working, err := s.DesignConversation(ctx, design)
	if err != nil {
		t.Fatalf("DesignConversation: %v", err)
	}
	read, err := reader.DesignConversation(ctx, design)
	if err != nil {
		t.Fatalf("Reader.DesignConversation: %v", err)
	}
	record, err := s.Design(ctx, design)
	if err != nil {
		t.Fatalf("Design: %v", err)
	}
	records, err := s.Designs(ctx)
	if err != nil {
		t.Fatalf("Designs: %v", err)
	}
	served, err := reader.Design(ctx, design)
	if err != nil {
		t.Fatalf("Reader.Design: %v", err)
	}
	servedRecords, err := reader.Designs(ctx)
	if err != nil {
		t.Fatalf("Reader.Designs: %v", err)
	}
	got := []string{
		working, read, record.Conversation, conversationOf(records, design), served.Conversation,
		conversationOf(servedRecords, design),
	}
	for _, conversation := range got {
		if conversation != want {
			t.Fatalf("working conversations of design %s = %q, want %q for every read", design, got, want)
		}
	}
}

// TestRecordsConversationNewestIsWorkingAndServed checks that a design has one working conversation at a time: its
// creating conversation until it is continued, then the newest session it continues in, which every read of the design
// names, with each continuation storing the conversation it replaces as its previous.
func TestRecordsConversationNewestIsWorkingAndServed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	seedConversationDesign(t, s)
	seedConversationSessions(t, s, conversationFirst, conversationSecond, conversationThird)
	requireWorkingConversation(t, s, path, conversationDesign, conversationFirst)

	if err := s.ContinueDesign(ctx, conversationDesign, conversationSecond, conversationAt(1)); err != nil {
		t.Fatalf("ContinueDesign second: %v", err)
	}
	requireWorkingConversation(t, s, path, conversationDesign, conversationSecond)

	if err := s.ContinueDesign(ctx, conversationDesign, conversationThird, conversationAt(2)); err != nil {
		t.Fatalf("ContinueDesign third: %v", err)
	}
	requireWorkingConversation(t, s, path, conversationDesign, conversationThird)

	stored, err := sqlcgen.New(s.read).ListDesignConversations(ctx, conversationDesign)
	if err != nil {
		t.Fatalf("ListDesignConversations: %v", err)
	}
	want := []sqlcgen.DesignConversation{
		{
			Design: conversationDesign, Seq: 1, Session: conversationSecond, Previous: conversationFirst,
			At: designLogStart + 1,
		},
		{
			Design: conversationDesign, Seq: 2, Session: conversationThird, Previous: conversationSecond,
			At: designLogStart + 2,
		},
	}
	if !reflect.DeepEqual(stored, want) {
		t.Fatalf("conversations = %+v, want %+v", stored, want)
	}
}

// TestRecordsConversationContinueLogsLine checks that a continuation appends the log line continued, made by orreryd
// with the new session as its id, numbered as the design's next line.
func TestRecordsConversationContinueLogsLine(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openDesignLogStore(t)
	seedConversationDesign(t, s)
	seedConversationSessions(t, s, conversationFirst, conversationSecond)

	if err := s.ContinueDesign(ctx, conversationDesign, conversationSecond, conversationAt(1)); err != nil {
		t.Fatalf("ContinueDesign: %v", err)
	}
	lines, err := s.DesignLog(ctx, conversationDesign)
	if err != nil {
		t.Fatalf("DesignLog: %v", err)
	}
	want := []DesignLogLine{{
		Design: conversationDesign, Seq: 1, At: designLogStart + 1, Maker: LogMakerOrreryd, What: continuedWhat,
		IDs: []string{conversationSecond},
	}}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("log = %+v, want %+v", lines, want)
	}
}

// TestRecordsConversationContinueOfDesignOfOne checks that a contract in no design continues as a design of one: its
// working conversation is its writer until a continuation, and then the session the continuation names.
func TestRecordsConversationContinueOfDesignOfOne(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	seedConversationSessions(t, s, conversationFirst, conversationSecond)
	if err := s.ProposeContract(ctx, conversationContract, ContractVersion{
		Repository: "/work/synthetic-repo", Title: "Conversation contract", Care: "serious",
		Body: `{"id":"synthetic-conversation"}`, Words: "Propose the synthetic contract", Writer: conversationFirst,
		At: designLogStart,
	}); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}
	requireWorkingConversation(t, s, path, conversationContract, conversationFirst)

	if err := s.ContinueDesign(ctx, conversationContract, conversationSecond, conversationAt(1)); err != nil {
		t.Fatalf("ContinueDesign: %v", err)
	}
	requireWorkingConversation(t, s, path, conversationContract, conversationSecond)
	record, err := s.Design(ctx, conversationContract)
	if err != nil {
		t.Fatalf("Design: %v", err)
	}
	if !record.OfOne {
		t.Fatalf("Design %s OfOne = false, want a design of one", conversationContract)
	}
}

// TestRecordsConversationContinueStoresNothingWithoutDesign checks that a continuation of a design no record holds is
// refused as ErrNoDesign and stores no conversation and no log line.
func TestRecordsConversationContinueStoresNothingWithoutDesign(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openDesignLogStore(t)
	seedConversationSessions(t, s, conversationSecond)

	err := s.ContinueDesign(ctx, "design-missing", conversationSecond, conversationAt(1))
	if !errors.Is(err, ErrNoDesign) {
		t.Fatalf("ContinueDesign of a missing design = %v, want ErrNoDesign", err)
	}
	stored, err := sqlcgen.New(s.read).ListDesignConversations(ctx, "design-missing")
	if err != nil {
		t.Fatalf("ListDesignConversations: %v", err)
	}
	lines, err := s.DesignLog(ctx, "design-missing")
	if err != nil {
		t.Fatalf("DesignLog: %v", err)
	}
	if len(stored) != 0 || len(lines) != 0 {
		t.Fatalf("stored conversations %+v and log %+v, want none", stored, lines)
	}
}
