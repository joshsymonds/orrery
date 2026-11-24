package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// continuedWhat is what the log line a continuation appends says it records.
const continuedWhat = "continued"

// ContinueDesign makes session the design's working conversation, continuing the one it replaces, in one write. It
// stores a design_conversations row for session, numbered after the design's last, whose previous is the working
// conversation as the write reads it. It then appends the log line continued, made by orreryd with session as its id,
// at at. The line's engine_design event goes to the working conversation, which is session once the row is stored, and
// is skipped when session has no sessions row. It returns an error that matches ErrNoDesign when no design or contract
// in no design has the id, and stores nothing on an error.
func (s *Store) ContinueDesign(ctx context.Context, design, session string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		previous, err := readDesignConversation(ctx, queries, design)
		if err != nil {
			return fmt.Errorf("read working conversation of design %s: %w", design, err)
		}
		seq, err := nextDesignConversationSeq(ctx, queries, design)
		if err != nil {
			return err
		}
		if err = queries.InsertDesignConversation(ctx, sqlcgen.InsertDesignConversationParams{
			Design: design, Seq: seq, Session: session, Previous: previous, At: at.Unix(),
		}); err != nil {
			return fmt.Errorf("insert conversation %d of design %s: %w", seq, design, err)
		}
		return appendContinuedLine(ctx, queries, DesignLogLine{
			Design: design, At: at.Unix(), Maker: LogMakerOrreryd, What: continuedWhat, IDs: []string{session},
		})
	})
}

// nextDesignConversationSeq returns the seq of the next conversation of design: one more than the highest seq the
// design holds, counted from 1. Read with queries.
func nextDesignConversationSeq(ctx context.Context, queries *sqlcgen.Queries, design string) (int64, error) {
	conversations, err := queries.ListDesignConversations(ctx, design)
	if err != nil {
		return 0, fmt.Errorf("list conversations of design %s: %w", design, err)
	}
	next := int64(1)
	for _, conversation := range conversations {
		if conversation.Seq >= next {
			next = conversation.Seq + 1
		}
	}
	return next, nil
}

// appendContinuedLine appends line to the log of its design, numbered as the design's next line, and appends its
// engine_design event to the design's working conversation, as AppendDesignLog does. It is called within the write that
// stores the design's new conversation. The line has no session, since orreryd makes it.
func appendContinuedLine(ctx context.Context, queries *sqlcgen.Queries, line DesignLogLine) error {
	rows, err := queries.ListDesignLog(ctx, line.Design)
	if err != nil {
		return fmt.Errorf("number a log line of design %s: %w", line.Design, err)
	}
	next := int64(1)
	for _, row := range rows {
		if row.Seq >= next {
			next = row.Seq + 1
		}
	}
	ids, err := encodeList(line.IDs)
	if err != nil {
		return fmt.Errorf("encode ids of a log line of design %s: %w", line.Design, err)
	}
	if err = queries.InsertDesignLogLine(ctx, sqlcgen.InsertDesignLogLineParams{
		Design: line.Design, Seq: next, At: line.At, Maker: line.Maker, Session: "", What: line.What, Ids: ids,
		Words: line.Words,
	}); err != nil {
		return fmt.Errorf("insert log line %d of design %s: %w", next, line.Design, err)
	}
	return appendDesignEvent(ctx, queries, line)
}
