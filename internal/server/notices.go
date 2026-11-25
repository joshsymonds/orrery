package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
)

// noticesServer answers orrery.v1.Notices by forwarding to engine. A nil engine answers unavailable. Watch relays each
// engine message as its orrery.v1 twin, each send bounded by sendTimeout, and ends when shutdown closes.
type noticesServer struct {
	engine      enginev1connect.NoticesClient
	timeout     time.Duration
	sendTimeout time.Duration
	shutdown    <-chan struct{}
}

// newNoticesHandler returns the path and handler that serve Notices over engine. A zero timeout is
// defaultControlTimeout. The handler carries the ResponseWriter its send deadlines are set on.
func newNoticesHandler(
	engine enginev1connect.NoticesClient,
	timeout, sendTimeout time.Duration,
	shutdown <-chan struct{},
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	if timeout == 0 {
		timeout = defaultControlTimeout
	}
	service := noticesServer{engine: engine, timeout: timeout, sendTimeout: sendTimeout, shutdown: shutdown}
	path, handler := orreryv1connect.NewNoticesHandler(service, opts...)
	return path, withResponseWriter(handler)
}

// Watch relays the engine's Notices.Watch to the page until the engine stream ends or fails, or until shutdown closes.
// An engine stream that ends or fails ends the page's stream as unavailable, so the page reopens.
func (n noticesServer) Watch(
	ctx context.Context,
	_ *connect.Request[orreryv1.NoticesWatchRequest],
	stream *connect.ServerStream[orreryv1.NoticesWatchResponse],
) error {
	return endWithin(ctx, n.watch(ctx, stream), n.sendTimeout, n.shutdown)
}

