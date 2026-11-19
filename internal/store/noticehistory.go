package store

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// NoticeHistoryKey names what a notice in the history is about. Exactly one of its fields is set: Pane, Session, Epic,
// Limit or Alert, as the notice's key is.
type NoticeHistoryKey struct {
	Pane    string
	Session string
	Epic    string
	Limit   string
	Alert   string
}

// NoticeHistoryRow is one notice the router delivered. Key names the notice's key. Time is the unix second the router
// delivered it. Read reports whether a client has read it. ID is the row's id, set by the store, and ignored when a
// row is recorded.
type NoticeHistoryRow struct {
	ID       int64
	Key      NoticeHistoryKey
	State    string
	Title    string
	Body     string
	Block    string
	BlockRef string
	Time     int64
	Read     bool
}

// RecordNoticeHistory records row. When the newest row of row.Key is unread, it replaces that row's state, title, body,
// block, block reference and time with row's, so a key holds one unread row for a notice not yet read. Otherwise it
// inserts row as a new unread row. Both happen in one write.
func (s *Store) RecordNoticeHistory(ctx context.Context, row NoticeHistoryRow) error {
	key := row.Key
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		updated, err := queries.UpdateUnreadNoticeHistory(ctx, sqlcgen.UpdateUnreadNoticeHistoryParams{
			State: row.State, Title: row.Title, Body: row.Body, Block: row.Block, BlockRef: row.BlockRef,
			Time: row.Time, Pane: key.Pane, Session: key.Session, Epic: key.Epic, LimitKey: key.Limit, Alert: key.Alert,
		})
		if err != nil {
			return fmt.Errorf("update unread notice history of %+v: %w", key, err)
		}
		if updated > 0 {
			return nil
		}
		err = queries.InsertNoticeHistory(ctx, sqlcgen.InsertNoticeHistoryParams{
			Pane:     key.Pane,
			Session:  key.Session,
			Epic:     key.Epic,
			LimitKey: key.Limit,
			Alert:    key.Alert,
			State:    row.State,
			Title:    row.Title,
			Body:     row.Body,
			Block:    row.Block,
			BlockRef: row.BlockRef,
			Time:     row.Time,
		})
		if err != nil {
			return fmt.Errorf("insert notice history of %+v: %w", key, err)
		}
		return nil
	})
}

// MarkNoticeHistoryRead marks read every row of key whose time is at or before at. It reports whether any row of key
// is still unread afterwards, as the same write finds it.
func (s *Store) MarkNoticeHistoryRead(ctx context.Context, key NoticeHistoryKey, at int64) (bool, error) {
	var unread bool
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.MarkNoticeHistoryRead(ctx, sqlcgen.MarkNoticeHistoryReadParams{
			Pane: key.Pane, Session: key.Session, Epic: key.Epic, LimitKey: key.Limit, Alert: key.Alert, Time: at,
		}); err != nil {
			return fmt.Errorf("mark notice history of %+v read: %w", key, err)
		}
		count, err := queries.CountUnreadNoticeHistory(ctx, sqlcgen.CountUnreadNoticeHistoryParams{
			Pane: key.Pane, Session: key.Session, Epic: key.Epic, LimitKey: key.Limit, Alert: key.Alert,
		})
		if err != nil {
			return fmt.Errorf("count unread notice history of %+v: %w", key, err)
		}
		unread = count > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return unread, nil
}

// ReadAllNoticeHistory marks every row of the notice history read.
func (s *Store) ReadAllNoticeHistory(ctx context.Context) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.MarkAllNoticeHistoryRead(ctx); err != nil {
			return fmt.Errorf("mark all notice history read: %w", err)
		}
		return nil
	})
}

// NoticeHistory returns the rows whose time is at or after since, newest first: by time descending, then by id
// descending.
func (s *Store) NoticeHistory(ctx context.Context, since int64) ([]NoticeHistoryRow, error) {
	rows, err := sqlcgen.New(s.db).ListNoticeHistorySince(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("list notice history since %d: %w", since, err)
	}
	out := make([]NoticeHistoryRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, NoticeHistoryRow{
			ID: row.ID,
			Key: NoticeHistoryKey{
				Pane: row.Pane, Session: row.Session, Epic: row.Epic, Limit: row.LimitKey, Alert: row.Alert,
			},
			State: row.State, Title: row.Title, Body: row.Body, Block: row.Block, BlockRef: row.BlockRef,
			Time: row.Time, Read: row.Read,
		})
	}
	return out, nil
}

// UnreadNoticeHistoryKeys returns each key that has an unread row, once, ordered by pane, session, epic, limit and
// alert.
func (s *Store) UnreadNoticeHistoryKeys(ctx context.Context) ([]NoticeHistoryKey, error) {
	rows, err := sqlcgen.New(s.db).ListUnreadNoticeHistoryKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list unread notice history keys: %w", err)
	}
	out := make([]NoticeHistoryKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, NoticeHistoryKey{
			Pane: row.Pane, Session: row.Session, Epic: row.Epic, Limit: row.LimitKey, Alert: row.Alert,
		})
	}
	return out, nil
}
