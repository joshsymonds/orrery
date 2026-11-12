package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

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

// headlessIDLen is how many leading characters of a session id name its row, as a session notice does.
const headlessIDLen = 8

// summaryRunes is the most runes a row's summary keeps.
const summaryRunes = 300

// ListHeadless answers the headless engine sessions with an event in the last store.EventRetention, in id order.
// Each row's label and summary are derived when it is read. Store detail never reaches the client.
func (s sessionsServer) ListHeadless(
	ctx context.Context,
	_ *connect.Request[orreryv1.ListHeadlessRequest],
) (*connect.Response[orreryv1.ListHeadlessResponse], error) {
	if s.reader == nil {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	cutoff := time.Now().Add(-store.EventRetention).Unix()
	rows, err := s.reader.HeadlessSessions(ctx, cutoff)
	if errors.Is(err, store.ErrUnavailable) {
		return nil, listError(connect.CodeUnavailable, "store unavailable")
	}
	if err != nil {
		return nil, listError(connect.CodeInternal, "read headless sessions")
	}
	sessions := make([]*orreryv1.HeadlessSession, 0, len(rows))
	for _, row := range rows {
		session, convErr := headlessMessage(row)
		if convErr != nil {
			return nil, listError(connect.CodeInternal, "read headless sessions")
		}
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
		Label:       headlessLabel(row.ID, row.Dir),
		State:       row.State,
		Changed:     changed,
		Seen:        seen,
		Summary:     headlessSummary(row),
		Question:    row.Question,
		NewestReply: row.NewestReply,
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

// headlessLabel names a row: the base name of dir, " · ", then the first characters of the id.
func headlessLabel(id, dir string) string {
	short := id
	if len(short) > headlessIDLen {
		short = short[:headlessIDLen]
	}
	return filepath.Base(dir) + " · " + short
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
