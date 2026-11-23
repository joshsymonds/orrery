package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// Engine session states that take a row's summary from its reason.
const (
	stateFailed      = "failed"
	stateInterrupted = "interrupted"
)

// labelPromptRunes is the most runes of a session's first prompt its label keeps.
const labelPromptRunes = 40

// summaryRunes is the most runes a row's summary keeps.
const summaryRunes = 300

// ListHeadless answers the headless engine sessions with an event in the last store.EventRetention, in id order.
// Each row's label, headline and summary are derived when it is read. A done row that a live epic steers or a running
// step calls lists as working. Store detail never reaches the client.
func (s sessionsServer) ListHeadless(
	ctx context.Context,
	_ *connect.Request[orreryv1.ListHeadlessRequest],
) (*connect.Response[orreryv1.ListHeadlessResponse], error) {
	if s.reader == nil {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	now := s.now()
	cutoff := now.Add(-store.EventRetention).Unix()
	rows, err := s.reader.HeadlessSessions(ctx, cutoff)
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read headless sessions")
	}
	marks, err := readArchiveMarks(ctx, s.reader)
	if err != nil {
		return nil, err
	}
	verdicts, err := readClosureVerdicts(ctx, s.reader)
	if err != nil {
		return nil, err
	}
	puts, err := readPutOffMarks(ctx, s.reader)
	if err != nil {
		return nil, err
	}
	reads, err := readSteered(ctx, s.epics, s.steps, s.reader)
	if err != nil {
		return nil, err
	}
	records, err := readListDesigns(ctx, s.reader)
	if err != nil {
		return nil, err
	}
	designs := designMessages(records)
	sessions := make([]*orreryv1.HeadlessSession, 0, len(rows))
	for _, row := range rows {
		session, convErr := headlessMessage(row)
		if convErr != nil {
			return nil, listError(connect.CodeInternal, "read headless sessions")
		}
		mark := marks[archiveKey{kind: archiveKindHeadless, id: row.ID}]
		verdict, ok := verdicts[row.ID]
		session.Ended, session.EndedAt = endedRow(
			session.GetState() == stateStopped, verdict, ok, session.GetChanged(), mark,
		)
		session.Ended, session.EndedAt = steeredEnd(reads.kept, row.ID, session.Ended, session.EndedAt)
		aged := floorAged(session.GetChanged(), now) || endedAged(session.GetEnded(), session.GetEndedAt(), now)
		session.Archived = archivedRow(mark, session.GetChanged(), aged)
		session.PutOff = headlessPutOff(puts, row.ID, row.RequestID, row.State)
		// Only the listed state takes the busy set. The fields above read the stored state.
		session.State = busyState(reads.busy, row.ID, session.State)
		session.Designs = conversationDesigns(designs, row.ID)
		sessions = append(sessions, session)
	}
	return connect.NewResponse(&orreryv1.ListHeadlessResponse{Sessions: sessions}), nil
}

// headlessMessage converts one headless row to its proto message. It fails when changed or seen does not parse.
func headlessMessage(row store.HeadlessRow) (*orreryv1.HeadlessSession, error) {
	changed, err := parseStamp(row.Changed)
	if err != nil {
		return nil, err
	}
	seen, err := parseStamp(row.Seen)
	if err != nil {
		return nil, err
	}
	return &orreryv1.HeadlessSession{
		Id:          row.ID,
		Dir:         row.Dir,
		Label:       sessionLabel(row.Dir, row.Title, row.FirstPrompt),
		Headline:    headlineOf(row),
		State:       row.State,
		Changed:     changed,
		Seen:        seen,
		Summary:     headlessSummary(row),
		Question:    row.Question,
		NewestReply: row.NewestReply,
		Host:        row.Host,
	}, nil
}

// parseStamp reads a unix-seconds column. The empty string is 0.
func parseStamp(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	unix, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse unix seconds: %w", err)
	}
	return unix, nil
}

// sessionLabel names a headless session. A non-blank title is the label. Otherwise the label is the base name of dir,
// then " · " and the first words of firstPrompt cut to labelPromptRunes runes, or the base name of dir alone when the
// prompt has no words. The session id is never part of a label.
func sessionLabel(dir, title, firstPrompt string) string {
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		return trimmed
	}
	base := filepath.Base(dir)
	words := promptWords(firstPrompt)
	if words == "" {
		return base
	}
	return base + " · " + cutRunes(words, labelPromptRunes)
}

// headlineOf is a headless row's headline: its stored headline once one is written, else its title, else the first
// words of its first prompt cut to labelPromptRunes runes, else "". The directory is never part of it.
func headlineOf(row store.HeadlessRow) string {
	if stored := strings.TrimSpace(row.Headline); stored != "" {
		return stored
	}
	if title := strings.TrimSpace(row.Title); title != "" {
		return title
	}
	return cutRunes(promptWords(row.FirstPrompt), labelPromptRunes)
}

// promptWords is the words of a prompt joined by single spaces, so a prompt's line breaks become spaces.
func promptWords(prompt string) string {
	return strings.Join(strings.Fields(prompt), " ")
}

// headlessSummary is the question while the session waits on one, else the reason of a failed or interrupted
// session, else its newest reply. It is cut to summaryRunes runes.
func headlessSummary(row store.HeadlessRow) string {
	summary := row.Reply
	switch {
	case row.State == string(protocol.StateWaiting) && row.Question != "":
		summary = row.Question
	case (row.State == stateFailed || row.State == stateInterrupted) && row.Reason != "":
		summary = row.Reason
	}
	return cutRunes(summary, summaryRunes)
}

// cutRunes returns text cut to at most limit runes.
func cutRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
