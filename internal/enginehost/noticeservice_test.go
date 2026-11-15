package enginehost

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/deskstream"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/inapp"
	"github.com/joshsymonds/orrery/internal/notice"
)

// noticeNow is the clock the notice tests read. It is a fixed time, never the real clock, and its seconds are 1700000456.
func noticeNow() time.Time {
	return time.Unix(1700000456, 900000000)
}

// fakeNoticeSource is a NoticeSource whose changes the test sends. Its Watch shows one snapshot notice, so the stream has a
// message to send and the call returns to the client, and its stop closes stopped, so a test can see the watch end.
type fakeNoticeSource struct {
	changes chan inapp.Change
	stopped chan struct{}
}

func newFakeNoticeSource() *fakeNoticeSource {
	return &fakeNoticeSource{changes: make(chan inapp.Change), stopped: make(chan struct{})}
}

func (f *fakeNoticeSource) Watch() ([]inapp.Notice, <-chan inapp.Change, func()) {
	return []inapp.Notice{{Key: notice.Key{Pane: "%1"}, State: "waiting"}}, f.changes, func() { close(f.stopped) }
}

func (f *fakeNoticeSource) Seen(context.Context, notice.Key) {}

// startNoticesServer serves notices on an engine socket in a temporary directory and returns a Notices client of it. The
// server stops at cleanup. A test ends its own streams before cleanup, since the server waits for open streams to end.
func startNoticesServer(t *testing.T, notices NoticeSource) enginev1connect.NoticesClient {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, nil, nil, nil, nil, nil, notices) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve after cleanup: %v", err)
		}
	})
	return enginev1connect.NewNoticesClient(unixHTTPClient(t, path), "http://notices.invalid")
}

// nextNoticeChange returns the next message of a Watch stream. It fails the test when the stream ends first.
func nextNoticeChange(
	t *testing.T,
	stream *connect.ServerStreamForClient[v1.NoticesWatchResponse],
) *v1.NoticesWatchResponse {
	t.Helper()
	if !stream.Receive() {
		t.Fatalf("Watch stream ended: %v", stream.Err())
	}
	return stream.Msg()
}

