package enginehost

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/inapp"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// NoticeSource is the set of in-app notice operations the Notices service answers with, and of the notice history it
// lists and marks read. PageNotices satisfies it.
type NoticeSource interface {
	Watch() ([]inapp.Notice, <-chan inapp.Change, func())
	Seen(ctx context.Context, key notice.Key)
	List(ctx context.Context) ([]store.NoticeHistoryRow, error)
	ReadAll(ctx context.Context) error
}

// NoticeHistory is the router's record of the notices it delivered, read and marked read through the Notices service.
// *route.Router satisfies it.
type NoticeHistory interface {
	History(ctx context.Context, since int64) ([]store.NoticeHistoryRow, error)
	ReadAll(ctx context.Context) error
}

// PageNotices watches the notices showing in pages, reports the owner's seen notices to route, and lists and marks read
// the notice history of history.
type PageNotices struct {
	pages   *inapp.Hub
	route   Router
	history NoticeHistory
	now     func() time.Time
}

// NewPageNotices returns a PageNotices that watches pages, reports each seen notice to route at now's time, and lists
// and marks read the notice history of history. A nil history answers List and ReadAll with route.ErrNoHistory.
func NewPageNotices(pages *inapp.Hub, route Router, history NoticeHistory, now func() time.Time) *PageNotices {
	return &PageNotices{pages: pages, route: route, history: history, now: now}
}

// Watch returns the notices showing in pages and the changes to them after that. See inapp.Hub.Watch.
func (s *PageNotices) Watch() ([]inapp.Notice, <-chan inapp.Change, func()) {
	return s.pages.Watch()
}

// Seen reports to route that the owner saw the notice under key, at now's time in unix seconds.
func (s *PageNotices) Seen(ctx context.Context, key notice.Key) {
	s.route.Seen(ctx, key, s.now().Unix())
}

// List returns the notice history timed within the last 14 days at now's time, newest first. See route.Router.History.
// It fails with route.ErrNoHistory when there is no history.
func (s *PageNotices) List(ctx context.Context) ([]store.NoticeHistoryRow, error) {
	if s.history == nil {
		return nil, route.ErrNoHistory
	}
	rows, err := s.history.History(ctx, s.now().Add(-store.EventRetention).Unix())
	if err != nil {
		return nil, fmt.Errorf("notice history window: %w", err)
	}
	return rows, nil
}

// ReadAll marks every notice in the notice history read. See route.Router.ReadAll. It fails with route.ErrNoHistory when
// there is no history.
func (s *PageNotices) ReadAll(ctx context.Context) error {
	if s.history == nil {
		return route.ErrNoHistory
	}
	if err := s.history.ReadAll(ctx); err != nil {
		return fmt.Errorf("mark notice history read: %w", err)
	}
	return nil
}

// noticesService implements the Notices RPCs over notices. A nil notices refuses every RPC as unavailable.
type noticesService struct {
	notices NoticeSource
}

// Watch streams the notices showing now, then each change to them, until the changes close or the call ends. Either way
// it stops the watch.
func (s *noticesService) Watch(
	ctx context.Context,
	_ *connect.Request[v1.NoticesWatchRequest],
	stream *connect.ServerStream[v1.NoticesWatchResponse],
) error {
	if s.notices == nil {
		return noticesUnavailable()
	}
	shown, changes, stop := s.notices.Watch()
	defer stop()
	for _, n := range shown {
		if err := stream.Send(shownResponse(n)); err != nil {
			return fmt.Errorf("send notice: %w", err)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case change, ok := <-changes:
			if !ok {
				return nil
			}
			if err := stream.Send(changeResponse(change)); err != nil {
				return fmt.Errorf("send notice change: %w", err)
			}
		}
	}
}

// Seen reports the owner's seen notice to the router. The key it names must set exactly one field.
func (s *noticesService) Seen(
	ctx context.Context,
	req *connect.Request[v1.NoticesSeenRequest],
) (*connect.Response[v1.NoticesSeenResponse], error) {
	if s.notices == nil {
		return nil, noticesUnavailable()
	}
	key, err := noticeKeyOf(req.Msg.GetKey())
	if err != nil {
		return nil, errors.Join(connect.NewError(connect.CodeInvalidArgument, err))
	}
	s.notices.Seen(ctx, key)
	return connect.NewResponse(&v1.NoticesSeenResponse{}), nil
}

