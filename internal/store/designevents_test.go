package store

import (
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// designEventSession is the sessions row id of the working conversation the design event tests give their designs.
const designEventSession = "session-one"

// addDesignSession stores the idle engine session id, which a design event test names as a conversation.
func addDesignSession(t *testing.T, s *Store, id string) {
	t.Helper()
	add := EngineAdd{ID: id, Dir: "/work/engine", State: "idle", Kind: KindEngine}
	if err := s.ApplyEngine(t.Context(), []EngineOp{add}, time.Unix(designLogStart, 0)); err != nil {
		t.Fatalf("ApplyEngine add session %s: %v", id, err)
	}
}

// createDesignOne creates the design design-one, made in conversation, at designLogStart.
func createDesignOne(t *testing.T, s *Store, conversation string) {
	t.Helper()
	if err := s.CreateDesign(t.Context(), Design{
		ID: "design-one", Title: "Design one", Conversation: conversation, Created: designLogTime(),
	}); err != nil {
		t.Fatalf("CreateDesign design-one: %v", err)
	}
}

// designTranscriptEvents returns the engine_design events of session, read through the Reader of path, oldest first.
func designTranscriptEvents(t *testing.T, path, session string) []EngineEvent {
	t.Helper()
	history, err := NewReader(path).EngineHistory(t.Context(), session, 0, 1000)
	if err != nil {
		t.Fatalf("EngineHistory of %s: %v", session, err)
	}
	var events []EngineEvent
	for _, event := range history.Events {
		if event.Kind == EventEngineDesign {
			events = append(events, event)
		}
	}
	return events
}

// requireDesignEvents fails the test unless DesignEvents returns want.
func requireDesignEvents(t *testing.T, s *Store, want []DesignEvent) {
	t.Helper()
	got, err := s.DesignEvents(t.Context())
	if err != nil {
		t.Fatalf("DesignEvents: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DesignEvents = %+v, want %+v", got, want)
	}
}

// TestDesignEventsRecordsTranscriptOwnerServeLineIsEngineDesignEvent checks that an owner's line made through serve
// appends one engine_design event to the working conversation's events, stamped with the line's time, whose payload is
// the line as an EngineDesign.
func TestDesignEventsRecordsTranscriptOwnerServeLineIsEngineDesignEvent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	addDesignSession(t, s, designEventSession)
	createDesignOne(t, s, designEventSession)

	if err := s.AppendDesignLog(ctx, DesignLogLine{
		Design: "design-one", At: designLogStart + 10, Maker: LogMakerOwner, What: "answered",
		IDs: []string{"Q1"}, Words: "Keep the scout",
	}); err != nil {
		t.Fatalf("AppendDesignLog: %v", err)
	}

	events := designTranscriptEvents(t, path, designEventSession)
	if len(events) != 1 {
		t.Fatalf("engine_design events of %s = %+v, want one", designEventSession, events)
	}
	if events[0].At != designLogStart+10 {
		t.Fatalf("engine_design event at %d, want the line's time %d", events[0].At, designLogStart+10)
	}
	var got orreryv1.EngineDesign
	if err := protojson.Unmarshal([]byte(events[0].Payload), &got); err != nil {
		t.Fatalf("decode engine_design payload %s: %v", events[0].Payload, err)
	}
	want := &orreryv1.EngineDesign{
		Design: "design-one", What: "answered", Ids: []string{"Q1"}, Words: "Keep the scout",
		Maker: &orreryv1.RecordMaker{Kind: LogMakerOwner},
	}
	if !proto.Equal(&got, want) {
		t.Fatalf("engine_design payload = %v, want %v", &got, want)
	}
}

// TestDesignEventsRecordsTranscriptConversationLineOwesNoEvent checks that a line the conversation made, and a line the
// owner made inside the conversation, each append an engine_design event to the working conversation, and that neither is
// owed to it as a caller event: only the owner's lines through serve are.
func TestDesignEventsRecordsTranscriptConversationLineOwesNoEvent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	addDesignSession(t, s, designEventSession)
	createDesignOne(t, s, designEventSession)

	for _, line := range []DesignLogLine{
		{
			Design: "design-one", At: designLogStart, Maker: LogMakerConversation, Session: designEventSession,
			What: "asked", IDs: []string{"Q1"}, Words: "Should the scout stay?",
		},
		{
			Design: "design-one", At: designLogStart + 10, Maker: LogMakerOwner, Session: designEventSession,
			What: "answered", IDs: []string{"Q1"}, Words: "Keep the scout",
		},
	} {
		if err := s.AppendDesignLog(ctx, line); err != nil {
			t.Fatalf("AppendDesignLog %s: %v", line.What, err)
		}
	}

	if got := designTranscriptEvents(t, path, designEventSession); len(got) != 2 {
		t.Fatalf("engine_design events of %s = %+v, want the two lines", designEventSession, got)
	}
	requireDesignEvents(t, s, nil)
}

