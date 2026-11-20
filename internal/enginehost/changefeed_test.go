package enginehost

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// changeFeedHold is a response writer whose first Flush sends a stream's opened message, then holds the handler inside
// that send: it closes held and waits for release. While the handler is held, it takes no change from the feed, as a
// subscriber that does not read would leave it.
type changeFeedHold struct {
	http.ResponseWriter
	held    chan struct{}
	release <-chan struct{}
	holding bool
}

// Flush sends the buffered bytes, then on the first call holds the handler until release is closed.
func (h *changeFeedHold) Flush() {
	if f, ok := h.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	if !h.holding {
		h.holding = true
		close(h.held)
		<-h.release
	}
}

// startChangeFeedServer serves feed over httptest, writing each response through wrap, and returns a Changes client of
// it. The server closes at cleanup.
func startChangeFeedServer(
	t *testing.T,
	feed *ChangeFeed,
	wrap func(http.ResponseWriter) http.ResponseWriter,
) enginev1connect.ChangesClient {
	t.Helper()
	path, handler := NewChangesHandler(feed)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(wrap(w), r)
	}))
	t.Cleanup(srv.Close)
	return enginev1connect.NewChangesClient(srv.Client(), srv.URL)
}

// nextChangeFeedMessage returns the next message of a Watch stream. It fails the test when the stream ends first.
func nextChangeFeedMessage(
	t *testing.T,
	stream *connect.ServerStreamForClient[v1.ChangesWatchResponse],
) *v1.ChangesWatchResponse {
	t.Helper()
	if !stream.Receive() {
		t.Fatalf("Watch stream ended: %v", stream.Err())
	}
	return stream.Msg()
}

func TestChangeFeedStreamFeedOpensThenRelays(t *testing.T) {
	t.Parallel()
	feed := NewChangeFeed()
	client := startChangeFeedServer(t, feed, func(w http.ResponseWriter) http.ResponseWriter { return w })

	ctx := t.Context()
	first, err := client.Watch(ctx, connect.NewRequest(&v1.ChangesWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch first: %v", err)
	}
	second, err := client.Watch(ctx, connect.NewRequest(&v1.ChangesWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch second: %v", err)
	}

	// Each stream's opened message comes once its subscription is registered, so a publish after both is relayed.
	opened := &v1.ChangesWatchResponse{Opened: true}
	for i, stream := range []*connect.ServerStreamForClient[v1.ChangesWatchResponse]{first, second} {
		if got := nextChangeFeedMessage(t, stream); !proto.Equal(got, opened) {
			t.Fatalf("stream %d first message = %v, want %v", i, got, opened)
		}
	}

	feed.Publish(7, []string{"s1"})
	want := &v1.ChangesWatchResponse{Seq: 7, Sessions: []string{"s1"}}
	for i, stream := range []*connect.ServerStreamForClient[v1.ChangesWatchResponse]{first, second} {
		if got := nextChangeFeedMessage(t, stream); !proto.Equal(got, want) {
			t.Fatalf("stream %d change = %v, want %v", i, got, want)
		}
	}
}

func TestChangeFeedStreamFeedSlowSubscriberCoalesces(t *testing.T) {
	t.Parallel()
	feed := NewChangeFeed()
	held := make(chan struct{})
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	client := startChangeFeedServer(t, feed, func(w http.ResponseWriter) http.ResponseWriter {
		return &changeFeedHold{ResponseWriter: w, held: held, release: release}
	})

	stream, err := client.Watch(t.Context(), connect.NewRequest(&v1.ChangesWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if got := nextChangeFeedMessage(t, stream); !proto.Equal(got, &v1.ChangesWatchResponse{Opened: true}) {
		t.Fatalf("first message = %v, want opened", got)
	}
	// The stream's handler is held inside its opened send, so these publishes find it unable to take a change.
	<-held
	feed.Publish(3, []string{"s2"})
	feed.Publish(9, []string{"s1"})
	feed.Publish(0, []string{"s3", "s1"})
	releaseOnce()

	want := &v1.ChangesWatchResponse{Seq: 9, Sessions: []string{"s1", "s2", "s3"}}
	if got := nextChangeFeedMessage(t, stream); !proto.Equal(got, want) {
		t.Fatalf("coalesced change = %v, want %v", got, want)
	}
}