// List answers the notice history, each row as a ListedNotice in the order the history gives. It answers unavailable
// when there is no history.
func (s *noticesService) List(
	ctx context.Context,
	_ *connect.Request[v1.NoticesListRequest],
) (*connect.Response[v1.NoticesListResponse], error) {
	if s.notices == nil {
		return nil, noticesUnavailable()
	}
	rows, err := s.notices.List(ctx)
	if err != nil {
		return nil, historyError("list notice history", err)
	}
	notices := make([]*v1.ListedNotice, 0, len(rows))
	for _, row := range rows {
		notices = append(notices, listedNotice(row))
	}
	return connect.NewResponse(&v1.NoticesListResponse{Notices: notices}), nil
}

// ReadAll marks every notice in the notice history read. It answers unavailable when there is no history.
func (s *noticesService) ReadAll(
	ctx context.Context,
	_ *connect.Request[v1.NoticesReadAllRequest],
) (*connect.Response[v1.NoticesReadAllResponse], error) {
	if s.notices == nil {
		return nil, noticesUnavailable()
	}
	if err := s.notices.ReadAll(ctx); err != nil {
		return nil, historyError("read all notice history", err)
	}
	return connect.NewResponse(&v1.NoticesReadAllResponse{}), nil
}

// noticesUnavailable returns the refusal every Notices RPC gets when there is no notice source to serve.
func noticesUnavailable() error {
	return errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(unavailableMessage)))
}

// historyError returns the refusal of a notice history call that failed with err. It is unavailable when no history is
// attached, and internal otherwise, wrapped with action.
func historyError(action string, err error) error {
	if errors.Is(err, route.ErrNoHistory) {
		return noticesUnavailable()
	}
	return errors.Join(connect.NewError(connect.CodeInternal, fmt.Errorf("%s: %w", action, err)))
}

// noticeKeyOf returns the notice key that key names. It is an error unless exactly one of key's fields is set.
func noticeKeyOf(key *v1.NoticeKey) (notice.Key, error) {
	set := 0
	for _, field := range []string{key.GetPane(), key.GetSession(), key.GetEpic(), key.GetLimit(), key.GetAlert()} {
		if field != "" {
			set++
		}
	}
	if set != 1 {
		return notice.Key{}, fmt.Errorf("notice key sets %d fields, want exactly one", set)
	}
	return notice.Key{
		Pane:    key.GetPane(),
		Session: key.GetSession(),
		Epic:    key.GetEpic(),
		Limit:   key.GetLimit(),
		Alert:   key.GetAlert(),
		Host:    key.GetHost(),
	}, nil
}

// shownResponse is the change that tells a stream the notice shown is showing now.
func shownResponse(shown inapp.Notice) *v1.NoticesWatchResponse {
	return &v1.NoticesWatchResponse{Change: &v1.NoticesWatchResponse_Shown{Shown: &v1.InAppNotice{
		Key:   keyMessage(shown.Key),
		State: shown.State,
		Title: shown.Title,
		Body:  shown.Body,
		Block: blockMessage(shown),
	}}}
}

// blockMessage returns the block the notice n opens on, or nil when n opens the epic view.
func blockMessage(n inapp.Notice) *v1.NoticeBlock {
	if n.Block == "" {
		return nil
	}
	return &v1.NoticeBlock{Kind: n.Block, Ref: n.BlockRef}
}

// changeResponse is the change that tells a stream of one change to the notices.
func changeResponse(c inapp.Change) *v1.NoticesWatchResponse {
	if c.Ended {
		return &v1.NoticesWatchResponse{Change: &v1.NoticesWatchResponse_Ended{Ended: keyMessage(c.Notice.Key)}}
	}
	return shownResponse(c.Notice)
}

// keyMessage returns the message for key.
func keyMessage(key notice.Key) *v1.NoticeKey {
	return &v1.NoticeKey{
		Pane: key.Pane, Session: key.Session, Epic: key.Epic, Limit: key.Limit, Alert: key.Alert, Host: key.Host,
	}
}

// listedNotice returns the message for the notice history row row, with the block it opens on only when it names one.
func listedNotice(row store.NoticeHistoryRow) *v1.ListedNotice {
	msg := &v1.ListedNotice{
		Key:   keyMessage(notice.Key(row.Key)),
		State: row.State,
		Title: row.Title,
		Body:  row.Body,
		Time:  row.Time,
		Read:  row.Read,
	}
	if row.Block != "" {
		msg.Block = &v1.NoticeBlock{Kind: row.Block, Ref: row.BlockRef}
	}
	return msg
}
