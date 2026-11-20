package enginehost

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// ChangeFeed relays the changes to sessions to each stream the Changes service serves. Publish never blocks on a
// subscriber: each one keeps a single pending change that later publishes merge into until the subscriber takes it.
type ChangeFeed struct {
	mu   sync.Mutex
	subs map[*changeSubscriber]struct{}
}

// changeSubscriber is one Watch stream. Its pending change is guarded by its feed's mu. signal holds at most one token,
// which says a change is pending.
type changeSubscriber struct {
	seq      int64
	sessions map[string]struct{}
	signal   chan struct{}
}

// NewChangeFeed returns a ChangeFeed with no subscribers.
func NewChangeFeed() *ChangeFeed {
	return &ChangeFeed{subs: make(map[*changeSubscriber]struct{})}
}

// Publish records a change to sessions, the sessions whose events, unfinished text or owed state changed (caller
// events, put-off marks, prompt states, called steps and created epics), with seq the newest event seq among them or 0
// when no event was appended. It merges the change into each subscriber's pending change and signals each one, and
// never waits for a subscriber to read.
func (f *ChangeFeed) Publish(seq int64, sessions []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for sub := range f.subs {
		sub.seq = max(sub.seq, seq)
		for _, session := range sessions {
			sub.sessions[session] = struct{}{}
		}
		select {
		case sub.signal <- struct{}{}:
		default:
		}
	}
}

// subscribe registers a new subscriber, which Publish signals from then on.
func (f *ChangeFeed) subscribe() *changeSubscriber {
	sub := &changeSubscriber{sessions: make(map[string]struct{}), signal: make(chan struct{}, 1)}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[sub] = struct{}{}
	return sub
}

// unsubscribe removes sub, so Publish no longer merges into its pending change.
func (f *ChangeFeed) unsubscribe(sub *changeSubscriber) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.subs, sub)
}

// take returns sub's pending change as a message, sessions sorted, and clears it.
func (f *ChangeFeed) take(sub *changeSubscriber) *v1.ChangesWatchResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := &v1.ChangesWatchResponse{Seq: sub.seq, Sessions: slices.Sorted(maps.Keys(sub.sessions))}
	sub.seq = 0
	clear(sub.sessions)
	return resp
}

// changesService implements the Changes RPCs over feed. A nil feed refuses Watch as unavailable.
type changesService struct {
	feed *ChangeFeed
}

// Watch registers a subscriber, sends the opened message, then sends each pending change until the call ends.
func (s *changesService) Watch(
	ctx context.Context,
	_ *connect.Request[v1.ChangesWatchRequest],
	stream *connect.ServerStream[v1.ChangesWatchResponse],
) error {
	if s.feed == nil {
		return changesUnavailable()
	}
	sub := s.feed.subscribe()
	defer s.feed.unsubscribe(sub)
	if err := stream.Send(&v1.ChangesWatchResponse{Opened: true}); err != nil {
		return fmt.Errorf("send opened change: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sub.signal:
			if err := stream.Send(s.feed.take(sub)); err != nil {
				return fmt.Errorf("send change: %w", err)
			}
		}
	}
}

// changesUnavailable returns the refusal Watch gets when there is no change feed to serve.
func changesUnavailable() error {
	return errors.Join(connect.NewError(connect.CodeUnavailable, errors.New(unavailableMessage)))
}

// NewChangesHandler returns the path and handler that serve the Changes service from feed.
func NewChangesHandler(feed *ChangeFeed, opts ...connect.HandlerOption) (string, http.Handler) {
	return enginev1connect.NewChangesHandler(&changesService{feed: feed}, opts...)
}