// TestDesignEventsRecordsTranscriptOwnerServeLineOwedUntilMarked checks that an owner's line through serve is owed to the
// working conversation as a caller event, woke the caller events, and is listed as a pending event with its design until
// the conversation is marked. The accepted, set aside and reopened lines of the owner are not owed, and wake nothing.
func TestDesignEventsRecordsTranscriptOwnerServeLineOwedUntilMarked(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	addDesignSession(t, s, designEventSession)
	createDesignOne(t, s, designEventSession)
	woken := 0
	s.OnCallerEvent(func() { woken++ })

	answer := DesignLogLine{
		Design: "design-one", At: designLogStart + 10, Maker: LogMakerOwner, What: "answered",
		IDs: []string{"Q1"}, Words: "Keep the scout",
	}
	if err := s.AppendDesignLog(ctx, answer); err != nil {
		t.Fatalf("AppendDesignLog answered: %v", err)
	}
	if woken != 1 {
		t.Fatalf("the owner's answer woke caller events %d times, want 1", woken)
	}
	for _, what := range []string{"accepted", "set aside", "reopened"} {
		if err := s.AppendDesignLog(ctx, DesignLogLine{
			Design: "design-one", At: designLogStart + 20, Maker: LogMakerOwner, What: what,
		}); err != nil {
			t.Fatalf("AppendDesignLog %s: %v", what, err)
		}
	}
	if woken != 1 {
		t.Fatalf("the owner's accepted, set aside and reopened lines woke caller events; woken = %d, want 1", woken)
	}

	line := DesignLogLine{
		Design: "design-one", Seq: 1, At: designLogStart + 10, Maker: LogMakerOwner, What: "answered",
		IDs: []string{"Q1"}, Words: "Keep the scout",
	}
	const event = "design:design-one:1"
	want := DesignEvent{
		ID:        event,
		Recipient: CallerSession{ID: designEventSession, Kind: KindEngine, State: "idle"},
		Line:      line,
	}
	requireDesignEvents(t, s, []DesignEvent{want})
	pending, err := NewReader(path).PendingCallerEvents(ctx, designEventSession)
	if err != nil {
		t.Fatalf("PendingCallerEvents: %v", err)
	}
	wantPending := []PendingEvent{{ID: event, Kind: CallerKindDesign, Design: line}}
	if !reflect.DeepEqual(pending, wantPending) {
		t.Fatalf("PendingCallerEvents = %+v, want %+v", pending, wantPending)
	}

	if err = s.MarkCallerEvent(ctx, event, designEventSession, time.Unix(designLogStart+30, 0)); err != nil {
		t.Fatalf("MarkCallerEvent: %v", err)
	}
	requireDesignEvents(t, s, nil)
	pending, err = NewReader(path).PendingCallerEvents(ctx, designEventSession)
	if err != nil || len(pending) != 0 {
		t.Fatalf("PendingCallerEvents after the mark = %+v, %v, want none", pending, err)
	}
}

// TestDesignEventsRecordsTranscriptNoEventWithoutSessionRow checks that an owner's line whose design's working
// conversation has no sessions row is still stored, and is owed nothing, since there is no session to tell.
func TestDesignEventsRecordsTranscriptNoEventWithoutSessionRow(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, _ := openDesignLogStore(t)
	createDesignOne(t, s, "session-missing")

	if err := s.AppendDesignLog(ctx, DesignLogLine{
		Design: "design-one", At: designLogStart, Maker: LogMakerOwner, What: "answered", Words: "Keep it",
	}); err != nil {
		t.Fatalf("AppendDesignLog: %v", err)
	}
	lines, err := s.DesignLog(ctx, "design-one")
	if err != nil || len(lines) != 1 {
		t.Fatalf("DesignLog = %+v, %v, want the one line", lines, err)
	}
	requireDesignEvents(t, s, nil)
}

// TestDesignEventsRecordsTranscriptEventFollowsWorkingConversation checks that a design's engine_design events go to its
// working conversation, the session of its newest conversation row, and that the caller event of its owner's line is
// owed to that session and to no other.
func TestDesignEventsRecordsTranscriptEventFollowsWorkingConversation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	addDesignSession(t, s, "session-old")
	addDesignSession(t, s, designEventSession)
	createDesignOne(t, s, "session-old")
	if err := s.fixtureQueries().InsertDesignConversation(ctx, sqlcgen.InsertDesignConversationParams{
		Design: "design-one", Seq: 1, Session: designEventSession, Previous: "session-old", At: designLogStart + 5,
	}); err != nil {
		t.Fatalf("InsertDesignConversation: %v", err)
	}

	if err := s.AppendDesignLog(ctx, DesignLogLine{
		Design: "design-one", At: designLogStart + 10, Maker: LogMakerOwner, What: "answered", Words: "Keep it",
	}); err != nil {
		t.Fatalf("AppendDesignLog: %v", err)
	}

	if got := designTranscriptEvents(t, path, "session-old"); len(got) != 0 {
		t.Fatalf("engine_design events of the replaced session = %+v, want none", got)
	}
	if got := designTranscriptEvents(t, path, designEventSession); len(got) != 1 {
		t.Fatalf("engine_design events of the working session = %+v, want one", got)
	}
	events, err := s.DesignEvents(ctx)
	if err != nil || len(events) != 1 || events[0].Recipient.ID != designEventSession {
		t.Fatalf("DesignEvents = %+v, %v, want one event owed to %s", events, err, designEventSession)
	}
}