func TestWatchStreamsSnapshotThenChanges(t *testing.T) {
	t.Parallel()
	pages := inapp.NewHub()
	pages.Show(notice.Key{Pane: "%3"}, deskstream.Notice{State: "waiting", Title: "proj · pane 3", Body: "needs input"})
	client := startNoticesServer(t, NewPageNotices(pages, &fakeRouter{}, noticeNow))

	ctx := t.Context()
	stream, err := client.Watch(ctx, connect.NewRequest(&v1.NoticesWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	got := make([]*v1.NoticesWatchResponse, 0, 5)
	got = append(got, nextNoticeChange(t, stream))
	pages.Show(notice.Key{Session: "0123456789abcdef0123456789abcdef"}, deskstream.Notice{
		State: "done", Title: "proj · session 01234567", Body: "all done",
	})
	got = append(got, nextNoticeChange(t, stream))
	pages.End(notice.Key{Session: "0123456789abcdef0123456789abcdef"})
	got = append(got, nextNoticeChange(t, stream))
	pages.Show(notice.Key{Alert: "synthetic-alert"}, deskstream.Notice{
		State: "firing", Title: "synthetic alert", Body: "over budget",
	})
	got = append(got, nextNoticeChange(t, stream))
	pages.End(notice.Key{Alert: "synthetic-alert"})
	got = append(got, nextNoticeChange(t, stream))

	want := []*v1.NoticesWatchResponse{
		{Change: &v1.NoticesWatchResponse_Shown{Shown: &v1.InAppNotice{
			Key: &v1.NoticeKey{Pane: "%3"}, State: "waiting", Title: "proj · pane 3", Body: "needs input",
		}}},
		{Change: &v1.NoticesWatchResponse_Shown{Shown: &v1.InAppNotice{
			Key:   &v1.NoticeKey{Session: "0123456789abcdef0123456789abcdef"},
			State: "done", Title: "proj · session 01234567", Body: "all done",
		}}},
		{Change: &v1.NoticesWatchResponse_Ended{Ended: &v1.NoticeKey{
			Session: "0123456789abcdef0123456789abcdef",
		}}},
		{Change: &v1.NoticesWatchResponse_Shown{Shown: &v1.InAppNotice{
			Key:   &v1.NoticeKey{Alert: "synthetic-alert"},
			State: "firing", Title: "synthetic alert", Body: "over budget",
		}}},
		{Change: &v1.NoticesWatchResponse_Ended{Ended: &v1.NoticeKey{
			Alert: "synthetic-alert",
		}}},
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Fatalf("message %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestSeenReachesRouterWithClockTime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  *v1.NoticeKey
		want notice.Key
	}{
		{name: "pane", key: &v1.NoticeKey{Pane: "%3"}, want: notice.Key{Pane: "%3"}},
		{name: "alert", key: &v1.NoticeKey{Alert: "synthetic-alert"}, want: notice.Key{Alert: "synthetic-alert"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router := &fakeRouter{}
			client := startNoticesServer(t, NewPageNotices(inapp.NewHub(), router, noticeNow))

			if _, err := client.Seen(t.Context(), connect.NewRequest(&v1.NoticesSeenRequest{Key: tc.key})); err != nil {
				t.Fatalf("Seen: %v", err)
			}

			want := []seenReport{{key: tc.want, seen: noticeNow().Unix()}}
			if !reflect.DeepEqual(router.seens, want) {
				t.Fatalf("router seen = %+v, want %+v", router.seens, want)
			}
		})
	}
}

func TestSeenRefusesKeyWithoutExactlyOneField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  *v1.NoticeKey
	}{
		{name: "no key", key: nil},
		{name: "no field", key: &v1.NoticeKey{}},
		{name: "two fields", key: &v1.NoticeKey{Pane: "%3", Session: "0123456789abcdef0123456789abcdef"}},
		{name: "alert and pane", key: &v1.NoticeKey{Alert: "synthetic-alert", Pane: "%3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router := &fakeRouter{}
			client := startNoticesServer(t, NewPageNotices(inapp.NewHub(), router, noticeNow))

			ctx := t.Context()
			_, err := client.Seen(ctx, connect.NewRequest(&v1.NoticesSeenRequest{Key: tc.key}))
			if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
				t.Fatalf("Seen code = %v (err %v), want invalid_argument", code, err)
			}
			if len(router.seens) != 0 {
				t.Fatalf("router seen = %+v, want none", router.seens)
			}
		})
	}
}

func TestNilNoticeSourceIsUnavailable(t *testing.T) {
	t.Parallel()
	client := startNoticesServer(t, nil)

	ctx := t.Context()
	stream, err := client.Watch(ctx, connect.NewRequest(&v1.NoticesWatchRequest{}))
	if err == nil {
		stream.Receive()
		err = stream.Err()
	}
	if code := connect.CodeOf(err); code != connect.CodeUnavailable {
		t.Fatalf("Watch code = %v (err %v), want unavailable", code, err)
	}

	_, err = client.Seen(ctx, connect.NewRequest(&v1.NoticesSeenRequest{Key: &v1.NoticeKey{Pane: "%3"}}))
	if code := connect.CodeOf(err); code != connect.CodeUnavailable {
		t.Fatalf("Seen code = %v (err %v), want unavailable", code, err)
	}
}

func TestWatchEndsWhenChangesClose(t *testing.T) {
	t.Parallel()
	source := newFakeNoticeSource()
	client := startNoticesServer(t, source)

	ctx := t.Context()
	stream, err := client.Watch(ctx, connect.NewRequest(&v1.NoticesWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	nextNoticeChange(t, stream)

	close(source.changes)
	if stream.Receive() {
		t.Fatalf("Watch sent %v after its changes closed", stream.Msg())
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("Watch ended with %v, want a clean end", err)
	}
	receiveWithin(t, source.stopped, "watch stopped")
}

func TestWatchStopsWhenCallEnds(t *testing.T) {
	t.Parallel()
	source := newFakeNoticeSource()
	client := startNoticesServer(t, source)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := client.Watch(ctx, connect.NewRequest(&v1.NoticesWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	nextNoticeChange(t, stream)

	cancel()
	receiveWithin(t, source.stopped, "watch stopped")
}