// Seen forwards the key to the engine's Notices.Seen under the control timeout, and answers engine errors as Control
// does.
func (n noticesServer) Seen(
	ctx context.Context,
	req *connect.Request[orreryv1.NoticesSeenRequest],
) (*connect.Response[orreryv1.NoticesSeenResponse], error) {
	if n.engine == nil {
		return nil, listError(connect.CodeUnavailable, controlUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	seenReq := &enginev1.NoticesSeenRequest{Key: engineNoticeKey(req.Msg.GetKey())}
	if _, err := n.engine.Seen(ctx, connect.NewRequest(seenReq)); err != nil {
		return nil, translateError(wrapEngine("notices seen", err))
	}
	return connect.NewResponse(&orreryv1.NoticesSeenResponse{}), nil
}

// List forwards to the engine's Notices.List under the control timeout, and answers engine errors as Seen does. The
// notices keep the engine's order.
func (n noticesServer) List(
	ctx context.Context,
	_ *connect.Request[orreryv1.NoticesListRequest],
) (*connect.Response[orreryv1.NoticesListResponse], error) {
	if n.engine == nil {
		return nil, listError(connect.CodeUnavailable, controlUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	res, err := n.engine.List(ctx, connect.NewRequest(&enginev1.NoticesListRequest{}))
	if err != nil {
		return nil, translateError(wrapEngine("notices list", err))
	}
	notices := make([]*orreryv1.ListedNotice, 0, len(res.Msg.GetNotices()))
	for _, notice := range res.Msg.GetNotices() {
		notices = append(notices, listedNoticeMessage(notice))
	}
	return connect.NewResponse(&orreryv1.NoticesListResponse{Notices: notices}), nil
}

// ReadAll forwards to the engine's Notices.ReadAll under the control timeout, and answers engine errors as Seen does.
// It answers success only once the engine has marked the history read.
func (n noticesServer) ReadAll(
	ctx context.Context,
	_ *connect.Request[orreryv1.NoticesReadAllRequest],
) (*connect.Response[orreryv1.NoticesReadAllResponse], error) {
	if n.engine == nil {
		return nil, listError(connect.CodeUnavailable, controlUnavailable)
	}
	ctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	if _, err := n.engine.ReadAll(ctx, connect.NewRequest(&enginev1.NoticesReadAllRequest{})); err != nil {
		return nil, translateError(wrapEngine("notices read all", err))
	}
	return connect.NewResponse(&orreryv1.NoticesReadAllResponse{}), nil
}

// watch runs Watch's relay and returns why it ended. The engine call runs on a context that ends when the page's does
// or when shutdown closes, so a call still waiting for its first message ends at shutdown too. Canceling that context
// on return ends the engine call and its connection.
func (n noticesServer) watch(ctx context.Context, stream *connect.ServerStream[orreryv1.NoticesWatchResponse]) error {
	if n.engine == nil {
		return listError(connect.CodeUnavailable, controlUnavailable)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-n.shutdown:
			cancel()
		case <-ctx.Done():
		}
	}()
	engine, err := n.engine.Watch(ctx, connect.NewRequest(&enginev1.NoticesWatchRequest{}))
	if err != nil {
		return noticesEnd(n.shutdown)
	}
	for engine.Receive() {
		if stopping(n.shutdown) {
			return shutdownError()
		}
		msg := noticeWatchMessage(engine.Msg())
		if sendErr := sendWithin(ctx, n.sendTimeout, n.shutdown, stream, msg); sendErr != nil {
			return fmt.Errorf("send notice: %w", sendErr)
		}
	}
	return noticesEnd(n.shutdown)
}

// noticesEnd returns the error the page's stream ends with once the engine's has: shutdownError when shutdown has
// closed, else unavailable with the fixed control message, so the page reopens.
func noticesEnd(shutdown <-chan struct{}) error {
	if stopping(shutdown) {
		return shutdownError()
	}
	return listError(connect.CodeUnavailable, controlUnavailable)
}

// noticeWatchMessage returns the orrery.v1 twin of one engine notices change, carrying the field the engine set.
func noticeWatchMessage(msg *enginev1.NoticesWatchResponse) *orreryv1.NoticesWatchResponse {
	switch change := msg.GetChange().(type) {
	case *enginev1.NoticesWatchResponse_Shown:
		return &orreryv1.NoticesWatchResponse{Change: &orreryv1.NoticesWatchResponse_Shown{
			Shown: inAppNoticeMessage(change.Shown),
		}}
	case *enginev1.NoticesWatchResponse_Ended:
		return &orreryv1.NoticesWatchResponse{Change: &orreryv1.NoticesWatchResponse_Ended{
			Ended: noticeKeyMessage(change.Ended),
		}}
	default:
		return &orreryv1.NoticesWatchResponse{}
	}
}

// inAppNoticeMessage returns the orrery.v1 twin of one engine notice, field for field.
func inAppNoticeMessage(notice *enginev1.InAppNotice) *orreryv1.InAppNotice {
	return &orreryv1.InAppNotice{
		Key:   noticeKeyMessage(notice.GetKey()),
		State: notice.GetState(),
		Title: notice.GetTitle(),
		Body:  notice.GetBody(),
		Block: noticeBlockMessage(notice.GetBlock()),
	}
}

// listedNoticeMessage returns the orrery.v1 twin of one engine listed notice, field for field.
func listedNoticeMessage(notice *enginev1.ListedNotice) *orreryv1.ListedNotice {
	return &orreryv1.ListedNotice{
		Key:   noticeKeyMessage(notice.GetKey()),
		State: notice.GetState(),
		Title: notice.GetTitle(),
		Body:  notice.GetBody(),
		Block: noticeBlockMessage(notice.GetBlock()),
		Time:  notice.GetTime(),
		Read:  notice.GetRead(),
	}
}

// noticeBlockMessage returns the orrery.v1 twin of one engine notice block, field for field, or nil when there is none.
func noticeBlockMessage(block *enginev1.NoticeBlock) *orreryv1.NoticeBlock {
	if block == nil {
		return nil
	}
	return &orreryv1.NoticeBlock{Kind: block.GetKind(), Ref: block.GetRef()}
}

// noticeKeyMessage returns the orrery.v1 twin of one engine notice key, field for field.
func noticeKeyMessage(key *enginev1.NoticeKey) *orreryv1.NoticeKey {
	return &orreryv1.NoticeKey{
		Pane:    key.GetPane(),
		Session: key.GetSession(),
		Epic:    key.GetEpic(),
		Limit:   key.GetLimit(),
		Alert:   key.GetAlert(),
		Host:    key.GetHost(),
	}
}

// engineNoticeKey returns the engine twin of one notice key the page sent, field for field.
func engineNoticeKey(key *orreryv1.NoticeKey) *enginev1.NoticeKey {
	return &enginev1.NoticeKey{
		Pane:    key.GetPane(),
		Session: key.GetSession(),
		Epic:    key.GetEpic(),
		Limit:   key.GetLimit(),
		Alert:   key.GetAlert(),
		Host:    key.GetHost(),
	}
}
