package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// AppendDesignLog appends line to its design's log, numbered as the design's next line: counted from 1 within the
// design, one more than the highest number the design already holds. line.Seq is ignored. Rows only append, so a line
// once stored is never changed or removed. Its IDs are stored as a JSON array. In the same write it appends an
// engine_design event of the line to the design's working conversation, when that conversation has a sessions row (see
// appendDesignEvent). A line the owner made through serve is owed to that conversation, so its insert marks the write
// for the caller events (see withEpicEvent).
func (s *Store) AppendDesignLog(ctx context.Context, line DesignLogLine) error {
	ids, encodeErr := encodeList(line.IDs)
	if encodeErr != nil {
		return fmt.Errorf("encode ids of a log line of design %s: %w", line.Design, encodeErr)
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
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
		insertCtx := ctx
		if designLineOwed(line) {
			insertCtx = withEpicEvent(ctx)
		}
		if err = queries.InsertDesignLogLine(insertCtx, sqlcgen.InsertDesignLogLineParams{
			Design: line.Design, Seq: next, At: line.At, Maker: line.Maker, Session: line.Session, What: line.What,
			Ids: ids, Words: line.Words,
		}); err != nil {
			return fmt.Errorf("insert log line %d of design %s: %w", next, line.Design, err)
		}
		return appendDesignEvent(ctx, queries, line)
	})
}

// appendDesignEvent appends the engine_design event of line to the working conversation of its design, stamped with the
// line's time, and its payload is the line as an EngineDesign. It appends nothing when the design has no working
// conversation with a sessions row: a design with no row and no contract in it names no conversation at all.
func appendDesignEvent(ctx context.Context, queries *sqlcgen.Queries, line DesignLogLine) error {
	conversation, err := readDesignConversation(ctx, queries, line.Design)
	if errors.Is(err, ErrNoDesign) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read working conversation of design %s: %w", line.Design, err)
	}
	_, err = queries.GetSession(ctx, conversation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read session %s: %w", conversation, err)
	}
	payload, err := protojson.Marshal(designRecordOf(line))
	if err != nil {
		return fmt.Errorf("encode %s payload: %w", EventEngineDesign, err)
	}
	return appendEvent(ctx, queries, conversation, EventEngineDesign, string(payload), time.Unix(line.At, 0))
}

// designRecordOf returns line as the EngineDesign a design event carries: its design, what, ids and words, its maker's
// kind, and its session.
func designRecordOf(line DesignLogLine) *orreryv1.EngineDesign {
	return &orreryv1.EngineDesign{
		Design: line.Design, What: line.What, Ids: line.IDs, Words: line.Words,
		Maker: &orreryv1.RecordMaker{Kind: line.Maker}, Session: line.Session,
	}
}

// DesignLog returns the lines of the design's log, oldest first, which is by seq. A design with no lines has none.
func (s *Store) DesignLog(ctx context.Context, design string) ([]DesignLogLine, error) {
	return readDesignLog(ctx, sqlcgen.New(s.read), design)
}

// DesignLog returns the lines of the design's log as Store.DesignLog does, read through the Reader.
func (r *Reader) DesignLog(ctx context.Context, design string) ([]DesignLogLine, error) {
	var lines []DesignLogLine
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		lines, readErr = readDesignLog(ctx, queries, design)
		return readErr
	})
	return lines, err
}

// readDesignLog returns the lines of the design's log, oldest first, read with queries.
func readDesignLog(ctx context.Context, queries *sqlcgen.Queries, design string) ([]DesignLogLine, error) {
	rows, err := queries.ListDesignLog(ctx, design)
	if err != nil {
		return nil, fmt.Errorf("list log of design %s: %w", design, err)
	}
	lines := make([]DesignLogLine, 0, len(rows))
	for _, row := range rows {
		ids, decodeErr := decodeList[string](row.Ids)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode ids of log line %d of design %s: %w", row.Seq, design, decodeErr)
		}
		lines = append(lines, DesignLogLine{
			Design: row.Design, Seq: row.Seq, At: row.At, Maker: row.Maker, Session: row.Session, What: row.What,
			IDs: ids, Words: row.Words,
		})
	}
	return lines, nil
}

// DesignConversation returns the design's working conversation: the session of its newest design_conversations row,
// or else the design's conversation as Store.Design reads it, which for a design of one is its contract's conversation.
// It returns an error that matches ErrNoDesign when neither a design nor a contract in no design has the id.
func (s *Store) DesignConversation(ctx context.Context, design string) (string, error) {
	tx, err := s.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", fmt.Errorf("begin design conversation read: %w", err)
	}
	conversation, err := readDesignConversation(ctx, sqlcgen.New(tx), design)
	if err != nil {
		return "", errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return "", fmt.Errorf("end design conversation read: %w", err)
	}
	return conversation, nil
}

// DesignConversation returns the design's working conversation as Store.DesignConversation does, read through the
// Reader.
func (r *Reader) DesignConversation(ctx context.Context, design string) (string, error) {
	var conversation string
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		conversation, readErr = readDesignConversation(ctx, queries, design)
		return readErr
	})
	return conversation, err
}

// readDesignConversation returns the design's working conversation, read with queries: the session of its newest
// design_conversations row, or else the Conversation of the design as readDesign reads it.
func readDesignConversation(ctx context.Context, queries *sqlcgen.Queries, design string) (string, error) {
	newest, err := queries.NewestDesignConversation(ctx, design)
	if err == nil {
		return newest.Session, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read newest conversation of design %s: %w", design, err)
	}
	record, err := readDesign(ctx, queries, design)
	if err != nil {
		return "", err
	}
	return record.Conversation, nil
}
