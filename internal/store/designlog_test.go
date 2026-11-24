package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// designLogStart is the second the design log tests stamp their lines and conversations at.
const designLogStart int64 = 1700000100

// openDesignLogStore returns a migrated store at a fresh path, closed at cleanup, and the path of its file.
func openDesignLogStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(t.Context(), path, noSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s, path
}

// TestAppendDesignLogRecordsLogStageLinesInSeqOrder checks that AppendDesignLog numbers each design's lines from 1 in
// the order they are appended, that DesignLog and the Reader read every field of each line back, and that a line of
// one design does not number or show among another's.
func TestAppendDesignLogRecordsLogStageLinesInSeqOrder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	const design, other = "design-one", "design-two"
	lines := []DesignLogLine{
		{
			Design: design, At: designLogStart, Maker: LogMakerConversation, Session: "session-one", What: "asked",
			IDs: []string{"Q1"}, Words: "Should the scout stay?",
		},
		{
			Design: design, At: designLogStart + 10, Maker: LogMakerOwner, What: "answered", IDs: []string{"Q1"},
			Words: "Keep the scout",
		},
		{
			Design: design, At: designLogStart + 20, Maker: LogMakerOrreryd, What: "steelman started",
		},
	}
	for _, line := range lines {
		if err := s.AppendDesignLog(ctx, line); err != nil {
			t.Fatalf("AppendDesignLog %s: %v", line.What, err)
		}
	}
	if err := s.AppendDesignLog(ctx, DesignLogLine{
		Design: other, At: designLogStart, Maker: LogMakerOwner, What: "closure skipped", Words: "a rename",
	}); err != nil {
		t.Fatalf("AppendDesignLog of %s: %v", other, err)
	}

	want := []DesignLogLine{
		{Design: design, Seq: 1, At: designLogStart, Maker: LogMakerConversation, Session: "session-one",
			What: "asked", IDs: []string{"Q1"}, Words: "Should the scout stay?"},
		{Design: design, Seq: 2, At: designLogStart + 10, Maker: LogMakerOwner, Session: "",
			What: "answered", IDs: []string{"Q1"}, Words: "Keep the scout"},
		{Design: design, Seq: 3, At: designLogStart + 20, Maker: LogMakerOrreryd, Session: "",
			What: "steelman started"},
	}
	got, err := s.DesignLog(ctx, design)
	if err != nil {
		t.Fatalf("DesignLog: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DesignLog = %+v, want %+v", got, want)
	}
	read, err := NewReader(path).DesignLog(ctx, design)
	if err != nil {
		t.Fatalf("Reader.DesignLog: %v", err)
	}
	if !reflect.DeepEqual(read, want) {
		t.Fatalf("Reader.DesignLog = %+v, want %+v", read, want)
	}

	otherLines, err := s.DesignLog(ctx, other)
	if err != nil {
		t.Fatalf("DesignLog of %s: %v", other, err)
	}
	wantOther := []DesignLogLine{{
		Design: other, Seq: 1, At: designLogStart, Maker: LogMakerOwner, What: "closure skipped", Words: "a rename",
	}}
	if !reflect.DeepEqual(otherLines, wantOther) {
		t.Fatalf("DesignLog of %s = %+v, want %+v", other, otherLines, wantOther)
	}
	none, err := s.DesignLog(ctx, "design-none")
	if err != nil || len(none) != 0 {
		t.Fatalf("DesignLog of a design with no lines = %+v, %v, want none", none, err)
	}
}

// TestDesignConversationNewestRowThenContract checks that DesignConversation returns the session of a design's newest
// conversation row once it has one, and before that the conversation the design was made in. For a design of one that
// is its contract's conversation, and a design no row or contract names is ErrNoDesign.
func TestDesignConversationNewestRowThenContract(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, path := openDesignLogStore(t)
	const design, lone = "design-one", "contract-lone"
	if err := s.CreateDesign(ctx, Design{
		ID: design, Title: "Design one", Conversation: "conversation-design", Created: designLogTime(),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	if err := s.ProposeContract(ctx, lone, designLogProposal("conversation-lone")); err != nil {
		t.Fatalf("ProposeContract: %v", err)
	}

	before, err := s.DesignConversation(ctx, design)
	if err != nil || before != "conversation-design" {
		t.Fatalf("DesignConversation before any row = %q, %v, want conversation-design", before, err)
	}
	loneConversation, err := s.DesignConversation(ctx, lone)
	if err != nil || loneConversation != "conversation-lone" {
		t.Fatalf("DesignConversation of a design of one = %q, %v, want conversation-lone", loneConversation, err)
	}

	for _, row := range []sqlcgen.InsertDesignConversationParams{
		{Design: design, Seq: 1, Session: "session-first", Previous: "", At: designLogStart},
		{Design: design, Seq: 2, Session: "session-second", Previous: "session-first", At: designLogStart + 5},
	} {
		if err = s.fixtureQueries().InsertDesignConversation(ctx, row); err != nil {
			t.Fatalf("InsertDesignConversation %d: %v", row.Seq, err)
		}
	}
	after, err := s.DesignConversation(ctx, design)
	if err != nil || after != "session-second" {
		t.Fatalf("DesignConversation = %q, %v, want the newest row's session-second", after, err)
	}
	read, err := NewReader(path).DesignConversation(ctx, design)
	if err != nil || read != "session-second" {
		t.Fatalf("Reader.DesignConversation = %q, %v, want session-second", read, err)
	}

	_, err = s.DesignConversation(ctx, "design-none")
	if !errors.Is(err, ErrNoDesign) {
		t.Fatalf("DesignConversation of no design = %v, want ErrNoDesign", err)
	}
}

// designLogTime returns the time the design log tests create their designs at.
func designLogTime() time.Time {
	return time.Unix(designLogStart, 0)
}

// designLogProposal returns the proposal of a contract in no design, written by writer at designLogStart.
func designLogProposal(writer string) ContractVersion {
	return ContractVersion{
		Version: 1, Repository: "/work/synthetic-repo", Title: "Lone contract", Care: "serious",
		Body: `{"id":"contract-lone"}`, Words: "words", Writer: writer, At: designLogStart,
	}
}
