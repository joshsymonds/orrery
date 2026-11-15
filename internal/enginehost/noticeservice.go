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
)

// NoticeSource is the set of in-app notice operations the Notices service answers with. PageNotices satisfies it.
type NoticeSource interface {
	Watch() ([]inapp.Notice, <-chan inapp.Change, func())
	Seen(ctx context.Context, key notice.Key)
}

// PageNotices watches the notices showing in pages and reports the owner's seen notices to route.
type PageNotices struct {
	pages *inapp.Hub
	route Router
	now   func() time.Time
}

// NewPageNotices returns a PageNotices that watches pages and reports each seen notice to route at now's time.
func NewPageNotices(pages *inapp.Hub, route Router, now func() time.Time) *PageNotices {
	return &PageNotices{pages: pages, route: route, now: now}
}

// Watch returns the notices showing in pages and the changes to them after that. See inapp.Hub.Watch.
func (s *PageNotices) Watch() ([]inapp.Notice, <-chan inapp.Change, func()) {
	return s.pages.Watch()
}

// Seen reports to route that the owner saw the notice under key, at now's time in unix seconds.
func (s *PageNotices) Seen(ctx context.Context, key notice.Key) {
	s.route.Seen(ctx, key, s.now().Unix())
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

// noticesUnavailable returns the refusal every Notices RPC gets when there is no notice source to serve.
func noticesUnavailable() error {
	return errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(unavailableMessage)))
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
	}, nil
}

// shownResponse is the change that tells a stream the notice n shows now.
func shownResponse(n inapp.Notice) *v1.NoticesWatchResponse {
	return &v1.NoticesWatchResponse{Change: &v1.NoticesWatchResponse_Shown{Shown: &v1.InAppNotice{
		Key:   keyMessage(n.Key),
		State: n.State,
		Title: n.Title,
		Body:  n.Body,
	}}}
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
	return &v1.NoticeKey{Pane: key.Pane, Session: key.Session, Epic: key.Epic, Limit: key.Limit, Alert: key.Alert}
}
